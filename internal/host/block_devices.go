package host

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

// ListBlockDevices returns whole block devices from /sys/class/block. Partitions
// and pseudo devices such as loop and zram are omitted.
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

func skipBlockDevice(name string) bool {
	for _, prefix := range []string{"loop", "ram", "zram", "fd", "sr"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// stableBlockDevicePath prefers a persistent device-mapper or by-id path so
// cgroup I/O rules keep referring to the same volume after a reboot.
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
