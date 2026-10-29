package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.fd.io/govpp"
	"go.fd.io/govpp/binapi/ethernet_types"
	"go.fd.io/govpp/binapi/fib_types"
	"go.fd.io/govpp/binapi/interface"
	"go.fd.io/govpp/binapi/interface_types"
	ipapi "go.fd.io/govpp/binapi/ip"
	"go.fd.io/govpp/binapi/ip_types"
	"go.fd.io/govpp/binapi/l2"
	"go.fd.io/govpp/binapi/memif"
	"go.fd.io/govpp/binapi/tapv2"
	"go.fd.io/govpp/binapi/vhost_user"
	"go.fd.io/govpp/core"
)

const (
	// DefaultVhostDir is where VPP vhost-user sockets are created.
	DefaultVhostDir   = "/run/vpp"
	vppCleanupTimeout = 3 * time.Second

	// defaultInterfaceMTU is used for Rookery-created software interfaces.
	// VPP defaults to jumbo (9000) MTUs, which do not match the guest virtio
	// NICs or typical upstream networks. Keep every layer at Ethernet MTU.
	defaultInterfaceMTU = 1500
)

// VPPNetwork provisions VPP bridge domains and vhost-user interfaces.
type VPPNetwork struct {
	Socket   string
	VhostDir string
	CLI      string
	// CaptureDir is where VPP writes packet capture files. VPP's pcap CLI
	// only accepts a basename and always writes under /tmp.
	CaptureDir string

	captureMu sync.Mutex
	capture   *PacketCaptureStatus
}

// NewVPPNetwork returns a VPP network provisioner.
func NewVPPNetwork(socket, vhostDir string) (*VPPNetwork, error) {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		return nil, fmt.Errorf("VPP API socket is not configured")
	}
	if strings.TrimSpace(vhostDir) == "" {
		vhostDir = DefaultVhostDir
	}
	abs, err := filepath.Abs(vhostDir)
	if err != nil {
		return nil, fmt.Errorf("resolve vhost directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create vhost directory: %w", err)
	}
	return &VPPNetwork{Socket: socket, VhostDir: abs, CLI: "vppctl", CaptureDir: "/tmp"}, nil
}

// Ping checks the VPP binary API with a control ping.
func (v *VPPNetwork) Ping(ctx context.Context) error {
	return PingVPP(ctx, v.Socket)
}

// Identity returns a stable identity for the VPP API socket. VPP recreates the
// socket when it restarts, so a change indicates a new VPP instance.
func (v *VPPNetwork) Identity() (string, error) {
	info, err := os.Stat(v.Socket)
	if err != nil {
		return "", fmt.Errorf("stat VPP API socket %s: %w", v.Socket, err)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
	}
	return fmt.Sprintf("%d", info.ModTime().UnixNano()), nil
}

// InterfaceCounters contains cumulative VPP counters for a vhost interface.
type InterfaceCounters struct {
	RXPackets uint64 `json:"rx_packets"`
	TXPackets uint64 `json:"tx_packets"`
	RXBytes   uint64 `json:"rx_bytes"`
	TXBytes   uint64 `json:"tx_bytes"`
}

// InterfaceCounters returns counters for the named VPP interfaces.
func (v *VPPNetwork) InterfaceCounters(ctx context.Context, names []string) (map[string]InterfaceCounters, error) {
	result := make(map[string]InterfaceCounters, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		out, err := v.runCLI(ctx, "show", "interface", name)
		if err != nil {
			return nil, fmt.Errorf("show interface %s: %w: %s", name, err, out)
		}
		var counters InterfaceCounters
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			value, err := strconv.ParseUint(fields[2], 10, 64)
			if err != nil {
				continue
			}
			switch fields[0] + " " + fields[1] {
			case "rx packets":
				counters.RXPackets = value
			case "tx packets":
				counters.TXPackets = value
			case "rx bytes":
				counters.RXBytes = value
			case "tx bytes":
				counters.TXBytes = value
			}
		}
		result[name] = counters
	}
	return result, nil
}

// ResetInterfaceCounters clears the RX/TX counters for all VPP interfaces.
func (v *VPPNetwork) ResetInterfaceCounters(ctx context.Context) error {
	out, err := v.runCLI(ctx, "clear", "interfaces")
	if err != nil {
		return fmt.Errorf("clear interfaces: %w: %s", err, out)
	}
	return nil
}

// CreateBridgeDomain creates a VPP bridge domain and returns its ID.
func (v *VPPNetwork) CreateBridgeDomain(ctx context.Context, name string, reserved []uint32) (uint32, error) {
	conn, err := v.connect()
	if err != nil {
		return 0, err
	}
	defer conn.Disconnect()

	used := make(map[uint32]struct{}, len(reserved))
	for _, id := range reserved {
		used[id] = struct{}{}
	}

	client := l2.NewServiceClient(conn)
	for bdID := uint32(100); bdID < 10000; bdID++ {
		if _, skip := used[bdID]; skip {
			continue
		}
		reply, err := client.BridgeDomainAddDelV2(ctx, &l2.BridgeDomainAddDelV2{
			BdID:    bdID,
			Flood:   true,
			UuFlood: true,
			Forward: true,
			Learn:   true,
			ArpTerm: true,
			BdTag:   name,
			IsAdd:   true,
		})
		if err != nil {
			if isBridgeDomainExists(err) {
				continue
			}
			return 0, fmt.Errorf("create bridge domain: %w", err)
		}
		if err := checkVPPRetval("create bridge domain", reply.Retval); err != nil {
			if isBridgeDomainExists(err) {
				continue
			}
			return 0, err
		}
		return reply.BdID, nil
	}
	return 0, fmt.Errorf("create bridge domain: no free bridge domain ID")
}

