package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	// BlockDeviceDisk is a whole physical or virtual disk.
	BlockDeviceDisk = "disk"
	// BlockDevicePartition is one partition on a disk.
	BlockDevicePartition = "partition"
	// BlockDeviceLV is a Linux device-mapper volume, such as an LVM logical
	// volume.
	BlockDeviceLV = "lv"
	// BlockDeviceRAID is a Linux software RAID array.
	BlockDeviceRAID = "raid"
	// BlockDeviceLoop is a loop device backed by a regular file.
	BlockDeviceLoop = "loop"
	// BlockDeviceRAM is a brd ramdisk.
	BlockDeviceRAM = "ram"
	// BlockDeviceZRAM is a compressed RAM block device.
	BlockDeviceZRAM = "zram"
)

// BlockDevice describes a whole Linux block device for cgroup I/O limits.
type BlockDevice struct {
	Name       string
	Path       string
	Model      string
	SizeBytes  uint64
	Major      uint32
	Minor      uint32
	Rotational bool
}

// AttachableBlockDevice describes a host disk, partition, logical volume,
// RAID array, or virtual block device that can be passed through to a VM.
// Available is false when the device must not be handed to a guest, for
// example because the host has it mounted.
type AttachableBlockDevice struct {
	Name       string
	Path       string
	Kind       string
	Model      string
	SizeBytes  uint64
	Major      uint32
	Minor      uint32
	Rotational bool
	Available  bool
	Reason     string
}

type attachCandidate struct {
	name      string
	base      string
	real      string
	partition bool
	device    AttachableBlockDevice
}

// ListBlockDevices returns whole block devices from /sys/class/block. Partitions
// are omitted; loop, ram, and zram devices are included.
func ListBlockDevices() ([]BlockDevice, error) {
	entries, err := os.ReadDir("/sys/class/block")
	if err != nil {
		return nil, fmt.Errorf("list block devices: %w", err)
	}
	devices := make([]BlockDevice, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if skipBlockDevice(name) {
			continue
		}
		base := filepath.Join("/sys/class/block", name)
		if _, err := os.Stat(filepath.Join(base, "partition")); err == nil {
			continue // Partition, not a whole device.
		}
		devPath := filepath.Join("/dev", name)
		info, err := os.Stat(devPath)
		if err != nil || info.IsDir() {
			continue
		}
		device := BlockDevice{Name: name, Path: stableBlockDevicePath(base, devPath)}
		if dev, err := os.ReadFile(filepath.Join(base, "dev")); err == nil {
			major, minor, ok := parseDeviceNumber(strings.TrimSpace(string(dev)))
			if !ok {
				continue
			}
			device.Major, device.Minor = major, minor
		}
		if size, err := readUint(filepath.Join(base, "size")); err == nil {
			device.SizeBytes = size * 512
		}
		if rotational, err := readUint(filepath.Join(base, "queue/rotational")); err == nil {
			device.Rotational = rotational != 0
		}
		device.Model = blockDeviceModel(base)
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	return devices, nil
}

// ListAttachableBlockDevices returns whole disks, partitions, device-mapper
// volumes, and software RAID arrays that can be passed through to a VM. Each
// device carries a reason when it is currently used by the host.
func ListAttachableBlockDevices() ([]AttachableBlockDevice, error) {
	entries, err := os.ReadDir("/sys/class/block")
	if err != nil {
		return nil, fmt.Errorf("list attachable block devices: %w", err)
	}

	mounts, swaps := hostBusyDeviceNumbers()
	candidates := make([]attachCandidate, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if skipBlockDevice(name) {
			continue
		}
		base := filepath.Join("/sys/class/block", name)
		devPath := filepath.Join("/dev", name)
		info, err := os.Stat(devPath)
		if err != nil || info.IsDir() {
			continue
		}
		major, minor, ok := sysfsDeviceNumbers(base)
		if !ok {
			continue
		}
		real, _ := filepath.EvalSymlinks(base)
		device := AttachableBlockDevice{
			Name: name, Path: stableBlockDevicePath(base, devPath),
			Kind: blockDeviceKind(base), Model: blockDeviceModel(base),
			Major: major, Minor: minor, Available: true,
		}
		if size, err := readUint(filepath.Join(base, "size")); err == nil {
			device.SizeBytes = size * 512
		}
		if rotational, err := readUint(filepath.Join(base, "queue/rotational")); err == nil {
			device.Rotational = rotational != 0
		}
		_, partition := os.Stat(filepath.Join(base, "partition"))
		candidates = append(candidates, attachCandidate{
			name: name, base: base, real: real,
			partition: partition == nil, device: device,
		})
	}

	devices := make([]AttachableBlockDevice, 0, len(candidates))
	for _, item := range candidates {
		device := item.device
		device.Reason = blockDeviceBusyReason(item.base, item.real, item.device, item.partition, candidates, mounts, swaps)
		device.Available = device.Reason == ""
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].Kind != devices[j].Kind {
			return blockKindOrder(devices[i].Kind) < blockKindOrder(devices[j].Kind)
		}
		return devices[i].Name < devices[j].Name
	})
	return devices, nil
}

