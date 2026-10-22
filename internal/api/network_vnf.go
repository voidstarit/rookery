package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/netutil"
	"github.com/voidstarit/rookery/internal/store"
)

const (
	vnfTransportShared    = "shared"
	vnfTransportDedicated = "dedicated"
	vnfTransportTap       = "tap"
)

func networkRouteTableID(networkID int64) uint32 {
	return netutil.NetworkRouteTableBase + uint32(networkID)
}

func (s *Server) networkVNFMemifSocket(networkID int64) string {
	dir := strings.TrimSpace(s.memifDir)
	if dir == "" {
		dir = "/run/rookery/memif"
	}
	return filepath.Join(dir, fmt.Sprintf("net-%d.sock", networkID))
}

// effectiveVNFTransport resolves the per-VNF transport override against the
// network default. Empty values select the network default (shared).
func (s *Server) effectiveVNFTransport(network store.Network, vnf store.VNF) string {
	override := strings.ToLower(strings.TrimSpace(vnf.Config.Transport))
	switch override {
	case vnfTransportShared, vnfTransportDedicated, vnfTransportTap:
		return override
	case "memif":
		return vnfTransportDedicated
	}
	switch strings.ToLower(strings.TrimSpace(network.VNFTransport)) {
	case vnfTransportDedicated:
		return vnfTransportDedicated
	case vnfTransportTap:
		return vnfTransportTap
	default:
		return vnfTransportShared
	}
}

// ensureNetworkCIDRs backfills the required IPv4/IPv6 network prefixes for
// networks created before CIDRs were mandatory. New networks always have them.
func (s *Server) ensureNetworkCIDRs(ctx context.Context, network store.Network) (store.Network, error) {
	if network.IPv4CIDR != "" && network.IPv6CIDR != "" {
		return network, nil
	}
	ipv4CIDR := strings.TrimSpace(network.IPv4CIDR)
	ipv6CIDR := strings.TrimSpace(network.IPv6CIDR)
	if ipv4CIDR == "" && network.IPv4Gateway != "" {
		if prefix, _, err := netutil.NormalizeNetworkCIDRs(network.IPv4Gateway, "fd00:20::/64"); err == nil {
			ipv4CIDR = prefix
		}
	}
	if ipv4CIDR == "" || ipv6CIDR == "" {
		vnfs, err := s.store.ListVNFsByNetwork(ctx, network.ID)
		if err != nil {
			return network, err
		}
		for _, vnf := range vnfs {
			if ipv4CIDR == "" && vnf.Type != store.VNFTypeDHCPv6 {
				candidate := strings.TrimSpace(vnf.Config.Gateway)
				if candidate == "" {
					candidate = strings.TrimSpace(vnf.Config.LocalIP)
				}
				if candidate != "" {
					if prefix, _, err := netutil.NormalizeNetworkCIDRs(candidate, "fd00:20::/64"); err == nil {
						ipv4CIDR = prefix
					}
				}
			}
			if ipv6CIDR == "" && vnf.Type == store.VNFTypeDHCPv6 {
				candidate := strings.TrimSpace(vnf.Config.Gateway)
				if candidate != "" {
					if _, prefix, err := netutil.NormalizeNetworkCIDRs("10.0.0.0/16", candidate); err == nil {
						ipv6CIDR = prefix
					}
				}
			}
		}
	}
	if ipv4CIDR == "" {
		ipv4CIDR = "10.0.0.0/16"
	}
	if ipv6CIDR == "" {
		ipv6CIDR = "fd00:20::/64"
	}
	ipv4CIDR, ipv6CIDR, err := netutil.NormalizeNetworkCIDRs(ipv4CIDR, ipv6CIDR)
	if err != nil {
		return network, err
	}
	updated, err := s.store.SetNetworkCIDRs(ctx, network.ID, ipv4CIDR, ipv6CIDR)
	if err != nil {
		return network, err
	}
	s.logger.Info().
		Str("network", updated.Name).
		Str("ipv4_cidr", updated.IPv4CIDR).
		Str("ipv6_cidr", updated.IPv6CIDR).
		Msg("network CIDRs initialized")
	return updated, nil
}

