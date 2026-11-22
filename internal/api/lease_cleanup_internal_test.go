package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/store"
)

type fakeLeaseRuntime struct {
	leases  map[int64][]store.VNFLease
	removed map[int64][]string
}

func (f *fakeLeaseRuntime) Leases(_ context.Context, vnfID int64) ([]store.VNFLease, error) {
	return f.leases[vnfID], nil
}

func (f *fakeLeaseRuntime) RemoveLeases(_ context.Context, vnfID int64, clientIDs []string) (int, error) {
	f.removed[vnfID] = append(f.removed[vnfID], clientIDs...)
	return len(clientIDs), nil
}

func TestReconcileOrphanedLeases(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	network, err := st.CreateNetwork(ctx, store.Network{Name: "lab", BridgeDomainID: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateInterface(ctx, store.VirtualInterface{
		Name: "vm-01", NetworkID: network.ID, MAC: "02:00:00:00:00:01", SocketPath: "/run/vpp/vm-01.sock",
	}); err != nil {
		t.Fatal(err)
	}
	v4, err := st.CreateVNF(ctx, store.VNF{
		Name: "dhcp", NetworkID: network.ID, Type: store.VNFTypeDHCPv4, Enabled: true,
		Config: store.VNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
	})
	if err != nil {
		t.Fatal(err)
	}
	v6, err := st.CreateVNF(ctx, store.VNF{
		Name: "dhcp6", NetworkID: network.ID, Type: store.VNFTypeDHCPv6, Enabled: true,
		Config: store.VNFConfig{Gateway: "fd00:20::1/64", RangeStart: "fd00:20::100", RangeEnd: "fd00:20::200"},
	})
	if err != nil {
		t.Fatal(err)
	}

	expires := time.Now().Add(time.Hour)
	runtime := &fakeLeaseRuntime{
		leases: map[int64][]store.VNFLease{
			v4.ID: {
				{ClientID: "02:00:00:00:00:01", Address: "10.20.0.100", Active: true, ExpiresAt: expires},
				{ClientID: "02:00:00:00:00:02", Address: "10.20.0.101", Active: true, ExpiresAt: expires},
				{ClientID: "02:00:00:00:00:03", Address: "10.20.0.102", Active: false, ExpiresAt: expires.Add(-time.Hour)},
			},
			v6.ID: {
				{ClientID: "DUID-LL{HWType=Ethernet HWAddr=02:00:00:00:00:04}", Address: "fd00:20::100", Active: true, ExpiresAt: expires},
			},
		},
		removed: make(map[int64][]string),
	}

	reconcileOrphanedLeases(ctx, st, runtime, zerolog.Nop())

	removed := runtime.removed[v4.ID]
	if len(removed) != 1 || removed[0] != "02:00:00:00:00:02" {
		t.Fatalf("removed leases = %+v", removed)
	}
	if len(runtime.removed[v6.ID]) != 0 {
		t.Fatalf("DHCPv6 leases were removed: %+v", runtime.removed[v6.ID])
	}
}
