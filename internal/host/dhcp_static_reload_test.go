package host

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/insomniacslk/dhcp/dhcpv4"

	"github.com/voidstarit/rookery/internal/store"
)

// TestDHCPReloadAppliesNewStaticAssignment proves a static reservation added
// after the runtime started (the Terraform child-resource flow) takes effect on
// the live listener: the reserved client gets its address and the dynamic pool
// skips it, while the existing dynamic lease survives.
func TestDHCPReloadAppliesNewStaticAssignment(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "leases.json")
	vnf := dhcpReloadVNF("10.20.0.100", "10.20.0.110")
	handlers, err := newDHCPv4Handlers(vnf, leasePath, defaultDHCPPort)
	if err != nil {
		t.Fatalf("load handlers: %v", err)
	}

	dynamicMAC, err := net.ParseMAC("02:00:00:00:00:01")
	if err != nil {
		t.Fatal(err)
	}
	reply, _ := replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, dynamicMAC, dhcpv4.TransactionID{1}))
	if reply == nil || reply.YourIPAddr.String() != "10.20.0.100" {
		t.Fatalf("dynamic offer = %+v", reply)
	}

	vnf.Config.StaticAssignments = []store.DHCPStaticAssignment{
		{MAC: "02:00:00:00:00:02", IP: "10.20.0.102"},
	}
	if _, err := newDHCPv4Handlers(vnf, leasePath, defaultDHCPPort); err != nil {
		t.Fatalf("reload handlers: %v", err)
	}

	staticMAC, err := net.ParseMAC("02:00:00:00:00:02")
	if err != nil {
		t.Fatal(err)
	}
	reply, _ = replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, staticMAC, dhcpv4.TransactionID{2}))
	if reply == nil || reply.YourIPAddr.String() != "10.20.0.102" {
		t.Fatalf("static offer after reload = %+v, want 10.20.0.102", reply)
	}

	otherMAC, err := net.ParseMAC("02:00:00:00:00:03")
	if err != nil {
		t.Fatal(err)
	}
	reply, _ = replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, otherMAC, dhcpv4.TransactionID{3}))
	if reply == nil || reply.YourIPAddr.String() != "10.20.0.101" {
		t.Fatalf("dynamic offer after reload = %+v, want 10.20.0.101 (reserved .102 skipped)", reply)
	}
}
