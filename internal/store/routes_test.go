package store_test

import (
	"context"
	"errors"
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

	lookup := "default"
	preference := uint32(3)
	route, err := st.CreateRoute(ctx, store.Route{
		Prefix: "10.10.1.0/24", IPv4LookupTable: &lookup, Preference: &preference,
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.IPv4LookupTable == nil || *route.IPv4LookupTable != "default" {
		t.Fatalf("lookup table name = %+v", route.IPv4LookupTable)
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

func TestRouteTableNameReferences(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "route-names.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	v4, err := st.CreateRouteTable(ctx, store.RouteTable{Name: "lookup-v4", TableID: 500})
	if err != nil {
		t.Fatal(err)
	}
	v6, err := st.CreateRouteTable(ctx, store.RouteTable{Name: "lookup-v6", TableID: 501, IPv6: true})
	if err != nil {
		t.Fatal(err)
	}
	v4Name, v6Name := v4.Name, v6.Name
	route, err := st.CreateRoute(ctx, store.Route{
		Prefix: "10.10.1.0/24", NextHopTable: &v4Name, IPv4LookupTable: &v4Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.NextHopTable == nil || *route.NextHopTable != v4.Name || route.IPv4LookupTable == nil || *route.IPv4LookupTable != v4.Name {
		t.Fatalf("route table names = %+v", route)
	}
	if id, err := st.ResolveRouteTableName(ctx, v4.Name, false); err != nil || id != v4.TableID {
		t.Fatalf("resolve IPv4 table = %d, %v", id, err)
	}
	if id, err := st.ResolveRouteTableName(ctx, v6.Name, true); err != nil || id != v6.TableID {
		t.Fatalf("resolve IPv6 table = %d, %v", id, err)
	}
	v6Route, err := st.CreateRoute(ctx, store.Route{
		Prefix: "2001:db8:10::/64", IPv6: true, IPv6LookupTable: &v6Name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRoute(ctx, store.Route{
		Prefix: "10.10.2.0/24", IPv4LookupTable: strPtr("missing-table"),
	}); err == nil {
		t.Fatal("expected unknown lookup table to be rejected")
	}
	if err := st.DeleteRouteTable(ctx, v4.ID); !errors.Is(err, store.ErrRouteTableInUse) {
		t.Fatalf("delete referenced route table = %v, want ErrRouteTableInUse", err)
	}
	if err := st.DeleteRoute(ctx, route.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteRoute(ctx, v6Route.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteRouteTable(ctx, v4.ID); err != nil {
		t.Fatalf("delete unreferenced route table: %v", err)
	}
	if err := st.DeleteRouteTable(ctx, v6.ID); err != nil {
		t.Fatalf("delete unused IPv6 route table: %v", err)
	}
}

func strPtr(value string) *string { return &value }
