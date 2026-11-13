package host

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestListAttachableBlockDevices(t *testing.T) {
	devices, err := ListAttachableBlockDevices()
	if err != nil {
		t.Fatalf("list attachable block devices: %v", err)
	}
	if len(devices) == 0 {
		t.Skip("no block devices on this host")
	}
	seen := make(map[string]struct{}, len(devices))
	for _, device := range devices {
		if device.Name == "" || device.Kind == "" {
			t.Fatalf("incomplete device = %+v", device)
		}
		if !filepath.IsAbs(device.Path) {
			t.Fatalf("device path %q is not absolute", device.Path)
		}
		if device.Available != (device.Reason == "") {
			t.Fatalf("availability mismatch for %+v", device)
		}
		key := fmt.Sprintf("%d:%d", device.Major, device.Minor)
		if _, ok := seen[key]; ok {
			t.Fatalf("duplicate device number %s", key)
		}
		seen[key] = struct{}{}
	}
}

func TestInspectAttachableBlockDeviceRejectsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-device")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectAttachableBlockDevice(path); err == nil {
		t.Fatal("regular file was accepted as a host block device")
	}
	if _, err := InspectAttachableBlockDevice("relative/path"); err == nil {
		t.Fatal("relative path was accepted as a host block device")
	}
}

func TestSameBlockDeviceFollowsLinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "device")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if !SameBlockDevice(target, link) {
		t.Fatal("same file was not recognized through a symlink")
	}
	if SameBlockDevice(target, filepath.Join(dir, "missing")) {
		t.Fatal("missing path matched")
	}
}

