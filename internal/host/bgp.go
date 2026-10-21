package host

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	api "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/apiutil"
	"github.com/osrg/gobgp/v3/pkg/packet/bgp"
	"github.com/osrg/gobgp/v3/pkg/server"

	"github.com/voidstarit/rookery/internal/store"
)

// BGPRouteWriter installs and removes BGP-learned IPv4 routes in a VPP VRF.
type BGPRouteWriter interface {
	AddBGPRoute(ctx context.Context, tableID uint32, prefix, nextHop string) error
	DeleteBGPRoute(ctx context.Context, tableID uint32, prefix string) error
}

// defaultBGPMultihopTTL is used when the VNF config leaves multihop_ttl
// unset. 64 keeps directly attached sessions working while allowing replies
// to survive routed topologies (for example a peer reached through a BVI).
const defaultBGPMultihopTTL = 64

type bgpStartConfig struct {
	VNF           store.VNF
	LocalIP       net.IP
	LocalNet      *net.IPNet
	PeerIP        net.IP
	PeerGateway   net.IP
	NextHop       net.IP
	LocalAS       uint32
	PeerAS        uint32
	RouterID      string
	Password      string
	Passive       bool
	MultihopTTL   uint8
	TargetTableID uint32
	Advertise     []*net.IPNet
	RouteWriter   BGPRouteWriter
	Neighbor      string
	LocalAddress  string
	ListenAddress []string
	ListenPort    int32
	RemoteAddress string
	RemotePort    uint32
	BindToDevice  string
}

type bgpInstance struct {
	vnfID         int64
	server        *server.BgpServer
	routeWriter   BGPRouteWriter
	targetTableID uint32
	neighbor      string
	localAS       uint32
	peerAS        uint32
	peerAddress   string
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	installedMu   sync.Mutex
	installed     map[string]string
	closeOnce     sync.Once
}

// BGPReceivedPrefix is a received unicast prefix with its next hop and AS path.
type BGPReceivedPrefix struct {
	Prefix  string
	NextHop string
	ASPath  []uint32
}

// bgpIsIPv6 reports whether the address belongs to the IPv6 family.
func bgpIsIPv6(ip net.IP) bool { return ip != nil && ip.To4() == nil }

// bgpAFI returns the GoBGP AFI for an address family.
func bgpAFI(ip net.IP) api.Family_Afi {
	if bgpIsIPv6(ip) {
		return api.Family_AFI_IP6
	}
	return api.Family_AFI_IP
}

// bgpPathPrefix extracts the printable prefix from a native path NLRI.
func bgpPathPrefix(path *api.Path) (string, bool) {
	nlri, err := apiutil.GetNativeNlri(path)
	if err != nil {
		return "", false
	}
	switch value := nlri.(type) {
	case *bgp.IPAddrPrefix:
		return value.String(), true
	case *bgp.IPv6AddrPrefix:
		return value.String(), true
	}
	return "", false
}

// bgpPathNextHop extracts the IPv4 or IPv6 next hop from path attributes.
func bgpPathNextHop(attrs []bgp.PathAttributeInterface) (string, bool) {
	for _, attr := range attrs {
		switch value := attr.(type) {
		case *bgp.PathAttributeNextHop:
			if value.Value != nil && !value.Value.IsUnspecified() {
				return value.Value.String(), true
			}
		case *bgp.PathAttributeMpReachNLRI:
			if value.Nexthop != nil && !value.Nexthop.IsUnspecified() {
				return value.Nexthop.String(), true
			}
		}
	}
	return "", false
}

// bgpPathASPath extracts the flattened AS path from path attributes.
func bgpPathASPath(attrs []bgp.PathAttributeInterface) []uint32 {
	result := make([]uint32, 0, 4)
	for _, attr := range attrs {
		if value, ok := attr.(*bgp.PathAttributeAsPath); ok {
			for _, segment := range value.Value {
				result = append(result, segment.GetAS()...)
			}
		}
	}
	return result
}

