package host

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"

	"github.com/voidstarit/rookery/internal/store"
)

func testDHCPv4Frame(t *testing.T, mac net.HardwareAddr, messageType dhcpv4.MessageType, xid dhcpv4.TransactionID, requested net.IP) []byte {
	t.Helper()
	req, err := dhcpv4.New(
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithMessageType(messageType),
		dhcpv4.WithTransactionID(xid),
		dhcpv4.WithBroadcast(true),
	)
	if err != nil {
		t.Fatalf("build DHCP request: %v", err)
	}
	if requested != nil {
		req.UpdateOption(dhcpv4.OptRequestedIPAddress(requested))
	}
	payload := req.ToBytes()

	frame := make([]byte, header.EthernetMinimumSize+header.IPv4MinimumSize+header.UDPMinimumSize+len(payload))
	header.Ethernet(frame).Encode(&header.EthernetFields{
		SrcAddr: tcpip.LinkAddress(mac),
		DstAddr: header.EthernetBroadcastAddress,
		Type:    header.IPv4ProtocolNumber,
	})
	ip := header.IPv4(frame[header.EthernetMinimumSize:])
	ip.Encode(&header.IPv4Fields{
		TotalLength: uint16(header.IPv4MinimumSize + header.UDPMinimumSize + len(payload)),
		TTL:         64,
		Protocol:    uint8(header.UDPProtocolNumber),
		SrcAddr:     tcpip.AddrFrom4([4]byte{0, 0, 0, 0}),
		DstAddr:     header.IPv4Broadcast,
	})
	ip.SetChecksum(0)
	ip.SetChecksum(^ip.CalculateChecksum())
	udp := header.UDP(ip.Payload())
	udp.Encode(&header.UDPFields{
		SrcPort: dhcpv4.ClientPort,
		DstPort: dhcpv4.ServerPort,
		Length:  uint16(header.UDPMinimumSize + len(payload)),
	})
	copy(udp.Payload(), payload)
	udp.SetChecksum(0)
	return frame
}

func testVNFForMemifDHCP() store.VNF {
	return store.VNF{
		ID:   7,
		Type: store.VNFTypeDHCPv4,
		Config: store.VNFConfig{
			Gateway:    "10.20.0.1/24",
			BindIP:     "10.20.0.2/24",
			RangeStart: "10.20.0.100",
			RangeEnd:   "10.20.0.110",
			LeaseTime:  "12h",
		},
	}
}

func TestMemifDHCPv4OfferAndAck(t *testing.T) {
	vnf := testVNFForMemifDHCP()
	leasePath := filepath.Join(t.TempDir(), "leases.json")
	handlers, err := newDHCPv4Handlers(vnf, leasePath, defaultDHCPPort)
	if err != nil {
		t.Fatalf("load handlers: %v", err)
	}
	server, err := newMemifDHCPv4Stack(vnf, net.HardwareAddr{0x02, 0, 0, 0, 0, 0x07}, handlers, defaultDHCPPort)
	if err != nil {
		t.Fatalf("create netstack DHCP: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.serveDHCP(ctx)
	defer server.Close()

	readReply := func() *dhcpv4.DHCPv4 {
		readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
		defer readCancel()
		pkt := server.link.ReadContext(readCtx)
		if pkt == nil {
			t.Fatal("timed out waiting for DHCP reply")
		}
		defer pkt.DecRef()
		frame := pkt.ToView().AsSlice()
		if header.Ethernet(frame).Type() != header.IPv4ProtocolNumber {
			t.Fatalf("reply ethertype = %v", header.Ethernet(frame).Type())
		}
		ip := header.IPv4(frame[header.EthernetMinimumSize:])
		if ip.DestinationAddress() != header.IPv4Broadcast {
			t.Fatalf("reply destination = %v, want broadcast", ip.DestinationAddress())
		}
		udp := header.UDP(ip.Payload())
		if udp.SourcePort() != dhcpv4.ServerPort || udp.DestinationPort() != dhcpv4.ClientPort {
			t.Fatalf("reply UDP = %d->%d", udp.SourcePort(), udp.DestinationPort())
		}
		reply, err := dhcpv4.FromBytes(udp.Payload())
		if err != nil {
			t.Fatalf("parse DHCP reply: %v", err)
		}
		return reply
	}

	clientMAC := net.HardwareAddr{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	xid := dhcpv4.TransactionID{0x12, 0x34, 0x56, 0x78}
	server.injectFrame(testDHCPv4Frame(t, clientMAC, dhcpv4.MessageTypeDiscover, xid, nil))
	offer := readReply()
	if offer.MessageType() != dhcpv4.MessageTypeOffer {
		t.Fatalf("message type = %v, want OFFER", offer.MessageType())
	}
	offered := offer.YourIPAddr
	if !net.ParseIP("10.20.0.110").Equal(offered) && !(&net.IPNet{IP: net.ParseIP("10.20.0.0"), Mask: net.CIDRMask(24, 32)}).Contains(offered) {
		t.Fatalf("offered address %v is outside the configured subnet", offered)
	}
	if sid := offer.ServerIdentifier(); sid == nil || !sid.Equal(net.ParseIP("10.20.0.2")) {
		t.Fatalf("server identifier = %v, want 10.20.0.2", sid)
	}

	server.injectFrame(testDHCPv4Frame(t, clientMAC, dhcpv4.MessageTypeRequest, xid, offered))
	ack := readReply()
	if ack.MessageType() != dhcpv4.MessageTypeAck {
		t.Fatalf("message type = %v, want ACK", ack.MessageType())
	}
	if !ack.YourIPAddr.Equal(offered) {
		t.Fatalf("ACK address = %v, want %v", ack.YourIPAddr, offered)
	}
}
