package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestNetworkHostTaps(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "network-host-taps.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	network, err := st.CreateNetwork(ctx, Network{
		Name: "lab", BridgeDomainID: 100, IPv4CIDR: "10.20.0.0/24", IPv6CIDR: "fd00:20::/64",
	})
	if err != nil {
		t.Fatal(err)
	}
	tap, err := st.CreateNetworkHostTap(ctx, NetworkHostTap{
		NetworkID: network.ID, Name: "hostnet1", IPv4Address: "10.20.0.50/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tap.ID == 0 || tap.NetworkName != "lab" || tap.Name != "hostnet1" ||
		tap.IPv4Address != "10.20.0.50/24" || tap.CreatedAt.IsZero() {
		t.Fatalf("tap = %+v", tap)
	}
	if _, err := st.CreateNetworkHostTap(ctx, NetworkHostTap{
		NetworkID: network.ID, Name: "HOSTNET1",
	}); !errors.Is(err, ErrNetworkHostTapNameTaken) {
		t.Fatalf("duplicate name error = %v, want ErrNetworkHostTapNameTaken", err)
	}

	updated, err := st.SetNetworkHostTapRuntime(ctx, tap.ID, 42)
	if err != nil {
		t.Fatal(err)
	}
	if updated.SwIfIndex != 42 {
		t.Fatalf("sw_if_index = %d, want 42", updated.SwIfIndex)
	}
	updated, err = st.SetNetworkHostTapAddress(ctx, tap.ID, "10.20.0.51/24")
	if err != nil {
		t.Fatal(err)
	}
	if updated.IPv4Address != "10.20.0.51/24" {
		t.Fatalf("ipv4_address = %q", updated.IPv4Address)
	}

	byName, err := st.GetNetworkHostTapByName(ctx, "hostnet1")
	if err != nil || byName.ID != tap.ID {
		t.Fatalf("by name = %+v, err = %v", byName, err)
	}
	taps, err := st.ListNetworkHostTapsByNetwork(ctx, network.ID)
	if err != nil || len(taps) != 1 || taps[0].ID != tap.ID {
		t.Fatalf("taps = %+v, err = %v", taps, err)
	}
	all, err := st.ListNetworkHostTaps(ctx, nil)
	if err != nil || len(all) != 1 {
		t.Fatalf("all taps = %+v, err = %v", all, err)
	}

	if err := st.DeleteNetworkHostTap(ctx, tap.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetNetworkHostTap(ctx, tap.ID); !errors.Is(err, ErrNetworkHostTapNotFound) {
		t.Fatalf("get after delete = %v, want ErrNetworkHostTapNotFound", err)
	}
}
