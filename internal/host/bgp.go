package host

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	api "github.com/osrg/gobgp/v3/api"
	"github.com/osrg/gobgp/v3/pkg/apiutil"
	"github.com/osrg/gobgp/v3/pkg/packet/bgp"
	"github.com/osrg/gobgp/v3/pkg/server"
	"github.com/rs/zerolog/log"

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
	localIP       net.IP
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	installedMu   sync.Mutex
	installed     map[string]string
	advertisedMu  sync.Mutex
	advertisedIDs [][]byte
	advertised    []*net.IPNet
	nextHop       net.IP
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
		log.Warn().Int64("vnf_id", b.vnfID).Err(err).Msg("could not list BGP peers")
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
			log.Warn().Int64("vnf_id", b.vnfID).Str("afi", afi.String()).Err(err).Msg("could not list BGP paths")
		}
	}
	return status
}

// startBGPServer validates the BGP configuration, starts an embedded GoBGP
// server and peer, and advertises the configured prefixes. It returns the
// running instance with route and peer-state watchers, or an error after
// stopping GoBGP when validation or startup fails.
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
		localIP:       append(net.IP(nil), cfg.LocalIP...),
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

	log.Info().
		Int64("vnf_id", cfg.VNF.ID).
		Str("transport", cfg.VNF.Config.Transport).
		Str("family", bgpAFI(cfg.LocalIP).String()).
		Uint32("local_as", cfg.LocalAS).
		Str("peer", cfg.Neighbor).
		Str("peer_address", cfg.PeerIP.String()).
		Uint32("peer_as", cfg.PeerAS).
		Uint32("target_table", cfg.TargetTableID).
		Msg("BGP VNF started")
	return instance, nil
}

// advertise adds each configured prefix to the global RIB with its next hop
// (LocalIP unless overridden), using MP_REACH_NLRI or NEXT_HOP according to the
// family. It is a no-op when no prefixes are configured.
func (b *bgpInstance) advertise(ctx context.Context, cfg bgpStartConfig) error {
	nextHop := cfg.LocalIP
	if cfg.NextHop != nil {
		nextHop = cfg.NextHop
	}
	ids, err := b.advertisePaths(ctx, cfg.Advertise, nextHop)
	if err != nil {
		return err
	}
	b.advertisedMu.Lock()
	b.advertisedIDs = ids
	b.advertised = append([]*net.IPNet(nil), cfg.Advertise...)
	b.nextHop = append(net.IP(nil), nextHop...)
	b.advertisedMu.Unlock()
	return nil
}

// advertisePaths adds prefixes to the global RIB and returns the UUIDs GoBGP
// assigned to the locally originated paths, including any paths added before a
// failure so the caller can withdraw a partial update.
func (b *bgpInstance) advertisePaths(ctx context.Context, prefixes []*net.IPNet, nextHop net.IP) ([][]byte, error) {
	ids := make([][]byte, 0, len(prefixes))
	if len(prefixes) == 0 {
		return ids, nil
	}
	nextHopText := nextHop.String()
	for _, network := range prefixes {
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
			return ids, fmt.Errorf("build advertised BGP path %s: %w", network, err)
		}
		response, err := b.server.AddPath(ctx, &api.AddPathRequest{
			TableType: api.TableType_GLOBAL,
			Path:      path,
		})
		if err != nil {
			return ids, fmt.Errorf("advertise BGP prefix %s: %w", network, err)
		}
		ids = append(ids, response.GetUuid())
		log.Trace().
			Int64("vnf_id", b.vnfID).
			Str("prefix", network.String()).
			Str("next_hop", nextHopText).
			Msg("advertised BGP prefix")
	}
	return ids, nil
}

// withdrawPaths removes locally originated paths by the UUIDs GoBGP returned
// when they were advertised.
func (b *bgpInstance) withdrawPaths(ctx context.Context, ids [][]byte) error {
	for _, id := range ids {
		if err := b.server.DeletePath(ctx, &api.DeletePathRequest{
			TableType: api.TableType_GLOBAL,
			Uuid:      id,
		}); err != nil {
			return fmt.Errorf("withdraw advertised BGP prefix: %w", err)
		}
	}
	return nil
}

