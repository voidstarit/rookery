package vnfd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeManager struct {
	running   map[int64]bool
	healthy   bool
	starts    []string
	startVNFs []store.VNF
	stopped   []int64
	removed   map[int64][]string
	leases    map[int64][]store.VNFLease
	dns       map[int64][]host.DNSRecordView
	bgp       map[int64]host.BGPStatus
	reloaded  []store.VNF
	cleaned   []int64
	startErr  error
	removeErr error
	reloadErr error
	resetErr  error
	reset     bool
}

func newFakeManager() *fakeManager {
	return &fakeManager{
		running: make(map[int64]bool),
		healthy: true,
		removed: make(map[int64][]string),
		leases:  make(map[int64][]store.VNFLease),
		dns:     make(map[int64][]host.DNSRecordView),
		bgp:     make(map[int64]host.BGPStatus),
	}
}

func (f *fakeManager) Start(_ context.Context, vnf store.VNF, hostIf string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.running[vnf.ID] = true
	f.startVNFs = append(f.startVNFs, vnf)
	f.starts = append(f.starts, vnf.Type+":tap:"+hostIf)
	return nil
}

func (f *fakeManager) StartMemif(_ context.Context, vnf store.VNF, socketPath string, _ uint32) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.running[vnf.ID] = true
	f.startVNFs = append(f.startVNFs, vnf)
	f.starts = append(f.starts, vnf.Type+":memif:"+socketPath)
	return nil
}

func (f *fakeManager) StartShared(_ context.Context, vnf store.VNF, socketPath string, _ uint32, address, _ string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.running[vnf.ID] = true
	f.startVNFs = append(f.startVNFs, vnf)
	f.starts = append(f.starts, vnf.Type+":shared:"+socketPath+"@"+address)
	return nil
}

func (f *fakeManager) Stop(_ context.Context, vnfID int64, _ bool) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	delete(f.running, vnfID)
	f.stopped = append(f.stopped, vnfID)
	return nil
}

func (f *fakeManager) Running(vnfID int64) bool { return f.running[vnfID] }
func (f *fakeManager) Healthy(int64) bool       { return f.healthy }

func (f *fakeManager) Reload(_ context.Context, vnf store.VNF) error {
	if f.reloadErr != nil {
		return f.reloadErr
	}
	if !f.running[vnf.ID] {
		return fmt.Errorf("VNF %d is not running", vnf.ID)
	}
	f.reloaded = append(f.reloaded, vnf)
	return nil
}

func (f *fakeManager) Leases(_ context.Context, vnfID int64) ([]store.VNFLease, error) {
	return f.leases[vnfID], nil
}

func (f *fakeManager) RemoveLeases(_ context.Context, vnfID int64, clientIDs []string) (int, error) {
	if f.removeErr != nil {
		return 0, f.removeErr
	}
	f.removed[vnfID] = append(f.removed[vnfID], clientIDs...)
	return len(clientIDs), nil
}

func (f *fakeManager) DNSRecords(vnfID int64) []host.DNSRecordView { return f.dns[vnfID] }

func (f *fakeManager) BGPStatus(_ context.Context, vnfID int64) (host.BGPStatus, error) {
	return f.bgp[vnfID], nil
}

func (f *fakeManager) CleanupLegacyNetwork(networkID int64) {
	f.cleaned = append(f.cleaned, networkID)
}

func (f *fakeManager) ResetRuntime(context.Context) error {
	if f.resetErr != nil {
		return f.resetErr
	}
	f.reset = true
	f.running = make(map[int64]bool)
	return nil
}

func (f *fakeManager) Close() error { return nil }

