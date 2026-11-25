package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/store"
)

func testLightVMTemplateSpec() store.LightVMTemplateSpec {
	return store.LightVMTemplateSpec{
		VCPUs:       2,
		MemoryMB:    1024,
		KernelID:    1,
		InitramfsID: 1,
		Image:       "example.test/rootfs@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Env:         []store.LightVMEnvVar{{Name: "APP_ENV", Value: "test"}},
	}
}

func TestLightVMTemplateAndGroupCRUD(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-6.12")
	initramfs := createInitramfs(t, st, "initramfs-6.12")

	spec := testLightVMTemplateSpec()
	spec.KernelID = kernel.ID
	spec.InitramfsID = initramfs.ID

	template, version, err := st.CreateLightVMTemplate(ctx, "web", "web fleet", spec)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if template.LatestVersion != 1 || template.LatestVersionID != version.ID || template.VersionCount != 1 {
		t.Fatalf("template = %+v, version = %+v", template, version)
	}
	if version.Version != 1 || version.Spec.Append != store.DefaultLightVMAppend {
		t.Fatalf("version = %+v", version)
	}
	versions, err := st.ListLightVMTemplateVersions(ctx, template.ID)
	if err != nil || len(versions) != 1 || versions[0].DiskCount != 0 {
		t.Fatalf("versions = %+v, err = %v", versions, err)
	}
	version2, err := st.CreateLightVMTemplateVersion(ctx, template.ID, spec)
	if err != nil {
		t.Fatalf("create version 2: %v", err)
	}
	if version2.Version != 2 {
		t.Fatalf("version2 = %+v", version2)
	}
	if _, err := st.GetLightVMTemplateVersion(ctx, template.ID+100, version2.ID); !errors.Is(err, store.ErrLightVMTemplateVersionNotFound) {
		t.Fatalf("wrong-parent version error = %v", err)
	}

	group, err := st.CreateLightVMGroup(ctx, store.LightVMGroup{
		Name: "web-group", Description: "fleet", TemplateVersionID: version.ID, DesiredCount: 1,
	})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if group.State != "active" || group.TemplateVersion != 1 {
		t.Fatalf("group = %+v", group)
	}
	counts, err := st.GetLightVMGroupCounts(ctx, group.ID)
	if err != nil || counts.Actual != 0 || counts.Converged {
		t.Fatalf("counts = %+v, err = %v", counts, err)
	}
	if err := st.DeleteLightVMTemplate(ctx, template.ID); !errors.Is(err, store.ErrLightVMTemplateInUse) {
		t.Fatalf("delete pinned template error = %v", err)
	}

	member, created, err := st.CreateGroupMemberIntent(ctx, group.ID, "web-abc", version.ID, 1, "running")
	if err != nil || !created {
		t.Fatalf("create member intent = %+v, created=%v, err=%v", member, created, err)
	}
	if member.State != "provisioning" || member.TemplateVersion != 1 {
		t.Fatalf("member = %+v", member)
	}
	if _, created, err := st.CreateGroupMemberIntent(ctx, group.ID, "web-def", version.ID, 1, "running"); err != nil || created {
		t.Fatalf("second member intent created=%v err=%v", created, err)
	}
	if err := st.SetGroupMemberState(ctx, member.ID, "active"); err != nil {
		t.Fatalf("set member active: %v", err)
	}
	if err := st.SetGroupMemberRetry(ctx, member.ID, 2, time.Time{}, "retry"); err != nil {
		t.Fatalf("set member retry: %v", err)
	}
	member, err = st.GetGroupMember(ctx, member.ID)
	if err != nil || member.RetryAttempts != 2 || member.State != "active" {
		t.Fatalf("member after retry = %+v, err=%v", member, err)
	}

	vm, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "web-abc-vm", VCPUs: 2, MemoryMB: 1024,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: spec.Image, GroupMemberID: &member.ID,
	})
	if err != nil {
		t.Fatalf("create member lightvm: %v", err)
	}
	if vm.GroupMemberID == nil || *vm.GroupMemberID != member.ID || vm.GroupName != group.Name {
		t.Fatalf("lightvm membership = %+v", vm)
	}
	if _, err := st.SetLightVMStatus(ctx, vm.ID, store.StatusRunning); err != nil {
		t.Fatalf("set running: %v", err)
	}
	counts, err = st.GetLightVMGroupCounts(ctx, group.ID)
	if err != nil || counts.Actual != 1 || counts.Running != 1 || !counts.Converged {
		t.Fatalf("counts after lightvm = %+v, err=%v", counts, err)
	}
	if err := st.SetGroupMemberPower(ctx, member.ID, vm.ID, store.DesiredStopped); err != nil {
		t.Fatalf("set member power: %v", err)
	}
	vm, err = st.GetLightVM(ctx, vm.ID)
	if err != nil || vm.DesiredState != store.DesiredStopped {
		t.Fatalf("lightvm desired state = %+v, err=%v", vm, err)
	}

	if err := st.SetGroupMemberState(ctx, member.ID, "removing"); err != nil {
		t.Fatalf("set member removing: %v", err)
	}
	counts, err = st.GetLightVMGroupCounts(ctx, group.ID)
	if err != nil || counts.Removing != 1 || counts.Converged {
		t.Fatalf("counts removing = %+v, err=%v", counts, err)
	}
	if err := st.ClearLightVMGroupOwner(ctx, vm.ID); err != nil {
		t.Fatalf("clear owner: %v", err)
	}
	if err := st.DeleteLightVM(ctx, vm.ID); err != nil {
		t.Fatalf("delete lightvm: %v", err)
	}
	if err := st.DeleteGroupMember(ctx, member.ID); err != nil {
		t.Fatalf("delete member: %v", err)
	}

	groupID := group.ID
	deleted, deletedGroup, err := st.SetLightVMGroupDeleting(ctx, groupID, false)
	if err != nil {
		t.Fatalf("delete empty group: %v", err)
	}
	if !deleted || deletedGroup.ID != 0 {
		t.Fatalf("deleted=%v group=%+v", deleted, deletedGroup)
	}
	if _, err := st.GetLightVMGroup(ctx, groupID); !errors.Is(err, store.ErrLightVMGroupNotFound) {
		t.Fatalf("deleted group lookup error = %v", err)
	}
}
