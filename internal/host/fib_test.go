package host

import (
	"testing"

	"go.fd.io/govpp/binapi/fib_types"
	"go.fd.io/govpp/binapi/ip_types"
)

func TestFIBRouteMetadata(t *testing.T) {
	if got := fibSourceName(8); got != "API" {
		t.Fatalf("source 8 = %q, want API", got)
	}
	if got := fibSourceName(12); got != "DHCP" {
		t.Fatalf("source 12 = %q, want DHCP", got)
	}
	if dynamicFIBRoute(8) {
		t.Fatal("API source should not be dynamic")
	}
	if !dynamicFIBRoute(4) {
		t.Fatal("interface source should be dynamic")
	}
	if got := cleanFIBPathType("FIB_API_PATH_TYPE_LOCAL"); got != "LOCAL" {
		t.Fatalf("clean path type = %q", got)
	}
	address := ip_types.AddressUnionIP4(ip_types.IP4Address{10, 20, 0, 1})
	if got := fibPathNextHop(fib_types.FIB_API_PATH_NH_PROTO_IP4, address); got != "10.20.0.1" {
		t.Fatalf("IPv4 next hop = %q", got)
	}
}