// EnsureBridgeDomain creates a bridge domain with a specific ID if it is
// missing. It is used to restore network state after a VPP restart.
func (v *VPPNetwork) EnsureBridgeDomain(ctx context.Context, bdID uint32, name string) error {
	if bdID == 0 {
		return errors.New("bridge domain ID is required")
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	client := l2.NewServiceClient(conn)
	stream, err := client.BridgeDomainDump(ctx, &l2.BridgeDomainDump{BdID: bdID})
	if err != nil {
		return fmt.Errorf("dump bridge domain %d: %w", bdID, err)
	}
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read bridge domain dump: %w", err)
		}
		if details.BdID != bdID {
			continue
		}
		if details.BdTag != "" && details.BdTag != name {
			return fmt.Errorf("bridge domain %d already exists with tag %q", bdID, details.BdTag)
		}
		return nil
	}

	reply, err := client.BridgeDomainAddDelV2(ctx, &l2.BridgeDomainAddDelV2{
		BdID:    bdID,
		Flood:   true,
		UuFlood: true,
		Forward: true,
		Learn:   true,
		ArpTerm: true,
		BdTag:   name,
		IsAdd:   true,
	})
	if err != nil {
		if isBridgeDomainExists(err) {
			return nil
		}
		return fmt.Errorf("recreate bridge domain %d: %w", bdID, err)
	}
	if reply.Retval != 0 {
		return fmt.Errorf("recreate bridge domain %d: VPP returned retval %d", bdID, reply.Retval)
	}
	return nil
}

// DeleteBridgeDomain removes a VPP bridge domain.
func (v *VPPNetwork) DeleteBridgeDomain(ctx context.Context, bdID uint32) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := l2.NewServiceClient(conn).BridgeDomainAddDelV2(ctx, &l2.BridgeDomainAddDelV2{
		BdID:  bdID,
		IsAdd: false,
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete bridge domain %d: %w", bdID, err)
	}
	return checkVPPRetval("delete bridge domain", reply.Retval)
}

// CreateVhostUser creates a vhost-user interface, brings it up, and adds it to
// the bridge domain. It returns the socket path and VPP sw_if_index.
func (v *VPPNetwork) CreateVhostUser(ctx context.Context, name, mac string, bdID uint32) (string, uint32, error) {
	macAddr, err := ethernet_types.ParseMacAddress(mac)
	if err != nil {
		return "", 0, fmt.Errorf("parse MAC %q: %w", mac, err)
	}
	socket := filepath.Join(v.VhostDir, name+".sock")
	if len(socket) > 255 {
		return "", 0, fmt.Errorf("vhost socket path is too long: %s", socket)
	}

	conn, err := v.connect()
	if err != nil {
		return "", 0, err
	}
	defer conn.Disconnect()

	vhostClient := vhost_user.NewServiceClient(conn)
	reply, err := vhostClient.CreateVhostUserIf(ctx, &vhost_user.CreateVhostUserIf{
		IsServer:     true,
		SockFilename: socket,
		UseCustomMac: true,
		MacAddress:   macAddr,
		Tag:          name,
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unknown message") {
			return "", 0, fmt.Errorf("create vhost-user interface: %w (install/load vpp-plugin-core and restart VPP)", err)
		}
		return "", 0, fmt.Errorf("create vhost-user interface: %w", err)
	}
	if err := checkVPPRetval("create vhost-user interface", reply.Retval); err != nil {
		return "", 0, err
	}
	swIfIndex := reply.SwIfIndex

	rollback := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), vppCleanupTimeout)
		defer cancel()
		_, _ = vhostClient.DeleteVhostUserIf(cleanupCtx, &vhost_user.DeleteVhostUserIf{SwIfIndex: swIfIndex})
	}

	nameReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetInterfaceName(ctx, &interfaces.SwInterfaceSetInterfaceName{
		SwIfIndex: swIfIndex,
		Name:      name,
	})
	if err != nil {
		rollback()
		return "", 0, fmt.Errorf("name vhost-user interface: %w", err)
	}
	if err := checkVPPRetval("name vhost-user interface", nameReply.Retval); err != nil {
		rollback()
		return "", 0, err
	}
	if err := setInterfaceMTU(ctx, conn, uint32(swIfIndex), defaultInterfaceMTU); err != nil {
		rollback()
		return "", 0, fmt.Errorf("set vhost-user interface MTU: %w", err)
	}

	flagsReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetFlags(ctx, &interfaces.SwInterfaceSetFlags{
		SwIfIndex: swIfIndex,
		Flags:     interface_types.IF_STATUS_API_FLAG_ADMIN_UP,
	})
	if err != nil {
		rollback()
		return "", 0, fmt.Errorf("set vhost-user interface up: %w", err)
	}
	if err := checkVPPRetval("set vhost-user interface up", flagsReply.Retval); err != nil {
		rollback()
		return "", 0, err
	}

	l2Reply, err := l2.NewServiceClient(conn).SwInterfaceSetL2Bridge(ctx, &l2.SwInterfaceSetL2Bridge{
		RxSwIfIndex: swIfIndex,
		BdID:        bdID,
		PortType:    l2.L2_API_PORT_TYPE_NORMAL,
		Enable:      true,
	})
	if err != nil {
		rollback()
		return "", 0, fmt.Errorf("attach vhost-user interface to bridge domain: %w", err)
	}
	if err := checkVPPRetval("attach vhost-user interface to bridge domain", l2Reply.Retval); err != nil {
		rollback()
		return "", 0, err
	}

	// VPP creates the Unix socket as part of interface creation.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			rollback()
			return "", 0, fmt.Errorf("vhost socket %s was not created", socket)
		}
		time.Sleep(50 * time.Millisecond)
	}

	return socket, uint32(swIfIndex), nil
}