// reload withdraws the currently advertised prefix set and advertises the
// proposed one. The GoBGP server and its peer session stay up. A validation
// failure changes nothing; an advertisement failure restores the previous set.
func (b *bgpInstance) reload(ctx context.Context, vnf store.VNF) error {
	prefixes, nextHop, err := b.reloadConfig(vnf)
	if err != nil {
		return err
	}
	b.advertisedMu.Lock()
	defer b.advertisedMu.Unlock()

	if err := b.withdrawPaths(ctx, b.advertisedIDs); err != nil {
		return err
	}
	ids, err := b.advertisePaths(ctx, prefixes, nextHop)
	if err != nil {
		// Drop any paths this attempt added and restore the previous set.
		_ = b.withdrawPaths(ctx, ids)
		restored, restoreErr := b.advertisePaths(ctx, b.advertised, b.nextHop)
		if restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore advertised BGP prefixes: %w", restoreErr))
		}
		b.advertisedIDs = restored
		return err
	}
	b.advertisedIDs = ids
	b.advertised = append([]*net.IPNet(nil), prefixes...)
	b.nextHop = append(net.IP(nil), nextHop...)
	log.Info().
		Int64("vnf_id", b.vnfID).
		Int("prefixes", len(prefixes)).
		Msg("BGP advertised prefixes reloaded")
	return nil
}

// reloadConfig validates a proposed BGP config against the running session.
// Reload only changes the advertised path set, so the local address must stay
// the same; a parent address or transport change uses a stop/start instead.
func (b *bgpInstance) reloadConfig(vnf store.VNF) ([]*net.IPNet, net.IP, error) {
	if vnf.Type != store.VNFTypeBGP {
		return nil, nil, fmt.Errorf("BGP runtime only supports BGP VNFs, got %q", vnf.Type)
	}
	if b.localIP == nil {
		return nil, nil, errors.New("BGP runtime has no local address")
	}
	localIP, _, err := net.ParseCIDR(strings.TrimSpace(vnf.Config.LocalIP))
	if err != nil || localIP == nil {
		return nil, nil, errors.New("BGP local_ip must be an IPv4 or IPv6 CIDR")
	}
	if bgpIsIPv6(localIP) != bgpIsIPv6(b.localIP) || !localIP.Equal(b.localIP) {
		return nil, nil, errors.New("BGP local_ip changed; restart the VNF instead of reloading")
	}
	nextHop := b.localIP
	if strings.TrimSpace(vnf.Config.NextHop) != "" {
		nextHop = net.ParseIP(strings.TrimSpace(vnf.Config.NextHop))
		if nextHop == nil {
			return nil, nil, errors.New("BGP next_hop must be an IPv4 or IPv6 address")
		}
		if bgpIsIPv6(nextHop) != bgpIsIPv6(b.localIP) {
			return nil, nil, errors.New("BGP next_hop must match the local_ip address family")
		}
		if !bgpIsIPv6(nextHop) {
			nextHop = nextHop.To4()
		}
	}
	prefixes, err := bgpPrefixes(vnf.Config.AdvertisePrefixes)
	if err != nil {
		return nil, nil, err
	}
	for _, network := range prefixes {
		if bgpIsIPv6(network.IP) != bgpIsIPv6(b.localIP) {
			return nil, nil, errors.New("BGP advertised prefixes must match the local address family")
		}
	}
	return prefixes, nextHop, nil
}

// watchRoutes streams the best paths from GoBGP's global table into handlePaths
// until ctx is canceled or the watcher fails. It always releases the
// instance's wait group slot.
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
		log.Warn().Int64("vnf_id", b.vnfID).Err(err).Msg("BGP route watcher stopped")
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
		log.Info().
			Int64("vnf_id", b.vnfID).
			Str("neighbor", event.Peer.Conf.GetNeighborAddress()).
			Str("session_state", state.SessionState.String()).
			Str("admin_state", state.AdminState.String()).
			Msg("BGP session state changed")
	})
	if err != nil && ctx.Err() == nil {
		log.Warn().Int64("vnf_id", b.vnfID).Err(err).Msg("BGP peer state watcher stopped")
	}
}

