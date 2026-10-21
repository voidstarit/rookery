package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

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

	"github.com/voidstarit/rookery/internal/store"
)

type bgpMemifRuntime struct {
	stack  *stack.Stack
	link   *channel.Endpoint
	memif  *memifLink
	server *bgpInstance
	proxy  *bgpTCPProxy

	cancel    context.CancelFunc
	closeOnce sync.Once
	done      chan struct{}
}

type bgpTCPProxy struct {
	listener   net.Listener
	stack      *stack.Stack
	peerIP     net.IP
	proto      tcpip.NetworkProtocolNumber
	serverIP   string
	serverPort int
	active     bool
	closeOnce  sync.Once
}

func (p *bgpTCPProxy) Close() {
	p.closeOnce.Do(func() {
		if p.listener != nil {
			_ = p.listener.Close()
		}
	})
}

func (p *bgpTCPProxy) acceptLoop(ctx context.Context) {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		go p.handle(ctx, conn)
	}
}

func (p *bgpTCPProxy) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	var remote net.Conn
	var err error
	if p.active {
		remote, err = gonet.DialTCPWithBind(ctx, p.stack,
			tcpip.FullAddress{NIC: 1},
			tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFromSlice(p.peerIP), Port: 179},
			p.proto)
	} else {
		remote, err = net.DialTimeout("tcp", net.JoinHostPort(p.serverIP, strconv.Itoa(p.serverPort)), 3*time.Second)
	}
	if err != nil {
		log.Warn().Err(err).Msg("BGP memif proxy connect failed")
		return
	}
	defer remote.Close()
	proxyConnections(conn, remote)
}

func proxyConnections(a, b net.Conn) {
	done := make(chan struct{}, 2)
	copyOne := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		_ = dst.Close()
		done <- struct{}{}
	}
	go copyOne(a, b)
	go copyOne(b, a)
	<-done
}