func testRuntime(id int64, transport string) *rookeryv1.RuntimeSpec {
	runtime := &rookeryv1.RuntimeSpec{Id: id, Name: fmt.Sprintf("vnf%d", id), NetworkId: 1, Transport: transport}
	switch transport {
	case "tap":
		runtime.TapHostName = fmt.Sprintf("vnf%d", id)
	case "memif", "dedicated":
		runtime.MemifSocket = fmt.Sprintf("/run/rookery/memif/%d.sock", id)
		runtime.MemifSwIfIndex = uint32(id)
	case "shared":
		runtime.MemifSocket = "/run/rookery/memif/net-1.sock"
		runtime.MemifSwIfIndex = 1
		runtime.SharedAddress = "10.20.0.2/24"
		runtime.SharedIpv6Address = "fd00:20::2/64"
	}
	return runtime
}

func dhcpSpec(id int64, family, transport string) *rookeryv1.DHCPRuntimeSpec {
	spec := &rookeryv1.DHCPRuntimeSpec{Runtime: testRuntime(id, transport), Family: family}
	if family == "dhcpv6" {
		spec.Config = &rookeryv1.DHCPRuntimeSpec_Dhcpv6{Dhcpv6: &rookeryv1.DHCPV6RuntimeConfig{
			Gateway: "fd00:20::1/64", RangeStart: "fd00:20::10", RangeEnd: "fd00:20::20",
		}}
		return spec
	}
	spec.Config = &rookeryv1.DHCPRuntimeSpec_Dhcpv4{Dhcpv4: &rookeryv1.DHCPV4RuntimeConfig{
		Gateway: "10.20.0.1/24", RangeStart: "10.20.0.10", RangeEnd: "10.20.0.20",
	}}
	return spec
}

func dnsSpec(id int64, transport string) *rookeryv1.DNSRuntimeSpec {
	return &rookeryv1.DNSRuntimeSpec{
		Runtime: testRuntime(id, transport),
		Config: &rookeryv1.DNSRuntimeConfig{
			Records: []*rookeryv1.DNSRecordSpec{{Name: "vm1.lab1.test.", Type: "A", Value: "10.20.0.50", Ttl: 120}},
		},
	}
}

func bgpSpec(id int64, transport string) *rookeryv1.BGPRuntimeSpec {
	return &rookeryv1.BGPRuntimeSpec{
		Runtime: testRuntime(id, transport),
		Config: &rookeryv1.BGPRuntimeConfig{
			LocalAs: 64512, PeerAs: 64513, RouterId: "10.0.0.1",
			PeerIp: "10.40.0.2", LocalIp: "10.40.0.1/24",
		},
	}
}

func TestEnsureDHCPIsIdempotentAndRestartsOnChange(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())

	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(manager.starts) != 1 {
		t.Fatalf("starts = %+v", manager.starts)
	}
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	if len(manager.starts) != 1 || len(manager.stopped) != 0 {
		t.Fatalf("unchanged spec restarted the runtime: starts=%+v stopped=%+v", manager.starts, manager.stopped)
	}

	changed := dhcpSpec(1, "dhcpv4", "tap")
	changed.GetDhcpv4().RangeEnd = "10.20.0.30"
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: changed}); err != nil {
		t.Fatalf("ensure changed: %v", err)
	}
	if len(manager.starts) != 2 || len(manager.stopped) != 1 {
		t.Fatalf("changed spec did not restart: starts=%+v stopped=%+v", manager.starts, manager.stopped)
	}
}

