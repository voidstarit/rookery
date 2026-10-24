package host

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/coredhcp/coredhcp/handler"
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
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"github.com/voidstarit/rookery/internal/store"
)

// sharedNetwork is one gVisor netstack connected to a network's shared VPP
// memif. All userspace VNFs on that network (DHCPv4 and later BGP) register
// listeners on this stack instead of creating a memif per VNF.
type sharedNetwork struct {
	networkID int64
	address   string
	socket    string
	ifID      uint32
	mac       net.HardwareAddr

	stack  *stack.Stack
	link   *channel.Endpoint
	memif  *memifLink
	cancel context.CancelFunc
	done   chan struct{}

	mu    sync.Mutex
	vnfs  map[int64]string
	dhcp4 *sharedDHCPv4
	dns   *sharedDNS

	closeOnce sync.Once
	closed    bool
}

type sharedNetworkConfig struct {
	NetworkID   int64
	SocketPath  string
	InterfaceID uint32
	Address     string // IPv4 service CIDR, normally .2/prefix
	MAC         net.HardwareAddr
}

type sharedDHCPv4 struct {
	vnfID    int64
	conn     *gonet.UDPConn
	handlers []handler.Handler4
	cancel   context.CancelFunc
	done     chan struct{}
}

// NetworkVNFMemifID is the memif interface/socket id for a network's shared
// VNF memif. The high bit keeps it distinct from per-VNF memif ids.
func NetworkVNFMemifID(networkID int64) uint32 {
	return 0x80000000 | uint32(networkID)
}

// NetworkVNFMemifMAC is the VPP master MAC for a network's shared VNF memif.
func NetworkVNFMemifMAC(networkID int64) net.HardwareAddr {
	return net.HardwareAddr{0x02, 0x4d, 0x4e, byte(uint64(networkID) >> 16), byte(uint64(networkID) >> 8), byte(networkID)}
}

// NetworkVNFStackMAC is the gVisor slave MAC for a network's shared VNF memif.
func NetworkVNFStackMAC(networkID int64) net.HardwareAddr {
	return net.HardwareAddr{0x02, 0x4e, 0x4e, byte(uint64(networkID) >> 16), byte(uint64(networkID) >> 8), byte(networkID)}
}

func startSharedNetwork(ctx context.Context, cfg sharedNetworkConfig) (*sharedNetwork, error) {
	if cfg.NetworkID < 1 {
		return nil, errors.New("invalid network id")
	}
	if cfg.SocketPath == "" {
		return nil, errors.New("shared memif socket path is required")
	}
	if len(cfg.MAC) != 6 {
		return nil, errors.New("shared memif MAC must contain 6 bytes")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ip, ipnet, err := net.ParseCIDR(cfg.Address)
	if err != nil || ip.To4() == nil {
		return nil, fmt.Errorf("parse shared VNF address %q: %w", cfg.Address, err)
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return nil, fmt.Errorf("shared VNF address %q is not IPv4", cfg.Address)
	}

	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol, arp.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{udp.NewProtocol, tcp.NewProtocol},
	})
	link := channel.New(memifDHCPRingSize, memifDHCPMTU, tcpip.LinkAddress(cfg.MAC))
	eth := ethernet.New(link)
	if err := s.CreateNIC(1, eth); err != nil {
		return nil, fmt.Errorf("create shared netstack NIC: %v", err)
	}
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          header.IPv4ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFromSlice(ip.To4()), PrefixLen: ones},
	}, stack.AddressProperties{}); err != nil {
		return nil, fmt.Errorf("assign shared netstack address %s: %v", ip, err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})

	memif, err := newMemifLink(cfg.SocketPath, cfg.InterfaceID, fmt.Sprintf("net%d-vnf", cfg.NetworkID), cfg.MAC)
	if err != nil {
		s.Close()
		s.Wait()
		return nil, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	n := &sharedNetwork{
		networkID: cfg.NetworkID,
		address:   cfg.Address,
		socket:    cfg.SocketPath,
		ifID:      cfg.InterfaceID,
		mac:       cfg.MAC,
		stack:     s,
		link:      link,
		memif:     memif,
		cancel:    cancel,
		done:      make(chan struct{}),
		vnfs:      make(map[int64]string),
	}
	go n.pumpRX(runCtx)
	go n.pumpTX(runCtx)
	return n, nil
}

