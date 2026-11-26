package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/store"
)

// blockingVNFManager blocks ReloadVNF until released, so the resync helper's
// single-flight guard can be observed.
type blockingVNFManager struct {
	VNFManager
	started chan int64
	release chan struct{}
}

func (m *blockingVNFManager) ReloadVNF(ctx context.Context, vnf store.VNF) error {
	m.started <- vnf.ID
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestStartPendingVNFResyncSingleFlight covers the empty queue, one queued ID,
// the in-flight guard, and the successful dequeue.
func TestStartPendingVNFResyncSingleFlight(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "resync.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	network, err := st.CreateNetwork(ctx, store.Network{
		Name: "resync-lab", IPv4CIDR: "10.20.0.0/24", IPv6CIDR: "fd00:20::/64",
	})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	vnf, err := st.CreateDNSVNF(ctx, store.NewDNSVNF{
		Name: "resync-dns", NetworkID: network.ID, Enabled: true, Config: store.DNSVNFConfig{Forward: true},
	})
	if err != nil {
		t.Fatalf("create DNS VNF: %v", err)
	}
	manager := &blockingVNFManager{started: make(chan int64, 2), release: make(chan struct{})}
	server := &Server{store: st, vnfs: manager, logger: zerolog.Nop()}

	// An empty queue does not start a pass.
	server.startPendingVNFResync(ctx)

	server.QueueVNFResync(vnf)
	server.startPendingVNFResync(ctx)
	select {
	case id := <-manager.started:
		if id != vnf.ID {
			t.Fatalf("reloaded VNF %d, want %d", id, vnf.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("resync pass did not start")
	}

	// A second tick while the first pass is blocked must not start another.
	server.startPendingVNFResync(ctx)
	select {
	case id := <-manager.started:
		t.Fatalf("second resync pass started for VNF %d", id)
	case <-time.After(50 * time.Millisecond):
	}

	close(manager.release)
	deadline := time.Now().Add(time.Second)
	for {
		server.pendingVNFMu.Lock()
		queued := len(server.pendingVNFResync)
		server.pendingVNFMu.Unlock()
		if queued == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resync queue did not drain after a successful pass")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