// BGPStatus is a snapshot of a BGP VNF's session and routing state.
type BGPStatus struct {
	Running      bool
	Established  bool
	SessionState string
	PeerAddress  string
	PeerAS       uint32
	LocalAS      uint32
	Advertised   []string
	Received     []BGPReceivedPrefix
}

// Status snapshots the GoBGP session state and IPv4/IPv6 unicast RIB.
func (b *bgpInstance) Status(ctx context.Context) BGPStatus {
	status := BGPStatus{
		Running:     true,
		PeerAddress: b.peerAddress,
		PeerAS:      b.peerAS,
		LocalAS:     b.localAS,
		Advertised:  []string{},
		Received:    []BGPReceivedPrefix{},
	}
	if err := b.server.ListPeer(ctx, &api.ListPeerRequest{Address: b.neighbor}, func(peer *api.Peer) {
		if peer == nil || peer.State == nil || status.SessionState != "" {
			return
		}
		status.SessionState = peer.State.SessionState.String()
		status.Established = peer.State.SessionState == api.PeerState_ESTABLISHED
	}); err != nil {
		slog.Warn("could not list BGP peers", "vnf_id", b.vnfID, "error", err)
	}
	families := []api.Family_Afi{api.Family_AFI_IP, api.Family_AFI_IP6}
	for _, afi := range families {
		err := b.server.ListPath(ctx, &api.ListPathRequest{
			TableType: api.TableType_GLOBAL,
			Family:    &api.Family{Afi: afi, Safi: api.Family_SAFI_UNICAST},
		}, func(destination *api.Destination) {
			if destination == nil {
				return
			}
			for _, path := range destination.Paths {
				if path == nil || path.GetIsWithdraw() {
					continue
				}
				// Locally originated paths may carry a nil Family; only filter
				// by family when it is present.
				if path.Family != nil && (path.Family.Afi != afi || path.Family.Safi != api.Family_SAFI_UNICAST) {
					continue
				}
				prefixText, ok := bgpPathPrefix(path)
				if !ok {
					continue
				}
				if path.NeighborIp == "" || path.NeighborIp == "<nil>" {
					// Locally originated (GoBGP reports "<nil>" for local
					// paths): an advertised prefix.
					status.Advertised = append(status.Advertised, prefixText)
					continue
				}
				received := BGPReceivedPrefix{Prefix: prefixText, ASPath: []uint32{}}
				if attrs, attrErr := apiutil.GetNativePathAttributes(path); attrErr == nil {
					if nextHop, ok := bgpPathNextHop(attrs); ok {
						received.NextHop = nextHop
					}
					received.ASPath = bgpPathASPath(attrs)
				}
				status.Received = append(status.Received, received)
			}
		})
		if err != nil {
			slog.Warn("could not list BGP paths", "vnf_id", b.vnfID, "afi", afi.String(), "error", err)
		}
	}
	return status
}

