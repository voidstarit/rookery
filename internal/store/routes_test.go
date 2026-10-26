package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestRouteOptionPresence(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "routes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	zero := uint32(0)
	preference := uint32(3)
	route, err := st.CreateRoute(ctx, store.Route{
		Prefix: "10.10.1.0/24", IPv4LookupTable: &zero, Preference: &preference,
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.IPv4LookupTable == nil || *route.IPv4LookupTable != 0 {
		t.Fatalf("explicit zero lookup table = %+v", route.IPv4LookupTable)
	}
	if route.Preference == nil || *route.Preference != 3 {
		t.Fatalf("preference = %+v", route.Preference)
	}
	if route.Count != nil || route.NextHopTable != nil || route.Weight != nil || route.UDPEncap != nil {
		t.Fatalf("unexpected optional fields = %+v", route)
	}

	updated, err := st.UpdateRoute(ctx, route.ID, store.Route{
		Prefix: "10.10.1.0/24", NextHop: "10.10.1.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.IPv4LookupTable != nil {
		t.Fatalf("lookup table was not cleared: %+v", updated.IPv4LookupTable)
	}
	if updated.Preference != nil {
		t.Fatalf("preference was not cleared: %+v", updated.Preference)
	}

	zeroCount := uint32(0)
	if _, err := st.CreateRoute(ctx, store.Route{
		Prefix: "10.10.2.0/24", Count: &zeroCount,
	}); err == nil {
		t.Fatal("expected explicit zero count to be rejected")
	}
}
