package gateway_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeFleetGuard struct {
	epoch uint64
	ready bool
}

func (g fakeFleetGuard) Epoch() uint64 { return g.epoch }
func (g fakeFleetGuard) CanProvision(epoch uint64) bool {
	return g.ready && epoch != 0 && epoch == g.epoch
}

func TestFleetReconcileCreatesAndRemovesMember(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "fleet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{Name: "vmlinuz", Path: "/tmp/vmlinuz", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{Name: "initramfs", Path: "/tmp/initramfs", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	template, version, err := st.CreateLightVMTemplate(ctx, "web", "", store.LightVMTemplateSpec{
		VCPUs: 2, MemoryMB: 1024, KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "example.test/rootfs@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatal(err)
	}
	group, err := st.CreateLightVMGroup(ctx, store.LightVMGroup{
		Name: "web-group", TemplateVersionID: version.ID, DesiredCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeLightVMRuntime{running: make(map[string]bool)}
	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithLightVMRuntime(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	controller := gw.FleetController()
	if controller == nil {
		t.Fatal("fleet controller is nil")
	}
	guard := fakeFleetGuard{epoch: 1, ready: true}
	if err := controller.ReconcileFleet(ctx, guard); err != nil {
		t.Fatalf("reconcile create: %v", err)
	}
	members, err := st.ListGroupMembers(ctx, group.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("members = %+v, err = %v", members, err)
	}
	vm, err := st.GetLightVMByGroupMemberID(ctx, members[0].ID)
	if err != nil {
		t.Fatalf("member lightvm: %v", err)
	}
	if !runtime.RunningLightVM(vm.ID) {
		t.Fatalf("member lightvm was not started: %+v", vm)
	}
	gotGroup, err := st.GetLightVMGroup(ctx, group.ID)
	if err != nil || gotGroup.ActualCount != 1 || gotGroup.RunningCount != 1 || !gotGroup.Converged {
		t.Fatalf("group after create = %+v, err = %v", gotGroup, err)
	}

	zero := uint32(0)
	if _, err := st.UpdateLightVMGroup(ctx, group.ID, store.LightVMGroupUpdate{DesiredCount: &zero}); err != nil {
		t.Fatal(err)
	}
	if err := controller.ReconcileFleet(ctx, guard); err != nil {
		t.Fatalf("reconcile scale-in: %v", err)
	}
	members, err = st.ListGroupMembers(ctx, group.ID)
	if err != nil || len(members) != 0 {
		t.Fatalf("members after scale-in = %+v, err = %v", members, err)
	}
	if _, err := st.GetLightVM(ctx, vm.ID); !errors.Is(err, store.ErrLightVMNotFound) {
		t.Fatalf("lightvm after scale-in error = %v", err)
	}
	gotGroup, err = st.GetLightVMGroup(ctx, group.ID)
	if err != nil || gotGroup.ActualCount != 0 || !gotGroup.Converged {
		t.Fatalf("group after scale-in = %+v, err = %v", gotGroup, err)
	}
	if template.ID == 0 {
		t.Fatal("template missing")
	}
}