// EnsureVhostUser finds or recreates a vhost-user interface by name. It is used
// by startup reconciliation after a VPP restart, when all vhost interfaces have
// been lost but their Unix socket files may still exist.
func (v *VPPNetwork) EnsureVhostUser(ctx context.Context, name, mac string, bdID uint32) (string, uint32, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", 0, errors.New("vhost interface name is required")
	}
	if bdID == 0 {
		return "", 0, errors.New("bridge domain id is required")
	}
	expectedSocket := filepath.Join(v.VhostDir, name+".sock")
	conn, err := v.connect()
	if err != nil {
		return "", 0, err
	}
	defer conn.Disconnect()

	vhostClient := vhost_user.NewServiceClient(conn)
	stream, err := vhostClient.SwInterfaceVhostUserDump(ctx, &vhost_user.SwInterfaceVhostUserDump{
		SwIfIndex: interface_types.InterfaceIndex(^uint32(0)),
	})
	if err != nil {
		return "", 0, fmt.Errorf("dump vhost-user interfaces: %w", err)
	}
	var foundIndex uint32
	var foundSocket string
	for {
		details, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return "", 0, fmt.Errorf("read vhost-user interface dump: %w", recvErr)
		}
		if details.InterfaceName == name {
			foundIndex = uint32(details.SwIfIndex)
			foundSocket = strings.TrimSpace(details.SockFilename)
			break
		}
	}
	if foundIndex != 0 {
		if foundSocket == "" {
			foundSocket = expectedSocket
		}
		flagsReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetFlags(ctx, &interfaces.SwInterfaceSetFlags{
			SwIfIndex: interface_types.InterfaceIndex(foundIndex),
			Flags:     interface_types.IF_STATUS_API_FLAG_ADMIN_UP,
		})
		if err != nil {
			return "", 0, fmt.Errorf("set vhost-user interface %s up: %w", name, err)
		}
		if err := checkVPPRetval("set vhost-user interface up", flagsReply.Retval); err != nil {
			return "", 0, err
		}
		l2Reply, err := l2.NewServiceClient(conn).SwInterfaceSetL2Bridge(ctx, &l2.SwInterfaceSetL2Bridge{
			RxSwIfIndex: interface_types.InterfaceIndex(foundIndex),
			BdID:        bdID,
			PortType:    l2.L2_API_PORT_TYPE_NORMAL,
			Enable:      true,
		})
		if err != nil {
			return "", 0, fmt.Errorf("attach vhost-user interface %s to bridge domain: %w", name, err)
		}
		if err := checkVPPRetval("attach vhost-user interface to bridge domain", l2Reply.Retval); err != nil {
			return "", 0, err
		}
		if err := setInterfaceMTU(ctx, conn, foundIndex, defaultInterfaceMTU); err != nil {
			return "", 0, fmt.Errorf("set vhost-user interface %s MTU: %w", name, err)
		}
		return foundSocket, foundIndex, nil
	}

	// VPP has no interface by this name. The socket file is stale (for example,
	// left over after a VPP restart), so remove it before recreating the
	// vhost-user listener.
	if err := os.Remove(expectedSocket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", 0, fmt.Errorf("remove stale vhost socket %s: %w", expectedSocket, err)
	}
	return v.CreateVhostUser(ctx, name, mac, bdID)
}

// DeleteVhostUser removes a vhost-user interface.
func (v *VPPNetwork) DeleteVhostUser(ctx context.Context, swIfIndex uint32) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := vhost_user.NewServiceClient(conn).DeleteVhostUserIf(ctx, &vhost_user.DeleteVhostUserIf{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete vhost-user interface %d: %w", swIfIndex, err)
	}
	return checkVPPRetval("delete vhost-user interface", reply.Retval)
}

