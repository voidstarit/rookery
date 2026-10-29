package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func routeUint32(value uint32) *uint32 { return &value }

func TestAddRouteSpecBuildsFullVPPCommand(t *testing.T) {
	commands := filepath.Join(t.TempDir(), "command")
	script := filepath.Join(t.TempDir(), "vppctl")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$ROOKERY_ROUTE_CAPTURE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROOKERY_ROUTE_CAPTURE", commands)
	v := &VPPNetwork{CLI: script}

	err := v.AddRouteSpec(context.Background(), RouteSpec{
		TableID: 100001, Prefix: "192.168.88.0/24", NextHop: "192.168.88.1",
		EgressInterface: "host-eno4", Preference: routeUint32(10), Count: routeUint32(2),
		NextHopTable: routeUint32(5), Weight: routeUint32(3), UDPEncap: routeUint32(7),
		IPv4LookupTable: routeUint32(8), IPv6LookupTable: routeUint32(9), MPLSLookupTable: routeUint32(10),
		ResolveViaHost: true, ResolveViaConnected: true,
		OutLabels: []uint32{100, 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	want := "ip route add count 2 192.168.88.0/24 table 100001 via 192.168.88.1 host-eno4 next-hop-table 5 weight 3 preference 10 udp-encap 7 ip4-lookup-in-table 8 ip6-lookup-in-table 9 mpls-lookup-in-table 10 resolve-via-host resolve-via-connected out-labels 100 200"
	if got := strings.TrimSpace(string(data)); got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestAddRouteSpecSupportsDropWithoutNextHop(t *testing.T) {
	commands := filepath.Join(t.TempDir(), "command")
	script := filepath.Join(t.TempDir(), "vppctl")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$ROOKERY_ROUTE_CAPTURE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROOKERY_ROUTE_CAPTURE", commands)
	v := &VPPNetwork{CLI: script}
	if err := v.AddRouteSpec(context.Background(), RouteSpec{
		Prefix: "192.168.88.0/24", Drop: true,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	want := "ip route add 192.168.88.0/24 via drop"
	if got := strings.TrimSpace(string(data)); got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestAddRouteSpecPreservesExplicitZeroLookupTable(t *testing.T) {
	commands := filepath.Join(t.TempDir(), "command")
	script := filepath.Join(t.TempDir(), "vppctl")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$ROOKERY_ROUTE_CAPTURE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROOKERY_ROUTE_CAPTURE", commands)
	v := &VPPNetwork{CLI: script}
	if err := v.AddRouteSpec(context.Background(), RouteSpec{
		Prefix: "10.10.1.0/24", IPv4LookupTable: routeUint32(0),
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	want := "ip route add 10.10.1.0/24 via ip4-lookup-in-table 0"
	if got := strings.TrimSpace(string(data)); got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}
