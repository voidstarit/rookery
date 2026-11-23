package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/store"
)

type fakeLeaseRuntime struct {
	leases    map[int64][]store.VNFLease
	removed   map[int64][]string
	leaseErr  error
	removeErr error
}

func (f *fakeLeaseRuntime) Leases(_ context.Context, vnfID int64) ([]store.VNFLease, error) {
	if f.leaseErr != nil {
		return nil, f.leaseErr
	}
	return f.leases[vnfID], nil
}

func (f *fakeLeaseRuntime) RemoveLeases(_ context.Context, vnfID int64, clientIDs []string) (int, error) {
	if f.removeErr != nil {
		return 0, f.removeErr
	}
	f.removed[vnfID] = append(f.removed[vnfID], clientIDs...)
	return len(clientIDs), nil
}

type fakeLeaseStore struct {
	interfaces    []store.VirtualInterface
	networks      []store.Network
	vnfs          map[int64][]store.VNF
	interfacesErr error
	networksErr   error
	vnfsErr       error
}

func (f *fakeLeaseStore) ListInterfaces(context.Context) ([]store.VirtualInterface, error) {
	if f.interfacesErr != nil {
		return nil, f.interfacesErr
	}
	return f.interfaces, nil
}

func (f *fakeLeaseStore) ListNetworks(context.Context) ([]store.Network, error) {
	if f.networksErr != nil {
		return nil, f.networksErr
	}
	return f.networks, nil
}

func (f *fakeLeaseStore) ListVNFsByNetwork(_ context.Context, networkID int64) ([]store.VNF, error) {
	if f.vnfsErr != nil {
		return nil, f.vnfsErr
	}
	return f.vnfs[networkID], nil
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

func TestReconcileOrphanedLeasesGuards(t *testing.T) {
	ctx := context.Background()
	// A closed store exercises the store-error branches without needing a
	// database package hook.
	closed, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}

	reconcileOrphanedLeasesFor(ctx, &fakeLeaseStore{interfacesErr: errors.New("boom")}, &fakeLeaseRuntime{}, zerolog.Nop())
	reconcileOrphanedLeasesFor(ctx, &fakeLeaseStore{networksErr: errors.New("boom")}, &fakeLeaseRuntime{}, zerolog.Nop())
	reconcileOrphanedLeasesFor(ctx, closed, &fakeLeaseRuntime{}, zerolog.Nop())
	reconcileOrphanedLeases(ctx, nil, &fakeLeaseRuntime{}, zerolog.Nop())
	reconcileOrphanedLeases(ctx, closed, nil, zerolog.Nop())
}

func TestReconcileOrphanedLeasesErrorPaths(t *testing.T) {
	ctx := context.Background()
	network := store.Network{ID: 1, Name: "lab"}
	vnf := store.VNF{ID: 9, NetworkID: network.ID, Type: store.VNFTypeDHCPv4, Enabled: true}

	t.Run("list vnfs", func(t *testing.T) {
		st := &fakeLeaseStore{networks: []store.Network{network}, vnfsErr: errors.New("boom")}
		reconcileOrphanedLeasesFor(ctx, st, &fakeLeaseRuntime{removed: map[int64][]string{}}, zerolog.Nop())
	})
	t.Run("leases", func(t *testing.T) {
		st := &fakeLeaseStore{networks: []store.Network{network}, vnfs: map[int64][]store.VNF{network.ID: {vnf}}}
		reconcileOrphanedLeasesFor(ctx, st, &fakeLeaseRuntime{leaseErr: errors.New("boom")}, zerolog.Nop())
	})
	t.Run("remove", func(t *testing.T) {
		st := &fakeLeaseStore{networks: []store.Network{network}, vnfs: map[int64][]store.VNF{network.ID: {vnf}}}
		runtime := &fakeLeaseRuntime{
			leases:    map[int64][]store.VNFLease{vnf.ID: {{ClientID: "02:00:00:00:00:01", Active: true}}},
			removeErr: errors.New("boom"),
		}
		reconcileOrphanedLeasesFor(ctx, st, runtime, zerolog.Nop())
	})
	t.Run("nothing stale", func(t *testing.T) {
		st := &fakeLeaseStore{
			interfaces: []store.VirtualInterface{
				{NetworkID: network.ID, MAC: "02:00:00:00:00:01"},
				{NetworkID: network.ID, MAC: "not-a-mac"},
			},
			networks: []store.Network{network},
			vnfs:     map[int64][]store.VNF{network.ID: {vnf}},
		}
		runtime := &fakeLeaseRuntime{
			leases: map[int64][]store.VNFLease{vnf.ID: {
				{ClientID: "02:00:00:00:00:01", Active: true},
				{ClientID: "DUID-LL{...}", Active: true},
			}},
			removed: map[int64][]string{},
		}
		reconcileOrphanedLeasesFor(ctx, st, runtime, zerolog.Nop())
		if len(runtime.removed) != 0 {
			t.Fatalf("removed leases = %+v", runtime.removed)
		}
	})
}

