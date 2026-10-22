package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/coredhcp/coredhcp/handler"
	"github.com/coredhcp/coredhcp/plugins"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/rs/zerolog/log"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/link/ethernet"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"github.com/voidstarit/rookery/internal/store"
)

const (
	memifDHCPMTU        = 1500
	memifDHCPRingSize   = 1024
	memifDHCPBufferSize = 2048
)

// memifDHCPv4Config describes one userspace DHCPv4 server attached to a memif
// interface. VPP is expected to be the memif master; the API process is the
// slave that owns the netstack and the DHCP responder.
type memifDHCPv4Config struct {
	VNF         store.VNF
	Name        string
	SocketPath  string
	InterfaceID uint32
	MAC         net.HardwareAddr
	LeasePath   string
	Port        int
}

type memifDHCPv4 struct {
	vnfID    int64
	link     *channel.Endpoint
	stack    *stack.Stack
	conn     *gonet.UDPConn
	handlers []handler.Handler4
	memif    *memifLink

	cancel    context.CancelFunc
	closeOnce sync.Once
	done      chan struct{}
}

// newDHCPv4Handlers loads the same plugin chain used by the tap-backed
// CoreDHCP server, without starting CoreDHCP's kernel listeners.
func newDHCPv4Handlers(vnf store.VNF, leasePath string, port int) ([]handler.Handler4, error) {
	ensureCoreDHCPPlugin()
	cfg, err := buildDHCPv4Config(vnf, "memif", leasePath, port)
	if err != nil {
		return nil, err
	}
	handlers4, _, err := plugins.LoadPlugins(cfg)
	if err != nil {
		return nil, fmt.Errorf("load DHCPv4 plugin: %w", err)
	}
	if len(handlers4) == 0 {
		return nil, errors.New("no DHCPv4 handler configured")
	}
	return handlers4, nil
}

func newMemifDHCPv4Stack(vnf store.VNF, mac net.HardwareAddr, handlers []handler.Handler4, port int) (*memifDHCPv4, error) {
	cidr := strings.TrimSpace(vnf.Config.BindIP)
	if cidr == "" {
		cidr = strings.TrimSpace(vnf.Config.Gateway)
	}
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("parse DHCPv4 bind address %q: %w", cidr, err)
	}
	if ip.To4() == nil {
		return nil, fmt.Errorf("DHCPv4 bind address %q is not IPv4", cidr)
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return nil, fmt.Errorf("DHCPv4 bind address %q is not IPv4", cidr)
	}

	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, arp.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{udp.NewProtocol},
	})
	link := channel.New(memifDHCPRingSize, memifDHCPMTU, tcpip.LinkAddress(mac))
	eth := ethernet.New(link)
	if err := s.CreateNIC(1, eth); err != nil {
		return nil, fmt.Errorf("create netstack NIC: %v", err)
	}
	addr := tcpip.AddrFromSlice(ip.To4())
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          header.IPv4ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: addr, PrefixLen: ones},
	}, stack.AddressProperties{}); err != nil {
		return nil, fmt.Errorf("assign netstack address %s: %v", ip, err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})

	var wq waiter.Queue
	endpoint, tcpipErr := s.NewEndpoint(udp.ProtocolNumber, header.IPv4ProtocolNumber, &wq)
	if tcpipErr != nil {
		return nil, fmt.Errorf("create DHCPv4 UDP endpoint: %v", tcpipErr)
	}
	endpoint.SocketOptions().SetBroadcast(true)
	if err := endpoint.Bind(tcpip.FullAddress{NIC: 1, Port: uint16(port)}); err != nil {
		endpoint.Close()
		return nil, fmt.Errorf("bind DHCPv4 UDP endpoint: %v", err)
	}

	return &memifDHCPv4{
		vnfID:    vnf.ID,
		link:     link,
		stack:    s,
		conn:     gonet.NewUDPConn(&wq, endpoint),
		handlers: handlers,
		done:     make(chan struct{}),
	}, nil
}

