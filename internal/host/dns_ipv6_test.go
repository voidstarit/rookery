package host

import (
	"net"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/link/ethernet"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"

	"github.com/voidstarit/rookery/internal/store"
)

func TestSharedDNSListensOnIPv4AndIPv6(t *testing.T) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol, arp.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{udp.NewProtocol, tcp.NewProtocol},
	})
	link := channel.New(64, 1500, "")
	if err := s.CreateNIC(1, ethernet.New(link)); err != nil {
		t.Fatal(err)
	}
	v4, _, _ := net.ParseCIDR("10.20.0.2/24")
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          header.IPv4ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFromSlice(v4.To4()), PrefixLen: 24},
	}, stack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	v6, _, _ := net.ParseCIDR("fd00:20::2/64")
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          header.IPv6ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFromSlice(v6.To16()), PrefixLen: 64},
	}, stack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: 1},
		{Destination: header.IPv6EmptySubnet, NIC: 1},
	})
	n := &sharedNetwork{
		networkID:   1,
		address:     "10.20.0.2/24",
		ipv6Address: "fd00:20::2/64",
		stack:       s,
		vnfs:        make(map[int64]string),
	}
	if err := n.addDNS(store.VNF{ID: 1, Type: store.VNFTypeDNS}); err != nil {
		t.Fatalf("add DNS: %v", err)
	}
	if n.dns == nil || n.dns.udpServer6 == nil || n.dns.tcpServer6 == nil {
		t.Fatal("IPv6 DNS listeners were not created")
	}
	n.dns.close()
	s.Close()
	s.Wait()
}

func TestSharedDNSWithoutIPv6AddressOnlyCreatesIPv4Listeners(t *testing.T) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol, arp.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{udp.NewProtocol, tcp.NewProtocol},
	})
	link := channel.New(64, 1500, "")
	if err := s.CreateNIC(1, ethernet.New(link)); err != nil {
		t.Fatal(err)
	}
	v4, _, _ := net.ParseCIDR("10.20.0.2/24")
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          header.IPv4ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFromSlice(v4.To4()), PrefixLen: 24},
	}, stack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	n := &sharedNetwork{networkID: 1, address: "10.20.0.2/24", stack: s, vnfs: make(map[int64]string)}
	if err := n.addDNS(store.VNF{ID: 1, Type: store.VNFTypeDNS}); err != nil {
		t.Fatalf("add DNS: %v", err)
	}
	if n.dns == nil || n.dns.udpServer6 != nil || n.dns.tcpServer6 != nil {
		t.Fatal("unexpected IPv6 DNS listeners")
	}
	n.dns.close()
	s.Close()
	s.Wait()
}

func TestSharedNetworkAddsIPv6AddressAfterStartup(t *testing.T) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol, arp.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{udp.NewProtocol, tcp.NewProtocol},
	})
	link := channel.New(64, 1500, "")
	if err := s.CreateNIC(1, ethernet.New(link)); err != nil {
		t.Fatal(err)
	}
	v4, _, _ := net.ParseCIDR("10.20.0.2/24")
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          header.IPv4ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFromSlice(v4.To4()), PrefixLen: 24},
	}, stack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	n := &sharedNetwork{networkID: 1, address: "10.20.0.2/24", stack: s, vnfs: make(map[int64]string)}
	if err := n.ensureIPv6Address("fd00:20::2/64"); err != nil {
		t.Fatalf("add IPv6 address: %v", err)
	}
	if err := n.addDNS(store.VNF{ID: 1, Type: store.VNFTypeDNS}); err != nil {
		t.Fatalf("add DNS: %v", err)
	}
	if n.dns == nil || n.dns.udpServer6 == nil || n.dns.tcpServer6 == nil {
		t.Fatal("IPv6 DNS listeners were not created after upgrade")
	}
	n.dns.close()
	s.Close()
	s.Wait()
}