// validateVNFNetwork enforces that DHCP ranges and static assignments stay
// inside the network prefix and outside the first five reserved addresses.
func (s *Server) validateVNFNetwork(vnf store.VNF, network store.Network) error {
	cidr := network.IPv4CIDR
	if vnf.Type == store.VNFTypeDHCPv6 {
		cidr = network.IPv6CIDR
	}
	if cidr == "" {
		return nil
	}
	switch vnf.Type {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
		if !netutil.InRange(cidr, vnf.Config.RangeStart, vnf.Config.RangeEnd) {
			return fmt.Errorf("DHCP range %s-%s must be inside %s and avoid the first %d reserved addresses", vnf.Config.RangeStart, vnf.Config.RangeEnd, cidr, netutil.ReservedHosts)
		}
		for _, assignment := range vnf.Config.StaticAssignments {
			if !netutil.Contains(cidr, assignment.IP) || netutil.IsReserved(cidr, assignment.IP) {
				return fmt.Errorf("static assignment %s (%s) must be inside %s and avoid the first %d reserved addresses", assignment.MAC, assignment.IP, cidr, netutil.ReservedHosts)
			}
		}
	case store.VNFTypeNAT44:
		for _, rule := range vnf.Config.DNATRules {
			if !netutil.Contains(cidr, rule.LocalIP) || netutil.IsReserved(cidr, rule.LocalIP) {
				return fmt.Errorf("DNAT local address %s must be inside %s and avoid the first %d reserved addresses", rule.LocalIP, cidr, netutil.ReservedHosts)
			}
		}
	}
	return nil
}

// ensureNetworkRouteTable creates the per-network IPv4 and IPv6 VRF tables,
// stores the table ID, and registers the network-owned route table records so
// they appear in the Route tables UI.
func (s *Server) ensureNetworkRouteTable(ctx context.Context, network store.Network) (store.Network, error) {
	if s.networks == nil {
		return network, nil
	}
	tableID := network.RouteTableID
	if tableID == 0 {
		tableID = networkRouteTableID(network.ID)
	}
	name := "network-" + network.Name
	if err := s.networks.EnsureRouteTable(ctx, tableID, false, name); err != nil {
		return network, fmt.Errorf("ensure network IPv4 VRF %d: %w", tableID, err)
	}
	if err := s.networks.EnsureRouteTable(ctx, tableID, true, name); err != nil {
		return network, fmt.Errorf("ensure network IPv6 VRF %d: %w", tableID, err)
	}
	if network.RouteTableID != tableID {
		updated, err := s.store.SetNetworkRouteTable(ctx, network.ID, tableID)
		if err != nil {
			return network, fmt.Errorf("save network VRF table: %w", err)
		}
		network = updated
	}
	if _, err := s.store.UpsertNetworkRouteTable(ctx, network.ID, name, tableID, false); err != nil {
		return network, fmt.Errorf("register network IPv4 route table: %w", err)
	}
	if _, err := s.store.UpsertNetworkRouteTable(ctx, network.ID, name, tableID, true); err != nil {
		return network, fmt.Errorf("register network IPv6 route table: %w", err)
	}
	return network, nil
}

// bindNetworkInterface places an interface in the network's IPv4 and IPv6 VRF
// tables.
func (s *Server) bindNetworkInterface(ctx context.Context, network store.Network, swIfIndex uint32) error {
	if s.networks == nil || swIfIndex == 0 || network.RouteTableID == 0 {
		return nil
	}
	for _, ipv6 := range []bool{false, true} {
		if current, err := s.networks.InterfaceRouteTable(ctx, swIfIndex, ipv6); err == nil && current == network.RouteTableID {
			continue
		}
		if err := s.networks.SetInterfaceRouteTable(ctx, swIfIndex, ipv6, network.RouteTableID); err != nil {
			family := "IPv4"
			if ipv6 {
				family = "IPv6"
			}
			return fmt.Errorf("bind interface %d to %s VRF %d: %w", swIfIndex, family, network.RouteTableID, err)
		}
	}
	return nil
}

