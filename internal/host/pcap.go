package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	interfaces "go.fd.io/govpp/binapi/interface"
	"go.fd.io/govpp/binapi/interface_types"
)

var (
	// ErrCaptureActive is returned when VPP is already capturing packets.
	ErrCaptureActive = errors.New("a packet capture is already active")
	// ErrCaptureNotFound is returned for an unknown capture file.
	ErrCaptureNotFound = errors.New("packet capture not found")
	// ErrCaptureFileActive is returned when deleting the capture being written.
	ErrCaptureFileActive = errors.New("cannot delete an active packet capture")

	captureFilenamePattern  = regexp.MustCompile(`^rookery-[A-Za-z0-9._-]+\.(?:pcap|pcapng)$`)
	captureInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,64}$`)

	captureMaxPattern            = regexp.MustCompile(`(?m)max is (\d+),`)
	captureInterfacePatternMatch = regexp.MustCompile(`(?m)for interface ([^ ]+) to file`)
	captureFilePattern           = regexp.MustCompile(`(?m)to file (\S+)`)
	captureProgressPattern       = regexp.MustCompile(`(\d+) of (\d+) pkts`)
	captureCapturedPattern       = regexp.MustCompile(`(?i)(?:captured|write|wrote) (\d+) (?:pkts?|packets?)`)
	captureSavedPattern          = regexp.MustCompile(`(?i)(?:saved to|write \d+ (?:pkts?|packets?) to) (\S+)`)
	captureOnPattern             = regexp.MustCompile(`(?m)pcap (?:(?:rx and tx|tx and rx|rx/tx|tx/rx|rx|tx) )?(?:dispatch )?capture (?:is on|enabled)`)
	captureOffPattern            = regexp.MustCompile(`(?m)pcap (?:(?:rx and tx|tx and rx|rx/tx|tx/rx|rx|tx) )?(?:dispatch )?capture (?:is off|disabled)`)
)

// VPPInterface is a VPP software interface available for capture.
type VPPInterface struct {
	Name      string `json:"name"`
	SwIfIndex uint32 `json:"sw_if_index"`
	Type      string `json:"type,omitempty"`
	MAC       string `json:"mac,omitempty"`
	MTU       uint16 `json:"mtu,omitempty"`
	Up        bool   `json:"up"`
	Tag       string `json:"tag,omitempty"`
}

// PacketCaptureOptions configures a VPP pcap trace.
type PacketCaptureOptions struct {
	Interface  string `json:"interface"`
	Direction  string `json:"direction"`
	MaxBytes   int64  `json:"max_bytes"`
	SnapLength int    `json:"snap_length"`
}

// PacketCaptureStatus describes the current or most recent VPP capture.
type PacketCaptureStatus struct {
	Active     bool       `json:"active"`
	Interface  string     `json:"interface,omitempty"`
	Direction  string     `json:"direction,omitempty"`
	File       string     `json:"file,omitempty"`
	Size       int64      `json:"size"`
	MaxPackets uint64     `json:"max_packets"`
	MaxBytes   int64      `json:"max_bytes"`
	SnapLength int        `json:"snap_length"`
	Packets    uint64     `json:"packets"`
	Buffered   uint64     `json:"buffered"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	StoppedAt  *time.Time `json:"stopped_at,omitempty"`
	Raw        string     `json:"raw,omitempty"`
}

// PacketCaptureFile is one retained pcap file.
type PacketCaptureFile struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

func (v *VPPNetwork) captureDir() string {
	if strings.TrimSpace(v.CaptureDir) == "" {
		return "/tmp"
	}
	return v.CaptureDir
}

