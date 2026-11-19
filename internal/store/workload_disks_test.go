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

func TestReadOnlyDiskAllowsMultipleWorkloads(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "read-only-kernel")
	initramfs := createInitramfs(t, st, "read-only-initramfs")

	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "shared-ro", SizeGB: 20, VG: "vg0", ThinPool: "rookery", ReadOnly: true,
	})
	if err != nil {
		t.Fatalf("create read-only disk: %v", err)
	}
	first, err := st.CreateVM(ctx, store.VM{
		Name: "ro-first", VCPUs: 1, MemoryMB: 256, DiskIDs: []int64{disk.ID},
	})
	if err != nil {
		t.Fatalf("attach read-only disk to first VM: %v", err)
	}
	second, err := st.CreateVM(ctx, store.VM{
		Name: "ro-second", VCPUs: 1, MemoryMB: 256, DiskIDs: []int64{disk.ID},
	})
	if err != nil {
		t.Fatalf("attach read-only disk to second VM: %v", err)
	}
	light, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "ro-light", VCPUs: 1, MemoryMB: 256,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		DiskIDs: []int64{disk.ID},
	})
	if err != nil {
		t.Fatalf("attach read-only disk to LightVM: %v", err)
	}

	got, err := st.GetDisk(ctx, disk.ID)
	if err != nil {
		t.Fatalf("get shared disk: %v", err)
	}
	if !got.ReadOnly {
		t.Fatal("shared disk lost its read-only flag")
	}
	if len(got.Attachments) != 3 {
		t.Fatalf("attachments = %+v, want three workloads", got.Attachments)
	}
	if got.AttachedVMID != nil || got.AttachedLightVMID != nil {
		t.Fatalf("legacy owner fields set for a shared disk: %+v", got)
	}
	if !store.DiskAttachedToAnyWorkload(got) {
		t.Fatal("shared disk reports no attachments")
	}
	if err := st.DeleteDisk(ctx, disk.ID); !errors.Is(err, store.ErrDiskInUse) {
		t.Fatalf("delete shared attached disk = %v, want ErrDiskInUse", err)
	}

	for _, id := range []string{first.ID, second.ID} {
		if err := st.DeleteVM(ctx, id); err != nil {
			t.Fatalf("delete vm %s: %v", id, err)
		}
	}
	if err := st.DeleteLightVM(ctx, light.ID); err != nil {
		t.Fatalf("delete lightvm: %v", err)
	}
	if err := st.DeleteDisk(ctx, disk.ID); err != nil {
		t.Fatalf("delete detached shared disk: %v", err)
	}
}

func TestSetDiskReadOnlyRequiresDetachedDisk(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	disk := createDisk(t, st, "read-only-toggle")

	vm, err := st.CreateVM(ctx, store.VM{
		Name: "toggle-vm", VCPUs: 1, MemoryMB: 256, DiskIDs: []int64{disk.ID},
	})
	if err != nil {
		t.Fatalf("create vm: %v", err)
	}
	if _, err := st.SetDiskReadOnly(ctx, disk.ID, true); !errors.Is(err, store.ErrDiskInUse) {
		t.Fatalf("promote attached disk = %v, want ErrDiskInUse", err)
	}
	// The same value is a no-op even while attached.
	if _, err := st.SetDiskReadOnly(ctx, disk.ID, false); err != nil {
		t.Fatalf("no-op read-only update: %v", err)
	}

	if err := st.DeleteVM(ctx, vm.ID); err != nil {
		t.Fatalf("delete vm: %v", err)
	}
	promoted, err := st.SetDiskReadOnly(ctx, disk.ID, true)
	if err != nil {
		t.Fatalf("promote detached disk: %v", err)
	}
	if !promoted.ReadOnly {
		t.Fatal("disk is not read-only after update")
	}

	first, err := st.CreateVM(ctx, store.VM{
		Name: "shared-first", VCPUs: 1, MemoryMB: 256, DiskIDs: []int64{disk.ID},
	})
	if err != nil {
		t.Fatalf("attach promoted disk to first VM: %v", err)
	}
	second, err := st.CreateVM(ctx, store.VM{
		Name: "shared-second", VCPUs: 1, MemoryMB: 256, DiskIDs: []int64{disk.ID},
	})
	if err != nil {
		t.Fatalf("attach promoted disk to second VM: %v", err)
	}
	if _, err := st.SetDiskReadOnly(ctx, disk.ID, false); !errors.Is(err, store.ErrDiskInUse) {
		t.Fatalf("demote shared disk = %v, want ErrDiskInUse", err)
	}

	for _, id := range []string{first.ID, second.ID} {
		if err := st.DeleteVM(ctx, id); err != nil {
			t.Fatalf("delete vm %s: %v", id, err)
		}
	}
	demoted, err := st.SetDiskReadOnly(ctx, disk.ID, false)
	if err != nil {
		t.Fatalf("demote detached disk: %v", err)
	}
	if demoted.ReadOnly {
		t.Fatal("disk is still read-only after demotion")
	}
}

func TestReadOnlyDiskCannotBeTemplate(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if _, err := st.CreateDisk(ctx, store.Disk{
		Name: "ro-template", SizeGB: 10, VG: "vg0", ThinPool: "rookery",
		IsTemplate: true, ReadOnly: true,
	}); !errors.Is(err, store.ErrDiskReadOnlyTemplate) {
		t.Fatalf("create read-only template = %v, want ErrDiskReadOnlyTemplate", err)
	}

	disk := createDisk(t, st, "plain-then-template")
	if _, err := st.SetDiskReadOnly(ctx, disk.ID, true); err != nil {
		t.Fatalf("mark plain disk read-only: %v", err)
	}
	if _, err := st.SetDiskTemplate(ctx, disk.ID, true); !errors.Is(err, store.ErrDiskReadOnlyTemplate) {
		t.Fatalf("mark read-only disk as template = %v, want ErrDiskReadOnlyTemplate", err)
	}
}
