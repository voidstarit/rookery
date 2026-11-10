package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestWorkloadDiskAttachmentGuards(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-test")
	initramfs := createInitramfs(t, st, "initramfs-test")

	lightDisk := createDisk(t, st, "light-owned")
	lightVM, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "owner-light", VCPUs: 1, MemoryMB: 256,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh",
		DiskIDs: []int64{lightDisk.ID},
	})
	if err != nil {
		t.Fatalf("create lightvm: %v", err)
	}

	if err := st.MarkDiskDeleting(ctx, lightDisk.ID); !errors.Is(err, store.ErrDiskInUse) {
		t.Fatalf("MarkDiskDeleting attached lightvm disk = %v, want ErrDiskInUse", err)
	}
	var owner *store.DiskInUseError
	if err := st.MarkDiskDeleting(ctx, lightDisk.ID); !errors.As(err, &owner) ||
		owner.OwnerKind != "lightvm" || owner.OwnerID != lightVM.ID {
		t.Fatalf("MarkDiskDeleting owner = %#v, want lightvm %s", owner, lightVM.ID)
	}
	unchanged, err := st.GetDisk(ctx, lightDisk.ID)
	if err != nil {
		t.Fatalf("get attached disk: %v", err)
	}
	if unchanged.DeletionState != "" {
		t.Fatalf("attached disk deletion state = %q, want empty", unchanged.DeletionState)
	}

	vmDisk := createDisk(t, st, "vm-owned")
	vm, err := st.CreateVM(ctx, store.VM{
		Name: "owner-vm", VCPUs: 1, MemoryMB: 256,
		DiskIDs: []int64{vmDisk.ID},
	})
	if err != nil {
		t.Fatalf("create vm: %v", err)
	}
	_, err = st.CreateLightVM(ctx, store.LightVM{
		Name: "conflict-light", VCPUs: 1, MemoryMB: 256,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh",
		DiskIDs: []int64{vmDisk.ID},
	})
	if !errors.Is(err, store.ErrDiskInUse) {
		t.Fatalf("create lightvm with vm disk = %v, want ErrDiskInUse", err)
	}
	_, err = st.CreateVM(ctx, store.VM{
		Name: "conflict-vm", VCPUs: 1, MemoryMB: 256,
		DiskIDs: []int64{lightDisk.ID},
	})
	if !errors.Is(err, store.ErrDiskInUse) {
		t.Fatalf("create vm with lightvm disk = %v, want ErrDiskInUse", err)
	}
	if err := st.DeleteVM(ctx, vm.ID); err != nil {
		t.Fatalf("delete vm: %v", err)
	}
}

func TestUnmarkTemplateClearsProvisioningAndAllowsAttach(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	disk := createDisk(t, st, "template-lifecycle")

	template, err := st.SetDiskTemplate(ctx, disk.ID, true)
	if err != nil {
		t.Fatalf("mark template: %v", err)
	}
	if !template.IsTemplate {
		t.Fatal("disk is not a template after marking")
	}
	if _, err := st.SetDiskProvisioning(ctx, disk.ID, "/bin/true", "1m"); err != nil {
		t.Fatalf("set provisioning: %v", err)
	}
	plain, err := st.SetDiskTemplate(ctx, disk.ID, false)
	if err != nil {
		t.Fatalf("unmark template: %v", err)
	}
	if plain.IsTemplate || plain.ProvisioningScript != "" || plain.ProvisioningTimeout != "" {
		t.Fatalf("unmarked disk = %+v, want no template/provisioning state", plain)
	}
	vm, err := st.CreateVM(ctx, store.VM{
		Name: "template-user", VCPUs: 1, MemoryMB: 256,
		DiskIDs: []int64{disk.ID},
	})
	if err != nil {
		t.Fatalf("attach unmarked disk: %v", err)
	}
	if vm.DiskIDs[0] != disk.ID {
		t.Fatalf("vm disk ids = %v, want %d", vm.DiskIDs, disk.ID)
	}
	if _, err := st.SetDiskProvisioning(ctx, disk.ID, "/bin/true", "1m"); err == nil {
		t.Fatal("SetDiskProvisioning accepted an attached non-template")
	}
}

func TestDeletingDiskCannotBeAttached(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	disk := createDisk(t, st, "deleting-disk")
	if err := st.MarkDiskDeleting(ctx, disk.ID); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	_, err := st.CreateVM(ctx, store.VM{
		Name: "cannot-attach", VCPUs: 1, MemoryMB: 256,
		DiskIDs: []int64{disk.ID},
	})
	if !errors.Is(err, store.ErrDiskDeleting) {
		t.Fatalf("attach deleting disk = %v, want ErrDiskDeleting", err)
	}
	if _, err := st.SetDiskTemplate(ctx, disk.ID, true); !errors.Is(err, store.ErrDiskDeleting) {
		t.Fatalf("template deleting disk = %v, want ErrDiskDeleting", err)
	}
}