func TestEnsureRoutesPerTypeAndTransport(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())

	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "shared")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(2, "dhcpv6", "tap")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.EnsureDNS(ctx, &rookeryv1.EnsureDNSRequest{Spec: dnsSpec(3, "shared")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.EnsureBGP(ctx, &rookeryv1.EnsureBGPRequest{Spec: bgpSpec(4, "memif")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.EnsureBGP(ctx, &rookeryv1.EnsureBGPRequest{Spec: bgpSpec(5, "tap")}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"dhcpv4:shared:/run/rookery/memif/net-1.sock@10.20.0.2/24",
		"dhcpv6:tap:vnf2",
		"dns:shared:/run/rookery/memif/net-1.sock@10.20.0.2/24",
		"bgp:memif:/run/rookery/memif/4.sock",
		"bgp:tap:vnf5",
	}
	if strings.Join(manager.starts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("starts = %+v, want %+v", manager.starts, want)
	}
	for i, vnf := range manager.startVNFs {
		if vnf.Config.Transport == "" {
			t.Fatalf("start %d lost the transport: %+v", i, vnf)
		}
	}
}

func TestEnsureRejectsInvalidTransportEndpoints(t *testing.T) {
	ctx := context.Background()
	server := NewServer(newFakeManager(), t.TempDir(), zerolog.Nop())

	memif := dhcpSpec(1, "dhcpv4", "memif")
	memif.Runtime.MemifSocket = ""
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: memif}); err == nil {
		t.Fatal("expected an error for a memif spec without a socket")
	}
	dns := dnsSpec(2, "tap")
	if _, err := server.EnsureDNS(ctx, &rookeryv1.EnsureDNSRequest{Spec: dns}); err == nil {
		t.Fatal("expected an error for a DNS spec without the shared transport")
	}
	bgp := bgpSpec(3, "tap")
	bgp.Runtime.TapHostName = ""
	if _, err := server.EnsureBGP(ctx, &rookeryv1.EnsureBGPRequest{Spec: bgp}); err == nil {
		t.Fatal("expected an error for a tap spec without an interface")
	}
}

func TestReloadDHCPUpdatesDesiredSpecWithoutRestart(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	dir := t.TempDir()
	server := NewServer(manager, dir, zerolog.Nop())

	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	before := server.desired[1].hash
	server.mu.Unlock()

	changed := dhcpSpec(1, "dhcpv4", "tap")
	changed.GetDhcpv4().RangeEnd = "10.20.0.30"
	state, err := server.ReloadDHCP(ctx, &rookeryv1.ReloadDHCPRequest{Spec: changed})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !state.GetRunning() {
		t.Fatal("reload lost the running state")
	}
	if len(manager.reloaded) != 1 || manager.reloaded[0].Config.RangeEnd != "10.20.0.30" {
		t.Fatalf("reloaded = %+v", manager.reloaded)
	}
	if len(manager.starts) != 1 || len(manager.stopped) != 0 {
		t.Fatalf("reload restarted the runtime: starts=%+v stopped=%+v", manager.starts, manager.stopped)
	}
	server.mu.Lock()
	after := server.desired[1].hash
	server.mu.Unlock()
	if before == after {
		t.Fatal("reload did not update the desired spec hash")
	}
	persisted := readPersisted(t, dir, 1)
	if got := persisted.GetDhcp().GetDhcpv4().GetRangeEnd(); got != "10.20.0.30" {
		t.Fatalf("persisted range_end = %q", got)
	}
}

func TestReloadDHCPFailureRestoresDesiredSpec(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	dir := t.TempDir()
	server := NewServer(manager, dir, zerolog.Nop())

	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	before := server.desired[1].hash
	server.mu.Unlock()
	manager.reloadErr = errors.New("boom")

	changed := dhcpSpec(1, "dhcpv4", "tap")
	changed.GetDhcpv4().RangeEnd = "10.20.0.30"
	if _, err := server.ReloadDHCP(ctx, &rookeryv1.ReloadDHCPRequest{Spec: changed}); err == nil {
		t.Fatal("expected reload to fail")
	}
	server.mu.Lock()
	after := server.desired[1].hash
	server.mu.Unlock()
	if before != after {
		t.Fatal("failed reload changed the desired spec")
	}
	persisted := readPersisted(t, dir, 1)
	if got := persisted.GetDhcp().GetDhcpv4().GetRangeEnd(); got != "10.20.0.20" {
		t.Fatalf("persisted range_end = %q after failed reload", got)
	}

	// An ensure with the proposed spec still restarts because the old desired
	// spec survived the failed reload.
	manager.reloadErr = nil
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: changed}); err != nil {
		t.Fatal(err)
	}
	if len(manager.starts) != 2 || len(manager.stopped) != 1 {
		t.Fatalf("ensure after failed reload did not restart: starts=%+v stopped=%+v", manager.starts, manager.stopped)
	}
}

