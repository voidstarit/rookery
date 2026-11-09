package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/diskthrottle"
	"github.com/voidstarit/rookery/internal/store"
)

func createThrottleClass(t *testing.T, st *store.Store, name string) store.DiskThrottleClass {
	t.Helper()
	class, err := st.CreateDiskThrottleClass(context.Background(), store.DiskThrottleClass{
		Name:        name,
		Description: "test class",
		Limits:      diskthrottle.Limits{BPSRead: 1_000_000, BPSWrite: 500_000},
	})
	if err != nil {
		t.Fatalf("create throttle class: %v", err)
	}
	return class
}

func TestDiskThrottleClassCRUD(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	class := createThrottleClass(t, st, "standard")
	if class.ID == 0 || class.DiskCount != 0 {
		t.Fatalf("created class = %+v", class)
	}
	got, err := st.GetDiskThrottleClass(ctx, class.ID)
	if err != nil || got.Name != "standard" || got.Limits.BPSRead != 1_000_000 {
		t.Fatalf("get class = %+v, %v", got, err)
	}
	classes, err := st.ListDiskThrottleClasses(ctx)
	if err != nil || len(classes) != 1 {
		t.Fatalf("list classes = %+v, %v", classes, err)
	}
	class.Limits = diskthrottle.Limits{BPSTotal: 2_000_000}
	class.Description = "updated"
	updated, err := st.UpdateDiskThrottleClass(ctx, class)
	if err != nil || updated.Limits.BPSTotal != 2_000_000 || updated.Description != "updated" {
		t.Fatalf("updated class = %+v, %v", updated, err)
	}
	if _, err := st.CreateDiskThrottleClass(ctx, store.DiskThrottleClass{
		Name: "STANDARD", Limits: diskthrottle.Limits{BPSTotal: 1},
	}); !errors.Is(err, store.ErrDiskThrottleClassNameTaken) {
		t.Fatalf("duplicate name error = %v", err)
	}
	if err := st.DeleteDiskThrottleClass(ctx, class.ID); err != nil {
		t.Fatalf("delete class: %v", err)
	}
	if _, err := st.GetDiskThrottleClass(ctx, class.ID); !errors.Is(err, store.ErrDiskThrottleClassNotFound) {
		t.Fatalf("get deleted class error = %v", err)
	}
}

func TestDiskThrottleClassLiveReferenceAndTemplate(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	class := createThrottleClass(t, st, "standard")
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "data", SizeGB: 20, VG: "vg0", ThinPool: "rookery",
		ThrottleClassID: &class.ID,
	})
	if err != nil {
		t.Fatalf("create classed disk: %v", err)
	}
	if disk.ThrottleClassID == nil || *disk.ThrottleClassID != class.ID || disk.ThrottleClassName != "standard" {
		t.Fatalf("disk class = %+v", disk)
	}
	vm, err := st.CreateVM(ctx, store.VM{
		Name: "vm", Status: store.StatusStopped, VCPUs: 1, MemoryMB: 512, DiskID: &disk.ID,
	})
	if err != nil {
		t.Fatalf("create vm: %v", err)
	}
	got, err := st.GetVM(ctx, vm.ID)
	if err != nil {
		t.Fatalf("get vm: %v", err)
	}
	if len(got.Disks) != 1 || got.Disks[0].Throttle == nil || got.Disks[0].Throttle.BPSRead != 1_000_000 {
		t.Fatalf("vm disks = %+v", got.Disks)
	}

	class.Limits = diskthrottle.Limits{BPSRead: 2_000_000}
	if _, err := st.UpdateDiskThrottleClass(ctx, class); err != nil {
		t.Fatalf("update class: %v", err)
	}
	got, err = st.GetVM(ctx, vm.ID)
	if err != nil {
		t.Fatalf("get vm after update: %v", err)
	}
	if got.Disks[0].Throttle == nil || got.Disks[0].Throttle.BPSRead != 2_000_000 {
		t.Fatalf("live class limits = %+v", got.Disks[0].Throttle)
	}

	template, err := st.CreateDisk(ctx, store.Disk{
		Name: "template", SizeGB: 20, VG: "vg0", ThinPool: "rookery",
		ThrottleClassID: &class.ID,
	})
	if err != nil {
		t.Fatalf("create classed unattached disk: %v", err)
	}
	template, err = st.SetDiskTemplate(ctx, template.ID, true)
	if err != nil {
		t.Fatalf("mark template: %v", err)
	}
	if template.ThrottleClassID != nil || template.ThrottleClassName != "" {
		t.Fatalf("template retained class: %+v", template)
	}
	if _, err := st.CreateDisk(ctx, store.Disk{
		Name: "bad-template", SizeGB: 20, VG: "vg0", ThinPool: "rookery",
		IsTemplate: true, ThrottleClassID: &class.ID,
	}); !errors.Is(err, store.ErrDiskThrottleClassOnTemplate) {
		t.Fatalf("template class error = %v", err)
	}
}