func startBGPServer(ctx context.Context, cfg bgpStartConfig) (*bgpInstance, error) {
	if cfg.VNF.Type != store.VNFTypeBGP {
		return nil, fmt.Errorf("BGP runtime only supports BGP VNFs, got %q", cfg.VNF.Type)
	}
	if cfg.LocalAS == 0 || cfg.PeerAS == 0 {
		return nil, errors.New("local and peer ASNs are required")
	}
	if cfg.RouterID == "" || net.ParseIP(cfg.RouterID).To4() == nil {
		return nil, errors.New("BGP router ID must be IPv4")
	}
	if cfg.LocalIP == nil || cfg.PeerIP == nil {
		return nil, errors.New("BGP local and peer addresses are required")
	}
	if bgpIsIPv6(cfg.LocalIP) != bgpIsIPv6(cfg.PeerIP) {
		return nil, errors.New("BGP local and peer addresses must use the same address family")
	}
	for _, network := range cfg.Advertise {
		if bgpIsIPv6(network.IP) != bgpIsIPv6(cfg.LocalIP) {
			return nil, errors.New("BGP advertised prefixes must match the local address family")
		}
	}
	if cfg.Password != "" && cfg.VNF.Config.Transport == "memif" {
		return nil, errors.New("BGP TCP MD5 password requires tap transport")
	}
	if cfg.Neighbor == "" {
		return nil, errors.New("BGP neighbor address is required")
	}

	bgpServer := server.NewBgpServer()
	go bgpServer.Serve()

	global := &api.Global{
		Asn:        cfg.LocalAS,
		RouterId:   cfg.RouterID,
		ListenPort: cfg.ListenPort,
		// GoBGP's global AfiSafi list indexes: 0 = IPv4 unicast, 1 = IPv6
		// unicast (oc.IntToAfiSafiTypeMap). Both RIBs are enabled; the peer
		// config selects the negotiated session family.
		ListenAddresses: append([]string(nil), cfg.ListenAddress...),
		Families:        []uint32{0, 1},
		BindToDevice:    cfg.BindToDevice,
	}
	if err := bgpServer.StartBgp(ctx, &api.StartBgpRequest{Global: global}); err != nil {
		bgpServer.Stop()
		return nil, fmt.Errorf("start GoBGP: %w", err)
	}

	peerType := api.PeerType_EXTERNAL
	if cfg.LocalAS == cfg.PeerAS {
		peerType = api.PeerType_INTERNAL
	}
	peer := &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: cfg.Neighbor,
			PeerAsn:         cfg.PeerAS,
			LocalAsn:        cfg.LocalAS,
			AuthPassword:    cfg.Password,
			Type:            peerType,
		},
		Transport: &api.Transport{
			LocalAddress:  cfg.LocalAddress,
			PassiveMode:   cfg.Passive,
			RemoteAddress: cfg.RemoteAddress,
			RemotePort:    cfg.RemotePort,
			BindInterface: cfg.BindToDevice,
		},
		AfiSafis: []*api.AfiSafi{{
			Config: &api.AfiSafiConfig{
				Family:  &api.Family{Afi: bgpAFI(cfg.LocalIP), Safi: api.Family_SAFI_UNICAST},
				Enabled: true,
			},
		}},
	}
	if cfg.MultihopTTL > 1 && peerType == api.PeerType_EXTERNAL {
		peer.EbgpMultihop = &api.EbgpMultihop{Enabled: true, MultihopTtl: uint32(cfg.MultihopTTL)}
	}
	if err := bgpServer.AddPeer(ctx, &api.AddPeerRequest{Peer: peer}); err != nil {
		bgpServer.Stop()
		return nil, fmt.Errorf("add BGP peer: %w", err)
	}

	instance := &bgpInstance{
		vnfID:         cfg.VNF.ID,
		server:        bgpServer,
		routeWriter:   cfg.RouteWriter,
		targetTableID: cfg.TargetTableID,
		neighbor:      cfg.Neighbor,
		localAS:       cfg.LocalAS,
		peerAS:        cfg.PeerAS,
		peerAddress:   cfg.PeerIP.String(),
		installed:     make(map[string]string),
	}
	if err := instance.advertise(ctx, cfg); err != nil {
		bgpServer.Stop()
		return nil, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	instance.cancel = cancel
	instance.wg.Add(1)
	go instance.watchRoutes(runCtx)
	instance.wg.Add(1)
	go instance.watchPeerState(runCtx)

	slog.Info("BGP VNF started",
		"vnf_id", cfg.VNF.ID,
		"transport", cfg.VNF.Config.Transport,
		"family", bgpAFI(cfg.LocalIP).String(),
		"local_as", cfg.LocalAS,
		"peer", cfg.Neighbor,
		"peer_address", cfg.PeerIP.String(),
		"peer_as", cfg.PeerAS,
		"target_table", cfg.TargetTableID,
	)
	return instance, nil
}

