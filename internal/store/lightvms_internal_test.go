package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func openInternalTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "internal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestDiskInUseErrorFormatting(t *testing.T) {
	var nilError *DiskInUseError
	if got := nilError.Error(); got != ErrDiskInUse.Error() {
		t.Fatalf("nil error = %q", got)
	}
	cases := []struct {
		err  *DiskInUseError
		want string
	}{
		{err: &DiskInUseError{OwnerKind: "vm", OwnerName: "app"}, want: `disk is attached to vm "app"`},
		{err: &DiskInUseError{OwnerKind: "lightvm", OwnerID: "abc"}, want: "disk is attached to lightvm abc"},
		{err: &DiskInUseError{OwnerKind: "vm"}, want: "disk is attached to a vm"},
		{err: &DiskInUseError{}, want: ErrDiskInUse.Error()},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Fatalf("error = %q, want %q", got, tc.want)
		}
		if !errors.Is(tc.err, ErrDiskInUse) {
			t.Fatalf("%+v does not unwrap to ErrDiskInUse", tc.err)
		}
	}
}

func TestLightVMDiskIDNormalization(t *testing.T) {
	if _, err := normalizeLightVMDiskIDs([]int64{0}); !errors.Is(err, ErrInvalidDiskIDs) {
		t.Fatalf("zero id error = %v", err)
	}
	ids, err := normalizeLightVMDiskIDs([]int64{2, 2, 1})
	if err != nil || len(ids) != 2 || ids[0] != 2 || ids[1] != 1 {
		t.Fatalf("deduplicated ids = %v, %v", ids, err)
	}
	many := make([]int64, MaxWorkloadDisks+1)
	for i := range many {
		many[i] = int64(i + 1)
	}
	if _, err := normalizeLightVMDiskIDs(many); !errors.Is(err, ErrInvalidDiskIDs) {
		t.Fatalf("too many ids error = %v", err)
	}
}

func TestDiskIDNormalization(t *testing.T) {
	if _, err := normalizeDiskIDs(nil); err == nil {
		t.Fatal("empty ids were accepted")
	}
	if _, err := normalizeDiskIDs([]int64{-1}); err == nil {
		t.Fatal("negative id was accepted")
	}
	ids, err := normalizeDiskIDs([]int64{3, 3, 1})
	if err != nil || len(ids) != 2 || ids[0] != 3 || ids[1] != 1 {
		t.Fatalf("deduplicated ids = %v, %v", ids, err)
	}
	many := make([]int64, MaxWorkloadDisks+1)
	for i := range many {
		many[i] = int64(i + 1)
	}
	if _, err := normalizeDiskIDs(many); !errors.Is(err, ErrInvalidDiskIDs) {
		t.Fatalf("too many ids error = %v", err)
	}
}

func TestLightVMDeviceBudget(t *testing.T) {
	if err := validateLightVMDeviceBudget(0, 0, MaxLightVMDevices, 0, true); err != nil {
		t.Fatalf("at-limit create: %v", err)
	}
	if err := validateLightVMDeviceBudget(0, 0, MaxLightVMDevices+1, 0, true); !errors.Is(err, ErrLightVMDeviceLimit) {
		t.Fatalf("over-limit create error = %v", err)
	}
	if err := validateLightVMDeviceBudget(MaxLightVMDevices+1, 0, MaxLightVMDevices, 0, false); err != nil {
		t.Fatalf("over-limit definition reduction: %v", err)
	}
	if err := validateLightVMDeviceBudget(MaxLightVMDevices+1, 0, MaxLightVMDevices+2, 0, false); !errors.Is(err, ErrLightVMDeviceLimit) {
		t.Fatalf("over-limit growth error = %v", err)
	}
	if err := validateLightVMDeviceBudget(0, 0, 0, MaxLightVMDevices+1, false); !errors.Is(err, ErrLightVMDeviceLimit) {
		t.Fatalf("over-limit update error = %v", err)
	}
	if err := validateLightVMDeviceBudget(0, 0, 1, 1, false); err != nil {
		t.Fatalf("within-limit update: %v", err)
	}
}

func TestLoadWorkloadDisksValidation(t *testing.T) {
	st := openInternalTestStore(t)
	ctx := context.Background()
	if err := st.loadLightVMDisks(ctx, nil); err != nil {
		t.Fatalf("load no lightvms: %v", err)
	}
	if err := st.loadVMDisks(ctx, nil); err != nil {
		t.Fatalf("load no vms: %v", err)
	}
	result, err := st.loadWorkloadDisks(ctx, "vm", nil)
	if err != nil || len(result) != 0 {
		t.Fatalf("load no owners = %v, %v", result, err)
	}
	if _, err := st.loadWorkloadDisks(ctx, "unknown", []string{"one"}); err == nil {
		t.Fatal("unsupported owner kind was accepted")
	}
}

func TestLightVMReferenceValidation(t *testing.T) {
	ctx := context.Background()
	st := openInternalTestStore(t)
	kernel, err := st.CreateLightVMKernel(ctx, LightVMKernel{Name: "vmlinuz", Path: "/tmp/vmlinuz", FileName: "vmlinuz", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, LightVMInitramfs{Name: "initramfs", Path: "/tmp/initramfs", FileName: "initramfs", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	base := LightVM{
		Name: "invalid", VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/app",
	}
	invalidDisk := base
	invalidDisk.DiskIDs = []int64{0}
	if _, err := st.CreateLightVM(ctx, invalidDisk); !errors.Is(err, ErrInvalidDiskIDs) {
		t.Fatalf("create invalid disk id error = %v", err)
	}
	many := base
	many.DiskIDs = make([]int64, MaxWorkloadDisks+1)
	for i := range many.DiskIDs {
		many.DiskIDs[i] = int64(i + 1)
	}
	if _, err := st.CreateLightVM(ctx, many); !errors.Is(err, ErrInvalidDiskIDs) {
		t.Fatalf("create too many disk ids error = %v", err)
	}
	created, err := st.CreateLightVM(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	update := created
	update.ID = "missing"
	if _, err := st.UpdateLightVM(ctx, update); !errors.Is(err, ErrLightVMNotFound) {
		t.Fatalf("update missing error = %v", err)
	}
	update = created
	update.DiskIDs = []int64{0}
	if _, err := st.UpdateLightVM(ctx, update); !errors.Is(err, ErrInvalidDiskIDs) {
		t.Fatalf("update invalid disk id error = %v", err)
	}
}