// startBGPMemif starts an embedded GoBGP instance behind a gVisor netstack
// connected to a VPP memif. GoBGP itself still uses a loopback TCP socket;
// the proxy translates that stream to the actual peer over gVisor.
func startBGPMemif(ctx context.Context, vnf store.VNF, socketPath string, ifID uint32, routeWriter BGPRouteWriter) (*bgpMemifRuntime, error) {
	if vnf.Type != store.VNFTypeBGP {
		return nil, fmt.Errorf("memif BGP runtime only supports BGP VNFs, got %q", vnf.Type)
	}
	if vnf.Config.Transport != "memif" {
		return nil, errors.New("BGP transport is not memif")
	}
	if strings.TrimSpace(socketPath) == "" {
		return nil, errors.New("memif socket path is required")
	}
	localIP, localNet, err := net.ParseCIDR(strings.TrimSpace(vnf.Config.LocalIP))
	if err != nil || localIP == nil {
		return nil, errors.New("BGP local_ip must be an IPv4 or IPv6 CIDR")
	}
	isIPv6 := localIP.To4() == nil
	peerIP := net.ParseIP(strings.TrimSpace(vnf.Config.PeerIP))
	if peerIP == nil || (peerIP.To4() == nil) != isIPv6 {
		return nil, errors.New("BGP peer_ip must match the local_ip address family")
	}
	if !isIPv6 {
		peerIP = peerIP.To4()
	}
	gateway := net.ParseIP(strings.TrimSpace(vnf.Config.PeerGateway))
	if gateway != nil && (gateway.To4() == nil) != isIPv6 {
		return nil, errors.New("BGP peer_gateway must match the local_ip address family")
	}
	if !isIPv6 && gateway != nil {
		gateway = gateway.To4()
	}
	nextHop := net.ParseIP(strings.TrimSpace(vnf.Config.NextHop))
	if nextHop != nil && (nextHop.To4() == nil) != isIPv6 {
		return nil, errors.New("BGP next_hop must match the local_ip address family")
	}
	if !isIPv6 && nextHop != nil {
		nextHop = nextHop.To4()
	}
	local := localIP
	networkProtocol := header.IPv6ProtocolNumber
	emptySubnet := header.IPv6EmptySubnet
	if !isIPv6 {
		local = localIP.To4()
		networkProtocol = header.IPv4ProtocolNumber
		emptySubnet = header.IPv4EmptySubnet
	}
	prefixes, err := bgpPrefixes(vnf.Config.AdvertisePrefixes)
	if err != nil {
		return nil, err
	}

	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol, arp.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
	})
	// The gVisor NIC needs its own MAC, distinct from the VPP memif master's
	// VNFMemifMAC, so bridge-domain learning maps the BGP address correctly.
	link := channel.New(memifDHCPRingSize, memifDHCPMTU, tcpip.LinkAddress(vnfMemifBGPMAC(vnf.ID)))
	eth := ethernet.New(link)
	if err := s.CreateNIC(1, eth); err != nil {
		return nil, fmt.Errorf("create BGP netstack NIC: %v", err)
	}
	ones, _ := localNet.Mask.Size()
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol: networkProtocol,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   tcpip.AddrFromSlice(local),
			PrefixLen: ones,
		},
	}, stack.AddressProperties{}); err != nil {
		return nil, fmt.Errorf("assign BGP netstack address: %v", err)
	}
	ttl := vnf.Config.MultihopTTL
	if ttl == 0 {
		ttl = defaultBGPMultihopTTL
	}
	ttlOption := tcpip.DefaultTTLOption(uint8(ttl))
	_ = s.SetNetworkProtocolOption(ipv4.ProtocolNumber, &ttlOption)
	_ = s.SetNetworkProtocolOption(ipv6.ProtocolNumber, &ttlOption)
	// The default route is on-link unless a peer gateway is configured. With a
	// gateway, ALL off-subnet traffic (the BGP session and ICMP echo replies to
	// routed sources alike) goes through it, so ping answers survive a BVI hop
	// just like the TCP session. Same-subnet destinations keep using the
	// connected route for the local prefix.
	routes := []tcpip.Route{{Destination: emptySubnet, NIC: 1}}
	if gateway != nil {
		routes[0].Gateway = tcpip.AddrFromSlice(gateway)
	}
	s.SetRouteTable(routes)

	memif, err := newMemifLink(socketPath, ifID, vnfMemifName(vnf), vnfMemifBGPMAC(vnf.ID))
	if err != nil {
		s.Close()
		s.Wait()
		return nil, err
	}

	runtime := &bgpMemifRuntime{
		stack: s,
		link:  link,
		memif: memif,
		done:  make(chan struct{}),
	}

	// Create the local TCP relay. In passive mode GoBGP listens on loopback
	// and the gVisor listener accepts the real peer. In active mode GoBGP
	// dials a loopback listener which then dials the real peer through gVisor.
	var gobgpPort int
	var proxyPort int
	passive := vnf.Config.Passive
	if passive {
		gobgpPort, err = freeTCPPort()
		if err != nil {
			runtime.closeStack()
			return nil, err
		}
	} else {
		proxyPort, err = freeTCPPort()
		if err != nil {
			runtime.closeStack()
			return nil, err
		}
	}

	// Start gVisor pumps before BGP so established sessions can carry traffic.
	runCtx, cancel := context.WithCancel(context.Background())
	runtime.cancel = cancel
	go runtime.pumpRX(runCtx)
	go runtime.pumpTX(runCtx)

	var proxy *bgpTCPProxy
	if passive {
		listener, err := gonet.ListenTCP(s, tcpip.FullAddress{NIC: 1, Port: 179}, networkProtocol)
		if err != nil {
			runtime.Close(ctx)
			return nil, fmt.Errorf("listen for BGP on memif: %v", err)
		}
		proxy = &bgpTCPProxy{
			listener: listener, stack: s, proto: networkProtocol,
			serverIP: "127.0.0.1", serverPort: gobgpPort,
		}
	} else {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)))
		if err != nil {
			runtime.Close(ctx)
			return nil, fmt.Errorf("listen for GoBGP proxy: %w", err)
		}
		proxy = &bgpTCPProxy{
			listener: listener, stack: s, peerIP: peerIP, proto: networkProtocol, active: true,
		}
	}
	runtime.proxy = proxy

	cfg := bgpStartConfig{
		VNF: vnf, LocalIP: local, LocalNet: localNet, PeerIP: peerIP,
		PeerGateway: gateway, NextHop: nextHop,
		LocalAS: vnf.Config.LocalAS, PeerAS: vnf.Config.PeerAS, RouterID: vnf.Config.RouterID,
		Password: "", Passive: passive, MultihopTTL: uint8(ttl),
		TargetTableID: vnf.Config.TargetTableID, Advertise: prefixes, RouteWriter: routeWriter,
		Neighbor: "127.0.0.1",
	}
	if passive {
		cfg.ListenAddress = []string{"127.0.0.1"}
		cfg.ListenPort = int32(gobgpPort)
		cfg.LocalAddress = "127.0.0.1"
	} else {
		cfg.LocalAddress = "127.0.0.1"
		cfg.RemoteAddress = "127.0.0.1"
		cfg.RemotePort = uint32(proxyPort)
		// GoBGP defaults a zero port to 179 and opens a listener. Active
		// memif mode has no GoBGP-facing listener; use -1 to suppress it.
		cfg.ListenPort = -1
	}
	server, err := startBGPServer(ctx, cfg)
	if err != nil {
		runtime.Close(ctx)
		return nil, err
	}
	runtime.server = server
	go proxy.acceptLoop(runCtx)

	if passive {
		log.Info().
			Int64("vnf_id", vnf.ID).
			Str("memif", socketPath).
			Str("memif_name", vnfMemifName(vnf)).
			Uint32("interface_id", ifID).
			Str("mac", vnfMemifBGPMAC(vnf.ID).String()).
			Str("mode", "passive").
			Str("listen", localIP.String()+":179").
			Str("gobgp_listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(gobgpPort))).
			Msg("BGP memif runtime started")
	} else {
		log.Info().
			Int64("vnf_id", vnf.ID).
			Str("memif", socketPath).
			Str("memif_name", vnfMemifName(vnf)).
			Uint32("interface_id", ifID).
			Str("mac", vnfMemifBGPMAC(vnf.ID).String()).
			Str("mode", "active").
			Str("dial", peerIP.String()+":179").
			Str("proxy_listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort))).
			Msg("BGP memif runtime started")
	}
	return runtime, nil
}

func (m *bgpMemifRuntime) pumpRX(ctx context.Context) {
	for {
		frame, err := m.memif.readFrame(ctx)
		if err != nil {
			if errors.Is(err, errMemifClosed) || ctx.Err() != nil {
				return
			}
			// Transient memif read errors must not kill the receive path
			// permanently; back off briefly and keep reading.
			log.Warn().Err(err).Msg("BGP memif receive failed")
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(frame)})
		m.link.InjectInbound(header.IPv4ProtocolNumber, pkt)
	}
}

func (m *bgpMemifRuntime) pumpTX(ctx context.Context) {
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
		if errors.Is(err, errMemifClosed) || ctx.Err() != nil {
			return
		}
		log.Warn().Err(err).Msg("BGP memif transmit failed")
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (m *bgpMemifRuntime) Close(ctx context.Context) {
	m.closeOnce.Do(func() {
		if m.cancel != nil {
			m.cancel()
		}
		if m.proxy != nil {
			m.proxy.Close()
		}
		if m.server != nil {
			m.server.Close(ctx)
		}
		if m.memif != nil {
			m.memif.close()
		}
		if m.link != nil {
			m.link.Close()
		}
		if m.stack != nil {
			m.stack.Close()
			m.stack.Wait()
		}
		log.Info().Msg("BGP memif runtime stopped")
		close(m.done)
	})
}

func (m *bgpMemifRuntime) closeStack() {
	if m.memif != nil {
		m.memif.close()
	}
	if m.link != nil {
		m.link.Close()
	}
	if m.stack != nil {
		m.stack.Close()
		m.stack.Wait()
	}
}

func freeTCPPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