// bindNetworkBVI moves the network BVI into its VRF. VPP refuses to change an
// interface's table while it has addresses, so the BVI address is removed
// first and re-added after the binding.
func (s *Server) bindNetworkBVI(ctx context.Context, network store.Network, bviName string, bviIndex uint32, gatewayCIDR string) error {
	if s.networks == nil || bviIndex == 0 || network.RouteTableID == 0 {
		return nil
	}
	current, err := s.networks.InterfaceRouteTable(ctx, bviIndex, false)
	if err == nil && current == network.RouteTableID {
		if gatewayCIDR != "" && s.nat != nil {
			return s.nat.AddInterfaceIPv4(ctx, bviName, gatewayCIDR)
		}
		return nil
	}
	if gatewayCIDR != "" {
		_ = s.networks.DelInterfaceIPv4(ctx, bviName, gatewayCIDR)
	}
	if err := s.bindNetworkInterface(ctx, network, bviIndex); err != nil {
		return err
	}
	if gatewayCIDR != "" && s.nat != nil {
		if err := s.nat.AddInterfaceIPv4(ctx, bviName, gatewayCIDR); err != nil {
			return fmt.Errorf("assign network gateway %s: %w", gatewayCIDR, err)
		}
	}
	return nil
}

// ensureNetworkVNFResources ensures the network has its gateway BVI and its
// shared VNF memif. gatewayCIDR is used when the network has no stored gateway
// yet (it normally comes from the network CIDR).
func (s *Server) ensureNetworkVNFResources(ctx context.Context, network store.Network, gatewayCIDR string) (store.Network, error) {
	if s.networks == nil {
		return network, nil
	}
	var err error
	network, err = s.ensureNetworkCIDRs(ctx, network)
	if err != nil {
		return network, err
	}
	network, err = s.ensureNetworkRouteTable(ctx, network)
	if err != nil {
		return network, err
	}

	if network.IPv4CIDR != "" {
		gatewayCIDR, err = netutil.HostCIDR(network.IPv4CIDR, 1)
		if err != nil {
			return network, err
		}
	} else if strings.TrimSpace(gatewayCIDR) == "" {
		gatewayCIDR = strings.TrimSpace(network.IPv4Gateway)
	}
	if gatewayCIDR != "" && s.nat != nil {
		// EnsureBVI and AddInterfaceIPv4 are idempotent. Always call them so a
		// VPP restart recreates a BVI that the database still remembers.
		bviName, bviIndex, err := s.nat.EnsureBVI(ctx, network.ID, network.BridgeDomainID)
		if err != nil {
			return network, fmt.Errorf("ensure network BVI: %w", err)
		}
		// Bind the BVI to the network VRF before assigning its address;
		// otherwise overlapping BVI addresses in other networks conflict in
		// VPP's default table.
		if err := s.bindNetworkBVI(ctx, network, bviName, bviIndex, gatewayCIDR); err != nil {
			return network, err
		}
		if network.BVIHostName != bviName || network.BVISwIfIndex != bviIndex || network.IPv4Gateway != gatewayCIDR {
			updated, err := s.store.SetNetworkBVI(ctx, network.ID, bviName, bviIndex, gatewayCIDR)
			if err != nil {
				return network, fmt.Errorf("save network BVI: %w", err)
			}
			network = updated
		}
	}

	// Dedicated memif and tap networks also use the network BVI; only the
	// shared network-wide memif is skipped.
	if strings.EqualFold(strings.TrimSpace(network.VNFTransport), vnfTransportTap) ||
		strings.EqualFold(strings.TrimSpace(network.VNFTransport), vnfTransportDedicated) {
		return network, nil
	}
	return s.ensureNetworkSharedMemif(ctx, network)
}