// CreateTap creates a VPP tap interface, brings it up, and adds it to the
// bridge domain. The host side is named hostName and configured with gatewayCIDR.
func (v *VPPNetwork) CreateTap(ctx context.Context, hostName, gatewayCIDR string, bdID uint32, tableID uint32) (uint32, error) {
	if len(hostName) > 15 {
		return 0, fmt.Errorf("tap host interface name %q is too long", hostName)
	}

	tap := &tapv2.TapCreateV2{
		// VPP uses ^uint32(0) as the "auto assign" tap device ID. Sending the
		// zero value instead creates device instance 0, which conflicts with
		// any existing tap and makes a second CreateTapV2 fail.
		ID:            ^uint32(0),
		UseRandomMac:  true,
		HostIfNameSet: true,
		HostIfName:    hostName,
		Tag:           "rookery-vnf:" + hostName,
	}
	conn, err := v.connect()
	if err != nil {
		return 0, err
	}
	defer conn.Disconnect()

	tapClient := tapv2.NewServiceClient(conn)
	reply, err := tapClient.TapCreateV2(ctx, tap)
	if err != nil {
		return 0, fmt.Errorf("create VNF tap: %w", err)
	}
	if err := checkVPPRetval("create VNF tap", reply.Retval); err != nil {
		return 0, err
	}
	swIfIndex := reply.SwIfIndex

	rollback := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), vppCleanupTimeout)
		defer cancel()
		_, _ = tapClient.TapDeleteV2(cleanupCtx, &tapv2.TapDeleteV2{SwIfIndex: swIfIndex})
	}

	nameReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetInterfaceName(ctx, &interfaces.SwInterfaceSetInterfaceName{
		SwIfIndex: swIfIndex,
		Name:      hostName,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("name VNF tap: %w", err)
	}
	if err := checkVPPRetval("name VNF tap", nameReply.Retval); err != nil {
		rollback()
		return 0, err
	}
	if err := setInterfaceMTU(ctx, conn, uint32(swIfIndex), defaultInterfaceMTU); err != nil {
		rollback()
		return 0, fmt.Errorf("set VNF tap MTU: %w", err)
	}

	flagsReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetFlags(ctx, &interfaces.SwInterfaceSetFlags{
		SwIfIndex: swIfIndex,
		Flags:     interface_types.IF_STATUS_API_FLAG_ADMIN_UP,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("set VNF tap up: %w", err)
	}
	if err := checkVPPRetval("set VNF tap up", flagsReply.Retval); err != nil {
		rollback()
		return 0, err
	}

	l2Reply, err := l2.NewServiceClient(conn).SwInterfaceSetL2Bridge(ctx, &l2.SwInterfaceSetL2Bridge{
		RxSwIfIndex: swIfIndex,
		BdID:        bdID,
		PortType:    l2.L2_API_PORT_TYPE_NORMAL,
		Enable:      true,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("attach VNF tap to bridge domain: %w", err)
	}
	if err := checkVPPRetval("attach VNF tap to bridge domain", l2Reply.Retval); err != nil {
		rollback()
		return 0, err
	}

	if tableID != 0 {
		if err := v.SetInterfaceRouteTable(ctx, uint32(swIfIndex), false, tableID); err != nil {
			rollback()
			return 0, err
		}
		if err := v.SetInterfaceRouteTable(ctx, uint32(swIfIndex), true, tableID); err != nil {
			rollback()
			return 0, err
		}
	}
	// The host-side tap address is configured by VNFManager.Start; the VPP tap
	// interface stays L2-only. Assigning the VNF address here would duplicate
	// the shared memif/BVI service address inside the network VRF.
	_ = gatewayCIDR

	return uint32(swIfIndex), nil
}

// DeleteTapByName removes a tap by its VPP interface name. This avoids using a
// stale sw_if_index after a VPP restart, where an old index may refer to an
// unrelated interface.
func (v *VPPNetwork) DeleteTapByName(ctx context.Context, hostName string) error {
	if strings.TrimSpace(hostName) == "" {
		return nil
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	client := interfaces.NewServiceClient(conn)
	stream, err := client.SwInterfaceDump(ctx, &interfaces.SwInterfaceDump{})
	if err != nil {
		return fmt.Errorf("dump VPP interfaces: %w", err)
	}
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read VPP interface dump: %w", err)
		}
		if details.InterfaceName != hostName {
			continue
		}
		// Only delete interfaces created by this service. A user-created
		// vhost-user interface could otherwise share the same name.
		if details.Tag != "" && details.Tag != "rookery-vnf:"+hostName && details.Tag != "rookery-dhcp:"+hostName {
			return nil
		}
		reply, err := tapv2.NewServiceClient(conn).TapDeleteV2(ctx, &tapv2.TapDeleteV2{
			SwIfIndex: details.SwIfIndex,
		})
		if err != nil {
			if isVPPNotFound(err) {
				return nil
			}
			return fmt.Errorf("delete VNF tap %s: %w", hostName, err)
		}
		return checkVPPRetval("delete VNF tap", reply.Retval)
	}
}

// DeleteTap removes a VPP tap interface.
func (v *VPPNetwork) DeleteTap(ctx context.Context, swIfIndex uint32) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := tapv2.NewServiceClient(conn).TapDeleteV2(ctx, &tapv2.TapDeleteV2{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete DHCP tap %d: %w", swIfIndex, err)
	}
	return checkVPPRetval("delete VNF tap", reply.Retval)
}

// hostTapTag marks VPP tap interfaces owned by the host-tap feature.
const hostTapTagPrefix = "rookery-host-tap:"

func hostTapTag(name string) string {
	return hostTapTagPrefix + name
}

// EnsureHostTap finds or creates a VPP tap attached to a network bridge
// domain and configures the Linux side of the tap. It is idempotent so the
// host connection can be restored after an API or VPP restart.
func (v *VPPNetwork) EnsureHostTap(ctx context.Context, name string, bdID uint32, ipv4Address string) (uint32, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("host tap name is required")
	}
	if len(name) > 15 {
		return 0, fmt.Errorf("host tap name %q exceeds the Linux limit of 15 characters", name)
	}
	if bdID == 0 {
		return 0, errors.New("bridge domain id is required")
	}

	conn, err := v.connect()
	if err != nil {
		return 0, err
	}
	defer conn.Disconnect()

	details, err := findInterfaceDetails(ctx, conn, name)
	if err == nil {
		if details.Tag != hostTapTag(name) {
			return 0, fmt.Errorf("VPP interface %s exists but is not a Rookery host tap", name)
		}
		if err := v.setTapUpAndAttach(ctx, conn, uint32(details.SwIfIndex), bdID); err != nil {
			return 0, err
		}
		if err := configureHostTap(ctx, name, ipv4Address); err != nil {
			return 0, err
		}
		return uint32(details.SwIfIndex), nil
	}
	if !errors.Is(err, errVPPInterfaceNotFound) {
		return 0, err
	}

	// A VPP restart normally removes its tap devices, but a stale Linux tap
	// can survive a crash and would make TapCreateV2 fail on the same name.
	if _, err := net.InterfaceByName(name); err == nil {
		if out, err := runIP(ctx, "link", "delete", "dev", name); err != nil {
			return 0, fmt.Errorf("remove stale host tap %s: %w: %s", name, err, out)
		}
	}
	return v.createHostTap(ctx, name, bdID, ipv4Address)
}

