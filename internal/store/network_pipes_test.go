package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestNetworkPipes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "network-pipes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	networkA, err := st.CreateNetwork(ctx, store.Network{Name: "lab-a", BridgeDomainID: 300})
	if err != nil {
		t.Fatal(err)
	}
	networkB, err := st.CreateNetwork(ctx, store.Network{Name: "lab-b", BridgeDomainID: 301})
	if err != nil {
		t.Fatal(err)
	}

	pipe, err := st.CreateNetworkPipe(ctx, store.NetworkPipe{
		NetworkAID: networkB.ID, NetworkBID: networkA.ID,
		Instance: 7, ParentSwIfIndex: 50, EndpointASwIfIndex: 51, EndpointBSwIfIndex: 52,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pipe.NetworkAID != networkA.ID || pipe.NetworkBID != networkB.ID {
		t.Fatalf("pipe networks = %d/%d, want %d/%d", pipe.NetworkAID, pipe.NetworkBID, networkA.ID, networkB.ID)
	}
	if pipe.NetworkAName != "lab-a" || pipe.NetworkBName != "lab-b" {
		t.Fatalf("pipe names = %q/%q", pipe.NetworkAName, pipe.NetworkBName)
	}
	if pipe.Instance != 7 || pipe.ParentSwIfIndex != 50 || pipe.EndpointASwIfIndex != 51 || pipe.EndpointBSwIfIndex != 52 {
		t.Fatalf("pipe runtime = %+v", pipe)
	}

	if _, err := st.CreateNetworkPipe(ctx, store.NetworkPipe{
		NetworkAID: networkA.ID, NetworkBID: networkB.ID,
	}); !errors.Is(err, store.ErrNetworkPipeExists) {
		t.Fatalf("duplicate pipe error = %v, want ErrNetworkPipeExists", err)
	}

	count, err := st.CountNetworkPipesForNetwork(ctx, networkA.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("pipe count = %d, want 1", count)
	}
	if err := st.DeleteNetwork(ctx, networkA.ID); !errors.Is(err, store.ErrNetworkInUse) {
		t.Fatalf("delete network with pipe error = %v, want ErrNetworkInUse", err)
	}

	updated, err := st.SetNetworkPipeRuntime(ctx, pipe.ID, 8, 60, 61, 62)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Instance != 8 || updated.ParentSwIfIndex != 60 || updated.EndpointASwIfIndex != 61 || updated.EndpointBSwIfIndex != 62 {
		t.Fatalf("updated pipe runtime = %+v", updated)
	}

	pipes, err := st.ListNetworkPipes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pipes) != 1 || pipes[0].ID != pipe.ID {
		t.Fatalf("pipes = %+v", pipes)
	}

	endpoint := store.NetworkPipeEndpointNames(updated.Instance)[0]
	if err := st.ReplaceVPPInterfaceAddresses(ctx, endpoint, []string{"10.0.0.1/24"}); err != nil {
		t.Fatal(err)
	}
	managed, err := st.IsManagedVPPInterface(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if !managed {
		t.Fatal("pipe endpoint should be managed")
	}
	if err := st.DeleteNetworkPipe(ctx, pipe.ID); err != nil {
		t.Fatal(err)
	}
	addresses, err := st.ListVPPInterfaceAddresses(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 0 {
		t.Fatalf("pipe endpoint addresses after delete = %v", addresses)
	}
	managed, err = st.IsManagedVPPInterface(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if managed {
		t.Fatal("deleted pipe endpoint should not be managed")
	}
	if err := st.DeleteNetworkPipe(ctx, pipe.ID); !errors.Is(err, store.ErrNetworkPipeNotFound) {
		t.Fatalf("second delete error = %v, want ErrNetworkPipeNotFound", err)
	}
	if err := st.DeleteNetwork(ctx, networkA.ID); err != nil {
		t.Fatalf("delete network after pipe removal: %v", err)
	}
}