// ensureNetworkSharedMemif creates or finds the per-network shared VNF memif.
// It is idempotent and can be used by VNF types that always need the shared
// stack, regardless of the network's default transport.
func (s *Server) ensureNetworkSharedMemif(ctx context.Context, network store.Network) (store.Network, error) {
	hostName := strings.TrimSpace(network.VNFMemifHostName)
	if hostName == "" {
		hostName = fmt.Sprintf("net%d-vnf", network.ID)
	}
	socketPath := strings.TrimSpace(network.VNFMemifSocket)
	if socketPath == "" {
		socketPath = s.networkVNFMemifSocket(network.ID)
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return network, fmt.Errorf("create shared memif socket directory: %w", err)
	}
	swIfIndex, err := s.networks.EnsureMemif(
		ctx, hostName, socketPath, host.NetworkVNFMemifID(network.ID), host.NetworkVNFMemifMAC(network.ID), network.BridgeDomainID,
	)
	if err != nil {
		return network, fmt.Errorf("ensure network VNF memif: %w", err)
	}
	if err := s.bindNetworkInterface(ctx, network, swIfIndex); err != nil {
		return network, err
	}
	if network.VNFMemifSocket != socketPath || network.VNFMemifSwIfIndex != swIfIndex || network.VNFMemifHostName != hostName {
		updated, err := s.store.SetNetworkVNFMemif(ctx, network.ID, hostName, socketPath, swIfIndex)
		if err != nil {
			return network, fmt.Errorf("save network VNF memif: %w", err)
		}
		network = updated
	}
	return network, nil
}

// PrepareNetworkVNFs creates the shared VNF network resources for a new
// network. The gateway BVI is derived from the network CIDR.
func (s *Server) PrepareNetworkVNFs(ctx context.Context, network store.Network) (store.Network, error) {
	return s.ensureNetworkVNFResources(ctx, network, "")
}

// DeleteNetworkRouteTables removes the network VRF records and their VPP
// tables. It runs after interfaces/BVI are gone.
func (s *Server) DeleteNetworkRouteTables(ctx context.Context, network store.Network) error {
	if s.networks != nil && network.RouteTableID != 0 {
		_ = s.networks.DeleteRouteTable(ctx, network.RouteTableID, false)
		_ = s.networks.DeleteRouteTable(ctx, network.RouteTableID, true)
	}
	if s.store != nil {
		return s.store.DeleteRouteTablesByNetwork(ctx, network.ID)
	}
	return nil
}

// CleanupNetworkVNFs removes the network's shared VNF memif. The BVI is
// removed by the normal network deletion path.
func (s *Server) CleanupNetworkVNFs(ctx context.Context, network store.Network) error {
	if s.networks == nil || (network.VNFMemifSwIfIndex == 0 && strings.TrimSpace(network.VNFMemifSocket) == "") {
		return nil
	}
	return s.deleteMemif(ctx, network.VNFMemifSwIfIndex, network.VNFMemifHostName)
}

// activateSharedDNS starts a DNS VNF on the network's shared VNF memif. The
// server listens on the network service address (.2) and answers local records
// plus optional upstream queries.
func (s *Server) activateSharedDNS(ctx context.Context, vnf store.VNF, network store.Network) (store.VNF, error) {
	if s.vnfs == nil || s.networks == nil {
		return vnf, fmt.Errorf("VNF runtime is not configured")
	}
	var err error
	network, err = s.ensureNetworkCIDRs(ctx, network)
	if err != nil {
		return vnf, err
	}
	gateway := ""
	if network.IPv4CIDR != "" {
		gateway, err = netutil.HostCIDR(network.IPv4CIDR, 1)
		if err != nil {
			return vnf, err
		}
	}
	serviceCIDR, err := netutil.HostCIDR(network.IPv4CIDR, 2)
	if err != nil {
		return vnf, err
	}
	network, err = s.ensureNetworkVNFResources(ctx, network, gateway)
	if err != nil {
		return vnf, err
	}
	network, err = s.ensureNetworkSharedMemif(ctx, network)
	if err != nil {
		return vnf, err
	}

	// Drop resources from a previous dedicated memif/tap generation.
	if vnf.MemifSocket != "" && vnf.MemifSocket != network.VNFMemifSocket {
		_ = s.deleteMemif(ctx, vnf.MemifSwIfIndex, vnfMemifDeleteName(vnf))
	}
	if vnf.TapSwIfIndex != 0 || vnf.TapHostName != "" {
		_ = s.deleteTap(ctx, vnf.TapSwIfIndex, vnf.TapHostName)
	}

	if err := s.vnfs.StartShared(ctx, vnf, network.VNFMemifSocket, host.NetworkVNFMemifID(network.ID), serviceCIDR); err != nil {
		return vnf, err
	}
	vnf.Enabled = true
	vnf.MemifSocket = network.VNFMemifSocket
	vnf.MemifSwIfIndex = network.VNFMemifSwIfIndex
	vnf.MemifHostName = network.VNFMemifHostName
	vnf.TapSwIfIndex = 0
	vnf.TapHostName = ""
	updated, err := s.store.UpdateVNF(ctx, vnf.ID, vnf)
	if err != nil {
		_ = s.vnfs.Stop(ctx, vnf.ID, false)
		return vnf, err
	}
	s.logger.Info().
		Str("vnf", updated.Name).
		Str("type", updated.Type).
		Str("network", network.Name).
		Str("memif", network.VNFMemifSocket).
		Str("address", serviceCIDR).
		Int("records", len(updated.Config.DNSRecords)).
		Bool("forward", updated.Config.DNSForward).
		Msg("DNS VNF activated on shared memif")
	return updated, nil
}