func (v *VPPNetwork) createHostTap(ctx context.Context, name string, bdID uint32, ipv4Address string) (uint32, error) {
	conn, err := v.connect()
	if err != nil {
		return 0, err
	}
	defer conn.Disconnect()

	tapClient := tapv2.NewServiceClient(conn)
	reply, err := tapClient.TapCreateV2(ctx, &tapv2.TapCreateV2{
		// ^uint32(0) asks VPP to allocate the tap device index. The zero value
		// requests instance 0 and fails once another tap exists.
		ID:            ^uint32(0),
		UseRandomMac:  true,
		HostIfNameSet: true,
		HostIfName:    name,
		Tag:           hostTapTag(name),
	})
	if err != nil {
		return 0, fmt.Errorf("create host tap: %w", err)
	}
	if err := checkVPPRetval("create host tap", reply.Retval); err != nil {
		return 0, err
	}
	swIfIndex := uint32(reply.SwIfIndex)

	rollback := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), vppCleanupTimeout)
		defer cancel()
		_, _ = tapClient.TapDeleteV2(cleanupCtx, &tapv2.TapDeleteV2{SwIfIndex: reply.SwIfIndex})
	}

	nameReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetInterfaceName(ctx, &interfaces.SwInterfaceSetInterfaceName{
		SwIfIndex: reply.SwIfIndex,
		Name:      name,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("name host tap: %w", err)
	}
	if err := checkVPPRetval("name host tap", nameReply.Retval); err != nil {
		rollback()
		return 0, err
	}
	if err := v.setTapUpAndAttach(ctx, conn, swIfIndex, bdID); err != nil {
		rollback()
		return 0, err
	}
	if err := configureHostTap(ctx, name, ipv4Address); err != nil {
		rollback()
		return 0, err
	}
	return swIfIndex, nil
}

func (v *VPPNetwork) setTapUpAndAttach(ctx context.Context, conn *core.Connection, swIfIndex uint32, bdID uint32) error {
	if err := setInterfaceMTU(ctx, conn, swIfIndex, defaultInterfaceMTU); err != nil {
		return fmt.Errorf("set host tap MTU: %w", err)
	}
	flagsReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetFlags(ctx, &interfaces.SwInterfaceSetFlags{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		Flags:     interface_types.IF_STATUS_API_FLAG_ADMIN_UP,
	})
	if err != nil {
		return fmt.Errorf("set host tap up: %w", err)
	}
	if err := checkVPPRetval("set host tap up", flagsReply.Retval); err != nil {
		return err
	}

	l2Reply, err := l2.NewServiceClient(conn).SwInterfaceSetL2Bridge(ctx, &l2.SwInterfaceSetL2Bridge{
		RxSwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		BdID:        bdID,
		PortType:    l2.L2_API_PORT_TYPE_NORMAL,
		Enable:      true,
	})
	if err != nil {
		return fmt.Errorf("attach host tap to bridge domain: %w", err)
	}
	if err := checkVPPRetval("attach host tap to bridge domain", l2Reply.Retval); err != nil {
		return err
	}
	return nil
}

// DeleteHostTapByName removes a host tap by name and tolerates a stale
// sw_if_index after a VPP restart.
func (v *VPPNetwork) DeleteHostTapByName(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	details, err := findInterfaceDetails(ctx, conn, name)
	if errors.Is(err, errVPPInterfaceNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if details.Tag != hostTapTag(name) {
		return nil
	}
	reply, err := tapv2.NewServiceClient(conn).TapDeleteV2(ctx, &tapv2.TapDeleteV2{
		SwIfIndex: details.SwIfIndex,
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete host tap %s: %w", name, err)
	}
	return checkVPPRetval("delete host tap", reply.Retval)
}

// configureHostTap brings the Linux side of a VPP-created tap up and, when an
// address is configured, assigns it. The interface is created asynchronously
// by VPP, so wait briefly for it to appear.
func configureHostTap(ctx context.Context, name, ipv4Address string) error {
	if err := waitForLink(ctx, name, 3*time.Second); err != nil {
		return err
	}
	if address := strings.TrimSpace(ipv4Address); address != "" {
		if out, err := runIP(ctx, "addr", "replace", address, "dev", name); err != nil {
			return fmt.Errorf("configure host tap %s address: %w: %s", name, err, out)
		}
	}
	if out, err := runIP(ctx, "link", "set", "dev", name, "mtu", strconv.Itoa(defaultInterfaceMTU)); err != nil {
		return fmt.Errorf("set host tap %s MTU: %w: %s", name, err, out)
	}
	if out, err := runIP(ctx, "link", "set", "up", "dev", name); err != nil {
		return fmt.Errorf("bring host tap %s up: %w: %s", name, err, out)
	}
	return nil
}

func findInterfaceDetails(ctx context.Context, conn *core.Connection, name string) (*interfaces.SwInterfaceDetails, error) {
	client := interfaces.NewServiceClient(conn)
	stream, err := client.SwInterfaceDump(ctx, &interfaces.SwInterfaceDump{})
	if err != nil {
		return nil, fmt.Errorf("dump VPP interfaces: %w", err)
	}
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil, errVPPInterfaceNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("read VPP interface dump: %w", err)
		}
		if details.InterfaceName == name {
			return details, nil
		}
	}
}

