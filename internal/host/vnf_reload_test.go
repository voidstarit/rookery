package host

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/miekg/dns"

	"github.com/voidstarit/rookery/internal/store"
)

func testDHCPDiscover(t *testing.T, mac net.HardwareAddr, xid dhcpv4.TransactionID) []byte {
	t.Helper()
	request, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(dhcpv4.MessageTypeDiscover),
		dhcpv4.WithTransactionID(xid),
		dhcpv4.WithBroadcast(true),
	)
	if err != nil {
		t.Fatalf("build DHCP discover: %v", err)
	}
	return request.ToBytes()
}

func dhcpReloadVNF(start, end string) store.VNF {
	return store.VNF{
		ID:   7,
		Type: store.VNFTypeDHCPv4,
		Config: store.VNFConfig{
			Gateway:    "10.20.0.1/24",
			BindIP:     "10.20.0.2/24",
			RangeStart: start,
			RangeEnd:   end,
			LeaseTime:  "12h",
		},
	}
}

// TestDHCPReloadSwapsHandlerAndKeepsLeaseFile proves a reload replaces the
// packet handler behind the same listener while the lease file survives.
func TestDHCPReloadSwapsHandlerAndKeepsLeaseFile(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "leases.json")
	vnf := dhcpReloadVNF("10.20.0.100", "10.20.0.110")
	handlers, err := newDHCPv4Handlers(vnf, leasePath, defaultDHCPPort)
	if err != nil {
		t.Fatalf("load handlers: %v", err)
	}

	mac1, err := net.ParseMAC("02:00:00:00:00:01")
	if err != nil {
		t.Fatal(err)
	}
	reply, _ := replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, mac1, dhcpv4.TransactionID{1}))
	if reply == nil || reply.YourIPAddr.String() != "10.20.0.100" {
		t.Fatalf("first offer = %+v", reply)
	}
	before, err := os.ReadFile(leasePath)
	if err != nil {
		t.Fatalf("lease file after allocation: %v", err)
	}

	// A config-only reload must not remove or rewrite the lease file.
	reloaded := dhcpReloadVNF("10.20.0.101", "10.20.0.111")
	if _, err := newDHCPv4Handlers(reloaded, leasePath, defaultDHCPPort); err != nil {
		t.Fatalf("reload handlers: %v", err)
	}
	after, err := os.ReadFile(leasePath)
	if err != nil {
		t.Fatalf("lease file after reload: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("reload rewrote the lease file:\nbefore=%s\nafter=%s", before, after)
	}

	// The original handler chain now serves the reloaded range.
	mac2, err := net.ParseMAC("02:00:00:00:00:02")
	if err != nil {
		t.Fatal(err)
	}
	reply, _ = replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, mac2, dhcpv4.TransactionID{2}))
	if reply == nil {
		t.Fatal("no offer after reload")
	}
	offered := reply.YourIPAddr.String()
	if offered != "10.20.0.101" && offered != "10.20.0.111" {
		t.Fatalf("offer after reload = %s, want an address from the new range", offered)
	}

	// A failed reload leaves the previous range serving: an invalid config
	// cannot publish a new handler.
	invalid := dhcpReloadVNF("10.20.1.100", "10.20.1.110") // outside the gateway subnet
	if _, err := newDHCPv4Handlers(invalid, leasePath, defaultDHCPPort); err == nil {
		t.Fatal("expected an invalid reload to fail")
	}
	mac3, err := net.ParseMAC("02:00:00:00:00:03")
	if err != nil {
		t.Fatal(err)
	}
	reply, _ = replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, mac3, dhcpv4.TransactionID{3}))
	if reply == nil || !strings.HasPrefix(reply.YourIPAddr.String(), "10.20.0.1") {
		t.Fatalf("failed reload changed the serving config: %+v", reply)
	}
}

