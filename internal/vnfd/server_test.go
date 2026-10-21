package vnfd

import (
	"context"
	"testing"
	"time"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeManager struct {
	running  map[int64]bool
	healthy  bool
	starts   []string
	stopped  []int64
	leases   map[int64][]store.VNFLease
	bgp      map[int64]host.BGPStatus
	startErr error
}

func newFakeManager() *fakeManager {
	return &fakeManager{
		running: make(map[int64]bool),
		healthy: true,
		leases:  make(map[int64][]store.VNFLease),
		bgp:     make(map[int64]host.BGPStatus),
	}
}

func (f *fakeManager) Start(_ context.Context, vnf store.VNF, hostIf string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.running[vnf.ID] = true
	f.starts = append(f.starts, vnf.Type+":"+hostIf)
	return nil
}

func (f *fakeManager) StartMemif(_ context.Context, vnf store.VNF, socketPath string, _ uint32) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.running[vnf.ID] = true
	f.starts = append(f.starts, vnf.Type+":memif:"+socketPath)
	return nil
}

func (f *fakeManager) Stop(_ context.Context, vnfID int64, _ bool) error {
	delete(f.running, vnfID)
	f.stopped = append(f.stopped, vnfID)
	return nil
}

func (f *fakeManager) Running(vnfID int64) bool { return f.running[vnfID] }
func (f *fakeManager) Healthy(int64) bool       { return f.healthy }

func (f *fakeManager) Leases(_ context.Context, vnfID int64) ([]store.VNFLease, error) {
	return f.leases[vnfID], nil
}

func (f *fakeManager) BGPStatus(_ context.Context, vnfID int64) (host.BGPStatus, error) {
	return f.bgp[vnfID], nil
}

func (f *fakeManager) CleanupLegacyNetwork(int64) {}
func (f *fakeManager) ResetRuntime(context.Context) error {
	f.running = make(map[int64]bool)
	return nil
}
func (f *fakeManager) Close() error { return nil }

func tapSpec(id int64, name string) *rookeryv1.VNFSpec {
	return &rookeryv1.VNFSpec{
		Id: id, Name: name, Type: "dhcpv4", NetworkId: 1, Transport: "tap",
		TapHostName: "vnf1",
		Config: &rookeryv1.VNFConfig{
			Gateway: "10.0.0.1/24", RangeStart: "10.0.0.10", RangeEnd: "10.0.0.20",
		},
	}
}

func TestEnsureIsIdempotentAndRestartsOnChange(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), nil)

	if _, err := server.EnsureVNF(ctx, &rookeryv1.EnsureVNFRequest{Vnf: tapSpec(1, "dhcp")}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(manager.starts) != 1 {
		t.Fatalf("starts = %+v", manager.starts)
	}
	if _, err := server.EnsureVNF(ctx, &rookeryv1.EnsureVNFRequest{Vnf: tapSpec(1, "dhcp")}); err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if len(manager.starts) != 1 || len(manager.stopped) != 0 {
		t.Fatalf("unchanged spec restarted the runtime: starts=%+v stopped=%+v", manager.starts, manager.stopped)
	}

	changed := tapSpec(1, "dhcp")
	changed.Config.RangeEnd = "10.0.0.30"
	if _, err := server.EnsureVNF(ctx, &rookeryv1.EnsureVNFRequest{Vnf: changed}); err != nil {
		t.Fatalf("ensure changed: %v", err)
	}
	if len(manager.starts) != 2 || len(manager.stopped) != 1 {
		t.Fatalf("changed spec did not restart: starts=%+v stopped=%+v", manager.starts, manager.stopped)
	}
}

func TestEnsureMemifNeedsSocket(t *testing.T) {
	server := NewServer(newFakeManager(), t.TempDir(), nil)
	spec := tapSpec(1, "dhcp")
	spec.Transport = "memif"
	spec.TapHostName = ""
	if _, err := server.EnsureVNF(context.Background(), &rookeryv1.EnsureVNFRequest{Vnf: spec}); err == nil {
		t.Fatal("expected an error for a memif spec without a socket")
	}
}

