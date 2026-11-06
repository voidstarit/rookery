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
