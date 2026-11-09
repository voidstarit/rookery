package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/diskthrottle"
)

func TestDiskThrottleClassCorruptAndDanglingReferences(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir() + "/corrupt.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	class, err := st.CreateDiskThrottleClass(ctx, DiskThrottleClass{
		Name:   "standard",
		Limits: diskthrottle.Limits{BPSRead: 1_000_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := st.CreateDisk(ctx, Disk{
		Name: "data", SizeGB: 20, VG: "vg0", ThinPool: "rookery",
		ThrottleClassID: &class.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateVM(ctx, VM{
		Name: "vm", Status: StatusStopped, VCPUs: 1, MemoryMB: 512, DiskID: &disk.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE disk_throttle_classes SET limits = 'not-json' WHERE id = ?`, class.ID); err != nil {
		t.Fatal(err)
	}
	corrupt, err := st.GetDiskThrottleClass(ctx, class.ID)
	if err != nil {
		t.Fatal(err)
	}
	if corrupt.ValidationError == "" {
		t.Fatal("corrupt class did not expose validation_error")
	}
	got, err := st.GetVM(ctx, vm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Disks) != 1 || got.Disks[0].ThrottleError == "" || got.Disks[0].Throttle != nil {
		t.Fatalf("corrupt VM disk = %+v", got.Disks)
	}

	if _, err := st.db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE disks SET throttle_class_id = 999 WHERE id = ?`, disk.ID); err != nil {
		t.Fatal(err)
	}
	dangling, err := st.GetVM(ctx, vm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(dangling.Disks) != 1 || dangling.Disks[0].ThrottleError == "" {
		t.Fatalf("dangling class disk = %+v", dangling.Disks)
	}
}

func TestDiskThrottleClassValidationAndErrorBranches(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir() + "/errors.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.CreateDiskThrottleClass(ctx, DiskThrottleClass{
		Name: "", Limits: diskthrottle.Limits{BPSRead: 1},
	}); err == nil {
		t.Fatal("empty name accepted")
	}
	if _, err := st.CreateDiskThrottleClass(ctx, DiskThrottleClass{
		Name: strings.Repeat("a", 128), Limits: diskthrottle.Limits{BPSRead: 1},
	}); err == nil {
		t.Fatal("long name accepted")
	}
	if _, err := st.CreateDiskThrottleClass(ctx, DiskThrottleClass{
		Name: "-bad", Limits: diskthrottle.Limits{BPSRead: 1},
	}); err == nil {
		t.Fatal("invalid start accepted")
	}
	if _, err := st.CreateDiskThrottleClass(ctx, DiskThrottleClass{
		Name: "bad name", Limits: diskthrottle.Limits{BPSRead: 1},
	}); err == nil {
		t.Fatal("invalid character accepted")
	}
	if _, err := st.CreateDiskThrottleClass(ctx, DiskThrottleClass{
		Name: "desc", Description: strings.Repeat("a", 2001), Limits: diskthrottle.Limits{BPSRead: 1},
	}); err == nil {
		t.Fatal("long description accepted")
	}
	if _, err := st.CreateDiskThrottleClass(ctx, DiskThrottleClass{
		Name: "invalid", Limits: diskthrottle.Limits{BPSRead: 1, BPSTotal: 1},
	}); !errors.Is(err, ErrDiskThrottleClassInvalid) {
		t.Fatalf("invalid limits error = %v", err)
	}
	if _, err := st.GetDiskThrottleClass(ctx, 9999); !errors.Is(err, ErrDiskThrottleClassNotFound) {
		t.Fatalf("get missing error = %v", err)
	}

	class := DiskThrottleClass{Name: "one", Limits: diskthrottle.Limits{BPSRead: 1_000_000}}
	created, err := st.CreateDiskThrottleClass(ctx, class)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateDiskThrottleClass(ctx, class); !errors.Is(err, ErrDiskThrottleClassNameTaken) {
		t.Fatalf("duplicate error = %v", err)
	}
	second, err := st.CreateDiskThrottleClass(ctx, DiskThrottleClass{Name: "two", Limits: diskthrottle.Limits{BPSRead: 1}})
	if err != nil {
		t.Fatal(err)
	}
	second.Name = "one"
	if _, err := st.UpdateDiskThrottleClass(ctx, second); !errors.Is(err, ErrDiskThrottleClassNameTaken) {
		t.Fatalf("duplicate update error = %v", err)
	}
	second.Name = "two"
	second.ID = 9999
	if _, err := st.UpdateDiskThrottleClass(ctx, second); !errors.Is(err, ErrDiskThrottleClassNotFound) {
		t.Fatalf("missing update error = %v", err)
	}
	second.ID = created.ID + 1
	second.Name = ""
	if _, err := st.UpdateDiskThrottleClass(ctx, second); err == nil {
		t.Fatal("invalid update name accepted")
	}
	second.Name = "two"
	second.Limits = diskthrottle.Limits{BPSRead: 1, BPSTotal: 1}
	if _, err := st.UpdateDiskThrottleClass(ctx, second); !errors.Is(err, ErrDiskThrottleClassInvalid) {
		t.Fatalf("invalid update limits error = %v", err)
	}
	second.Limits = diskthrottle.Limits{BPSRead: 1}
	second.Description = strings.Repeat("a", 2001)
	if _, err := st.UpdateDiskThrottleClass(ctx, second); err == nil {
		t.Fatal("long update description accepted")
	}
	if err := st.DeleteDiskThrottleClass(ctx, 9999); !errors.Is(err, ErrDiskThrottleClassNotFound) {
		t.Fatalf("delete missing error = %v", err)
	}
	if names, err := st.ListDiskNamesByThrottleClass(ctx, created.ID, 0); err != nil || len(names) != 0 {
		t.Fatalf("names = %v, %v", names, err)
	}
	if _, err := st.SetDiskThrottleClass(ctx, 9999, nil); !errors.Is(err, ErrDiskNotFound) {
		t.Fatalf("clear missing disk error = %v", err)
	}
	negative := int64(-1)
	if _, err := st.SetDiskThrottleClass(ctx, 1, &negative); !errors.Is(err, ErrDiskThrottleClassInvalid) {
		t.Fatalf("negative class error = %v", err)
	}
	missingClass := int64(9999)
	if _, err := st.SetDiskThrottleClass(ctx, 1, &missingClass); !errors.Is(err, ErrDiskThrottleClassNotFound) {
		t.Fatalf("missing class error = %v", err)
	}
	if _, err := st.SetDiskThrottleClass(ctx, 9999, &created.ID); !errors.Is(err, ErrDiskNotFound) {
		t.Fatalf("missing disk assign error = %v", err)
	}

	disk, err := st.CreateDisk(ctx, Disk{
		Name: "template", SizeGB: 5, VG: "vg0", ThinPool: "pool",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetDiskTemplate(ctx, disk.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetDiskThrottleClass(ctx, disk.ID, &created.ID); !errors.Is(err, ErrDiskThrottleClassOnTemplate) {
		t.Fatalf("template class error = %v", err)
	}

	corrupt, err := st.CreateDiskThrottleClass(ctx, DiskThrottleClass{Name: "corrupt", Limits: diskthrottle.Limits{BPSRead: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE disk_throttle_classes SET limits = '{}' WHERE id = ?`, corrupt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetDiskThrottleClass(ctx, disk.ID, &corrupt.ID); !errors.Is(err, ErrDiskThrottleClassInvalid) {
		t.Fatalf("corrupt class assign error = %v", err)
	}
	if _, err := decodeDiskThrottleLimits("not-json"); err == "" {
		t.Fatal("invalid JSON decoded")
	}
	if _, err := decodeDiskThrottleLimits("{}"); err == "" {
		t.Fatal("empty limits decoded")
	}
}