func TestBlockDeviceKind(t *testing.T) {
	cases := []struct {
		name    string
		dirName string
		prepare func(string)
		want    string
	}{
		{name: "partition", dirName: "sda1", prepare: func(base string) { _ = os.WriteFile(filepath.Join(base, "partition"), nil, 0o600) }, want: BlockDevicePartition},
		{name: "lv", dirName: "dm-0", prepare: func(base string) {
			_ = os.MkdirAll(filepath.Join(base, "dm"), 0o755)
			_ = os.WriteFile(filepath.Join(base, "dm", "name"), []byte("vg-lv"), 0o600)
		}, want: BlockDeviceLV},
		{name: "raid", dirName: "md0", prepare: func(base string) { _ = os.MkdirAll(filepath.Join(base, "md"), 0o755) }, want: BlockDeviceRAID},
		{name: "loop", dirName: "loop0", prepare: func(string) {}, want: BlockDeviceLoop},
		{name: "loop-partition", dirName: "loop0p1", prepare: func(base string) { _ = os.WriteFile(filepath.Join(base, "partition"), nil, 0o600) }, want: BlockDevicePartition},
		{name: "ram", dirName: "ram0", prepare: func(string) {}, want: BlockDeviceRAM},
		{name: "zram", dirName: "zram0", prepare: func(string) {}, want: BlockDeviceZRAM},
		{name: "disk", dirName: "sda", prepare: func(string) {}, want: BlockDeviceDisk},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), test.dirName)
			if err := os.MkdirAll(base, 0o755); err != nil {
				t.Fatal(err)
			}
			test.prepare(base)
			if got := blockDeviceKind(base); got != test.want {
				t.Fatalf("blockDeviceKind = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBlockDeviceBusyReason(t *testing.T) {
	device := AttachableBlockDevice{Major: 8, Minor: 0}
	t.Run("mounted", func(t *testing.T) {
		got := blockDeviceBusyReason(t.TempDir(), "", device, false, nil,
			map[uint64]string{deviceKey(8, 0): "/mnt/data"}, nil)
		if got != "mounted at /mnt/data" {
			t.Fatalf("reason = %q", got)
		}
	})
	t.Run("swap", func(t *testing.T) {
		got := blockDeviceBusyReason(t.TempDir(), "", device, false, nil, nil,
			map[uint64]string{deviceKey(8, 0): "/dev/sda"})
		if got != "used as swap" {
			t.Fatalf("reason = %q", got)
		}
	})
	t.Run("read-only", func(t *testing.T) {
		base := t.TempDir()
		if err := os.WriteFile(filepath.Join(base, "ro"), []byte("1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := blockDeviceBusyReason(base, "", device, false, nil, nil, nil); got != "read-only" {
			t.Fatalf("reason = %q", got)
		}
	})
	t.Run("holders", func(t *testing.T) {
		base := t.TempDir()
		if err := os.MkdirAll(filepath.Join(base, "holders", "dm-9"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := blockDeviceBusyReason(base, "", device, false, nil, nil, nil); got != "backs dm-9" {
			t.Fatalf("reason = %q", got)
		}
	})
	t.Run("mounted partition", func(t *testing.T) {
		real := filepath.Join(t.TempDir(), "disk")
		partReal := filepath.Join(real, "part1")
		if err := os.MkdirAll(partReal, 0o755); err != nil {
			t.Fatal(err)
		}
		candidates := []attachCandidate{{
			name: "sda1", real: partReal, partition: true,
			device: AttachableBlockDevice{Major: 8, Minor: 1},
		}}
		got := blockDeviceBusyReason(t.TempDir(), real, device, false, candidates,
			map[uint64]string{deviceKey(8, 1): "/data"}, nil)
		if got != "partition sda1 is mounted at /data" {
			t.Fatalf("reason = %q", got)
		}
	})
	t.Run("swap partition", func(t *testing.T) {
		real := filepath.Join(t.TempDir(), "disk")
		partReal := filepath.Join(real, "part1")
		if err := os.MkdirAll(partReal, 0o755); err != nil {
			t.Fatal(err)
		}
		candidates := []attachCandidate{{
			name: "sda1", real: partReal, partition: true,
			device: AttachableBlockDevice{Major: 8, Minor: 1},
		}}
		got := blockDeviceBusyReason(t.TempDir(), real, device, false, candidates, nil,
			map[uint64]string{deviceKey(8, 1): "swap"})
		if got != "partition sda1 is used as swap" {
			t.Fatalf("reason = %q", got)
		}
	})
	t.Run("available", func(t *testing.T) {
		if got := blockDeviceBusyReason(t.TempDir(), "other", device, false, nil, nil, nil); got != "" {
			t.Fatalf("reason = %q", got)
		}
	})
}

func TestBlockDeviceHelpers(t *testing.T) {
	if skipBlockDevice("loop0") || skipBlockDevice("ram0") || skipBlockDevice("zram0") {
		t.Fatal("virtual block devices should be listed")
	}
	if !skipBlockDevice("fd0") || !skipBlockDevice("sr0") || skipBlockDevice("sda") || skipBlockDevice("dm-0") {
		t.Fatal("skipBlockDevice returned unexpected results")
	}
	if blockKindOrder(BlockDeviceDisk) >= blockKindOrder(BlockDevicePartition) ||
		blockKindOrder(BlockDevicePartition) >= blockKindOrder(BlockDeviceLV) ||
		blockKindOrder(BlockDeviceLV) >= blockKindOrder(BlockDeviceRAID) ||
		blockKindOrder(BlockDeviceRAID) >= blockKindOrder(BlockDeviceLoop) ||
		blockKindOrder(BlockDeviceLoop) >= blockKindOrder(BlockDeviceRAM) ||
		blockKindOrder(BlockDeviceRAM) >= blockKindOrder(BlockDeviceZRAM) {
		t.Fatal("block kind order is not stable")
	}
	if major, minor, ok := parseDeviceNumber("8:16"); !ok || major != 8 || minor != 16 {
		t.Fatalf("parseDeviceNumber = %d:%d, %v", major, minor, ok)
	}
	if _, _, ok := parseDeviceNumber("invalid"); ok {
		t.Fatal("invalid device number accepted")
	}
}

func TestBlockDeviceModelAndReadUint(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "device", "model"), []byte("Samsung\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := blockDeviceModel(base); got != "Samsung" {
		t.Fatalf("model = %q", got)
	}
	dmBase := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dmBase, "dm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dmBase, "dm", "name"), []byte("vg-lv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := blockDeviceModel(dmBase); got != "vg-lv" {
		t.Fatalf("dm model = %q", got)
	}
	sizePath := filepath.Join(t.TempDir(), "size")
	if err := os.WriteFile(sizePath, []byte("42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readUint(sizePath); err != nil || got != 42 {
		t.Fatalf("readUint = %d, %v", got, err)
	}
	if _, err := readUint(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("readUint accepted a missing file")
	}
}

func TestDevicesOverlap(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	if err := os.WriteFile(first, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !DevicesOverlap(first, first) {
		t.Fatal("device did not overlap itself")
	}
	if DevicesOverlap(first, second) {
		t.Fatal("different devices overlapped")
	}
	if DevicesOverlap(first, filepath.Join(dir, "missing")) {
		t.Fatal("missing device overlapped")
	}
}

func TestListBlockDevices(t *testing.T) {
	devices, err := ListBlockDevices()
	if err != nil {
		t.Fatalf("ListBlockDevices: %v", err)
	}
	for _, device := range devices {
		if device.Name == "" || !filepath.IsAbs(device.Path) {
			t.Fatalf("device = %+v", device)
		}
	}
}

func TestValidateAttachableBlockDeviceRejectsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateAttachableBlockDevice(path); err == nil {
		t.Fatal("regular file was accepted for passthrough")
	}
	if _, err := ValidateAttachableBlockDevice(""); err == nil {
		t.Fatal("empty path was accepted for passthrough")
	}
}

func TestReadDirNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	names := readDirNames(dir)
	if len(names) != 2 {
		t.Fatalf("names = %v", names)
	}
	if names := readDirNames(filepath.Join(dir, "missing")); names != nil {
		t.Fatalf("missing dir names = %v", names)
	}
}

func TestInspectAttachableBlockDeviceMissing(t *testing.T) {
	if _, err := InspectAttachableBlockDevice(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing path was accepted")
	}
}

func TestSameBlockDeviceEmpty(t *testing.T) {
	if SameBlockDevice("", "x") || SameBlockDevice("x", "") || SameBlockDevice("", "") {
		t.Fatal("empty path matched")
	}
}

func TestBlockDeviceSysfsPath(t *testing.T) {
	if _, ok := blockDeviceSysfsPath(filepath.Join(t.TempDir(), "missing")); ok {
		t.Fatal("missing path resolved to sysfs")
	}
	devices, err := ListBlockDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) == 0 {
		t.Skip("no block devices on this host")
	}
	path, ok := blockDeviceSysfsPath(devices[0].Path)
	if !ok || path == "" {
		t.Fatalf("sysfs path for %s = %q, %v", devices[0].Path, path, ok)
	}
}

func TestSysfsDeviceNumbersMissing(t *testing.T) {
	if _, _, ok := sysfsDeviceNumbers(t.TempDir()); ok {
		t.Fatal("missing dev file was accepted")
	}
}

func TestBlockKindOrderDefault(t *testing.T) {
	if blockKindOrder("other") != 7 {
		t.Fatalf("default order = %d, want 7", blockKindOrder("other"))
	}
}