// EnsureMemif finds an existing memif by name, or creates it and its socket.
// It is used for the shared per-network VNF memif, which survives API restarts
// but is recreated after a VPP restart.
func (v *VPPNetwork) EnsureMemif(ctx context.Context, name, socketPath string, ifID uint32, mac net.HardwareAddr, bdID uint32) (uint32, error) {
	conn, err := v.connect()
	if err != nil {
		return 0, err
	}
	index, findErr := findInterface(ctx, conn, name)
	if findErr == nil {
		if err := setInterfaceMTU(ctx, conn, index, defaultInterfaceMTU); err != nil {
			conn.Disconnect()
			return 0, fmt.Errorf("set VNF memif MTU: %w", err)
		}
		conn.Disconnect()
		return index, nil
	}
	conn.Disconnect()
	if !errors.Is(findErr, errVPPInterfaceNotFound) {
		return 0, findErr
	}
	_ = os.Remove(socketPath)
	return v.CreateMemif(ctx, name, socketPath, ifID, mac, bdID)
}

// CreateMemif creates a VPP memif master interface, brings it up, and adds it
// to the bridge domain. The userspace peer connects to socketPath as a slave.
func (v *VPPNetwork) CreateMemif(ctx context.Context, name, socketPath string, ifID uint32, mac net.HardwareAddr, bdID uint32) (uint32, error) {
	if strings.TrimSpace(name) == "" {
		return 0, errors.New("memif interface name is required")
	}
	if len(name) > 63 {
		return 0, fmt.Errorf("memif interface name %q exceeds the VPP limit of 63 characters", name)
	}
	if strings.TrimSpace(socketPath) == "" {
		return 0, errors.New("memif socket path is required")
	}
	if len(mac) != 6 {
		return 0, errors.New("memif MAC address must contain 6 bytes")
	}

	conn, err := v.connect()
	if err != nil {
		return 0, err
	}
	defer conn.Disconnect()
	memifClient := memif.NewServiceClient(conn)

	// Reuse the VNF id as the memif socket id. Clear a stale registration first
	// so deleting and recreating a VNF does not fail on an old socket path.
	_, _ = memifClient.MemifSocketFilenameAddDelV2(ctx, &memif.MemifSocketFilenameAddDelV2{
		IsAdd:    false,
		SocketID: ifID,
	})
	socketReply, err := memifClient.MemifSocketFilenameAddDelV2(ctx, &memif.MemifSocketFilenameAddDelV2{
		IsAdd:          true,
		SocketID:       ifID,
		SocketFilename: socketPath,
	})
	if err != nil {
		return 0, fmt.Errorf("register memif socket %s: %w", socketPath, err)
	}
	if err := checkVPPRetval("register memif socket", socketReply.Retval); err != nil {
		return 0, err
	}

	hwAddr := ethernet_types.MacAddress{}
	copy(hwAddr[:], mac)
	reply, err := memifClient.MemifCreateV2(ctx, &memif.MemifCreateV2{
		Role:       memif.MEMIF_ROLE_API_MASTER,
		Mode:       memif.MEMIF_MODE_API_ETHERNET,
		RxQueues:   1,
		TxQueues:   1,
		ID:         ifID,
		SocketID:   socketReply.SocketID,
		RingSize:   memifDHCPRingSize,
		BufferSize: memifDHCPBufferSize,
		HwAddr:     hwAddr,
	})
	if err != nil {
		return 0, fmt.Errorf("create VNF memif: %w", err)
	}
	if err := checkVPPRetval("create VNF memif", reply.Retval); err != nil {
		return 0, err
	}
	swIfIndex := reply.SwIfIndex

	rollback := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), vppCleanupTimeout)
		defer cancel()
		_, _ = memifClient.MemifDelete(cleanupCtx, &memif.MemifDelete{SwIfIndex: swIfIndex})
	}

	nameReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetInterfaceName(ctx, &interfaces.SwInterfaceSetInterfaceName{
		SwIfIndex: swIfIndex,
		Name:      name,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("name VNF memif: %w", err)
	}
	if err := checkVPPRetval("name VNF memif", nameReply.Retval); err != nil {
		rollback()
		return 0, err
	}
	if err := setInterfaceMTU(ctx, conn, uint32(swIfIndex), defaultInterfaceMTU); err != nil {
		rollback()
		return 0, fmt.Errorf("set VNF memif MTU: %w", err)
	}

	flagsReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetFlags(ctx, &interfaces.SwInterfaceSetFlags{
		SwIfIndex: swIfIndex,
		Flags:     interface_types.IF_STATUS_API_FLAG_ADMIN_UP,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("set VNF memif up: %w", err)
	}
	if err := checkVPPRetval("set VNF memif up", flagsReply.Retval); err != nil {
		rollback()
		return 0, err
	}

	l2Reply, err := l2.NewServiceClient(conn).SwInterfaceSetL2Bridge(ctx, &l2.SwInterfaceSetL2Bridge{
		RxSwIfIndex: swIfIndex,
		BdID:        bdID,
		PortType:    l2.L2_API_PORT_TYPE_NORMAL,
		Enable:      true,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("attach VNF memif to bridge domain: %w", err)
	}
	if err := checkVPPRetval("attach VNF memif to bridge domain", l2Reply.Retval); err != nil {
		rollback()
		return 0, err
	}

	return uint32(swIfIndex), nil
}

// DeleteMemif removes a VPP memif interface.
func (v *VPPNetwork) DeleteMemif(ctx context.Context, swIfIndex uint32) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := memif.NewServiceClient(conn).MemifDelete(ctx, &memif.MemifDelete{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete VNF memif %d: %w", swIfIndex, err)
	}
	return checkVPPRetval("delete VNF memif", reply.Retval)
}

// DeleteMemifByName removes a memif interface by its VPP name, tolerating a
// stale sw_if_index after a VPP restart.
func (v *VPPNetwork) DeleteMemifByName(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	client := interfaces.NewServiceClient(conn)
	stream, err := client.SwInterfaceDump(ctx, &interfaces.SwInterfaceDump{})
	if err != nil {
		return fmt.Errorf("dump VPP interfaces: %w", err)
	}
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read VPP interface dump: %w", err)
		}
		if details.InterfaceName != name {
			continue
		}
		if details.Tag != "" {
			return nil
		}
		reply, err := memif.NewServiceClient(conn).MemifDelete(ctx, &memif.MemifDelete{
			SwIfIndex: details.SwIfIndex,
		})
		if err != nil {
			if isVPPNotFound(err) {
				return nil
			}
			return fmt.Errorf("delete VNF memif %s: %w", name, err)
		}
		return checkVPPRetval("delete VNF memif", reply.Retval)
	}
}

