package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/store"
)

func TestRemoveLeasesForInterface(t *testing.T) {
	ta := newTestAPI(t)
	ctx := context.Background()
	network, err := ta.store.CreateNetwork(ctx, store.Network{Name: "lease-cleanup", BridgeDomainID: 220})
	if err != nil {
		t.Fatal(err)
	}
	mac := "02:00:00:00:00:0a"
	iface, err := ta.store.CreateInterface(ctx, store.VirtualInterface{
		Name: "vm-lease", NetworkID: network.ID, MAC: mac, SocketPath: "/run/vpp/vm-lease.sock",
	})
	if err != nil {
		t.Fatal(err)
	}
	vnf, err := ta.store.CreateVNF(ctx, store.VNF{
		Name: "dhcp", NetworkID: network.ID, Type: store.VNFTypeDHCPv4, Enabled: true,
		Config: store.VNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ta.vnfs.leases[vnf.ID] = []store.VNFLease{
		{ClientID: mac, Address: "10.20.0.100", Active: true, ExpiresAt: time.Now().Add(time.Hour)},
		{ClientID: "02:00:00:00:00:0b", Address: "10.20.0.101", Active: true, ExpiresAt: time.Now().Add(time.Hour)},
	}

	if err := ta.server.RemoveLeasesForInterface(ctx, network.ID, mac); err != nil {
		t.Fatal(err)
	}
	if removed := ta.vnfs.removedLeases[vnf.ID]; len(removed) != 0 {
		t.Fatalf("lease removed while its interface still exists: %+v", removed)
	}

	if err := ta.store.DeleteInterface(ctx, iface.ID); err != nil {
		t.Fatal(err)
	}
	if err := ta.server.RemoveLeasesForInterface(ctx, network.ID, mac); err != nil {
		t.Fatal(err)
	}
	removed := ta.vnfs.removedLeases[vnf.ID]
	if len(removed) != 1 || removed[0] != mac {
		t.Fatalf("removed leases = %+v", removed)
	}
}
