package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestVPPInterfaceRouteTables(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "vpp-interface-tables.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ipv4, err := st.CreateRouteTable(ctx, store.RouteTable{Name: "lab-v4", TableID: 500, IPv6: false})
	if err != nil {
		t.Fatal(err)
	}
	ipv6, err := st.CreateRouteTable(ctx, store.RouteTable{Name: "lab-v6", TableID: 501, IPv6: true})
	if err != nil {
		t.Fatal(err)
	}

	empty, err := st.GetVPPInterfaceRouteTables(ctx, "host-eth0")
	if err != nil {
		t.Fatal(err)
	}
	if empty.IPv4TableID != 0 || empty.IPv6TableID != 0 {
		t.Fatalf("empty route tables = %+v", empty)
	}

	updated, err := st.SetVPPInterfaceRouteTables(ctx, "host-eth0", ipv4.TableID, ipv6.TableID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.IPv4TableID != 500 || updated.IPv6TableID != 501 || updated.IPv4TableName != "lab-v4" || updated.IPv6TableName != "lab-v6" {
		t.Fatalf("updated route tables = %+v", updated)
	}

	count, err := st.RouteTableInterfaceCount(ctx, ipv4.TableID, false)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("route table interface count = %d, want 1", count)
	}

	all, err := st.ListAllVPPInterfaceRouteTables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all["host-eth0"].IPv4TableID != 500 {
		t.Fatalf("all route tables = %+v", all)
	}

	if _, err := st.SetVPPInterfaceRouteTables(ctx, "host-eth0", 999, 0); err == nil {
		t.Fatal("expected validation error for unknown IPv4 table")
	}

	network, err := st.CreateNetwork(ctx, store.Network{Name: "lab", BridgeDomainID: 800})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateInterface(ctx, store.VirtualInterface{Name: "vhost-lab", NetworkID: network.ID}); err != nil {
		t.Fatal(err)
	}
	isVirtual, err := st.IsVirtualInterfaceName(ctx, "vhost-lab")
	if err != nil {
		t.Fatal(err)
	}
	if !isVirtual {
		t.Fatal("vhost-lab should be detected as a virtual interface")
	}
	isVirtual, err = st.IsVirtualInterfaceName(ctx, "host-eth0")
	if err != nil {
		t.Fatal(err)
	}
	if isVirtual {
		t.Fatal("host-eth0 should not be detected as a virtual interface")
	}

	if err := st.DeleteVPPInterfaceAddresses(ctx, "host-eth0"); err != nil {
		t.Fatal(err)
	}
	cleared, err := st.GetVPPInterfaceRouteTables(ctx, "host-eth0")
	if err != nil {
		t.Fatal(err)
	}
	if cleared.IPv4TableID != 0 || cleared.IPv6TableID != 0 {
		t.Fatalf("cleared route tables = %+v", cleared)
	}
}