func TestRemoveLeasesForInterfaceHelper(t *testing.T) {
	ctx := context.Background()
	mac := "02:00:00:00:00:0a"
	v4 := store.VNF{ID: 3, NetworkID: 1, Type: store.VNFTypeDHCPv4, Enabled: true}
	v6 := store.VNF{ID: 4, NetworkID: 1, Type: store.VNFTypeDHCPv6, Enabled: true}
	disabled := store.VNF{ID: 5, NetworkID: 1, Type: store.VNFTypeDHCPv4, Enabled: false}

	t.Run("list interfaces", func(t *testing.T) {
		err := removeLeasesForInterface(ctx, &fakeLeaseStore{interfacesErr: errors.New("boom")}, &fakeLeaseRuntime{}, 1, mac)
		if err == nil {
			t.Fatal("expected a list error")
		}
	})
	t.Run("invalid mac", func(t *testing.T) {
		if err := removeLeasesForInterface(ctx, &fakeLeaseStore{}, &fakeLeaseRuntime{}, 1, "nope"); err != nil {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("list vnfs", func(t *testing.T) {
		st := &fakeLeaseStore{vnfsErr: errors.New("boom")}
		if err := removeLeasesForInterface(ctx, st, &fakeLeaseRuntime{}, 1, mac); err == nil {
			t.Fatal("expected a list error")
		}
	})
	t.Run("skips other interfaces and VNFs", func(t *testing.T) {
		st := &fakeLeaseStore{
			// The first interface is on another network, the second uses a
			// different MAC on the target network.
			interfaces: []store.VirtualInterface{
				{NetworkID: 2, MAC: mac},
				{NetworkID: 1, MAC: "02:00:00:00:00:0b"},
			},
			vnfs: map[int64][]store.VNF{1: {v6, disabled, v4}},
		}
		runtime := &fakeLeaseRuntime{removed: map[int64][]string{}}
		if err := removeLeasesForInterface(ctx, st, runtime, 1, mac); err != nil {
			t.Fatalf("error = %v", err)
		}
		removed := runtime.removed[v4.ID]
		if len(removed) != 1 || removed[0] != mac {
			t.Fatalf("removed leases = %+v", runtime.removed)
		}
		if len(runtime.removed[v6.ID]) != 0 || len(runtime.removed[disabled.ID]) != 0 {
			t.Fatalf("removed leases from the wrong VNFs: %+v", runtime.removed)
		}
	})
	t.Run("remove error", func(t *testing.T) {
		st := &fakeLeaseStore{vnfs: map[int64][]store.VNF{1: {v4}}}
		runtime := &fakeLeaseRuntime{removeErr: errors.New("boom")}
		err := removeLeasesForInterface(ctx, st, runtime, 1, mac)
		if err == nil {
			t.Fatal("expected a remove error")
		}
	})
}

// stubLeaseManager implements VNFManager through the embedded interface while
// only providing the lease methods the cleanup uses.
type stubLeaseManager struct {
	VNFManager
}

func (stubLeaseManager) Leases(context.Context, int64) ([]store.VNFLease, error) {
	return nil, nil
}

func (stubLeaseManager) RemoveLeases(context.Context, int64, []string) (int, error) {
	return 0, nil
}

func TestRunLeaseCleanupNilDependencies(t *testing.T) {
	server := &Server{}
	server.runLeaseCleanup(context.Background())
}

func TestCleanupOrphanedLeasesNilDependencies(t *testing.T) {
	server := &Server{}
	server.cleanupOrphanedLeases(context.Background())
}

func TestRunLeaseCleanupTicker(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	previous := leaseCleanupInterval
	leaseCleanupInterval = 5 * time.Millisecond
	t.Cleanup(func() { leaseCleanupInterval = previous })

	server := &Server{store: st, vnfs: stubLeaseManager{}, logger: zerolog.Nop()}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	server.runLeaseCleanup(ctx)
	if ctx.Err() == nil {
		t.Fatal("cleanup loop did not stop with its context")
	}
}