// VPPInterfaces lists VPP software interfaces for the capture picker.
func (v *VPPNetwork) VPPInterfaces(ctx context.Context) ([]VPPInterface, error) {
	conn, err := v.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Disconnect()

	stream, err := interfaces.NewServiceClient(conn).SwInterfaceDump(ctx, &interfaces.SwInterfaceDump{})
	if err != nil {
		return nil, fmt.Errorf("dump VPP interfaces: %w", err)
	}

	var result []VPPInterface
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read VPP interface dump: %w", err)
		}
		mac := net.HardwareAddr(details.L2Address[:]).String()
		if mac == "" {
			mac = "00:00:00:00:00:00"
		}
		devType := details.InterfaceDevType
		if devType == "" {
			devType = details.Type.String()
		}
		result = append(result, VPPInterface{
			Name:      details.InterfaceName,
			SwIfIndex: uint32(details.SwIfIndex),
			Type:      devType,
			MAC:       mac,
			MTU:       details.LinkMtu,
			Up:        details.Flags&interface_types.IF_STATUS_API_FLAG_ADMIN_UP != 0,
			Tag:       details.Tag,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

// StartPacketCapture starts a VPP pcap trace and returns its initial status.
func (v *VPPNetwork) StartPacketCapture(ctx context.Context, options PacketCaptureOptions) (PacketCaptureStatus, error) {
	v.captureMu.Lock()
	defer v.captureMu.Unlock()

	options.Interface = strings.TrimSpace(options.Interface)
	if options.Interface == "" {
		options.Interface = "any"
	}
	if !strings.EqualFold(options.Interface, "any") && !captureInterfacePattern.MatchString(options.Interface) {
		return PacketCaptureStatus{}, fmt.Errorf("invalid capture interface %q", options.Interface)
	}
	options.Direction = strings.ToLower(strings.TrimSpace(options.Direction))
	if options.Direction == "" {
		options.Direction = "both"
	}
	if options.Direction != "both" && options.Direction != "rx" && options.Direction != "tx" {
		return PacketCaptureStatus{}, fmt.Errorf("capture direction must be both, rx, or tx")
	}
	if options.SnapLength == 0 {
		options.SnapLength = 2048
	}
	if options.SnapLength < 64 || options.SnapLength > 9000 {
		return PacketCaptureStatus{}, fmt.Errorf("snap_length must be between 64 and 9000 bytes")
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 10 * 1024 * 1024
	}
	if options.MaxBytes < 64*1024 || options.MaxBytes > 200*1024*1024 {
		return PacketCaptureStatus{}, fmt.Errorf("max_bytes must be between 64 KiB and 200 MiB")
	}

	status, err := v.packetCaptureStatusLocked(ctx)
	if err != nil {
		return PacketCaptureStatus{}, err
	}
	if status.Active {
		return status, ErrCaptureActive
	}

	maxPackets := int64(math.Ceil(float64(options.MaxBytes) / float64(options.SnapLength+16)))
	if maxPackets < 1 {
		maxPackets = 1
	}
	if maxPackets > 1_000_000 {
		maxPackets = 1_000_000
	}
	options.MaxBytes = maxPackets * int64(options.SnapLength+16)

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return PacketCaptureStatus{}, fmt.Errorf("generate capture name: %w", err)
	}
	name := fmt.Sprintf("rookery-%s-%s.pcap", time.Now().UTC().Format("20060102-150405"), hex.EncodeToString(suffix))
	args := []string{"pcap", "trace"}
	switch options.Direction {
	case "both":
		args = append(args, "rx", "tx")
	default:
		args = append(args, options.Direction)
	}
	args = append(args,
		"max", strconv.FormatInt(maxPackets, 10),
		"intfc", options.Interface,
		"file", name,
		"max-bytes-per-pkt", strconv.Itoa(options.SnapLength),
	)
	out, err := v.runCLI(ctx, args...)
	if err != nil {
		return PacketCaptureStatus{}, fmt.Errorf("start VPP packet capture: %w: %s", err, out)
	}
	started := time.Now().UTC()
	v.capture = &PacketCaptureStatus{
		Active:     true,
		Interface:  options.Interface,
		Direction:  options.Direction,
		File:       name,
		MaxPackets: uint64(maxPackets),
		MaxBytes:   options.MaxBytes,
		SnapLength: options.SnapLength,
		StartedAt:  &started,
		Raw:        out,
	}
	return *v.capture, nil
}

// StopPacketCapture stops the active VPP capture and returns its final status.
func (v *VPPNetwork) StopPacketCapture(ctx context.Context) (PacketCaptureStatus, error) {
	v.captureMu.Lock()
	defer v.captureMu.Unlock()

	out, err := v.runCLI(ctx, "pcap", "trace", "off")
	if err != nil {
		return PacketCaptureStatus{}, fmt.Errorf("stop VPP packet capture: %w: %s", err, out)
	}
	status := parseCaptureStatus(out)
	if v.capture != nil {
		if status.Interface == "" || status.Interface == "any" {
			status.Interface = v.capture.Interface
		}
		if status.Direction == "" {
			status.Direction = v.capture.Direction
		}
		if status.File == "" {
			status.File = v.capture.File
		}
		if status.MaxPackets == 0 {
			status.MaxPackets = v.capture.MaxPackets
		}
		status.MaxBytes = v.capture.MaxBytes
		if status.SnapLength == 0 {
			status.SnapLength = v.capture.SnapLength
		}
		if status.StartedAt == nil {
			status.StartedAt = v.capture.StartedAt
		}
	}
	status.Active = false
	stopped := time.Now().UTC()
	status.StoppedAt = &stopped
	status.Raw = out
	if status.File != "" {
		if info, statErr := os.Stat(filepath.Join(v.captureDir(), status.File)); statErr == nil {
			status.Size = info.Size()
		}
	}
	v.capture = &status
	return status, nil
}

// PacketCaptureStatus returns the current VPP capture status.
func (v *VPPNetwork) PacketCaptureStatus(ctx context.Context) (PacketCaptureStatus, error) {
	v.captureMu.Lock()
	defer v.captureMu.Unlock()
	return v.packetCaptureStatusLocked(ctx)
}

func (v *VPPNetwork) packetCaptureStatusLocked(ctx context.Context) (PacketCaptureStatus, error) {
	out, err := v.runCLI(ctx, "pcap", "trace", "status")
	if err != nil {
		return PacketCaptureStatus{}, fmt.Errorf("read VPP packet capture status: %w: %s", err, out)
	}
	status := parseCaptureStatus(out)
	if v.capture != nil {
		if status.Interface == "" || status.Interface == "any" {
			if v.capture.Interface != "" {
				status.Interface = v.capture.Interface
			}
		}
		if status.Direction == "" {
			status.Direction = v.capture.Direction
		}
		if status.File == "" {
			status.File = v.capture.File
		}
		if status.MaxPackets == 0 {
			status.MaxPackets = v.capture.MaxPackets
		}
		if status.MaxBytes == 0 {
			status.MaxBytes = v.capture.MaxBytes
		}
		if status.SnapLength == 0 {
			status.SnapLength = v.capture.SnapLength
		}
		if status.StartedAt == nil {
			status.StartedAt = v.capture.StartedAt
		}
		if !status.Active && v.capture.Active && status.StoppedAt == nil {
			stopped := time.Now().UTC()
			status.StoppedAt = &stopped
		}
	}
	status.Raw = out
	if status.File != "" {
		if info, statErr := os.Stat(filepath.Join(v.captureDir(), status.File)); statErr == nil {
			status.Size = info.Size()
		}
	}
	if status.Active {
		v.capture = &status
	} else if v.capture != nil {
		v.capture = &status
	}
	return status, nil
}

func parseCaptureStatus(out string) PacketCaptureStatus {
	status := PacketCaptureStatus{}
	if captureOnPattern.MatchString(out) {
		status.Active = true
		status.Direction = captureDirectionFromStatus(out)
	}
	if captureOffPattern.MatchString(out) {
		status.Active = false
		status.Direction = captureDirectionFromStatus(out)
	}
	if match := captureMaxPattern.FindStringSubmatch(out); match != nil {
		status.MaxPackets, _ = strconv.ParseUint(match[1], 10, 64)
	}
	if match := captureInterfacePatternMatch.FindStringSubmatch(out); match != nil {
		status.Interface = match[1]
	}
	if match := captureFilePattern.FindStringSubmatch(out); match != nil {
		status.File = filepath.Base(strings.TrimRight(match[1], ".,"))
	}
	if match := captureProgressPattern.FindStringSubmatch(out); match != nil {
		status.Buffered, _ = strconv.ParseUint(match[1], 10, 64)
	}
	if match := captureCapturedPattern.FindStringSubmatch(out); match != nil {
		status.Packets, _ = strconv.ParseUint(match[1], 10, 64)
	}
	if match := captureSavedPattern.FindStringSubmatch(out); match != nil {
		status.File = filepath.Base(strings.TrimRight(match[1], ".,"))
	}
	return status
}

func captureDirectionFromStatus(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.ToLower(line)
		switch {
		case strings.Contains(line, "rx and tx"), strings.Contains(line, "tx and rx"), strings.Contains(line, "rx/tx"), strings.Contains(line, "tx/rx"):
			return "both"
		case strings.Contains(line, "pcap rx "):
			return "rx"
		case strings.Contains(line, "pcap tx "):
			return "tx"
		}
	}
	return ""
}

