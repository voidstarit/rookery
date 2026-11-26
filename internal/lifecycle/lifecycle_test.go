package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeBackend struct {
	store          *store.Store
	stopped        int
	activated      int
	reloaded       int
	reloadFailures int
	reloadErr      error
	natRules       int
}

func (f *fakeBackend) VPPBlocked() (bool, string)  { return false, "" }
func (f *fakeBackend) VNFWritesReady(string) error { return nil }

func (f *fakeBackend) NormalizeDHCPVNFConfig(_ string, config store.DHCPVNFConfig) (store.DHCPVNFConfig, error) {
	if config.Gateway == "" {
		config.Gateway = "10.0.0.1/24"
	}
	if config.RangeStart == "" {
		config.RangeStart = "10.0.0.10"
	}
	if config.RangeEnd == "" {
		config.RangeEnd = "10.0.0.20"
	}
	if config.LeaseTime == "" {
		config.LeaseTime = "12h"
	}
	return config, nil
}

func (f *fakeBackend) NormalizeNAT44VNFConfig(config store.NAT44VNFConfig) (store.NAT44VNFConfig, error) {
	if config.Gateway == "" {
		config.Gateway = "10.0.0.1/24"
	}
	if config.OutsideInterface == "" {
		config.OutsideInterface = "eth0"
	}
	return config, nil
}

func (f *fakeBackend) NormalizeBGPVNFConfig(config store.BGPVNFConfig) (store.BGPVNFConfig, error) {
	if config.Transport == "" {
		config.Transport = "memif"
	}
	if config.LocalAS == 0 {
		config.LocalAS = 65000
	}
	if config.PeerAS == 0 {
		config.PeerAS = 65001
	}
	return config, nil
}

func (f *fakeBackend) ApplyNATGatewayToDHCPConfig(context.Context, int64, string, *store.DHCPVNFConfig) error {
	return nil
}

func (f *fakeBackend) NormalizeDHCPStaticAssignment(_ context.Context, _ store.VNF, item store.DHCPStaticAssignment) (store.DHCPStaticAssignment, error) {
	return item, nil
}

func (f *fakeBackend) NormalizeDHCPOption(_ context.Context, _ store.VNF, item store.DHCPOption) (store.DHCPOption, error) {
	return item, nil
}

func (f *fakeBackend) NormalizeDNSRecord(_ context.Context, _ store.VNF, _ []store.DNSRecord, item store.DNSRecord) (store.DNSRecord, error) {
	return item, nil
}

func (f *fakeBackend) NormalizeDNSForwarder(_ context.Context, _ store.VNF, _ []store.DNSForwarder, item store.DNSForwarder) (store.DNSForwarder, error) {
	return item, nil
}

func (f *fakeBackend) NormalizeDNATRule(_ context.Context, _ store.VNF, _ []store.DNATRule, item store.DNATRule) (store.DNATRule, error) {
	return item, nil
}

func (f *fakeBackend) NormalizeBGPPrefix(_ context.Context, _ store.VNF, prefix string) (store.BGPPrefix, error) {
	return store.BGPPrefix{Prefix: prefix}, nil
}

func (f *fakeBackend) ActivateVNF(ctx context.Context, vnf store.VNF) (store.VNF, error) {
	f.activated++
	vnf.Enabled = true
	if f.store != nil {
		return f.store.UpdateVNFIdentity(ctx, vnf)
	}
	return vnf, nil
}

func (f *fakeBackend) StopVNF(context.Context, store.VNF, bool) error { f.stopped++; return nil }

func (f *fakeBackend) ReloadVNF(context.Context, store.VNF) error {
	f.reloaded++
	if f.reloadFailures > 0 {
		f.reloadFailures--
		return errors.New("reload failed")
	}
	return f.reloadErr
}

func (f *fakeBackend) ActivateNAT44(ctx context.Context, vnf store.VNF) (store.VNF, error) {
	f.activated++
	vnf.Enabled = true
	if f.store != nil {
		return f.store.UpdateVNFIdentity(ctx, vnf)
	}
	return vnf, nil
}

func (f *fakeBackend) StopNAT44(context.Context, store.VNF) error { f.stopped++; return nil }

func (f *fakeBackend) ApplyNAT44DNATRules(context.Context, store.VNF) error {
	f.natRules++
	return nil
}

func (f *fakeBackend) CleanupNetworkBVIAfterNATDelete(context.Context, int64) error { return nil }
func (f *fakeBackend) CleanupLegacyNetworkVNF(context.Context, store.Network) error { return nil }
func (f *fakeBackend) DeleteBVI(context.Context, string) error                      { return nil }
func (f *fakeBackend) CreateBridgeDomain(_ context.Context, _ string, _ []uint32) (uint32, error) {
	return 123, nil
}
func (f *fakeBackend) DeleteBridgeDomain(context.Context, uint32) error { return nil }
func (f *fakeBackend) CreateNetworkPipe(_ context.Context, bdA, bdB uint32) (host.PipeRuntime, error) {
	return host.PipeRuntime{Instance: 1, ParentSwIfIndex: 80, EndpointASwIfIndex: bdA, EndpointBSwIfIndex: bdB}, nil
}
func (f *fakeBackend) DeleteNetworkPipe(context.Context, uint32, uint32) error { return nil }
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

	vnf, err := service.CreateDHCPVNF(context.Background(), &rookeryv1.CreateDHCPVNFRequest{
		Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if vnf.ID == 0 || vnf.Enabled {
		t.Fatalf("vnf = %+v", vnf)
	}
	if err := service.DeleteDHCPVNF(context.Background(), &rookeryv1.DeleteDHCPVNFRequest{Id: vnf.ID}); err != nil {
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

func TestNetworkPipeLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "lifecycle-pipes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	networkA, err := st.CreateNetwork(ctx, store.Network{Name: "lab-a", BridgeDomainID: 10})
	if err != nil {
		t.Fatal(err)
	}
	networkB, err := st.CreateNetwork(ctx, store.Network{Name: "lab-b", BridgeDomainID: 11})
	if err != nil {
		t.Fatal(err)
	}
	service := New(st, &fakeBackend{}, &sync.Mutex{})

	pipe, err := service.CreateNetworkPipe(ctx, networkB.ID, networkA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pipe.NetworkAID != networkA.ID || pipe.NetworkBID != networkB.ID || pipe.Instance != 1 || pipe.ParentSwIfIndex != 80 {
		t.Fatalf("pipe = %+v", pipe)
	}
	if _, err := service.CreateNetworkPipe(ctx, networkA.ID, networkB.ID); err == nil {
		t.Fatal("expected duplicate pipe error")
	} else {
		var networkErr *NetworkError
		if !errors.As(err, &networkErr) || networkErr.Status != http.StatusConflict {
			t.Fatalf("duplicate pipe error = %v", err)
		}
	}
	if err := service.DeleteNetwork(ctx, networkA.ID); err == nil {
		t.Fatal("expected network deletion to be blocked by pipe")
	} else {
		var networkErr *NetworkError
		if !errors.As(err, &networkErr) || networkErr.Status != http.StatusConflict {
			t.Fatalf("delete network error = %v", err)
		}
	}
	if err := service.DeleteNetworkPipe(ctx, pipe.ID); err != nil {
		t.Fatal(err)
	}
	pipes, err := st.ListNetworkPipes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pipes) != 0 {
		t.Fatalf("pipes after delete = %+v", pipes)
	}
}