func TestReloadNotRunningReturnsStoppedState(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	dir := t.TempDir()
	server := NewServer(manager, dir, zerolog.Nop())
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	delete(manager.running, 1)

	changed := dhcpSpec(1, "dhcpv4", "tap")
	changed.GetDhcpv4().RangeEnd = "10.20.0.30"
	state, err := server.ReloadDHCP(ctx, &rookeryv1.ReloadDHCPRequest{Spec: changed})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if state.GetRunning() {
		t.Fatal("reload started a stopped runtime")
	}
	if len(manager.reloaded) != 0 || len(manager.starts) != 1 {
		t.Fatalf("stopped reload touched the runtime: reloaded=%+v starts=%+v", manager.reloaded, manager.starts)
	}
	// The new spec must replace the previous one even though the runtime is
	// stopped, so supervision or a manual start does not bring back the old
	// configuration.
	server.mu.Lock()
	desired := server.desired[1]
	server.mu.Unlock()
	if desired == nil || desired.spec.GetDhcp().GetDhcpv4().GetRangeEnd() != "10.20.0.30" {
		t.Fatalf("desired spec was not updated: %+v", desired)
	}
	data, err := os.ReadFile(filepath.Join(dir, "desired-1.json"))
	if err != nil {
		t.Fatalf("read persisted spec: %v", err)
	}
	persisted := &rookeryv1.DesiredRuntime{}
	if err := protojson.Unmarshal(data, persisted); err != nil {
		t.Fatalf("parse persisted spec: %v", err)
	}
	if persisted.GetDhcp().GetDhcpv4().GetRangeEnd() != "10.20.0.30" {
		t.Fatalf("persisted spec = %+v", persisted)
	}
}

func TestReloadMergesSharedRuntimeFields(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	dir := t.TempDir()
	server := NewServer(manager, dir, zerolog.Nop())
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "shared")}); err != nil {
		t.Fatal(err)
	}

	reload := dhcpSpec(1, "dhcpv4", "")
	reload.Runtime.SharedAddress = ""
	reload.Runtime.SharedIpv6Address = ""
	reload.Runtime.MemifSocket = ""
	reload.Runtime.MemifSwIfIndex = 0
	reload.GetDhcpv4().RangeEnd = "10.20.0.30"
	if _, err := server.ReloadDHCP(ctx, &rookeryv1.ReloadDHCPRequest{Spec: reload}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	persisted := readPersisted(t, dir, 1)
	runtime := persisted.GetDhcp().GetRuntime()
	if runtime.GetSharedAddress() != "10.20.0.2/24" || runtime.GetSharedIpv6Address() != "fd00:20::2/64" {
		t.Fatalf("reload dropped shared addresses: %+v", runtime)
	}
	if runtime.GetTransport() != "shared" || runtime.GetMemifSocket() == "" || runtime.GetMemifSwIfIndex() == 0 {
		t.Fatalf("reload dropped the shared endpoint: %+v", runtime)
	}
}