func TestDiskThrottleClassInUseAndAssignment(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	class := createThrottleClass(t, st, "standard")
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "data", SizeGB: 20, VG: "vg0", ThinPool: "rookery",
		ThrottleClassID: &class.ID,
	})
	if err != nil {
		t.Fatalf("create disk: %v", err)
	}
	if err := st.DeleteDiskThrottleClass(ctx, class.ID); !errors.Is(err, store.ErrDiskThrottleClassInUse) {
		t.Fatalf("delete in-use class error = %v", err)
	}
	names, err := st.ListDiskNamesByThrottleClass(ctx, class.ID, 5)
	if err != nil || len(names) != 1 || names[0] != "data" {
		t.Fatalf("assigned names = %v, %v", names, err)
	}

	before := disk.UpdatedAt
	time.Sleep(2 * time.Millisecond)
	cleared, err := st.SetDiskThrottleClass(ctx, disk.ID, nil)
	if err != nil || cleared.ThrottleClassID != nil {
		t.Fatalf("clear class = %+v, %v", cleared, err)
	}
	if !cleared.UpdatedAt.After(before) {
		t.Fatalf("updated_at did not advance: %v -> %v", before, cleared.UpdatedAt)
	}
	time.Sleep(2 * time.Millisecond)
	assigned, err := st.SetDiskThrottleClass(ctx, disk.ID, &class.ID)
	if err != nil || assigned.ThrottleClassID == nil || *assigned.ThrottleClassID != class.ID {
		t.Fatalf("assign class = %+v, %v", assigned, err)
	}
	if !assigned.UpdatedAt.After(cleared.UpdatedAt) {
		t.Fatalf("assignment updated_at did not advance: %v -> %v", cleared.UpdatedAt, assigned.UpdatedAt)
	}
}

func TestDiskThrottleClassStateGuards(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	class := createThrottleClass(t, st, "standard")
	provisioning, err := st.CreateDisk(ctx, store.Disk{
		Name: "provisioning", SizeGB: 20, VG: "vg0", ThinPool: "rookery",
		DeletionState: store.DiskDeletionProvisioning,
	})
	if err != nil {
		t.Fatalf("create provisioning disk: %v", err)
	}
	if _, err := st.SetDiskThrottleClass(ctx, provisioning.ID, &class.ID); err != nil {
		t.Fatalf("assign to provisioning disk: %v", err)
	}
	if err := st.MarkDiskDeleting(ctx, provisioning.ID); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if _, err := st.SetDiskThrottleClass(ctx, provisioning.ID, &class.ID); !errors.Is(err, store.ErrDiskThrottleClassInDeletion) {
		t.Fatalf("assign to deleting disk error = %v", err)
	}
	if _, err := st.SetDiskThrottleClass(ctx, provisioning.ID, nil); err != nil {
		t.Fatalf("clear deleting disk: %v", err)
	}

	failed, err := st.CreateDisk(ctx, store.Disk{
		Name: "failed", SizeGB: 20, VG: "vg0", ThinPool: "rookery",
		DeletionState: store.DiskDeletionDeleting,
	})
	if err != nil {
		t.Fatalf("create deleting disk: %v", err)
	}
	if err := st.SetDiskDeleteFailed(ctx, failed.ID, "boom"); err != nil {
		t.Fatalf("mark delete failed: %v", err)
	}
	if _, err := st.SetDiskThrottleClass(ctx, failed.ID, &class.ID); !errors.Is(err, store.ErrDiskThrottleClassInDeletion) {
		t.Fatalf("assign to delete_failed disk error = %v", err)
	}
	if _, err := st.SetDiskThrottleClass(ctx, failed.ID, nil); err != nil {
		t.Fatalf("clear delete_failed disk: %v", err)
	}
}