func (b *bgpInstance) advertise(ctx context.Context, cfg bgpStartConfig) error {
	if len(cfg.Advertise) == 0 {
		return nil
	}
	nextHop := cfg.LocalIP
	if cfg.NextHop != nil {
		nextHop = cfg.NextHop
	}
	nextHopText := nextHop.String()
	for _, network := range cfg.Advertise {
		ones, _ := network.Mask.Size()
		var nlri bgp.AddrPrefixInterface
		var attrs []bgp.PathAttributeInterface
		if bgpIsIPv6(network.IP) {
			nlri = bgp.NewIPv6AddrPrefix(uint8(ones), network.IP.String())
			attrs = []bgp.PathAttributeInterface{
				bgp.NewPathAttributeOrigin(0),
				bgp.NewPathAttributeMpReachNLRI(nextHopText, []bgp.AddrPrefixInterface{nlri}),
			}
		} else {
			nlri = bgp.NewIPAddrPrefix(uint8(ones), network.IP.String())
			attrs = []bgp.PathAttributeInterface{
				bgp.NewPathAttributeOrigin(0),
				bgp.NewPathAttributeNextHop(nextHopText),
			}
		}
		path, err := apiutil.NewPath(nlri, false, attrs, time.Now())
		if err != nil {
			return fmt.Errorf("build advertised BGP path %s: %w", network, err)
		}
		if _, err := b.server.AddPath(ctx, &api.AddPathRequest{
			TableType: api.TableType_GLOBAL,
			Path:      path,
		}); err != nil {
			return fmt.Errorf("advertise BGP prefix %s: %w", network, err)
		}
	}
	return nil
}

func (b *bgpInstance) watchRoutes(ctx context.Context) {
	defer b.wg.Done()
	err := b.server.WatchEvent(ctx, &api.WatchEventRequest{
		Table: &api.WatchEventRequest_Table{
			Filters: []*api.WatchEventRequest_Table_Filter{{
				Type: api.WatchEventRequest_Table_Filter_BEST,
				Init: true,
			}},
		},
	}, b.handlePaths)
	if err != nil && ctx.Err() == nil {
		slog.Warn("BGP route watcher stopped", "vnf_id", b.vnfID, "error", err)
	}
}

// watchPeerState logs BGP session state transitions so VNF connectivity
// problems are visible in the server log instead of only in the UI status.
func (b *bgpInstance) watchPeerState(ctx context.Context) {
	defer b.wg.Done()
	err := b.server.WatchEvent(ctx, &api.WatchEventRequest{
		Peer: &api.WatchEventRequest_Peer{},
	}, func(response *api.WatchEventResponse) {
		event := response.GetPeer()
		if event == nil || event.Peer == nil || event.Peer.State == nil {
			return
		}
		state := event.Peer.State
		slog.Info("BGP session state changed",
			"vnf_id", b.vnfID,
			"neighbor", event.Peer.Conf.GetNeighborAddress(),
			"session_state", state.SessionState.String(),
			"admin_state", state.AdminState.String(),
		)
	})
	if err != nil && ctx.Err() == nil {
		slog.Warn("BGP peer state watcher stopped", "vnf_id", b.vnfID, "error", err)
	}
}

