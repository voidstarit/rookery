package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/voidstarit/rookery/internal/cgroup"
)

func TestCgroupSettingsRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "cgroup.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	initial, err := st.GetCgroupSettings(ctx)
	if err != nil {
		t.Fatalf("get initial settings: %v", err)
	}
	if initial.Enabled || !initial.UpdatedAt.IsZero() {
		t.Fatalf("initial settings = %+v", initial)
	}

	settings := cgroup.Settings{
		Enabled: true, CPUs: "0-3", Mems: "0", MemoryMaxBytes: 8 << 30, CPUWeight: 500,
		IOLimits: []cgroup.IOLimit{{Device: "/dev/sda", ReadBPS: 10 << 20, Weight: 200}},
	}
	saved, err := st.SetCgroupSettings(ctx, settings)
	if err != nil {
		t.Fatalf("set settings: %v", err)
	}
	if !saved.Enabled || saved.CPUs != settings.CPUs || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved settings = %+v", saved)
	}
	loaded, err := st.GetCgroupSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if loaded.Enabled != settings.Enabled || loaded.MemoryMaxBytes != settings.MemoryMaxBytes ||
		loaded.CPUWeight != settings.CPUWeight || len(loaded.IOLimits) != 1 ||
		loaded.IOLimits[0].Device != "/dev/sda" || loaded.IOLimits[0].Weight != 200 {
		t.Fatalf("loaded settings = %+v, want %+v", loaded, settings)
	}
}

func TestVMCgroupRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "vm-cgroup.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	disk, err := st.CreateDisk(ctx, Disk{Name: "root", SizeGB: 10, VG: "vg0", ThinPool: "pool"})
	if err != nil {
		t.Fatalf("create disk: %v", err)
	}
	vm, err := st.CreateVM(ctx, VM{
		Name: "cgroup-vm", VCPUs: 2, MemoryMB: 512, DiskID: &disk.ID,
		Cgroup: cgroup.VMLimits{
			Enabled: true, CPUs: "0-1", MemoryMaxBytes: 1 << 30, CPUWeight: 300,
			IOLimits: []cgroup.IOLimit{{Device: disk.Device, WriteBPS: 5 << 20}},
		},
	})
	if err != nil {
		t.Fatalf("create VM: %v", err)
	}
	if !vm.Cgroup.Enabled || vm.Cgroup.CPUs != "0-1" || len(vm.Cgroup.IOLimits) != 1 {
		t.Fatalf("created VM cgroup = %+v", vm.Cgroup)
	}

	vm.Cgroup = cgroup.VMLimits{Enabled: true, CPUs: "2", CPUWeight: 700}
	updated, err := st.UpdateVM(ctx, vm)
	if err != nil {
		t.Fatalf("update VM: %v", err)
	}
	if updated.Cgroup.CPUs != "2" || updated.Cgroup.CPUWeight != 700 || len(updated.Cgroup.IOLimits) != 0 ||
		updated.Cgroup.MemoryMaxBytes != 0 {
		t.Fatalf("updated VM cgroup = %+v", updated.Cgroup)
	}
}
