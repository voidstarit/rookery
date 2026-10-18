package readiness

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/host"
)

type fakeInspector struct {
	thinPool host.Result
	vpp      host.Result
}

func (f fakeInspector) InspectThinPool(context.Context, string, string) host.Result {
	return f.thinPool
}

func (f fakeInspector) InspectVPP(context.Context, string) host.Result {
	return f.vpp
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewManagerStartsChecking(t *testing.T) {
	manager := NewManager(Config{
		VG:        "vg0",
		ThinPool:  "nerds-vms",
		VPPSocket: "/run/vpp/api.sock",
	}, fakeInspector{}, testLogger(), time.Second)

	status := manager.Status()
	if status.Ready {
		t.Fatal("initial status is ready")
	}
	if status.ThinPool.State != host.StateChecking || status.VPP.State != host.StateChecking {
		t.Fatalf("initial status = %+v", status)
	}
	if manager.Ready() {
		t.Fatal("Ready returned true before a check")
	}
}

func TestRefreshReady(t *testing.T) {
	manager := NewManager(Config{
		VG:        "vg0",
		ThinPool:  "nerds-vms",
		VPPSocket: "/run/vpp/api.sock",
	}, fakeInspector{
		thinPool: host.Result{Ready: true, State: host.StateReady, Detail: "found"},
		vpp:      host.Result{Ready: true, State: host.StateReady, Detail: "pong"},
	}, testLogger(), time.Second)

	status := manager.Refresh(context.Background())
	if !status.Ready {
		t.Fatalf("status = %+v", status)
	}
	if !status.ThinPool.Ready || status.ThinPool.VG != "vg0" || status.ThinPool.Name != "nerds-vms" {
		t.Fatalf("thin pool = %+v", status.ThinPool)
	}
	if !status.VPP.Ready || status.VPP.Socket != "/run/vpp/api.sock" {
		t.Fatalf("vpp = %+v", status.VPP)
	}
	if status.CheckedAt == nil || status.ThinPool.CheckedAt == nil || status.VPP.CheckedAt == nil {
		t.Fatal("checked_at is not set")
	}
	if !manager.Ready() {
		t.Fatal("Ready = false after ready refresh")
	}
}

func TestRefreshNotReady(t *testing.T) {
	manager := NewManager(Config{
		VG:        "vg0",
		ThinPool:  "nerds-vms",
		VPPSocket: "/run/vpp/api.sock",
	}, fakeInspector{
		thinPool: host.Result{Ready: true, State: host.StateReady},
		vpp:      host.Result{State: host.StateUnreachable, Detail: "no socket"},
	}, testLogger(), time.Second)

	status := manager.Refresh(context.Background())
	if status.Ready {
		t.Fatalf("status = %+v", status)
	}
	if status.VPP.Ready || status.VPP.Detail != "no socket" || status.VPP.State != host.StateUnreachable {
		t.Fatalf("vpp = %+v", status.VPP)
	}
	if manager.Ready() {
		t.Fatal("Ready = true while VPP is unreachable")
	}
}

func TestRefreshNotReadyWhenThinPoolMissing(t *testing.T) {
	manager := NewManager(Config{
		VG:        "vg0",
		ThinPool:  "nerds-vms",
		VPPSocket: "/run/vpp/api.sock",
	}, fakeInspector{
		thinPool: host.Result{State: host.StateMissing, Detail: "no pool"},
		vpp:      host.Result{Ready: true, State: host.StateReady},
	}, testLogger(), time.Second)

	status := manager.Refresh(context.Background())
	if status.Ready {
		t.Fatalf("status = %+v", status)
	}
	if status.ThinPool.Ready || status.ThinPool.Detail != "no pool" {
		t.Fatalf("thin pool = %+v", status.ThinPool)
	}
}