// TestDNSReloadKeepsListenerAndRestoresFailure proves the DNS listener and
// dynamic records survive a handler swap, and a rejected config leaves the
// previous handler serving.
func TestDNSReloadKeepsListenerAndRestoresFailure(t *testing.T) {
	vnf := store.VNF{
		ID: 3, Type: store.VNFTypeDNS,
		Config: store.VNFConfig{
			DNSRecords: []store.DNSRecord{{Name: "vm1.lab1.test.", Type: "A", Value: "10.20.0.50", TTL: 120}},
		},
	}
	handler, err := buildDNSHandler(vnf.Config)
	if err != nil {
		t.Fatal(err)
	}
	handler.vnfID = vnf.ID
	handler.networkID = 1
	handler.ReplaceDynamic("dhcp-7", []store.DNSRecord{{Name: "lease.lab1.test.", Type: "A", Value: "10.20.0.77", TTL: 60}})

	component := &sharedDNS{vnfID: vnf.ID, done: make(chan struct{})}
	component.handler.Store(handler)
	network := &sharedNetwork{networkID: 1, dns: component}

	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{PacketConn: packetConn, Handler: component}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })
	address := packetConn.LocalAddr().String()

	query := func(name string) *dns.Msg {
		request := new(dns.Msg)
		request.SetQuestion(dns.Fqdn(name), dns.TypeA)
		client := &dns.Client{Timeout: 2 * time.Second}
		response, _, err := client.Exchange(request, address)
		if err != nil {
			t.Fatalf("query %s: %v", name, err)
		}
		return response
	}

	if response := query("vm1.lab1.test"); len(response.Answer) != 1 {
		t.Fatalf("initial answer = %+v", response)
	}

	next := vnf
	next.Config.DNSRecords = []store.DNSRecord{{Name: "vm2.lab1.test.", Type: "A", Value: "10.20.0.51", TTL: 60}}
	if err := network.reloadDNS(next); err != nil {
		t.Fatalf("reload DNS: %v", err)
	}
	if network.dns != component {
		t.Fatal("reload replaced the listener component")
	}
	if response := query("vm2.lab1.test"); len(response.Answer) != 1 {
		t.Fatalf("reloaded answer = %+v", response)
	}
	if response := query("vm1.lab1.test"); response.Rcode != dns.RcodeNameError {
		t.Fatalf("old record still served after reload: %+v", response)
	}
	if response := query("lease.lab1.test"); len(response.Answer) != 1 {
		t.Fatalf("dynamic lease record lost on reload: %+v", response)
	}
	original := component.current()

	invalid := next
	invalid.Config.DNSRecords = []store.DNSRecord{{Name: "broken.lab1.test.", Type: "A", Value: "not-an-ip"}}
	if err := network.reloadDNS(invalid); err == nil {
		t.Fatal("expected invalid DNS reload to fail")
	}
	if component.current() != original {
		t.Fatal("failed reload replaced the serving handler")
	}
	if response := query("vm2.lab1.test"); len(response.Answer) != 1 {
		t.Fatalf("failed reload dropped the previous config: %+v", response)
	}
}

func advertisedSet(status BGPStatus) map[string]struct{} {
	set := make(map[string]struct{}, len(status.Advertised))
	for _, prefix := range status.Advertised {
		set[prefix] = struct{}{}
	}
	return set
}

// TestBGPReloadSwapsAdvertisedPrefixes keeps the GoBGP server up, withdraws the
// old prefix set, and leaves it in place when the proposed config is invalid.
func TestBGPReloadSwapsAdvertisedPrefixes(t *testing.T) {
	ctx := context.Background()
	_, initial, err := net.ParseCIDR("10.99.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	instance, err := startBGPServer(ctx, bgpStartConfig{
		VNF:     store.VNF{ID: 31, Type: store.VNFTypeBGP, Config: store.VNFConfig{Transport: "tap"}},
		LocalIP: net.ParseIP("127.0.0.1").To4(),
		PeerIP:  net.ParseIP("127.0.0.2").To4(),
		LocalAS: 64512, PeerAS: 64513, RouterID: "10.0.0.1",
		Passive: true, Neighbor: "127.0.0.2", LocalAddress: "127.0.0.1",
		ListenAddress: []string{"127.0.0.1"}, ListenPort: 1182,
		Advertise: []*net.IPNet{initial},
	})
	if err != nil {
		t.Fatalf("start BGP server: %v", err)
	}
	defer instance.Close(ctx)
	server := instance.server

	vnf := store.VNF{
		ID: 31, Type: store.VNFTypeBGP,
		Config: store.VNFConfig{
			Transport:         "tap",
			LocalIP:           "127.0.0.1/32",
			AdvertisePrefixes: []string{"10.98.0.0/24", "10.97.0.0/24"},
		},
	}
	if err := instance.reload(ctx, vnf); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if instance.server != server {
		t.Fatal("reload replaced the GoBGP server")
	}
	status := instance.Status(ctx)
	want := map[string]struct{}{"10.98.0.0/24": {}, "10.97.0.0/24": {}}
	if got := advertisedSet(status); len(got) != len(want) {
		t.Fatalf("advertised after reload = %+v", status.Advertised)
	} else {
		for prefix := range want {
			if _, ok := got[prefix]; !ok {
				t.Fatalf("advertised after reload = %+v, missing %s", status.Advertised, prefix)
			}
		}
	}

	bad := vnf
	bad.Config.AdvertisePrefixes = []string{"not-a-prefix"}
	if err := instance.reload(ctx, bad); err == nil {
		t.Fatal("expected an invalid BGP reload to fail")
	}
	status = instance.Status(ctx)
	if got := advertisedSet(status); len(got) != len(want) {
		t.Fatalf("failed reload changed advertised prefixes: %+v", status.Advertised)
	}
	for prefix := range want {
		if _, ok := advertisedSet(status)[prefix]; !ok {
			t.Fatalf("failed reload dropped %s: %+v", prefix, status.Advertised)
		}
	}
}

func TestVNFManagerReloadRequiresRunningRuntime(t *testing.T) {
	manager, err := NewVNFManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.Reload(context.Background(), store.VNF{ID: 1, Type: store.VNFTypeDHCPv4}); err == nil {
		t.Fatal("reload of a missing runtime succeeded")
	}
}
