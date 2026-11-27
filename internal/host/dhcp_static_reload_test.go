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
// the live listener: the reserved client gets its address, the dynamic pool
// skips it, and the existing dynamic leases survive the reload.
func TestDHCPReloadAppliesNewStaticAssignment(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "leases.json")
	vnf := dhcpReloadVNF("10.20.0.100", "10.20.0.110")
	handlers, err := newDHCPv4Handlers(vnf, leasePath, defaultDHCPPort)
	if err != nil {
		t.Fatalf("load handlers: %v", err)
	}

	// Take the first two dynamic addresses before the reservation exists, so a
	// later offer can prove the allocator skipped the reserved address rather
	// than reaching it after the leased ones.
	wantInitial := []string{"10.20.0.100", "10.20.0.101"}
	for i, macText := range []string{"02:00:00:00:00:01", "02:00:00:00:00:02"} {
		mac, err := net.ParseMAC(macText)
		if err != nil {
			t.Fatal(err)
		}
		reply, _ := replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, mac, dhcpv4.TransactionID{byte(i + 1)}))
		if reply == nil || reply.YourIPAddr.String() != wantInitial[i] {
			t.Fatalf("dynamic offer %d = %+v, want %s", i, reply, wantInitial[i])
		}
	}

	vnf.Config.StaticAssignments = []store.DHCPStaticAssignment{
		{MAC: "02:00:00:00:00:09", IP: "10.20.0.102"},
	}
	if _, err := newDHCPv4Handlers(vnf, leasePath, defaultDHCPPort); err != nil {
		t.Fatalf("reload handlers: %v", err)
	}

	staticMAC, err := net.ParseMAC("02:00:00:00:00:09")
	if err != nil {
		t.Fatal(err)
	}
	reply, _ := replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, staticMAC, dhcpv4.TransactionID{3}))
	if reply == nil || reply.YourIPAddr.String() != "10.20.0.102" {
		t.Fatalf("static offer after reload = %+v, want 10.20.0.102", reply)
	}

	// .100 and .101 are leased and .102 is reserved, so the next client must be
	// offered .103. Asserting .101 here would not prove the reservation is
	// skipped.
	otherMAC, err := net.ParseMAC("02:00:00:00:00:03")
	if err != nil {
		t.Fatal(err)
	}
	reply, _ = replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, otherMAC, dhcpv4.TransactionID{4}))
	if reply == nil || reply.YourIPAddr.String() != "10.20.0.103" {
		t.Fatalf("dynamic offer after reload = %+v, want 10.20.0.103 (reserved .102 skipped)", reply)
	}

	// The client holding .100 kept its lease across the reload.
	firstMAC, err := net.ParseMAC("02:00:00:00:00:01")
	if err != nil {
		t.Fatal(err)
	}
	reply, _ = replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, firstMAC, dhcpv4.TransactionID{5}))
	if reply == nil || reply.YourIPAddr.String() != "10.20.0.100" {
		t.Fatalf("existing lease after reload = %+v, want 10.20.0.100", reply)
	}
}

// TestDHCPReloadMigratesClientToStaticAssignment proves the documented
// NAK-and-migrate path: a client that already holds a dynamic lease and then
// gains a reservation is NAKed when it renews the old address, is offered its
// reserved address on the next discover, and releases the old address.
func TestDHCPReloadMigratesClientToStaticAssignment(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "leases.json")
	vnf := dhcpReloadVNF("10.20.0.100", "10.20.0.110")
	handlers, err := newDHCPv4Handlers(vnf, leasePath, defaultDHCPPort)
	if err != nil {
		t.Fatalf("load handlers: %v", err)
	}

	clientMAC, err := net.ParseMAC("02:00:00:00:00:01")
	if err != nil {
		t.Fatal(err)
	}
	reply, _ := replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, clientMAC, dhcpv4.TransactionID{1}))
	if reply == nil || reply.YourIPAddr.String() != "10.20.0.100" {
		t.Fatalf("dynamic offer = %+v, want 10.20.0.100", reply)
	}

	// The client now has a reservation, so its renewed .100 must be refused and
	// the next discover moves it to .102.
	vnf.Config.StaticAssignments = []store.DHCPStaticAssignment{
		{MAC: "02:00:00:00:00:01", IP: "10.20.0.102"},
	}
	if _, err := newDHCPv4Handlers(vnf, leasePath, defaultDHCPPort); err != nil {
		t.Fatalf("reload handlers: %v", err)
	}

	renew := dhcpTestRequest(t, dhcpv4.MessageTypeRequest, clientMAC,
		dhcpv4.WithOption(dhcpv4.OptRequestedIPAddress(net.ParseIP("10.20.0.100"))),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.ParseIP("10.20.0.2"))),
	)
	nak, _ := replyDHCPv4(vnf.ID, handlers, renew.ToBytes())
	if nak == nil || nak.MessageType() != dhcpv4.MessageTypeNak {
		t.Fatalf("renew of the old address = %v, want NAK", nak)
	}

	reply, _ = replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, clientMAC, dhcpv4.TransactionID{2}))
	if reply == nil || reply.YourIPAddr.String() != "10.20.0.102" {
		t.Fatalf("offer after the NAK = %+v, want 10.20.0.102", reply)
	}

	// The old dynamic address is released, so a new client can take it.
	freshMAC, err := net.ParseMAC("02:00:00:00:00:03")
	if err != nil {
		t.Fatal(err)
	}
	reply, _ = replyDHCPv4(vnf.ID, handlers, testDHCPDiscover(t, freshMAC, dhcpv4.TransactionID{3}))
	if reply == nil || reply.YourIPAddr.String() != "10.20.0.100" {
		t.Fatalf("offer for the released address = %+v, want 10.20.0.100", reply)
	}
}