func TestReloadDNSAndBGPUseTheirServices(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	dir := t.TempDir()
	server := NewServer(manager, dir, zerolog.Nop())

	if _, err := server.EnsureDNS(ctx, &rookeryv1.EnsureDNSRequest{Spec: dnsSpec(1, "shared")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.EnsureBGP(ctx, &rookeryv1.EnsureBGPRequest{Spec: bgpSpec(2, "tap")}); err != nil {
		t.Fatal(err)
	}
	dns := dnsSpec(1, "shared")
	dns.Config.Records = append(dns.Config.Records, &rookeryv1.DNSRecordSpec{Name: "vm2.lab1.test.", Type: "A", Value: "10.20.0.51", Ttl: 60})
	if _, err := server.ReloadDNS(ctx, &rookeryv1.ReloadDNSRequest{Spec: dns}); err != nil {
		t.Fatal(err)
	}
	bgp := bgpSpec(2, "tap")
	bgp.Config.AdvertisedPrefixes = []*rookeryv1.BGPPrefixSpec{{Prefix: "10.99.0.0/24"}}
	if _, err := server.ReloadBGP(ctx, &rookeryv1.ReloadBGPRequest{Spec: bgp}); err != nil {
		t.Fatal(err)
	}
	if len(manager.reloaded) != 2 {
		t.Fatalf("reloaded = %+v", manager.reloaded)
	}
	if len(manager.reloaded[1].Config.AdvertisePrefixes) != 1 {
		t.Fatalf("BGP reload config = %+v", manager.reloaded[1].Config)
	}
}

func TestRemoveForgetsStateAndPersistence(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	dir := t.TempDir()
	server := NewServer(manager, dir, zerolog.Nop())
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.RemoveDHCP(ctx, &rookeryv1.RemoveDHCPRequest{Id: 1, RemoveLeases: true}); err != nil {
		t.Fatal(err)
	}
	if manager.running[1] {
		t.Fatal("remove did not stop the runtime")
	}
	if _, err := server.GetDHCPState(ctx, &rookeryv1.GetDHCPStateRequest{Id: 1}); status.Code(err) != codes.NotFound {
		t.Fatalf("state after remove = %v", err)
	}
	// A fresh server (daemon restart) must not resurrect the removed VNF.
	fresh := NewServer(newFakeManager(), dir, zerolog.Nop())
	if err := fresh.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.GetDHCPState(ctx, &rookeryv1.GetDHCPStateRequest{Id: 1}); status.Code(err) != codes.NotFound {
		t.Fatalf("restored state = %v", err)
	}
}

func TestGetStateIsPerType(t *testing.T) {
	ctx := context.Background()
	server := NewServer(newFakeManager(), t.TempDir(), zerolog.Nop())
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.EnsureDNS(ctx, &rookeryv1.EnsureDNSRequest{Spec: dnsSpec(2, "shared")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.GetDHCPState(ctx, &rookeryv1.GetDHCPStateRequest{Id: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.GetDNSState(ctx, &rookeryv1.GetDNSStateRequest{Id: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.GetDNSState(ctx, &rookeryv1.GetDNSStateRequest{Id: 1}); status.Code(err) != codes.NotFound {
		t.Fatalf("wrong-type state = %v", err)
	}
	if _, err := server.GetBGPState(ctx, &rookeryv1.GetBGPStateRequest{Id: 99}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown state = %v", err)
	}
}

func TestSuperviseRestartsDeadRuntime(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	server.restartBackoff = 0
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
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

func TestLoadRestoresDesiredRuntimesAndRejectsOldSpecs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	manager := newFakeManager()
	server := NewServer(manager, dir, zerolog.Nop())
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.EnsureDNS(ctx, &rookeryv1.EnsureDNSRequest{Spec: dnsSpec(2, "shared")}); err != nil {
		t.Fatal(err)
	}

	// Old VNFSpec files and unparsable files must be ignored, not restored.
	oldSpec := []byte(`{"id":99,"name":"legacy","type":"dhcpv4","network_id":1,"transport":"tap","config":{"gateway":"10.0.0.1/24"}}`)
	if err := os.WriteFile(filepath.Join(dir, "desired-99.json"), oldSpec, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "desired-98.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	restartedManager := newFakeManager()
	restarted := NewServer(restartedManager, dir, zerolog.Nop())
	if err := restarted.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.GetDHCPState(ctx, &rookeryv1.GetDHCPStateRequest{Id: 99}); status.Code(err) != codes.NotFound {
		t.Fatalf("old VNFSpec file was restored: %v", err)
	}
	restarted.restartBackoff = 0
	restarted.superviseOnce(ctx)
	if !restartedManager.running[1] || !restartedManager.running[2] {
		t.Fatalf("persisted desired runtimes were not restored: running=%+v", restartedManager.running)
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
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.EnsureBGP(ctx, &rookeryv1.EnsureBGPRequest{Spec: bgpSpec(2, "tap")}); err != nil {
		t.Fatal(err)
	}

	leases, err := server.ListDHCPLeases(ctx, &rookeryv1.ListDHCPLeasesRequest{Id: 1})
	if err != nil || len(leases.GetLeases()) != 1 || leases.GetLeases()[0].GetAddress() != "10.0.0.11" {
		t.Fatalf("leases = %+v, err = %v", leases, err)
	}
	if _, err := server.ListDHCPLeases(ctx, &rookeryv1.ListDHCPLeasesRequest{Id: 2}); status.Code(err) != codes.NotFound {
		t.Fatalf("DHCP leases for a BGP VNF = %v", err)
	}
	bgpStatus, err := server.GetBGPStatus(ctx, &rookeryv1.GetBGPStatusRequest{Id: 2})
	if err != nil || !bgpStatus.GetEstablished() || len(bgpStatus.GetReceived()) != 1 ||
		bgpStatus.GetReceived()[0].GetNextHop() != "10.40.0.100" ||
		len(bgpStatus.GetReceived()[0].GetAsPath()) != 1 {
		t.Fatalf("bgp status = %+v, err = %v", bgpStatus, err)
	}
	if _, err := server.GetBGPStatus(ctx, &rookeryv1.GetBGPStatusRequest{Id: 1}); status.Code(err) != codes.NotFound {
		t.Fatalf("BGP status for a DHCP VNF = %v", err)
	}
}

func TestListDNSRecordsRequiresDNSRuntime(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	manager.dns[1] = []host.DNSRecordView{{Name: "vm1.lab1.test.", Type: "A", Value: "10.20.0.50", Source: "dhcp-7", SourceVNFID: 7}}
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	if _, err := server.EnsureDNS(ctx, &rookeryv1.EnsureDNSRequest{Spec: dnsSpec(1, "shared")}); err != nil {
		t.Fatal(err)
	}
	response, err := server.ListDNSRecords(ctx, &rookeryv1.ListDNSRecordsRequest{Id: 1})
	if err != nil || len(response.GetRecords()) != 1 || response.GetRecords()[0].GetSourceVnfId() != 7 {
		t.Fatalf("dns records = %+v, err = %v", response, err)
	}
	if _, err := server.ListDNSRecords(ctx, &rookeryv1.ListDNSRecordsRequest{Id: 2}); status.Code(err) != codes.NotFound {
		t.Fatalf("DNS records for an unknown VNF = %v", err)
	}
}

func TestRemoveDHCPLeases(t *testing.T) {
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	response, err := server.RemoveDHCPLeases(context.Background(), &rookeryv1.RemoveDHCPLeasesRequest{
		Id: 7, ClientIds: []string{"02:00:00:00:00:01", "02:00:00:00:00:02"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetRemoved() != 2 {
		t.Fatalf("removed = %d, want 2", response.GetRemoved())
	}
	if got := manager.removed[7]; len(got) != 2 {
		t.Fatalf("manager removed leases = %+v", got)
	}
	if _, err := server.RemoveDHCPLeases(context.Background(), &rookeryv1.RemoveDHCPLeasesRequest{Id: 7}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty lease removal = %v", err)
	}
}

func TestRemoveDHCPLeasesError(t *testing.T) {
	manager := newFakeManager()
	manager.removeErr = errors.New("boom")
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	_, err := server.RemoveDHCPLeases(context.Background(), &rookeryv1.RemoveDHCPLeasesRequest{
		Id: 7, ClientIds: []string{"02:00:00:00:00:01"},
	})
	if err == nil || !strings.Contains(err.Error(), "could not remove VNF leases") {
		t.Fatalf("error = %v", err)
	}
}

// readPersisted decodes the wrapper persisted for id.
func readPersisted(t *testing.T, dir string, id int64) *rookeryv1.DesiredRuntime {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("desired-%d.json", id)))
	if err != nil {
		t.Fatal(err)
	}
	spec := &rookeryv1.DesiredRuntime{}
	if err := protojson.Unmarshal(data, spec); err != nil {
		t.Fatal(err)
	}
	return spec
}