// InspectAttachableBlockDevice resolves path and reports the device together
// with whether it may be passed through. The returned Path preserves the path
// supplied by the caller so registrations can keep a stable /dev/mapper or
// /dev/disk/by-id name.
func InspectAttachableBlockDevice(path string) (AttachableBlockDevice, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return AttachableBlockDevice{}, errors.New("host device path is required")
	}
	if !filepath.IsAbs(path) {
		return AttachableBlockDevice{}, errors.New("host device path must be absolute")
	}
	cleaned := filepath.Clean(path)
	info, err := os.Stat(cleaned)
	if err != nil {
		return AttachableBlockDevice{}, fmt.Errorf("stat host device %s: %w", cleaned, err)
	}
	if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
		return AttachableBlockDevice{}, fmt.Errorf("%s is not a block device", cleaned)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return AttachableBlockDevice{}, fmt.Errorf("read device number for %s", cleaned)
	}
	major, minor := unix.Major(stat.Rdev), unix.Minor(stat.Rdev)
	devices, err := ListAttachableBlockDevices()
	if err != nil {
		return AttachableBlockDevice{}, err
	}
	for _, device := range devices {
		if device.Major != major || device.Minor != minor {
			continue
		}
		device.Path = cleaned
		return device, nil
	}
	return AttachableBlockDevice{}, fmt.Errorf("%s is not a supported host block device", cleaned)
}

// ValidateAttachableBlockDevice reports whether path can be passed through to
// a VM. It rejects missing devices, non-block files, host-mapped devices, and
// devices the API process cannot open for writing.
func ValidateAttachableBlockDevice(path string) (AttachableBlockDevice, error) {
	device, err := InspectAttachableBlockDevice(path)
	if err != nil {
		return AttachableBlockDevice{}, err
	}
	if !device.Available {
		return device, fmt.Errorf("%s is in use by the host: %s", device.Path, device.Reason)
	}
	file, err := os.OpenFile(device.Path, os.O_RDWR, 0)
	if err != nil {
		return device, fmt.Errorf("open %s read-write: %w", device.Path, err)
	}
	if err := file.Close(); err != nil {
		return device, fmt.Errorf("close %s: %w", device.Path, err)
	}
	return device, nil
}

// DevicesOverlap reports whether two paths refer to the same block device or
// to a whole disk and one of its partitions. Passing both to different guests
// would let them write to the same sectors.
func DevicesOverlap(left, right string) bool {
	if SameBlockDevice(left, right) {
		return true
	}
	leftPath, leftOK := blockDeviceSysfsPath(left)
	rightPath, rightOK := blockDeviceSysfsPath(right)
	if !leftOK || !rightOK {
		return false
	}
	return strings.HasPrefix(leftPath, rightPath+string(os.PathSeparator)) ||
		strings.HasPrefix(rightPath, leftPath+string(os.PathSeparator))
}

// SameBlockDevice reports whether two paths refer to the same block device.
func SameBlockDevice(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

// blockDeviceSysfsPath resolves a device path to its /sys/class/block target.
// Partitions resolve to a path nested below the whole disk.
func blockDeviceSysfsPath(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	major, minor := unix.Major(stat.Rdev), unix.Minor(stat.Rdev)
	entries, err := os.ReadDir("/sys/class/block")
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		base := filepath.Join("/sys/class/block", entry.Name())
		entryMajor, entryMinor, ok := sysfsDeviceNumbers(base)
		if !ok || entryMajor != major || entryMinor != minor {
			continue
		}
		real, err := filepath.EvalSymlinks(base)
		if err != nil {
			return "", false
		}
		return real, true
	}
	return "", false
}

func blockDeviceBusyReason(base, real string, device AttachableBlockDevice, partition bool, candidates []attachCandidate, mounts, swaps map[uint64]string) string {
	key := deviceKey(device.Major, device.Minor)
	if where, ok := mounts[key]; ok {
		return "mounted at " + where
	}
	if _, ok := swaps[key]; ok {
		return "used as swap"
	}
	if readonly, err := readUint(filepath.Join(base, "ro")); err == nil && readonly != 0 {
		return "read-only"
	}
	if holders := readDirNames(filepath.Join(base, "holders")); len(holders) > 0 {
		return "backs " + strings.Join(holders, ", ")
	}
	if !partition {
		for _, child := range candidates {
			if !child.partition || filepath.Dir(child.real) != real {
				continue
			}
			childKey := deviceKey(child.device.Major, child.device.Minor)
			if where, ok := mounts[childKey]; ok {
				return "partition " + child.name + " is mounted at " + where
			}
			if _, ok := swaps[childKey]; ok {
				return "partition " + child.name + " is used as swap"
			}
		}
	}
	return ""
}