func normalizeCaptureDirection(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "rx", "tx":
		return strings.ToLower(strings.TrimSpace(value))
	case "rx/tx", "tx/rx", "rx and tx", "tx and rx":
		return "both"
	default:
		return ""
	}
}

// ListPacketCaptures lists pcap files created by rookery.
func (v *VPPNetwork) ListPacketCaptures() ([]PacketCaptureFile, error) {
	entries, err := os.ReadDir(v.captureDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []PacketCaptureFile{}, nil
		}
		return nil, fmt.Errorf("list packet captures: %w", err)
	}
	result := make([]PacketCaptureFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !captureFilenamePattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, PacketCaptureFile{
			Name:     entry.Name(),
			Size:     info.Size(),
			Modified: info.ModTime().UTC(),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Modified.After(result[j].Modified)
	})
	return result, nil
}

// DeletePacketCapture removes a retained pcap file. The file currently being
// written by VPP cannot be deleted.
func (v *VPPNetwork) DeletePacketCapture(name string) error {
	v.captureMu.Lock()
	defer v.captureMu.Unlock()

	if filepath.Base(name) != name || !captureFilenamePattern.MatchString(name) {
		return ErrCaptureNotFound
	}
	if v.capture != nil && v.capture.Active && v.capture.File == name {
		return ErrCaptureFileActive
	}
	if err := os.Remove(filepath.Join(v.captureDir(), name)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrCaptureNotFound
		}
		return fmt.Errorf("delete packet capture: %w", err)
	}
	if v.capture != nil && !v.capture.Active && v.capture.File == name {
		v.capture.File = ""
	}
	return nil
}

// OpenPacketCapture opens a retained pcap for reading or download.
func (v *VPPNetwork) OpenPacketCapture(name string) (io.ReadSeekCloser, PacketCaptureFile, error) {
	if filepath.Base(name) != name || !captureFilenamePattern.MatchString(name) {
		return nil, PacketCaptureFile{}, ErrCaptureNotFound
	}
	path := filepath.Join(v.captureDir(), name)
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, PacketCaptureFile{}, ErrCaptureNotFound
		}
		return nil, PacketCaptureFile{}, fmt.Errorf("open packet capture: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, PacketCaptureFile{}, fmt.Errorf("stat packet capture: %w", err)
	}
	return file, PacketCaptureFile{
		Name:     name,
		Size:     info.Size(),
		Modified: info.ModTime().UTC(),
	}, nil
}
