package host

import (
	"context"
	"strings"
	"testing"

	coredhcpserver "github.com/coredhcp/coredhcp/server"

	"github.com/voidstarit/rookery/internal/store"
)

// newReloadBranchManager returns a manager whose runtime state is populated by
// the individual cases, and cleans up any handler/lease registrations they add.
func newReloadBranchManager(t *testing.T) *VNFManager {
	t.Helper()
	manager, err := NewVNFManager(t.TempDir())
	if err != nil {
		t.Fatalf("new VNF manager: %v", err)
	}
	t.Cleanup(func() {
		manager.mu.Lock()
		for id := range manager.servers {
			leasePath := manager.leasePath(id)
			unregisterLeaseRuntime(leasePath)
			liveDHCPv4.Delete(leasePath)
			liveDHCPv6.Delete(leasePath)
			delete(manager.servers, id)
		}
		manager.mu.Unlock()
		_ = manager.Close()
	})
	return manager
}

func reloadDHCPv4VNF(id int64) store.VNF {
	return store.VNF{
		ID:   id,
		Type: store.VNFTypeDHCPv4,
		Config: store.VNFConfig{
			Gateway:    "10.20.0.1/24",
			BindIP:     "10.20.0.2/24",
			RangeStart: "10.20.0.100",
			RangeEnd:   "10.20.0.110",
			LeaseTime:  "12h",
		},
	}
}

func reloadDHCPv6VNF(id int64) store.VNF {
	return store.VNF{
		ID:   id,
		Type: store.VNFTypeDHCPv6,
		Config: store.VNFConfig{
			Gateway:    "fd00:20::1/64",
			RangeStart: "fd00:20::100",
			RangeEnd:   "fd00:20::110",
			LeaseTime:  "12h",
		},
	}
}

func TestVNFManagerReloadValidation(t *testing.T) {
	ctx := context.Background()
	manager := newReloadBranchManager(t)

	if err := manager.Reload(ctx, store.VNF{ID: 0, Type: store.VNFTypeDHCPv4}); err == nil ||
		!strings.Contains(err.Error(), "invalid VNF id") {
		t.Fatalf("Reload with id 0 = %v", err)
	}
	if err := manager.Reload(ctx, store.VNF{ID: 1, Type: "bogus"}); err == nil ||
		!strings.Contains(err.Error(), "unsupported VNF type") {
		t.Fatalf("Reload with an unsupported type = %v", err)
	}
	if err := manager.Reload(ctx, reloadDHCPv4VNF(2)); err == nil ||
		!strings.Contains(err.Error(), "is not running") {
		t.Fatalf("Reload without a runtime = %v", err)
	}

	manager.servers[3] = &managedVNF{vnfType: store.VNFTypeDHCPv6}
	err := manager.Reload(ctx, reloadDHCPv4VNF(3))
	if err == nil || !strings.Contains(err.Error(), "is a dhcpv6 runtime") {
		t.Fatalf("Reload with a mismatched type = %v", err)
	}
}

func TestVNFManagerReloadDHCPv4Runtime(t *testing.T) {
	ctx := context.Background()
	manager := newReloadBranchManager(t)

	manager.servers[4] = &managedVNF{vnfType: store.VNFTypeDHCPv4}
	if err := manager.Reload(ctx, reloadDHCPv4VNF(4)); err == nil ||
		!strings.Contains(err.Error(), "no running server") {
		t.Fatalf("Reload without a DHCPv4 server = %v", err)
	}

	manager.servers[5] = &managedVNF{vnfType: store.VNFTypeDHCPv4, servers: &coredhcpserver.Servers{}}
	if err := manager.Reload(ctx, reloadDHCPv4VNF(5)); err != nil {
		t.Fatalf("Reload DHCPv4: %v", err)
	}
	manager.dnsMu.Lock()
	_, registered := manager.dhcpConfigs[5]
	manager.dnsMu.Unlock()
	if !registered {
		t.Fatal("Reload did not register the DHCPv4 config for DNS integration")
	}

	invalid := reloadDHCPv4VNF(6)
	invalid.Config.RangeStart = "10.20.1.100" // outside the gateway subnet
	manager.servers[6] = &managedVNF{vnfType: store.VNFTypeDHCPv4, servers: &coredhcpserver.Servers{}}
	if err := manager.Reload(ctx, invalid); err == nil {
		t.Fatal("Reload accepted an invalid DHCPv4 range")
	}
}