func (n *sharedNetwork) addDHCPv4(vnf store.VNF, leasePath string, port int) error {
	if port <= 0 {
		port = defaultDHCPPort
	}
	handlers, err := newDHCPv4Handlers(vnf, leasePath, port)
	if err != nil {
		return err
	}

	var wq waiter.Queue
	endpoint, tcpipErr := n.stack.NewEndpoint(udp.ProtocolNumber, header.IPv4ProtocolNumber, &wq)
	if tcpipErr != nil {
		return fmt.Errorf("create shared DHCPv4 endpoint: %v", tcpipErr)
	}
	endpoint.SocketOptions().SetBroadcast(true)
	if err := endpoint.Bind(tcpip.FullAddress{NIC: 1, Port: uint16(port)}); err != nil {
		endpoint.Close()
		return fmt.Errorf("bind shared DHCPv4 endpoint: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	component := &sharedDHCPv4{
		vnfID:    vnf.ID,
		conn:     gonet.NewUDPConn(&wq, endpoint),
		handlers: handlers,
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	n.mu.Lock()
	if n.dhcp4 != nil {
		n.mu.Unlock()
		component.cancel()
		_ = component.conn.Close()
		return errors.New("a DHCPv4 VNF is already registered on this network")
	}
	n.dhcp4 = component
	n.vnfs[vnf.ID] = vnf.Type
	n.mu.Unlock()

	go n.serveDHCPv4(runCtx, component)

	log.Info().
		Int64("vnf_id", vnf.ID).
		Int64("network_id", n.networkID).
		Str("address", n.address).
		Str("socket", n.socket).
		Str("leases", leasePath).
		Msg("DHCPv4 started on shared VNF memif")
	return nil
}

func (n *sharedNetwork) serveDHCPv4(ctx context.Context, component *sharedDHCPv4) {
	defer close(component.done)
	buf := make([]byte, 64*1024)
	for {
		read, _, err := component.conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			log.Warn().Int64("vnf_id", component.vnfID).Err(err).Msg("shared DHCPv4 read failed")
			return
		}
		reply, dst := replyDHCPv4(component.vnfID, component.handlers, buf[:read])
		if reply == nil || dst == nil {
			continue
		}
		if _, err := component.conn.WriteTo(reply.ToBytes(), dst); err != nil {
			log.Warn().Int64("vnf_id", component.vnfID).Stringer("dst", dst).Err(err).Msg("shared DHCPv4 write failed")
		}
	}
}

func replyDHCPv4(vnfID int64, handlers []handler.Handler4, payload []byte) (*dhcpv4.DHCPv4, *net.UDPAddr) {
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
	for _, h := range handlers {
		resp, stop = h(req, resp)
		if stop {
			break
		}
	}
	if resp == nil {
		return nil, nil
	}
	log.Trace().
		Int64("vnf_id", vnfID).
		Str("client_id", req.ClientHWAddr.String()).
		Str("request", req.MessageType().String()).
		Str("reply", resp.MessageType().String()).
		Str("address", resp.YourIPAddr.String()).
		Int("option_count", len(resp.Options)).
		Str("options", resp.Options.String()).
		Msg("shared DHCPv4 reply prepared")

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
		return resp, &net.UDPAddr{IP: resp.YourIPAddr, Port: dhcpv4.ClientPort}
	}
}

func (n *sharedNetwork) removeVNF(vnfID int64) bool {
	n.mu.Lock()
	_, exists := n.vnfs[vnfID]
	if exists {
		delete(n.vnfs, vnfID)
	}
	if n.dhcp4 != nil && n.dhcp4.vnfID == vnfID {
		component := n.dhcp4
		n.dhcp4 = nil
		n.mu.Unlock()
		component.cancel()
		_ = component.conn.Close()
		select {
		case <-component.done:
		case <-time.After(vnfStopTimeout):
		}
		n.mu.Lock()
	}
	if n.dns != nil && n.dns.vnfID == vnfID {
		component := n.dns
		n.dns = nil
		n.mu.Unlock()
		component.close()
		n.mu.Lock()
	}
	empty := len(n.vnfs) == 0
	n.mu.Unlock()
	return empty
}

func (n *sharedNetwork) pumpRX(ctx context.Context) {
	for {
		frame, err := n.memif.readFrame(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, errMemifClosed) {
				return
			}
			log.Warn().Int64("network_id", n.networkID).Err(err).Msg("shared memif receive failed")
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(frame)})
		n.link.InjectInbound(header.IPv4ProtocolNumber, pkt)
	}
}

func (n *sharedNetwork) pumpTX(ctx context.Context) {
	for {
		pkt := n.link.ReadContext(ctx)
		if pkt == nil {
			return
		}
		view := pkt.ToView()
		err := n.memif.writeFrame(view.AsSlice())
		pkt.DecRef()
		if err == nil {
			continue
		}
		if ctx.Err() != nil || errors.Is(err, errMemifClosed) {
			return
		}
		log.Warn().Int64("network_id", n.networkID).Err(err).Msg("shared memif transmit failed")
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (n *sharedNetwork) close() {
	n.closeOnce.Do(func() {
		n.mu.Lock()
		n.closed = true
		n.mu.Unlock()
		if n.cancel != nil {
			n.cancel()
		}
		n.mu.Lock()
		component := n.dhcp4
		n.dhcp4 = nil
		dnsComponent := n.dns
		n.dns = nil
		n.mu.Unlock()
		if component != nil {
			component.cancel()
			_ = component.conn.Close()
			select {
			case <-component.done:
			case <-time.After(vnfStopTimeout):
			}
		}
		if dnsComponent != nil {
			dnsComponent.close()
		}
		if n.memif != nil {
			n.memif.close()
		}
		if n.link != nil {
			n.link.Close()
		}
		close(n.done)
		if n.stack != nil {
			n.stack.Close()
			n.stack.Wait()
		}
	})
}

func (n *sharedNetwork) isClosed() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.closed
}
