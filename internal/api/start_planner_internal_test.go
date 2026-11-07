package api

import (
	"context"
	"errors"
	"testing"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

type plannerStub struct {
	plan qemu.StartPlan
	err  error
}

func (p *plannerStub) Plan(context.Context, store.VM) (qemu.StartPlan, error) {
	return p.plan, p.err
}

type plannerRuntime struct {
	started     int
	startedPlan int
	lastPlan    qemu.StartPlan
}

func (r *plannerRuntime) Start(context.Context, store.VM) error { r.started++; return nil }
func (r *plannerRuntime) StartWithPlan(_ context.Context, _ store.VM, plan qemu.StartPlan) error {
	r.startedPlan++
	r.lastPlan = plan
	return nil
}
func (r *plannerRuntime) Stop(context.Context, string) error           { return nil }
func (r *plannerRuntime) Reset(context.Context, string) error          { return nil }
func (r *plannerRuntime) SendCtrlAltDel(context.Context, string) error { return nil }
func (r *plannerRuntime) Running(string) bool                          { return false }
func (r *plannerRuntime) VNCSocket(string) string                      { return "" }
func (r *plannerRuntime) Metrics(string) (qemu.Metrics, error)         { return qemu.Metrics{}, nil }

type legacyPlannerRuntime struct{ started int }

func (r *legacyPlannerRuntime) Start(context.Context, store.VM) error        { r.started++; return nil }
func (r *legacyPlannerRuntime) Stop(context.Context, string) error           { return nil }
func (r *legacyPlannerRuntime) Reset(context.Context, string) error          { return nil }
func (r *legacyPlannerRuntime) SendCtrlAltDel(context.Context, string) error { return nil }
func (r *legacyPlannerRuntime) Running(string) bool                          { return false }
func (r *legacyPlannerRuntime) VNCSocket(string) string                      { return "" }
func (r *legacyPlannerRuntime) Metrics(string) (qemu.Metrics, error)         { return qemu.Metrics{}, nil }

func TestServerStartVMPlannerBranches(t *testing.T) {
	ctx := context.Background()
	server := &Server{startPlanner: &plannerStub{err: errors.New("plan failed")}, vms: &plannerRuntime{}}
	if err := server.startVM(ctx, store.VM{}); err == nil {
		t.Fatal("startVM accepted a planner error")
	}
	legacy := &legacyPlannerRuntime{}
	server = &Server{startPlanner: &plannerStub{}, vms: legacy}
	if err := server.startVM(ctx, store.VM{}); err != nil || legacy.started != 1 {
		t.Fatalf("local start = %v, runtime = %+v", err, legacy)
	}
	runtime := &plannerRuntime{}
	plan := qemu.StartPlan{Attachments: map[int64]blockdev.Attachment{1: {Protocol: blockdev.ProtocolISCSI}}}
	server = &Server{startPlanner: &plannerStub{plan: plan}, vms: runtime}
	if err := server.startVM(ctx, store.VM{}); err != nil || runtime.startedPlan != 1 {
		t.Fatalf("prepared start = %v, runtime = %+v", err, runtime)
	}
	legacy = &legacyPlannerRuntime{}
	server = &Server{startPlanner: &plannerStub{plan: plan}, vms: legacy}
	if err := server.startVM(ctx, store.VM{}); err == nil {
		t.Fatal("startVM accepted a legacy runtime with a remote plan")
	}
	server.ConfigureStartPlanner(&plannerStub{})
	if server.startPlanner == nil {
		t.Fatal("ConfigureStartPlanner did not set the planner")
	}
}