func TestSuperviseRestartsDeadRuntime(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), nil)
	server.restartBackoff = 0
	if _, err := server.EnsureVNF(ctx, &rookeryv1.EnsureVNFRequest{Vnf: tapSpec(1, "dhcp")}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	delete(manager.running, 1) // simulate a crashed runtime
	server.superviseOnce(ctx)
	if len(manager.starts) != 2 || !manager.running[1] {
		t.Fatalf("runtime was not restarted: starts=%+v", manager.starts)
	}

	// A reset pauses supervision so the API can rebuild VPP first.
	if _, err := server.ResetRuntime(ctx, &rookeryv1.ResetRuntimeRequest{}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if manager.running[1] {
		t.Fatal("reset did not stop the runtime")
	}
	server.superviseOnce(ctx)
	if manager.running[1] {
		t.Fatal("supervision ignored the reset pause")
	}
	server.mu.Lock()
	server.pausedUntil = time.Time{}
	server.mu.Unlock()
	server.superviseOnce(ctx)
	if !manager.running[1] {
		t.Fatal("runtime was not restarted after the pause")
	}
}

func TestLeasesAndBGPStatus(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	manager.leases[1] = []store.VNFLease{{ClientID: "aa:bb", Address: "10.0.0.11", Active: true}}
	manager.bgp[2] = host.BGPStatus{
		Running: true, Established: true,
		Received: []host.BGPReceivedPrefix{{Prefix: "6.6.6.0/24", NextHop: "10.40.0.100", ASPath: []uint32{65040}}},
	}
	server := NewServer(manager, t.TempDir(), nil)
	if _, err := server.EnsureVNF(ctx, &rookeryv1.EnsureVNFRequest{Vnf: tapSpec(1, "dhcp")}); err != nil {
		t.Fatal(err)
	}
	bgpSpec := tapSpec(2, "bgp")
	bgpSpec.Type = "bgp"
	if _, err := server.EnsureVNF(ctx, &rookeryv1.EnsureVNFRequest{Vnf: bgpSpec}); err != nil {
		t.Fatal(err)
	}

	leases, err := server.ListLeases(ctx, &rookeryv1.ListLeasesRequest{Id: 1})
	if err != nil || len(leases.GetLeases()) != 1 || leases.GetLeases()[0].GetAddress() != "10.0.0.11" {
		t.Fatalf("leases = %+v, err = %v", leases, err)
	}
	status, err := server.GetBGPStatus(ctx, &rookeryv1.GetBGPStatusRequest{Id: 2})
	if err != nil || !status.GetEstablished() || len(status.GetReceived()) != 1 ||
		status.GetReceived()[0].GetNextHop() != "10.40.0.100" ||
		len(status.GetReceived()[0].GetAsPath()) != 1 {
		t.Fatalf("bgp status = %+v, err = %v", status, err)
	}
}

func TestRemoveVNFForgetsState(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	dir := t.TempDir()
	server := NewServer(manager, dir, nil)
	if _, err := server.EnsureVNF(ctx, &rookeryv1.EnsureVNFRequest{Vnf: tapSpec(1, "dhcp")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.RemoveVNF(ctx, &rookeryv1.RemoveVNFRequest{Id: 1, RemoveLeases: true}); err != nil {
		t.Fatal(err)
	}
	if manager.running[1] {
		t.Fatal("remove did not stop the runtime")
	}
	if states, err := server.ListVNFStates(ctx, &rookeryv1.ListVNFStatesRequest{}); err != nil || len(states.GetVnfs()) != 0 {
		t.Fatalf("states after remove = %+v, err = %v", states, err)
	}
	// A fresh server (daemon restart) must not resurrect the removed VNF.
	fresh := NewServer(newFakeManager(), dir, nil)
	if err := fresh.Load(); err != nil {
		t.Fatal(err)
	}
	if states, err := fresh.ListVNFStates(ctx, &rookeryv1.ListVNFStatesRequest{}); err != nil || len(states.GetVnfs()) != 0 {
		t.Fatalf("restored states = %+v, err = %v", states, err)
	}
}

func TestLoadRestoresDesiredSpecs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	manager := newFakeManager()
	server := NewServer(manager, dir, nil)
	if _, err := server.EnsureVNF(ctx, &rookeryv1.EnsureVNFRequest{Vnf: tapSpec(1, "dhcp")}); err != nil {
		t.Fatal(err)
	}

	restartedManager := newFakeManager()
	restarted := NewServer(restartedManager, dir, nil)
	if err := restarted.Load(); err != nil {
		t.Fatal(err)
	}
	restarted.restartBackoff = 0
	restarted.superviseOnce(ctx)
	if !restartedManager.running[1] {
		t.Fatal("persisted desired spec was not restored")
	}
}