func (b *bgpInstance) handlePaths(response *api.WatchEventResponse) {
	table := response.GetTable()
	if table == nil {
		return
	}
	for _, path := range table.Paths {
		if path == nil || path.Family == nil {
			continue
		}
		if (path.Family.Afi != api.Family_AFI_IP && path.Family.Afi != api.Family_AFI_IP6) ||
			path.Family.Safi != api.Family_SAFI_UNICAST {
			continue
		}
		if path.NeighborIp == "" || (b.neighbor != "" && path.NeighborIp != b.neighbor) {
			continue
		}
		prefixText, ok := bgpPathPrefix(path)
		if !ok {
			slog.Warn("BGP route NLRI parse failed", "vnf_id", b.vnfID, "nlri", path.Nlri)
			continue
		}
		if path.IsWithdraw {
			b.removeRoute(context.Background(), prefixText)
			continue
		}
		attrs, err := apiutil.GetNativePathAttributes(path)
		if err != nil {
			slog.Warn("BGP route attribute parse failed", "vnf_id", b.vnfID, "prefix", prefixText, "error", err)
			continue
		}
		nextHop, ok := bgpPathNextHop(attrs)
		if !ok {
			slog.Warn("BGP route next hop parse failed", "vnf_id", b.vnfID, "prefix", prefixText)
			continue
		}
		b.installRoute(context.Background(), prefixText, nextHop)
	}
}

func bgpNextHop(path *api.Path) (string, error) {
	attrs, err := apiutil.GetNativePathAttributes(path)
	if err != nil {
		return "", err
	}
	if nextHop, ok := bgpPathNextHop(attrs); ok {
		return nextHop, nil
	}
	return "", errors.New("missing BGP next-hop attribute")
}

func (b *bgpInstance) installRoute(ctx context.Context, prefix, nextHop string) {
	if b.routeWriter == nil {
		return
	}
	b.installedMu.Lock()
	previous, exists := b.installed[prefix]
	b.installedMu.Unlock()
	if exists && previous == nextHop {
		return
	}
	if err := b.routeWriter.AddBGPRoute(ctx, b.targetTableID, prefix, nextHop); err != nil {
		slog.Warn("could not install BGP route in VPP",
			"vnf_id", b.vnfID,
			"prefix", prefix,
			"next_hop", nextHop,
			"table", b.targetTableID,
			"error", err,
		)
		return
	}
	b.installedMu.Lock()
	b.installed[prefix] = nextHop
	b.installedMu.Unlock()
}

func (b *bgpInstance) removeRoute(ctx context.Context, prefix string) {
	b.installedMu.Lock()
	_, exists := b.installed[prefix]
	b.installedMu.Unlock()
	if !exists {
		return
	}
	if b.routeWriter != nil {
		if err := b.routeWriter.DeleteBGPRoute(ctx, b.targetTableID, prefix); err != nil {
			slog.Warn("could not remove BGP route from VPP",
				"vnf_id", b.vnfID,
				"prefix", prefix,
				"table", b.targetTableID,
				"error", err,
			)
			return
		}
	}
	b.installedMu.Lock()
	delete(b.installed, prefix)
	b.installedMu.Unlock()
}

func (b *bgpInstance) Close(ctx context.Context) {
	b.closeOnce.Do(func() {
		if b.cancel != nil {
			b.cancel()
		}
		b.wg.Wait()
		if b.server != nil {
			b.server.Stop()
		}
		cleanupCtx := ctx
		if _, hasDeadline := cleanupCtx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			cleanupCtx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
		}
		b.installedMu.Lock()
		prefixes := make([]string, 0, len(b.installed))
		for prefix := range b.installed {
			prefixes = append(prefixes, prefix)
		}
		b.installedMu.Unlock()
		for _, prefix := range prefixes {
			if b.routeWriter != nil {
				if err := b.routeWriter.DeleteBGPRoute(cleanupCtx, b.targetTableID, prefix); err != nil {
					slog.Warn("could not remove BGP route during shutdown", "vnf_id", b.vnfID, "prefix", prefix, "error", err)
					continue
				}
			}
			b.installedMu.Lock()
			delete(b.installed, prefix)
			b.installedMu.Unlock()
		}
		slog.Info("BGP VNF stopped", "vnf_id", b.vnfID)
	})
}

func bgpPrefixes(values []string) ([]*net.IPNet, error) {
	prefixes := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		ip, network, err := net.ParseCIDR(strings.TrimSpace(value))
		if err != nil || ip == nil {
			return nil, fmt.Errorf("invalid BGP prefix %q", value)
		}
		prefixes = append(prefixes, network)
	}
	return prefixes, nil
}

