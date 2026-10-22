package lifecycle

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeBackend struct {
	stopped   int
	activated int
}

func (f *fakeBackend) VPPBlocked() (bool, string) { return false, "" }
func (f *fakeBackend) NormalizeVNFConfig(_ string, _ *rookeryv1.VNFConfig) (store.VNFConfig, error) {
	return store.VNFConfig{Gateway: "10.0.0.1/24", RangeStart: "10.0.0.10", RangeEnd: "10.0.0.20"}, nil
}
func (f *fakeBackend) VNFWritesReady(string) error { return nil }
func (f *fakeBackend) ApplyNATGatewayToDHCPConfig(context.Context, int64, string, *store.VNFConfig) error {
	return nil
}
func (f *fakeBackend) ActivateVNF(_ context.Context, vnf store.VNF) (store.VNF, error) {
	f.activated++
	vnf.Enabled = true
	return vnf, nil
}
func (f *fakeBackend) StopVNF(context.Context, store.VNF, bool) error { f.stopped++; return nil }
func (f *fakeBackend) ActivateNAT44(_ context.Context, vnf store.VNF) (store.VNF, error) {
	f.activated++
	vnf.Enabled = true
	return vnf, nil
}
func (f *fakeBackend) StopNAT44(context.Context, store.VNF) error                   { f.stopped++; return nil }
func (f *fakeBackend) CleanupNetworkBVIAfterNATDelete(context.Context, int64) error { return nil }
func (f *fakeBackend) CleanupLegacyNetworkVNF(context.Context, store.Network) error { return nil }
func (f *fakeBackend) DeleteBVI(context.Context, string) error                      { return nil }
func (f *fakeBackend) CreateBridgeDomain(_ context.Context, _ string, _ []uint32) (uint32, error) {
	return 123, nil
}
func (f *fakeBackend) DeleteBridgeDomain(context.Context, uint32) error { return nil }
func (f *fakeBackend) PrepareNetworkVNFs(_ context.Context, network store.Network) (store.Network, error) {
	return network, nil
}

func (f *fakeBackend) CleanupNetworkVNFs(context.Context, store.Network) error       { return nil }
func (f *fakeBackend) DeleteNetworkRouteTables(context.Context, store.Network) error { return nil }

func (f *fakeBackend) CreateNetworkHostTap(_ context.Context, network store.Network, name, address string) (store.NetworkHostTap, error) {
	return store.NetworkHostTap{ID: 1, NetworkID: network.ID, NetworkName: network.Name, Name: name, IPv4Address: address}, nil
}
func (f *fakeBackend) DeleteNetworkHostTap(context.Context, store.NetworkHostTap) error { return nil }
func (f *fakeBackend) CleanupNetworkHostTaps(context.Context, store.Network) error      { return nil }

func TestVNFAndNetworkLifecycle(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	network, err := st.CreateNetwork(context.Background(), store.Network{Name: "lab", BridgeDomainID: 1})
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{}
	service := New(st, backend, &sync.Mutex{})

	vnf, err := service.CreateVNF(context.Background(), &rookeryv1.CreateVNFRequest{
		Name: "dhcp", NetworkId: network.ID, Type: store.VNFTypeDHCPv4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if vnf.ID == 0 || vnf.Enabled {
		t.Fatalf("vnf = %+v", vnf)
	}
	if err := service.DeleteVNF(context.Background(), &rookeryv1.DeleteVNFRequest{Id: vnf.ID}); err != nil {
		t.Fatal(err)
	}
	if backend.stopped != 1 {
		t.Fatalf("stop calls = %d", backend.stopped)
	}

	created, err := service.CreateNetwork(context.Background(), "lab-2", "10.30.0.0/24", "fd00:30::/64")
	if err != nil {
		t.Fatal(err)
	}
	if created.BridgeDomainID != 123 {
		t.Fatalf("network = %+v", created)
	}
	if err := service.DeleteNetwork(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
}