// EnsureRouteTable creates a custom VPP IP FIB/VRF table if it does not exist.
func (v *VPPNetwork) EnsureRouteTable(ctx context.Context, tableID uint32, ipv6 bool, name string) error {
	if tableID == 0 {
		return nil
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := ipapi.NewServiceClient(conn).IPTableAddDel(ctx, &ipapi.IPTableAddDel{
		IsAdd: true,
		Table: ipapi.IPTable{TableID: tableID, IsIP6: ipv6, Name: name},
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return nil
		}
		return fmt.Errorf("create route table %d: %w", tableID, err)
	}
	if reply.Retval != 0 {
		return checkVPPRetval("create route table", reply.Retval)
	}
	return nil
}

// DeleteRouteTable removes a custom VPP IP FIB/VRF table.
func (v *VPPNetwork) DeleteRouteTable(ctx context.Context, tableID uint32, ipv6 bool) error {
	if tableID == 0 {
		return nil
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := ipapi.NewServiceClient(conn).IPTableAddDel(ctx, &ipapi.IPTableAddDel{
		IsAdd: false,
		Table: ipapi.IPTable{TableID: tableID, IsIP6: ipv6},
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete route table %d: %w", tableID, err)
	}
	return checkVPPRetval("delete route table", reply.Retval)
}

// SwIfIndexByName resolves a VPP interface name to its sw_if_index, or 0 when
// the interface does not exist.
func (v *VPPNetwork) SwIfIndexByName(ctx context.Context, name string) (uint32, error) {
	interfaces, err := v.VPPInterfaces(ctx)
	if err != nil {
		return 0, err
	}
	for _, iface := range interfaces {
		if iface.Name == name {
			return iface.SwIfIndex, nil
		}
	}
	return 0, fmt.Errorf("VPP interface %q not found", name)
}

// AddRoute installs a static route in an IP FIB/VRF table. swIfIndex may be 0
// when VPP can resolve the next hop through a connected route in the table;
// egressName selects a non-inventory interface (for example a BVI) by name.
// An empty nextHop with an egress interface installs an attached (glean) route
// that forwards directly into the interface's L2 domain.
//
// VPP decodes sw_if_index 0 as interface 0 (local0), not as "no interface";
// a recursive path must send the ~0 sentinel and resolve its next hop in the
// route's own table via the path table_id.
func (v *VPPNetwork) AddRoute(ctx context.Context, tableID uint32, ipv6 bool, prefix, nextHop, egressName string, swIfIndex uint32, preference uint32) error {
	parsedPrefix, err := ip_types.ParsePrefix(strings.TrimSpace(prefix))
	if err != nil {
		return fmt.Errorf("parse route prefix: %w", err)
	}
	if (parsedPrefix.Address.Af == ip_types.ADDRESS_IP6) != ipv6 {
		return errors.New("route prefix family does not match the table")
	}
	if egressName = strings.TrimSpace(egressName); egressName != "" {
		swIfIndex, err = v.SwIfIndexByName(ctx, egressName)
		if err != nil {
			return fmt.Errorf("resolve egress interface %q: %w", egressName, err)
		}
	}
	var nhAddress ip_types.AddressUnion
	if nextHop = strings.TrimSpace(nextHop); nextHop != "" {
		nh, err := ip_types.ParseAddress(nextHop)
		if err != nil {
			return fmt.Errorf("parse route next hop: %w", err)
		}
		if (nh.Af == ip_types.ADDRESS_IP6) != ipv6 {
			return errors.New("route next-hop family does not match the table")
		}
		nhAddress = nh.Un
	} else if swIfIndex == 0 {
		return errors.New("route needs a next hop or an egress interface")
	}
	proto := fib_types.FIB_API_PATH_NH_PROTO_IP4
	if ipv6 {
		proto = fib_types.FIB_API_PATH_NH_PROTO_IP6
	}
	pathIfIndex := swIfIndex
	pathTableID := uint32(0)
	if pathIfIndex == 0 {
		// Recursive next-hop lookup in this route's own table.
		pathIfIndex = ^uint32(0)
		pathTableID = tableID
	}
	route := ipapi.IPRoute{
		TableID: tableID,
		Prefix:  parsedPrefix,
		Paths: []fib_types.FibPath{{
			SwIfIndex:  pathIfIndex,
			TableID:    pathTableID,
			Type:       fib_types.FIB_API_PATH_TYPE_NORMAL,
			Proto:      proto,
			Preference: uint8(preference),
			Nh:         fib_types.FibPathNh{Address: nhAddress},
		}},
	}
	return v.changeRoute(ctx, true, route)
}

// DeleteRoute removes a static route from an IP FIB/VRF table.
func (v *VPPNetwork) DeleteRoute(ctx context.Context, tableID uint32, ipv6 bool, prefix string) error {
	apiErr := v.deleteRouteAPI(ctx, tableID, ipv6, prefix)
	cliErr := v.deleteRouteCLI(ctx, tableID, prefix)
	if apiErr != nil && cliErr != nil {
		return errors.Join(apiErr, cliErr)
	}
	return nil
}

func (v *VPPNetwork) changeRoute(ctx context.Context, isAdd bool, route ipapi.IPRoute) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()
	reply, err := ipapi.NewServiceClient(conn).IPRouteAddDel(ctx, &ipapi.IPRouteAddDel{
		IsAdd: isAdd,
		Route: route,
	})
	if err != nil {
		if !isAdd && isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("change route: %w", err)
	}
	if err := checkVPPRetval("change route", reply.Retval); err != nil {
		if !isAdd && isVPPNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// SetInterfaceRouteTable binds an interface to an IPv4 or IPv6 FIB/VRF.
func (v *VPPNetwork) SetInterfaceRouteTable(ctx context.Context, swIfIndex uint32, ipv6 bool, tableID uint32) error {
	if swIfIndex == 0 {
		return errors.New("interface index is required")
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := interfaces.NewServiceClient(conn).SwInterfaceSetTable(ctx, &interfaces.SwInterfaceSetTable{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		IsIPv6:    ipv6,
		VrfID:     tableID,
	})
	if err != nil {
		return fmt.Errorf("set interface route table: %w", err)
	}
	return checkVPPRetval("set interface route table", reply.Retval)
}

func (v *VPPNetwork) connect() (*core.Connection, error) {
	conn, err := govpp.Connect(v.Socket)
	if err != nil {
		return nil, fmt.Errorf("connect to VPP API %s: %w", v.Socket, err)
	}
	return conn, nil
}

// setInterfaceMTU sets the L3 and per-protocol MTUs. VPP's default MTU is
// 9000 even when the underlying Linux NIC is 1500; leaving that mismatch in
// place lets VPP emit frames the physical NIC or guest cannot receive.
func setInterfaceMTU(ctx context.Context, conn *core.Connection, swIfIndex uint32, mtu uint32) error {
	if mtu == 0 {
		mtu = defaultInterfaceMTU
	}
	reply, err := interfaces.NewServiceClient(conn).SwInterfaceSetMtu(ctx, &interfaces.SwInterfaceSetMtu{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		Mtu:       []uint32{mtu, mtu, mtu, mtu},
	})
	if err != nil {
		return fmt.Errorf("set interface MTU: %w", err)
	}
	return checkVPPRetval("set interface MTU", reply.Retval)
}

func (v *VPPNetwork) setInterfaceMTUByName(ctx context.Context, name string, mtu uint32) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()
	swIfIndex, err := findInterface(ctx, conn, name)
	if err != nil {
		return err
	}
	return setInterfaceMTU(ctx, conn, swIfIndex, mtu)
}

func setInterfaceTag(ctx context.Context, conn *core.Connection, swIfIndex uint32, tag string) error {
	reply, err := interfaces.NewServiceClient(conn).SwInterfaceTagAddDel(ctx, &interfaces.SwInterfaceTagAddDel{
		IsAdd:     true,
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		Tag:       tag,
	})
	if err != nil {
		return fmt.Errorf("set interface tag: %w", err)
	}
	return checkVPPRetval("set interface tag", reply.Retval)
}

func checkVPPRetval(operation string, retval int32) error {
	if retval != 0 {
		return fmt.Errorf("%s: VPP returned retval %d", operation, retval)
	}
	return nil
}

func isBridgeDomainExists(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "already exists")
}

func isVPPNotFound(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"not found",
		"no such",
		"unknown interface",
		"invalid interface",
		"unknown bridge domain",
		"invalid bridge domain",
		"unknown message",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// InterfaceAddresses returns the IPv4 and IPv6 prefixes currently assigned to
// a VPP interface.
func (v *VPPNetwork) InterfaceAddresses(ctx context.Context, name string) ([]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("interface name is required")
	}
	out, err := v.runCLIRaw(ctx, "show", "interface", "address", name)
	if err != nil {
		return nil, fmt.Errorf("show interface address %s: %w: %s", name, err, out)
	}
	seen := make(map[string]bool)
	addresses := make([]string, 0)
	for _, field := range strings.Fields(out) {
		field = strings.Trim(field, ",")
		if _, _, err := net.ParseCIDR(field); err == nil && !seen[field] {
			seen[field] = true
			addresses = append(addresses, field)
		}
	}
	sort.Strings(addresses)
	return addresses, nil
}

// DHCPClientStatus returns whether VPP has a DHCP client configured on an
// interface and the raw status text.
func (v *VPPNetwork) DHCPClientStatus(ctx context.Context, name string) (bool, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, "", errors.New("interface name is required")
	}
	out, err := v.runCLIRaw(ctx, "show", "dhcp", "client", "intfc", name)
	if err != nil {
		return false, strings.TrimSpace(out), fmt.Errorf("show DHCP client %s: %w", name, err)
	}
	return strings.Contains(out, name), strings.TrimSpace(out), nil
}