func sysfsDeviceNumbers(base string) (uint32, uint32, bool) {
	value, err := os.ReadFile(filepath.Join(base, "dev"))
	if err != nil {
		return 0, 0, false
	}
	return parseDeviceNumber(strings.TrimSpace(string(value)))
}

func blockDeviceKind(base string) string {
	// Check partitions first so a partition of a loop/ram/zram device is still
	// reported as a partition.
	if _, err := os.Stat(filepath.Join(base, "partition")); err == nil {
		return BlockDevicePartition
	}
	name := filepath.Base(base)
	switch {
	case strings.HasPrefix(name, "loop"):
		return BlockDeviceLoop
	case strings.HasPrefix(name, "zram"):
		return BlockDeviceZRAM
	case strings.HasPrefix(name, "ram"):
		return BlockDeviceRAM
	}
	if _, err := os.Stat(filepath.Join(base, "dm/name")); err == nil {
		return BlockDeviceLV
	}
	if _, err := os.Stat(filepath.Join(base, "md")); err == nil {
		return BlockDeviceRAID
	}
	return BlockDeviceDisk
}

func blockKindOrder(kind string) int {
	switch kind {
	case BlockDeviceDisk:
		return 0
	case BlockDevicePartition:
		return 1
	case BlockDeviceLV:
		return 2
	case BlockDeviceRAID:
		return 3
	case BlockDeviceLoop:
		return 4
	case BlockDeviceRAM:
		return 5
	case BlockDeviceZRAM:
		return 6
	default:
		return 7
	}
}

// hostBusyDeviceNumbers returns mounted and swap devices keyed by major:minor.
// Mount points and swap paths are included for actionable error messages.
func hostBusyDeviceNumbers() (map[uint64]string, map[uint64]string) {
	mounts := make(map[uint64]string)
	swaps := make(map[uint64]string)

	if data, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}
			major, minor, ok := parseDeviceNumber(fields[2])
			if !ok {
				continue
			}
			mounts[deviceKey(major, minor)] = fields[4]
		}
	}

	if data, err := os.ReadFile("/proc/swaps"); err == nil {
		for index, line := range strings.Split(string(data), "\n") {
			if index == 0 {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			info, err := os.Stat(fields[0])
			if err != nil {
				continue
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				continue
			}
			swaps[deviceKey(unix.Major(stat.Rdev), unix.Minor(stat.Rdev))] = fields[0]
		}
	}

	return mounts, swaps
}

func deviceKey(major, minor uint32) uint64 {
	return uint64(major)<<32 | uint64(minor)
}

func readDirNames(path string) []string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func skipBlockDevice(name string) bool {
	// loop, ram, and zram are virtual devices but are still useful in the
	// inventory (loop for file-backed images, ram/zram for memory-backed
	// volumes), so they are listed instead of filtered out.
	for _, prefix := range []string{"fd", "sr"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// stableBlockDevicePath prefers a persistent device-mapper or by-id path so
// registrations keep referring to the same volume after a reboot.
func stableBlockDevicePath(sysfsBase, devPath string) string {
	if data, err := os.ReadFile(filepath.Join(sysfsBase, "dm/name")); err == nil {
		candidate := filepath.Join("/dev/mapper", strings.TrimSpace(string(data)))
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	entries, err := os.ReadDir("/dev/disk/by-id")
	if err != nil {
		return devPath
	}
	for _, entry := range entries {
		candidate := filepath.Join("/dev/disk/by-id", entry.Name())
		target, err := filepath.EvalSymlinks(candidate)
		if err != nil || target != devPath {
			continue
		}
		return candidate
	}
	return devPath
}

func blockDeviceModel(base string) string {
	if data, err := os.ReadFile(filepath.Join(base, "device/model")); err == nil {
		if model := strings.TrimSpace(string(data)); model != "" {
			return model
		}
	}
	if data, err := os.ReadFile(filepath.Join(base, "dm/name")); err == nil {
		return strings.TrimSpace(string(data))
	}
	return ""
}

func parseDeviceNumber(value string) (uint32, uint32, bool) {
	majorText, minorText, ok := strings.Cut(value, ":")
	if !ok {
		return 0, 0, false
	}
	major, err := strconv.ParseUint(strings.TrimSpace(majorText), 10, 32)
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.ParseUint(strings.TrimSpace(minorText), 10, 32)
	if err != nil {
		return 0, 0, false
	}
	return uint32(major), uint32(minor), true
}

func readUint(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}
