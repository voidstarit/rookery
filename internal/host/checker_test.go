package host

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func testChecker(runner *fakeLVM, ping func(context.Context, string) error) *Checker {
	return &Checker{
		LVM: &LVM{Runner: runner},
		VPP: &VPP{PingFunc: ping},
	}
}

func TestInspectThinPoolNotConfigured(t *testing.T) {
	checker := testChecker(&fakeLVM{}, func(context.Context, string) error { return nil })

	result := checker.InspectThinPool(context.Background(), "", "nerds-vms")
	if result.Ready || result.State != StateNotConfigured {
		t.Fatalf("result = %+v", result)
	}
}

func TestInspectThinPoolMissingVG(t *testing.T) {
	checker := testChecker(&fakeLVM{}, func(context.Context, string) error { return nil })

	result := checker.InspectThinPool(context.Background(), "vg0", "nerds-vms")
	if result.Ready || result.State != StateMissing || !strings.Contains(result.Detail, "does not exist") {
		t.Fatalf("result = %+v", result)
	}
}

func TestInspectThinPoolMissingPool(t *testing.T) {
	checker := testChecker(&fakeLVM{vgExists: true}, func(context.Context, string) error { return nil })

	result := checker.InspectThinPool(context.Background(), "vg0", "nerds-vms")
	if result.Ready || result.State != StateMissing || !strings.Contains(result.Detail, "init") {
		t.Fatalf("result = %+v", result)
	}
}

func TestInspectThinPoolReady(t *testing.T) {
	checker := testChecker(&fakeLVM{vgExists: true, poolExists: true}, func(context.Context, string) error { return nil })

	result := checker.InspectThinPool(context.Background(), "vg0", "nerds-vms")
	if !result.Ready || result.State != StateReady {
		t.Fatalf("result = %+v", result)
	}
}

func TestInspectThinPoolNonThinLV(t *testing.T) {
	checker := testChecker(&fakeLVM{vgExists: true, poolExists: true, poolAttr: "-wi-a-----"}, func(context.Context, string) error { return nil })

	result := checker.InspectThinPool(context.Background(), "vg0", "nerds-vms")
	if result.Ready || result.State != StateError || !strings.Contains(result.Detail, "not a thin pool") {
		t.Fatalf("result = %+v", result)
	}
}

func TestInspectVPPNotConfigured(t *testing.T) {
	checker := testChecker(&fakeLVM{}, func(context.Context, string) error { return nil })

	result := checker.InspectVPP(context.Background(), "")
	if result.Ready || result.State != StateNotConfigured {
		t.Fatalf("result = %+v", result)
	}
}

func TestInspectVPPUnreachable(t *testing.T) {
	checker := testChecker(&fakeLVM{}, func(context.Context, string) error {
		return errors.New("connection refused")
	})

	result := checker.InspectVPP(context.Background(), "/run/vpp/api.sock")
	if result.Ready || result.State != StateUnreachable || !strings.Contains(result.Detail, "connection refused") {
		t.Fatalf("result = %+v", result)
	}
}

func TestInspectVPPReady(t *testing.T) {
	checker := testChecker(&fakeLVM{}, func(_ context.Context, socket string) error {
		if socket != "/run/vpp/api.sock" {
			t.Fatalf("socket = %q", socket)
		}
		return nil
	})

	result := checker.InspectVPP(context.Background(), "/run/vpp/api.sock")
	if !result.Ready || result.State != StateReady {
		t.Fatalf("result = %+v", result)
	}
}