// activateSharedVNF starts a DHCPv4 VNF on its network's shared memif. The
// network stack owns .2/prefix by default; the BVI owns .1 and is the DHCP
// router option.
func (s *Server) activateSharedVNF(ctx context.Context, vnf store.VNF, network store.Network) (store.VNF, error) {
	if s.vnfs == nil || s.networks == nil {
		return vnf, fmt.Errorf("VNF runtime is not configured")
	}
	var err error
	network, err = s.ensureNetworkCIDRs(ctx, network)
	if err != nil {
		return vnf, err
	}
	gateway, err := netutil.HostCIDR(network.IPv4CIDR, 1)
	if err != nil {
		return vnf, err
	}
	serviceCIDR, err := netutil.HostCIDR(network.IPv4CIDR, 2)
	if err != nil {
		return vnf, err
	}
	if err := s.validateVNFNetwork(vnf, network); err != nil {
		return vnf, err
	}
	network, err = s.ensureNetworkVNFResources(ctx, network, gateway)
	if err != nil {
		return vnf, err
	}

	// Drop resources from a previous dedicated memif/tap generation.
	if vnf.MemifSocket != "" && vnf.MemifSocket != network.VNFMemifSocket {
		_ = s.deleteMemif(ctx, vnf.MemifSwIfIndex, vnfMemifDeleteName(vnf))
	}
	if vnf.TapSwIfIndex != 0 || vnf.TapHostName != "" {
		_ = s.deleteTap(ctx, vnf.TapSwIfIndex, vnf.TapHostName)
	}

	vnf.Config.Gateway = gateway
	vnf.Config.BindIP = serviceCIDR
	// The daemon needs the memif interface ID for the handshake, not the VPP
	// sw_if_index stored on the network for cleanup/counters.
	if err := s.vnfs.StartShared(ctx, vnf, network.VNFMemifSocket, host.NetworkVNFMemifID(network.ID), serviceCIDR); err != nil {
		return vnf, err
	}

	vnf.Enabled = true
	vnf.MemifSocket = network.VNFMemifSocket
	vnf.MemifSwIfIndex = network.VNFMemifSwIfIndex
	vnf.MemifHostName = network.VNFMemifHostName
	vnf.TapSwIfIndex = 0
	vnf.TapHostName = ""
	updated, err := s.store.UpdateVNF(ctx, vnf.ID, vnf)
	if err != nil {
		_ = s.vnfs.Stop(ctx, vnf.ID, false)
		return vnf, err
	}
	s.logger.Info().
		Str("vnf", updated.Name).
		Str("type", updated.Type).
		Str("network", network.Name).
		Str("memif", network.VNFMemifSocket).
		Str("address", serviceCIDR).
		Msg("VNF activated on shared memif")
	return updated, nil
}