// handlePaths filters a GoBGP table event down to IPv4/IPv6 unicast paths
// learned from the configured neighbor, then installs or withdraws the
// corresponding route. Paths that cannot be parsed or that belong to other
// peers are skipped.
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
			log.Warn().Int64("vnf_id", b.vnfID).Interface("nlri", path.Nlri).Msg("BGP route NLRI parse failed")
			continue
		}
		if path.IsWithdraw {
			b.removeRoute(context.Background(), prefixText)
			continue
		}
		attrs, err := apiutil.GetNativePathAttributes(path)
		if err != nil {
			log.Warn().Int64("vnf_id", b.vnfID).Str("prefix", prefixText).Err(err).Msg("BGP route attribute parse failed")
			continue
		}
		nextHop, ok := bgpPathNextHop(attrs)
		if !ok {
			log.Warn().Int64("vnf_id", b.vnfID).Str("prefix", prefixText).Msg("BGP route next hop parse failed")
			continue
		}
		b.installRoute(context.Background(), prefixText, nextHop)
	}
}

// bgpNextHop extracts the next-hop attribute from a BGP path and returns its
// string form. It fails when the attributes cannot be decoded or no next hop is
// present.
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

// installRoute writes prefix with nextHop into the target VPP table through the
// route writer, skipping the write when the same prefix already maps to that
// next hop. The bookkeeping map is updated only after a successful write;
// failures are logged rather than returned.
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
		log.Warn().
			Int64("vnf_id", b.vnfID).
			Str("prefix", prefix).
			Str("next_hop", nextHop).
			Uint32("table", b.targetTableID).
			Err(err).
			Msg("could not install BGP route in VPP")
		return
	}
	log.Trace().
		Int64("vnf_id", b.vnfID).
		Str("prefix", prefix).
		Str("next_hop", nextHop).
		Uint32("table", b.targetTableID).
		Msg("installed BGP route in VPP")
	b.installedMu.Lock()
	b.installed[prefix] = nextHop
	b.installedMu.Unlock()
}

// removeRoute deletes prefix from the target VPP table if this instance
// installed it, then drops it from the bookkeeping map. Write failures are
// logged and leave the entry in place so shutdown can retry.
func (b *bgpInstance) removeRoute(ctx context.Context, prefix string) {
	b.installedMu.Lock()
	_, exists := b.installed[prefix]
	b.installedMu.Unlock()
	if !exists {
		return
	}
	if b.routeWriter != nil {
		if err := b.routeWriter.DeleteBGPRoute(ctx, b.targetTableID, prefix); err != nil {
			log.Warn().
				Int64("vnf_id", b.vnfID).
				Str("prefix", prefix).
				Uint32("table", b.targetTableID).
				Err(err).
				Msg("could not remove BGP route from VPP")
			return
		}
	}
	log.Trace().
		Int64("vnf_id", b.vnfID).
		Str("prefix", prefix).
		Uint32("table", b.targetTableID).
		Msg("removed BGP route from VPP")
	b.installedMu.Lock()
	delete(b.installed, prefix)
	b.installedMu.Unlock()
}

// Close stops the watchers and GoBGP server once and removes every route this
// instance installed from VPP. Cleanup uses a 10-second timeout when ctx has no
// deadline; failed deletions are logged and left behind.
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
					log.Warn().
						Int64("vnf_id", b.vnfID).
						Str("prefix", prefix).
						Err(err).
						Msg("could not remove BGP route during shutdown")
					continue
				}
			}
			log.Trace().
				Int64("vnf_id", b.vnfID).
				Str("prefix", prefix).
				Uint32("table", b.targetTableID).
				Msg("removed BGP route from VPP during shutdown")
			b.installedMu.Lock()
			delete(b.installed, prefix)
			b.installedMu.Unlock()
		}
		log.Info().Int64("vnf_id", b.vnfID).Msg("BGP VNF stopped")
	})
}

// bgpPrefixes parses each trimmed value as a CIDR and returns the network
// forms, rejecting any value that is not a valid prefix.
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

// startBGPTap configures the tap with the VNF's local address and peer route,
// then starts a tap-transport GoBGP instance registered with the manager. It
// rejects non-tap transports and malformed or mixed-family addresses.
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
	m.servers[vnf.ID] = &managedVNF{vnfType: vnf.Type, bgp: server, hostIf: hostIf, done: make(chan struct{})}
	m.mu.Unlock()
	return nil
}
