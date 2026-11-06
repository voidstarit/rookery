package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestHostDeviceDiskLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	disk, err := st.CreateDisk(ctx, store.Disk{
		Name:      "host-data",
		SizeGB:    21,
		SizeBytes: (20 << 30) + 1,
		Device:    "/dev/mapper/data",
		Source:    store.DiskSourceHostDevice,
	})
	if err != nil {
		t.Fatalf("create host device disk: %v", err)
	}
	if disk.Source != store.DiskSourceHostDevice || disk.Device != "/dev/mapper/data" ||
		disk.SizeBytes != (20<<30)+1 || disk.SizeGB != 21 {
		t.Fatalf("host device disk = %+v", disk)
	}
	if _, err := st.SetDiskTemplate(ctx, disk.ID, true); err == nil {
		t.Fatal("mark host device as template succeeded")
	}

	vm, err := st.CreateVM(ctx, store.VM{
		Name: "host-vm", Status: store.StatusStopped, VCPUs: 1, MemoryMB: 512,
		DiskIDs: []int64{disk.ID},
	})
	if err != nil {
		t.Fatalf("create vm with host device: %v", err)
	}
	if len(vm.Disks) != 1 || vm.Disks[0].Source != store.DiskSourceHostDevice ||
		vm.Disks[0].Device != "/dev/mapper/data" || vm.Disks[0].SizeBytes != (20<<30)+1 {
		t.Fatalf("vm disks = %+v", vm.Disks)
	}
	if err := st.DeleteDisk(ctx, disk.ID); !errors.Is(err, store.ErrDiskInUse) {
		t.Fatalf("delete attached host device error = %v, want ErrDiskInUse", err)
	}
	if err := st.DeleteVM(ctx, vm.ID); err != nil {
		t.Fatalf("delete vm: %v", err)
	}
	if err := st.DeleteDisk(ctx, disk.ID); err != nil {
		t.Fatalf("delete host device record: %v", err)
	}
}

func TestHostDeviceDiskRejectsDuplicateDevice(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if _, err := st.CreateDisk(ctx, store.Disk{
		Name: "host-data", SizeGB: 1, Device: "/dev/mapper/data", Source: store.DiskSourceHostDevice,
	}); err != nil {
		t.Fatalf("create first host device disk: %v", err)
	}
	_, err := st.CreateDisk(ctx, store.Disk{
		Name: "host-data-alias", SizeGB: 1, Device: "/dev/mapper/data", Source: store.DiskSourceHostDevice,
	})
	if !errors.Is(err, store.ErrDiskDeviceTaken) {
		t.Fatalf("duplicate host device error = %v, want ErrDiskDeviceTaken", err)
	}
}
