package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeDiagnostics struct {
	deleted string
}

func (f *fakeDiagnostics) VPPGraphNodes(context.Context) ([]host.VPPGraphNode, error) {
	return []host.VPPGraphNode{{ID: "ip4-input", Label: "ip4-input"}, {ID: "interface-output", Label: "interface-output"}}, nil
}
func (f *fakeDiagnostics) VPPNodeGraph(context.Context) (host.VPPGraph, error) {
	return host.VPPGraph{Nodes: []host.VPPGraphNode{{ID: "node", Label: "Node", Width: 2, FontSize: 10, Fill: 3}}, Edges: []host.VPPGraphEdge{{Source: "node", Target: "next"}}, GeneratedAt: time.Now()}, nil
}
func (f *fakeDiagnostics) StartTrace(_ context.Context, node string, count uint32) (host.TraceResult, error) {
	return host.TraceResult{Node: node, Count: count, Raw: "trace started", CapturedAt: time.Now()}, nil
}
func (f *fakeDiagnostics) GetTrace(context.Context) (host.TraceResult, error) {
	return host.TraceResult{Raw: "Packet 1\n  ip4-input", CapturedAt: time.Now()}, nil
}
func (f *fakeDiagnostics) ClearTrace(context.Context) error { return nil }
func (f *fakeDiagnostics) ACLInterfaceCounters(context.Context) ([]host.ACLInterfaceCounters, error) {
	return []host.ACLInterfaceCounters{{
		Interface: "tap0", SwIfIndex: 1,
		Ingress: host.ACLDirectionCounters{Matched: 2, Denied: 1, Permitted: 1, ACLIndexes: []uint32{4}, Rules: []host.ACLRuleCounter{{ACLIndex: 4, RuleIndex: 0, Action: "deny", Matched: 1}}},
	}}, nil
}
func (f *fakeDiagnostics) VPPInterfaces(context.Context) ([]host.VPPInterface, error) {
	return []host.VPPInterface{{Name: "tap0", SwIfIndex: 1, Type: "tap", Up: true, MTU: 1500}}, nil
}
func (f *fakeDiagnostics) StartPacketCapture(context.Context, host.PacketCaptureOptions) (host.PacketCaptureStatus, error) {
	return host.PacketCaptureStatus{Active: true, File: "rookery-test.pcap", StartedAt: ptrTime(time.Now())}, nil
}
func (f *fakeDiagnostics) StopPacketCapture(context.Context) (host.PacketCaptureStatus, error) {
	return host.PacketCaptureStatus{Active: false}, nil
}
func (f *fakeDiagnostics) PacketCaptureStatus(context.Context) (host.PacketCaptureStatus, error) {
	return host.PacketCaptureStatus{Active: false}, nil
}
func (f *fakeDiagnostics) ListPacketCaptures() ([]host.PacketCaptureFile, error) {
	return []host.PacketCaptureFile{{Name: "rookery-test.pcap", Size: 10, Modified: time.Now()}}, nil
}
func (f *fakeDiagnostics) DeletePacketCapture(name string) error {
	f.deleted = name
	return nil
}

func ptrTime(value time.Time) *time.Time { return &value }

func TestDiagnosticsRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "diagnostics.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	runtime := &fakeDiagnostics{}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{
		Ready: true, VPP: readiness.VPPStatus{ComponentStatus: readiness.ComponentStatus{Ready: true}},
	}}, gateway.WithDiagnosticsRuntime(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	for _, path := range []string{
		"/api/troubleshoot/vpp-graph",
		"/api/troubleshoot/acl-counters",
		"/api/troubleshoot/trace",
		"/api/troubleshoot/trace/nodes",
		"/api/troubleshoot/capture/interfaces",
		"/api/troubleshoot/capture",
		"/api/troubleshoot/capture/files",
	} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", path, response.StatusCode, readBody(t, response))
		}
		_ = response.Body.Close()
	}

	started, err := http.Post(server.URL+"/api/troubleshoot/capture", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if started.StatusCode != http.StatusCreated {
		t.Fatalf("start capture status = %d, body = %s", started.StatusCode, readBody(t, started))
	}
	_ = started.Body.Close()

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/troubleshoot/capture/files/rookery-test.pcap", nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete capture status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	_ = deleted.Body.Close()
	if runtime.deleted != "rookery-test.pcap" {
		t.Fatalf("deleted capture = %q", runtime.deleted)
	}
}
