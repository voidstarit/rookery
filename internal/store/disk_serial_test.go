package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestVMAndLightVMDiskSerials(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	first := createDisk(t, st, "serial-root")
	second := createDisk(t, st, "serial-data")
	vm, err := st.CreateVM(ctx, store.VM{
		Name: "serial-vm", VCPUs: 1, MemoryMB: 128,
		DiskIDs:     []int64{first.ID, second.ID},
		DiskSerials: map[int64]string{first.ID: "root", second.ID: "data"},
	})
	if err != nil {
		t.Fatalf("create VM: %v", err)
	}
	assertVMDiskSerials(t, vm.Disks, map[int64]string{first.ID: "root", second.ID: "data"})

	vm, err = st.SetVMDisksWithSerials(ctx, vm.ID, []int64{first.ID, second.ID}, map[int64]string{first.ID: "os"})
	if err != nil {
		t.Fatalf("update VM serials: %v", err)
	}
	assertVMDiskSerials(t, vm.Disks, map[int64]string{first.ID: "os"})

	// A serial-only update keeps the current attachment order.
	vm, err = st.SetVMDisksWithSerials(ctx, vm.ID, []int64{first.ID, second.ID}, map[int64]string{second.ID: "logs"})
	if err != nil {
		t.Fatalf("replace VM serials: %v", err)
	}
	assertVMDiskSerials(t, vm.Disks, map[int64]string{second.ID: "logs"})

	if _, err := st.SetVMDisksWithSerials(ctx, vm.ID, []int64{first.ID}, map[int64]string{second.ID: "data"}); !errors.Is(err, store.ErrInvalidDiskSerials) {
		t.Fatalf("detached serial error = %v", err)
	}
	if _, err := st.SetVMDisksWithSerials(ctx, vm.ID, []int64{first.ID}, map[int64]string{first.ID: "bad serial"}); !errors.Is(err, store.ErrInvalidDiskSerials) {
		t.Fatalf("invalid serial error = %v", err)
	}
	if _, err := st.SetVMDisksWithSerials(ctx, vm.ID, []int64{first.ID}, map[int64]string{first.ID: strings.Repeat("x", 21)}); !errors.Is(err, store.ErrInvalidDiskSerials) {
		t.Fatalf("long serial error = %v", err)
	}
	if _, err := st.SetVMDisksWithSerials(ctx, vm.ID, []int64{first.ID, second.ID}, map[int64]string{first.ID: "same", second.ID: "same"}); !errors.Is(err, store.ErrInvalidDiskSerials) {
		t.Fatalf("duplicate serial error = %v", err)
	}

	third := createDisk(t, st, "serial-lightvm-root")
	fourth := createDisk(t, st, "serial-lightvm-data")
	kernel := createKernel(t, st, "serial-kernel")
	initramfs := createInitramfs(t, st, "serial-initramfs")
	lightvm, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "serial-lightvm", VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh",
		DiskIDs:     []int64{third.ID, fourth.ID},
		DiskSerials: map[int64]string{third.ID: "root", fourth.ID: "data"},
	})
	if err != nil {
		t.Fatalf("create LightVM: %v", err)
	}
	assertVMDiskSerials(t, lightvm.Disks, map[int64]string{third.ID: "root", fourth.ID: "data"})

	update := lightvm
	update.DiskIDs = nil
	update.DiskSerials = map[int64]string{third.ID: "os", fourth.ID: "logs"}
	lightvm, err = st.UpdateLightVM(ctx, update)
	if err != nil {
		t.Fatalf("update LightVM serials: %v", err)
	}
	assertVMDiskSerials(t, lightvm.Disks, map[int64]string{third.ID: "os", fourth.ID: "logs"})
}

func assertVMDiskSerials(t *testing.T, disks []store.VMDisk, want map[int64]string) {
	t.Helper()
	seen := make(map[int64]bool, len(want))
	for _, disk := range disks {
		if disk.Serial != want[disk.ID] {
			t.Fatalf("disk %d serial = %q, want %q", disk.ID, disk.Serial, want[disk.ID])
		}
		if _, ok := want[disk.ID]; ok {
			seen[disk.ID] = true
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("disks = %+v, want serials for %v", disks, want)
	}
}