func (m *VNFManager) startBGPTap(ctx context.Context, vnf store.VNF, hostIf string) error {
	if strings.TrimSpace(vnf.Config.Transport) != "tap" {
		return errors.New("BGP transport is not tap")
	}
	localIP, localNet, err := net.ParseCIDR(strings.TrimSpace(vnf.Config.LocalIP))
	if err != nil || localIP == nil {
		return errors.New("BGP local_ip must be an IPv4 or IPv6 CIDR")
	}
	ipv6 := localIP.To4() == nil
	peerIP := net.ParseIP(strings.TrimSpace(vnf.Config.PeerIP))
	if peerIP == nil || (peerIP.To4() == nil) != ipv6 {
		return errors.New("BGP peer_ip must match the local_ip address family")
	}
	if !ipv6 {
		peerIP = peerIP.To4()
	}
	gateway := net.ParseIP(strings.TrimSpace(vnf.Config.PeerGateway))
	if gateway != nil && (gateway.To4() == nil) != ipv6 {
		return errors.New("BGP peer_gateway must match the local_ip address family")
	}
	if !ipv6 && gateway != nil {
		gateway = gateway.To4()
	}
	nextHop := net.ParseIP(strings.TrimSpace(vnf.Config.NextHop))
	if nextHop != nil && (nextHop.To4() == nil) != ipv6 {
		return errors.New("BGP next_hop must match the local_ip address family")
	}
	if !ipv6 && nextHop != nil {
		nextHop = nextHop.To4()
	}
	prefixes, err := bgpPrefixes(vnf.Config.AdvertisePrefixes)
	if err != nil {
		return err
	}
	ttl := vnf.Config.MultihopTTL
	if ttl == 0 {
		ttl = defaultBGPMultihopTTL
	}
	if err := m.stop(ctx, vnf.ID, false); err != nil {
		return err
	}
	if out, err := runIP(ctx, "addr", "replace", strings.TrimSpace(vnf.Config.LocalIP), "dev", hostIf); err != nil {
		return fmt.Errorf("configure BGP tap address: %w: %s", err, out)
	}
	if out, err := runIP(ctx, "link", "set", "up", "dev", hostIf); err != nil {
		return fmt.Errorf("bring BGP tap up: %w: %s", err, out)
	}
	if gateway != nil {
		peerRoute := peerIP.String() + "/32"
		routeArgs := []string{"route", "replace", peerRoute, "via", gateway.String(), "dev", hostIf}
		if ipv6 {
			peerRoute = peerIP.String() + "/128"
			routeArgs = []string{"-6", "route", "replace", peerRoute, "via", gateway.String(), "dev", hostIf}
		}
		if out, err := runIP(ctx, routeArgs...); err != nil {
			return fmt.Errorf("configure BGP peer route: %w: %s", err, out)
		}
	}
	server, err := startBGPServer(ctx, bgpStartConfig{
		VNF: vnf, LocalIP: localIP, LocalNet: localNet, PeerIP: peerIP,
		PeerGateway: gateway, NextHop: nextHop,
		LocalAS: vnf.Config.LocalAS, PeerAS: vnf.Config.PeerAS, RouterID: vnf.Config.RouterID,
		Password: vnf.Config.Password, Passive: vnf.Config.Passive, MultihopTTL: uint8(ttl),
		TargetTableID: vnf.Config.TargetTableID, Advertise: prefixes, RouteWriter: m.routeWriter,
		Neighbor: peerIP.String(), LocalAddress: localIP.String(),
		ListenAddress: []string{localIP.String()}, ListenPort: 179,
		BindToDevice: hostIf,
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.servers == nil {
		m.servers = make(map[int64]*managedVNF)
	}
	m.servers[vnf.ID] = &managedVNF{vnfType: vnf.Type, bgp: server, done: make(chan struct{})}
	m.mu.Unlock()
	return nil
}