// startMemifDHCPv4 starts a userspace DHCPv4 server on a memif slave.
func startMemifDHCPv4(ctx context.Context, cfg memifDHCPv4Config) (*memifDHCPv4, error) {
	if cfg.VNF.Type != store.VNFTypeDHCPv4 {
		return nil, fmt.Errorf("memif DHCP transport only supports DHCPv4, got %q", cfg.VNF.Type)
	}
	if strings.TrimSpace(cfg.SocketPath) == "" {
		return nil, errors.New("memif socket path is required")
	}
	mac := cfg.MAC
	if len(mac) != 6 {
		return nil, errors.New("memif MAC address must contain 6 bytes")
	}
	if cfg.Port <= 0 {
		cfg.Port = defaultDHCPPort
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	handlers, err := newDHCPv4Handlers(cfg.VNF, cfg.LeasePath, cfg.Port)
	if err != nil {
		return nil, err
	}
	server, err := newMemifDHCPv4Stack(cfg.VNF, mac, handlers, cfg.Port)
	if err != nil {
		return nil, err
	}

	link, err := newMemifLink(cfg.SocketPath, cfg.InterfaceID, cfg.Name, mac)
	if err != nil {
		server.closeStack()
		return nil, err
	}
	server.memif = link

	runCtx, cancel := context.WithCancel(context.Background())
	server.cancel = cancel
	go server.serveDHCP(runCtx)
	go server.pumpMemifRX(runCtx)
	go server.pumpMemifTX(runCtx)

	log.Info().
		Int64("vnf_id", cfg.VNF.ID).
		Str("type", cfg.VNF.Type).
		Str("interface", cfg.Name).
		Str("socket", cfg.SocketPath).
		Uint32("interface_id", cfg.InterfaceID).
		Str("bind", cfg.VNF.Config.BindIP).
		Str("gateway", cfg.VNF.Config.Gateway).
		Str("leases", cfg.LeasePath).
		Msg("VNF memif transport started")
	return server, nil
}

func (m *memifDHCPv4) serveDHCP(ctx context.Context) {
	defer close(m.done)
	buf := make([]byte, 64*1024)
	for {
		n, _, err := m.conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			log.Warn().Int64("vnf_id", m.vnfID).Err(err).Msg("memif DHCPv4 read failed")
			return
		}
		reply, dst := m.reply(buf[:n])
		if reply == nil || dst == nil {
			continue
		}
		if _, err := m.conn.WriteTo(reply.ToBytes(), dst); err != nil {
			log.Warn().Int64("vnf_id", m.vnfID).Stringer("dst", dst).Err(err).Msg("memif DHCPv4 write failed")
		}
	}
}

func (m *memifDHCPv4) reply(payload []byte) (*dhcpv4.DHCPv4, *net.UDPAddr) {
	req, err := dhcpv4.FromBytes(payload)
	if err != nil || req.OpCode != dhcpv4.OpcodeBootRequest {
		return nil, nil
	}
	resp, err := dhcpv4.NewReplyFromRequest(req)
	if err != nil {
		return nil, nil
	}
	switch mt := req.MessageType(); mt {
	case dhcpv4.MessageTypeDiscover:
		resp.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeOffer))
	case dhcpv4.MessageTypeRequest:
		resp.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeAck))
	case dhcpv4.MessageTypeRelease:
	case dhcpv4.MessageTypeInform:
		resp.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeAck))
	default:
		return nil, nil
	}

	var stop bool
	for _, h := range m.handlers {
		resp, stop = h(req, resp)
		if stop {
			break
		}
	}
	if resp == nil {
		return nil, nil
	}
	log.Trace().
		Int64("vnf_id", m.vnfID).
		Str("client_id", req.ClientHWAddr.String()).
		Str("request", req.MessageType().String()).
		Str("reply", resp.MessageType().String()).
		Str("address", resp.YourIPAddr.String()).
		Int("option_count", len(resp.Options)).
		Str("options", resp.Options.String()).
		Msg("DHCPv4 reply prepared")

	switch {
	case !req.GatewayIPAddr.IsUnspecified():
		return resp, &net.UDPAddr{IP: req.GatewayIPAddr, Port: dhcpv4.ServerPort}
	case resp.MessageType() == dhcpv4.MessageTypeNak:
		return resp, &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
	case !req.ClientIPAddr.IsUnspecified():
		return resp, &net.UDPAddr{IP: req.ClientIPAddr, Port: dhcpv4.ClientPort}
	case req.IsBroadcast():
		return resp, &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
	default:
		// The handler forces broadcast for clients that do not have an address
		// yet. A truly unicast reply would need an ARP entry for YourIPAddr.
		return resp, &net.UDPAddr{IP: resp.YourIPAddr, Port: dhcpv4.ClientPort}
	}
}

func (m *memifDHCPv4) pumpMemifRX(ctx context.Context) {
	for {
		frame, err := m.memif.readFrame(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, errMemifClosed) {
				return
			}
			// Transient memif read errors must not kill the receive path
			// permanently; back off briefly and keep reading.
			log.Warn().Int64("vnf_id", m.vnfID).Err(err).Msg("memif DHCPv4 receive failed")
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		m.injectFrame(frame)
	}
}

func (m *memifDHCPv4) pumpMemifTX(ctx context.Context) {
	for {
		pkt := m.link.ReadContext(ctx)
		if pkt == nil {
			return
		}
		view := pkt.ToView()
		err := m.memif.writeFrame(view.AsSlice())
		pkt.DecRef()
		if err == nil {
			continue
		}
		if ctx.Err() != nil || errors.Is(err, errMemifClosed) {
			return
		}
		log.Warn().Int64("vnf_id", m.vnfID).Err(err).Msg("memif DHCPv4 transmit failed")
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (m *memifDHCPv4) injectFrame(frame []byte) {
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(frame)})
	m.link.InjectInbound(header.IPv4ProtocolNumber, pkt)
}

func (m *memifDHCPv4) Close() {
	m.closeOnce.Do(func() {
		if m.cancel != nil {
			m.cancel()
		}
		if m.conn != nil {
			_ = m.conn.Close()
		}
		if m.memif != nil {
			m.memif.close()
		}
		if m.link != nil {
			m.link.Close()
		}
		select {
		case <-m.done:
		case <-time.After(vnfStopTimeout):
		}
		m.closeStack()
	})
}

func (m *memifDHCPv4) closeStack() {
	if m.stack != nil {
		m.stack.Close()
		m.stack.Wait()
	}
}
