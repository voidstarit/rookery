package host

import (
	"net"
	"testing"
)

func TestFirstIPv4Host(t *testing.T) {
	_, network, err := net.ParseCIDR("10.100.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	if got := firstIPv4Host(network); got == nil || got.String() != "10.100.0.1" {
		t.Fatalf("gateway = %v, want 10.100.0.1", got)
	}
	_, network, _ = net.ParseCIDR("192.168.88.0/24")
	if got := firstIPv4Host(network); got == nil || got.String() != "192.168.88.1" {
		t.Fatalf("gateway = %v, want 192.168.88.1", got)
	}
	if got := firstIPv4Host(nil); got != nil {
		t.Fatalf("nil network gateway = %v", got)
	}
}
