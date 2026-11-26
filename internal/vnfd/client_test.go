package vnfd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// startTestDaemon serves all four daemon services on a loopback listener and
// returns a connected client.
func startTestDaemon(t *testing.T, manager Manager, logger zerolog.Logger) *Client {
	t.Helper()
	listener, err := Listen("tcp://127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager, t.TempDir(), logger)
	grpcServer := grpc.NewServer()
	rookeryv1.RegisterDHCPDaemonServer(grpcServer, server)
	rookeryv1.RegisterDNSDaemonServer(grpcServer, server)
	rookeryv1.RegisterBGPDaemonServer(grpcServer, server)
	rookeryv1.RegisterVNFRuntimeAdminServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	client, err := NewClient("tcp://"+listener.Addr().String(), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func dhcpVNF(id int64) store.VNF {
	return store.VNF{
		ID: id, Name: "dhcp", NetworkID: 7, Type: store.VNFTypeDHCPv4,
		Config: store.VNFConfig{Gateway: "10.0.0.1/24", RangeStart: "10.0.0.10", RangeEnd: "10.0.0.20", Transport: "tap"},
	}
}

func dnsVNF(id int64) store.VNF {
	return store.VNF{
		ID: id, Name: "dns", NetworkID: 7, Type: store.VNFTypeDNS,
		Config: store.VNFConfig{
			DNSForward: true,
			DNSRecords: []store.DNSRecord{{Name: "vm1.lab1.test.", Type: "A", Value: "10.20.0.50", TTL: 120}},
		},
	}
}

func bgpVNF(id int64) store.VNF {
	return store.VNF{
		ID: id, Name: "bgp", NetworkID: 7, Type: store.VNFTypeBGP,
		Config: store.VNFConfig{
			Transport: "memif", LocalAS: 64512, PeerAS: 64513, RouterID: "10.0.0.1",
			PeerIP: "10.40.0.2", LocalIP: "10.40.0.1/24",
		},
	}
}

func TestClientDHCPRoundTrip(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	manager.leases[1] = []store.VNFLease{{ClientID: "aa:bb", Address: "10.0.0.11", Active: true}}
	client := startTestDaemon(t, manager, zerolog.Nop())

	if err := client.Start(ctx, dhcpVNF(1), "vnf1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !client.Running(ctx, dhcpVNF(1)) {
		t.Fatal("running = false after start")
	}
	if len(manager.startVNFs) != 1 || manager.startVNFs[0].Type != store.VNFTypeDHCPv4 || manager.startVNFs[0].Config.Transport != "tap" {
		t.Fatalf("started = %+v", manager.startVNFs)
	}
	leases, err := client.Leases(ctx, 1)
	if err != nil || len(leases) != 1 || leases[0].Address != "10.0.0.11" {
		t.Fatalf("leases = %+v, err = %v", leases, err)
	}
	removed, err := client.RemoveLeases(ctx, 1, []string{"aa:bb"})
	if err != nil || removed != 1 {
		t.Fatalf("remove leases = %d, err = %v", removed, err)
	}
	if got := manager.removed[1]; len(got) != 1 || got[0] != "aa:bb" {
		t.Fatalf("removed leases = %+v", got)
	}

	changed := dhcpVNF(1)
	changed.Config.RangeEnd = "10.0.0.30"
	if err := client.ReloadVNF(ctx, changed); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(manager.reloaded) != 1 || manager.reloaded[0].Config.RangeEnd != "10.0.0.30" {
		t.Fatalf("reloaded = %+v", manager.reloaded)
	}

	if err := client.Stop(ctx, dhcpVNF(1), true); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if client.Running(ctx, dhcpVNF(1)) {
		t.Fatal("running = true after stop")
	}
}

func TestClientDNSAndBGPRouting(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	manager.dns[2] = []host.DNSRecordView{{Name: "vm1.lab1.test.", Type: "A", Value: "10.20.0.50", Source: "dhcp-1", SourceVNFID: 1}}
	manager.bgp[3] = host.BGPStatus{Running: true, SessionState: "SESSION_STATE_ACTIVE"}
	client := startTestDaemon(t, manager, zerolog.Nop())

	if err := client.StartShared(ctx, dnsVNF(2), "/run/rookery/memif/net-7.sock", 1, "10.20.0.2/24", "fd00:20::2/64"); err != nil {
		t.Fatalf("start DNS: %v", err)
	}
	if !client.Running(ctx, dnsVNF(2)) {
		t.Fatal("DNS running = false after start")
	}
	records, err := client.DNSRecords(ctx, 2)
	if err != nil || len(records) != 1 || records[0].SourceVNFID != 1 {
		t.Fatalf("dns records = %+v, err = %v", records, err)
	}
	dns := dnsVNF(2)
	dns.Config.DNSRecords = append(dns.Config.DNSRecords, store.DNSRecord{Name: "vm2.lab1.test.", Type: "A", Value: "10.20.0.51", TTL: 60})
	if err := client.ReloadVNF(ctx, dns); err != nil {
		t.Fatalf("reload DNS: %v", err)
	}

	if err := client.StartMemif(ctx, bgpVNF(3), "/run/rookery/memif/3.sock", 3); err != nil {
		t.Fatalf("start BGP: %v", err)
	}
	if !client.Running(ctx, bgpVNF(3)) {
		t.Fatal("BGP running = false after start")
	}
	status, err := client.BGPStatus(ctx, 3)
	if err != nil || status.SessionState != "SESSION_STATE_ACTIVE" {
		t.Fatalf("bgp status = %+v, err = %v", status, err)
	}
	bgp := bgpVNF(3)
	bgp.Config.AdvertisePrefixes = []string{"10.99.0.0/24"}
	if err := client.ReloadVNF(ctx, bgp); err != nil {
		t.Fatalf("reload BGP: %v", err)
	}
	if len(manager.reloaded) != 2 {
		t.Fatalf("reloaded = %+v", manager.reloaded)
	}
	if len(manager.reloaded[0].Config.DNSRecords) != 2 {
		t.Fatalf("DNS reload config = %+v", manager.reloaded[0].Config)
	}
	if len(manager.reloaded[1].Config.AdvertisePrefixes) != 1 {
		t.Fatalf("BGP reload config = %+v", manager.reloaded[1].Config)
	}

	if err := client.Stop(ctx, dnsVNF(2), false); err != nil {
		t.Fatalf("stop DNS: %v", err)
	}
	if err := client.Stop(ctx, bgpVNF(3), false); err != nil {
		t.Fatalf("stop BGP: %v", err)
	}
}

func TestClientReloadOfStoppedRuntimeIsNoop(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	client := startTestDaemon(t, manager, zerolog.Nop())
	if err := client.Start(ctx, dhcpVNF(1), "vnf1"); err != nil {
		t.Fatal(err)
	}
	delete(manager.running, 1)
	changed := dhcpVNF(1)
	changed.Config.RangeEnd = "10.0.0.30"
	if err := client.ReloadVNF(ctx, changed); err != nil {
		t.Fatalf("reload of a stopped runtime: %v", err)
	}
	if len(manager.reloaded) != 0 {
		t.Fatalf("reloaded = %+v", manager.reloaded)
	}
}

func TestClientNAT44NeverReachesDaemon(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	client := startTestDaemon(t, manager, zerolog.Nop())
	vnf := store.VNF{ID: 9, Name: "nat", NetworkID: 7, Type: store.VNFTypeNAT44}
	if err := client.Start(ctx, vnf, "vnf9"); err == nil {
		t.Fatal("start NAT44 succeeded")
	}
	if err := client.Stop(ctx, vnf, true); err != nil {
		t.Fatalf("stop NAT44: %v", err)
	}
	if client.Running(ctx, vnf) {
		t.Fatal("NAT44 reported running")
	}
	if err := client.ReloadVNF(ctx, vnf); err != nil {
		t.Fatalf("reload NAT44: %v", err)
	}
	if len(manager.startVNFs) != 0 || len(manager.stopped) != 0 {
		t.Fatalf("NAT44 reached the daemon: starts=%+v stopped=%+v", manager.startVNFs, manager.stopped)
	}
}

func TestClientUnavailableDaemonReportsStopped(t *testing.T) {
	client, err := NewClient("tcp://127.0.0.1:1", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.Running(context.Background(), dhcpVNF(1)) {
		t.Fatal("running = true without a daemon")
	}
	if _, err := client.RemoveLeases(context.Background(), 1, []string{"aa"}); err == nil {
		t.Fatal("remove leases succeeded without a daemon")
	}
	reloadCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := client.ReloadVNF(reloadCtx, dhcpVNF(1)); err == nil {
		t.Fatal("reload succeeded without a daemon")
	}
}

func TestDialTarget(t *testing.T) {
	tests := []struct {
		addr string
		want string
		err  bool
	}{
		{addr: "unix:///run/rookery/vnfs.sock", want: "unix:///run/rookery/vnfs.sock"},
		{addr: "/run/rookery/vnfs.sock", want: "unix:///run/rookery/vnfs.sock"},
		{addr: "tcp://127.0.0.1:9090", want: "127.0.0.1:9090"},
		{addr: "tcp://", err: true},
		{addr: "127.0.0.1:9090", err: true},
	}
	for _, test := range tests {
		got, err := dialTarget(test.addr)
		if test.err {
			if err == nil {
				t.Errorf("dialTarget(%q) = %q, want an error", test.addr, got)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Errorf("dialTarget(%q) = %q, %v; want %q", test.addr, got, err, test.want)
		}
	}
}

func TestClientErrorIncludesAction(t *testing.T) {
	client, err := NewClient("tcp://127.0.0.1:1", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.RemoveLeases(context.Background(), 1, []string{"aa"})
	if err == nil || !strings.HasPrefix(err.Error(), "remove VNF leases: ") {
		t.Fatalf("error = %v", err)
	}
}

// TestClientErrorPreservesGRPCCode proves the API can distinguish a lost
// desired spec (NotFound) from a transient daemon failure.
func TestClientErrorPreservesGRPCCode(t *testing.T) {
	err := grpcError("reload VNF", status.Error(codes.NotFound, "vnf is not managed"))
	if code := status.Code(err); code != codes.NotFound {
		t.Fatalf("status code = %v, want NotFound (%v)", code, err)
	}
	transient := grpcError("reload VNF", errors.New("connection refused"))
	if code := status.Code(transient); code != codes.Unknown {
		t.Fatalf("non-gRPC status code = %v, want Unknown", code)
	}
}