func TestVNFManagerReloadDHCPv6Runtime(t *testing.T) {
	ctx := context.Background()
	manager := newReloadBranchManager(t)

	manager.servers[7] = &managedVNF{vnfType: store.VNFTypeDHCPv6}
	if err := manager.Reload(ctx, reloadDHCPv6VNF(7)); err == nil ||
		!strings.Contains(err.Error(), "no running server") {
		t.Fatalf("Reload without a DHCPv6 server = %v", err)
	}

	// An empty hostIf falls back to the memif zone name.
	manager.servers[8] = &managedVNF{vnfType: store.VNFTypeDHCPv6, servers: &coredhcpserver.Servers{}}
	if err := manager.Reload(ctx, reloadDHCPv6VNF(8)); err != nil {
		t.Fatalf("Reload DHCPv6: %v", err)
	}
	manager.dnsMu.Lock()
	_, registered := manager.dhcpConfigs[8]
	manager.dnsMu.Unlock()
	if !registered {
		t.Fatal("Reload did not register the DHCPv6 config for DNS integration")
	}

	invalid := reloadDHCPv6VNF(9)
	invalid.Config.RangeStart = "fd00:99::100"
	manager.servers[9] = &managedVNF{vnfType: store.VNFTypeDHCPv6, servers: &coredhcpserver.Servers{}}
	if err := manager.Reload(ctx, invalid); err == nil {
		t.Fatal("Reload accepted an out-of-prefix DHCPv6 range")
	}
}

func TestVNFManagerReloadDNSRuntime(t *testing.T) {
	ctx := context.Background()
	manager := newReloadBranchManager(t)

	manager.servers[10] = &managedVNF{vnfType: store.VNFTypeDNS}
	if err := manager.Reload(ctx, store.VNF{ID: 10, Type: store.VNFTypeDNS}); err == nil ||
		!strings.Contains(err.Error(), "not running on a shared network") {
		t.Fatalf("Reload without a shared network = %v", err)
	}

	network := &sharedNetwork{networkID: 3, dns: &sharedDNS{vnfID: 11, done: make(chan struct{})}}
	manager.servers[11] = &managedVNF{vnfType: store.VNFTypeDNS, shared: network, networkID: 3}
	vnf := store.VNF{
		ID:   11,
		Type: store.VNFTypeDNS,
		Config: store.VNFConfig{
			DNSRecords: []store.DNSRecord{{Name: "vm1.lab1.test.", Type: "A", Value: "10.20.0.50", TTL: 60}},
		},
	}
	if err := manager.Reload(ctx, vnf); err != nil {
		t.Fatalf("Reload DNS: %v", err)
	}
	manager.dnsMu.Lock()
	registeredDNS := manager.dnsRuntimes[11]
	manager.dnsMu.Unlock()
	if registeredDNS == nil {
		t.Fatal("Reload did not register the DNS runtime")
	}
	if network.dnsHandler() == nil {
		t.Fatal("Reload did not publish a DNS handler")
	}

	invalid := vnf
	invalid.Config.DNSRecords = []store.DNSRecord{{Name: "vm1.lab1.test.", Type: "A", Value: "not-an-ip"}}
	if err := manager.Reload(ctx, invalid); err == nil {
		t.Fatal("Reload accepted an invalid DNS record")
	}
}

func TestVNFManagerReloadBGPRuntime(t *testing.T) {
	ctx := context.Background()
	manager := newReloadBranchManager(t)

	bgpVNF := store.VNF{ID: 12, Type: store.VNFTypeBGP, Config: store.VNFConfig{LocalIP: "10.40.0.1/24"}}
	manager.servers[12] = &managedVNF{vnfType: store.VNFTypeBGP}
	if err := manager.Reload(ctx, bgpVNF); err == nil ||
		!strings.Contains(err.Error(), "no running server") {
		t.Fatalf("Reload without a BGP server = %v", err)
	}

	// A tap-backed BGP instance without a local address fails validation before
	// touching the GoBGP server.
	manager.servers[13] = &managedVNF{vnfType: store.VNFTypeBGP, bgp: &bgpInstance{}}
	bgpVNF.ID = 13
	if err := manager.Reload(ctx, bgpVNF); err == nil ||
		!strings.Contains(err.Error(), "no local address") {
		t.Fatalf("Reload of an uninitialised BGP instance = %v", err)
	}

	manager.servers[14] = &managedVNF{
		vnfType:  store.VNFTypeBGP,
		bgpMemif: &bgpMemifRuntime{server: &bgpInstance{}},
	}
	bgpVNF.ID = 14
	if err := manager.Reload(ctx, bgpVNF); err == nil ||
		!strings.Contains(err.Error(), "no local address") {
		t.Fatalf("Reload of an uninitialised BGP memif instance = %v", err)
	}
}

func TestVNFManagerReloadUnsupportedRuntime(t *testing.T) {
	ctx := context.Background()
	manager := newReloadBranchManager(t)
	manager.servers[15] = &managedVNF{vnfType: store.VNFTypeNAT44}
	err := manager.Reload(ctx, store.VNF{ID: 15, Type: store.VNFTypeNAT44})
	if err == nil || !strings.Contains(err.Error(), "has no daemon runtime") {
		t.Fatalf("Reload of a NAT44 VNF = %v", err)
	}
}
