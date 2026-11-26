package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/netutil"
	"github.com/voidstarit/rookery/internal/store"
)

// vnfView is the API-side status view of a hydrated VNF.
type vnfView struct {
	store.VNF
	Status       string `json:"status"`
	StatusDetail string `json:"status_detail,omitempty"`
	LeaseCount   int    `json:"lease_count"`
}

// leaseBelongsToVNF reports whether address falls inside the DHCP range of a
// DHCPv4 or DHCPv6 VNF, returning false for other types or an unparsable config.
func leaseBelongsToVNF(vnf store.VNF, address string) bool {
	switch vnf.Type {
	case store.VNFTypeDHCPv4:
		_, ipnet, err := net.ParseCIDR(vnf.Config.Gateway)
		if err != nil {
			return false
		}
		ip := net.ParseIP(address).To4()
		start := net.ParseIP(vnf.Config.RangeStart).To4()
		end := net.ParseIP(vnf.Config.RangeEnd).To4()
		if ip == nil || start == nil || end == nil || !ipnet.Contains(ip) {
			return false
		}
		value := binaryBigEndianUint32(ip)
		return value >= binaryBigEndianUint32(start) && value <= binaryBigEndianUint32(end)
	case store.VNFTypeDHCPv6:
		prefix, err := netip.ParsePrefix(vnf.Config.Gateway)
		if err != nil {
			return false
		}
		addr, err := netip.ParseAddr(address)
		if err != nil || !addr.Is6() || !prefix.Contains(addr) {
			return false
		}
		start, err := netip.ParseAddr(vnf.Config.RangeStart)
		if err != nil {
			return false
		}
		end, err := netip.ParseAddr(vnf.Config.RangeEnd)
		if err != nil {
			return false
		}
		return addr.Compare(start) >= 0 && addr.Compare(end) <= 0
	default:
		return false
	}
}

// viewVNF builds the API view for a VNF with disabled, running, or degraded
// status and a count of active leases that fall inside its DHCP range.
func (s *Server) viewVNF(ctx context.Context, vnf store.VNF) vnfView {
	view := vnfView{VNF: vnf, Status: "disabled"}
	if vnf.Enabled {
		if s.vnfRunning(ctx, vnf) {
			view.Status = "running"
		} else {
			view.Status = "degraded"
			view.StatusDetail = "runtime is not running; use Restart (recreate the network first if VPP was restarted)"
		}
	}
	if s.vnfs != nil {
		if leases, err := s.vnfs.Leases(ctx, vnf.ID); err == nil {
			for _, lease := range leases {
				if lease.Active && leaseBelongsToVNF(vnf, lease.Address) {
					view.LeaseCount++
				}
			}
		}
	}
	return view
}

// vnfRunning reports whether the VNF's runtime is up, consulting the tracked
// NAT44 flag for NAT44 VNFs and the VNF manager for other types.
func (s *Server) vnfRunning(ctx context.Context, vnf store.VNF) bool {
	if vnf.Type == store.VNFTypeNAT44 {
		s.natMu.Lock()
		defer s.natMu.Unlock()
		return s.natRunning[vnf.ID]
	}
	return s.vnfs != nil && s.vnfs.Running(ctx, vnf)
}

// setNAT44Running records or clears the running flag for a NAT44 VNF under
// natMu, lazily creating the tracking map.
func (s *Server) setNAT44Running(vnfID int64, running bool) {
	s.natMu.Lock()
	defer s.natMu.Unlock()
	if s.natRunning == nil {
		s.natRunning = make(map[int64]bool)
	}
	if running {
		s.natRunning[vnfID] = true
	} else {
		delete(s.natRunning, vnfID)
	}
}

// applyNATGatewayToDHCPConfig rewrites a DHCPv4 config to use the network's NAT
// gateway and service address, rejecting a range outside the network CIDR or
// over a reserved host. It is a no-op for networks without an IPv4 gateway.
func applyNATGatewayToDHCPConfig(network store.Network, config *store.DHCPVNFConfig) error {
	if network.IPv4Gateway == "" {
		return nil
	}
	natIP, natSubnet, err := net.ParseCIDR(network.IPv4Gateway)
	if err != nil {
		return fmt.Errorf("parse network IPv4 gateway: %w", err)
	}
	if natIP.To4() == nil {
		return errors.New("network IPv4 gateway is invalid")
	}
	start := net.ParseIP(config.RangeStart).To4()
	end := net.ParseIP(config.RangeEnd).To4()
	if start == nil || end == nil || !natSubnet.Contains(start) || !natSubnet.Contains(end) {
		return fmt.Errorf("DHCPv4 range must be inside the network NAT subnet %s", natSubnet)
	}
	if network.IPv4CIDR != "" {
		if !netutil.InRange(network.IPv4CIDR, config.RangeStart, config.RangeEnd) {
			return fmt.Errorf("DHCPv4 range %s-%s must stay inside %s and avoid the first %d reserved addresses", config.RangeStart, config.RangeEnd, network.IPv4CIDR, netutil.ReservedHosts)
		}
	}
	bindCIDR, err := nextIPv4CIDR(network.IPv4Gateway)
	if err != nil {
		return err
	}
	bindIP, _, _ := net.ParseCIDR(bindCIDR)
	if bytesCompareIPv4(start, bindIP) <= 0 && bytesCompareIPv4(end, bindIP) >= 0 {
		return fmt.Errorf("DHCPv4 range cannot include the NAT service address %s", bindIP)
	}
	config.Gateway = network.IPv4Gateway
	config.BindIP = bindCIDR
	return nil
}

// persistVNFActivation stores the identity fields of a successfully started VNF.
// When the activation derived a new DHCP gateway and bind address, the typed
// setter writes those and the record is re-read so the caller sees them.
func (s *Server) persistVNFActivation(ctx context.Context, vnf store.VNF, gateway, bindIP string, applyDerived bool) (store.VNF, error) {
	vnf.Enabled = true
	updated, err := s.store.UpdateVNFIdentity(ctx, vnf)
	if err != nil {
		return vnf, err
	}
	if !applyDerived {
		return updated, nil
	}
	if err := s.store.SetDHCPVNFNetworkBindings(ctx, vnf.ID, gateway, bindIP); err != nil {
		updated.Enabled = false
		_, _ = s.store.UpdateVNFIdentity(ctx, updated)
		return vnf, err
	}
	updated, err = s.store.GetVNF(ctx, vnf.ID)
	if err != nil {
		return vnf, err
	}
	return updated, nil
}

// activateVNF validates the VNF's network, cleans up legacy per-network DHCP
// state, and starts the runtime over the effective transport (shared or
// dedicated memif, or tap). Enabled and the tap fields are stored only after the
// runtime starts.
func (s *Server) activateVNF(ctx context.Context, vnf store.VNF) (store.VNF, error) {
	if s.vnfs == nil || s.networks == nil {
		return vnf, errors.New("VNF runtime is not configured")
	}
	if blocked, message := s.vppBlocked(); blocked {
		return vnf, errors.New(message)
	}
	network, err := s.store.GetNetwork(ctx, vnf.NetworkID)
	if err != nil {
		return vnf, fmt.Errorf("get VNF network: %w", err)
	}
	network, err = s.ensureNetworkCIDRs(ctx, network)
	if err != nil {
		return vnf, err
	}
	if err := s.validateVNFNetwork(vnf, network); err != nil {
		return vnf, err
	}
	if err := s.cleanupLegacyNetworkVNF(ctx, network); err != nil {
		return vnf, err
	}
	if vnf.Type == store.VNFTypeDNS {
		return s.activateSharedDNS(ctx, vnf, network)
	}

	transport := s.effectiveVNFTransport(network, vnf)
	if s.memifDHCP && vnf.Type == store.VNFTypeDHCPv4 {
		switch transport {
		case vnfTransportShared:
			return s.activateSharedVNF(ctx, vnf, network)
		case vnfTransportDedicated:
			return s.activateMemifVNF(ctx, vnf, network)
		}
	}
	if vnf.Type == store.VNFTypeBGP && vnf.Config.Transport == "memif" {
		return s.activateMemifVNF(ctx, vnf, network)
	}

	hostIf := fmt.Sprintf("vnf%d", vnf.ID)
	tapSwIfIndex, err := s.networks.CreateTap(ctx, hostIf, vnfTapCIDR(vnf), network.BridgeDomainID, network.RouteTableID)
	if err != nil {
		return vnf, fmt.Errorf("create VNF tap: %w", err)
	}
	if err := s.bindNetworkInterface(ctx, network, tapSwIfIndex); err != nil {
		_ = s.networks.DeleteTap(ctx, tapSwIfIndex)
		return vnf, err
	}
	if err := s.vnfs.Start(ctx, vnf, hostIf); err != nil {
		_ = s.networks.DeleteTap(ctx, tapSwIfIndex)
		return vnf, err
	}

	vnf.TapSwIfIndex = tapSwIfIndex
	vnf.TapHostName = hostIf
	updated, err := s.persistVNFActivation(ctx, vnf, "", "", false)
	if err != nil {
		_ = s.vnfs.Stop(ctx, vnf, false)
		_ = s.networks.DeleteTap(ctx, tapSwIfIndex)
		return vnf, err
	}
	return updated, nil
}

// stopVNF stops the VNF runtime and removes its tap and memif unless the memif
// is shared with the network, optionally dropping the leases and joining the
// per-step errors.
func (s *Server) stopVNF(ctx context.Context, vnf store.VNF, removeLeases bool) error {
	var errs []error
	if s.vnfs != nil {
		if err := s.vnfs.Stop(ctx, vnf, removeLeases); err != nil {
			errs = append(errs, fmt.Errorf("stop VNF runtime: %w", err))
		}
	}
	if s.networks != nil {
		sharedMemif := strings.TrimSpace(vnf.Config.Transport) == vnfTransportShared
		if network, err := s.store.GetNetwork(ctx, vnf.NetworkID); err == nil &&
			network.VNFMemifSocket != "" && vnf.MemifSocket == network.VNFMemifSocket {
			sharedMemif = true
		}
		if !sharedMemif && (vnf.MemifSwIfIndex > 0 || vnf.MemifSocket != "") {
			if err := s.deleteMemif(ctx, vnf.MemifSwIfIndex, vnfMemifDeleteName(vnf)); err != nil {
				errs = append(errs, fmt.Errorf("delete VNF memif: %w", err))
			}
		}
		if err := s.deleteTap(ctx, vnf.TapSwIfIndex, vnf.TapHostName); err != nil {
			errs = append(errs, fmt.Errorf("delete VNF tap: %w", err))
		}
	}
	return errors.Join(errs...)
}

type tapDeleterByName interface {
	DeleteTapByName(ctx context.Context, hostName string) error
}

// deleteTap deletes a VNF tap by host name when the network runtime supports
// it, otherwise by software interface index; it is a no-op when both are unset.
func (s *Server) deleteTap(ctx context.Context, swIfIndex uint32, hostName string) error {
	if swIfIndex == 0 && strings.TrimSpace(hostName) == "" {
		return nil
	}
	if hostName != "" {
		if deleter, ok := s.networks.(tapDeleterByName); ok {
			return deleter.DeleteTapByName(ctx, hostName)
		}
	}
	if swIfIndex == 0 {
		return nil
	}
	return s.networks.DeleteTap(ctx, swIfIndex)
}

// cleanupLegacyNetworkVNF deletes the pre-VNF network DHCP tap, clears the
// stored DHCP fields, and asks the VNF manager to drop legacy state for the
// network.
func (s *Server) cleanupLegacyNetworkVNF(ctx context.Context, network store.Network) error {
	if network.DHCPTapSwIfIndex > 0 || network.DHCPTapHostName != "" {
		if err := s.deleteTap(ctx, network.DHCPTapSwIfIndex, network.DHCPTapHostName); err != nil {
			return fmt.Errorf("delete legacy network DHCP tap: %w", err)
		}
	}
	if network.DHCPEnabled || network.DHCPTapSwIfIndex > 0 {
		if _, err := s.store.SetNetworkDHCP(ctx, network.ID, store.Network{}); err != nil {
			return fmt.Errorf("clear legacy network DHCP: %w", err)
		}
	}
	if s.vnfs != nil {
		s.vnfs.CleanupLegacyNetwork(network.ID)
	}
	return nil
}

// VNFStatus returns the status fields and active lease count used by the
// HTTP VNF views. The grpc-gateway reuses it so both listeners agree.
func (s *Server) VNFStatus(ctx context.Context, vnf store.VNF) (status, detail string, leaseCount int) {
	view := s.viewVNF(ctx, vnf)
	return view.Status, view.StatusDetail, view.LeaseCount
}

// VNFDNSRecords returns the dynamic DNS records registered for a DNS VNF.
func (s *Server) VNFDNSRecords(ctx context.Context, vnf store.VNF) ([]host.DNSRecordView, error) {
	if vnf.Type != store.VNFTypeDNS {
		return nil, errors.New("DNS records are available for DNS VNFs only")
	}
	if s.vnfs == nil {
		return []host.DNSRecordView{}, nil
	}
	return s.vnfs.DNSRecords(ctx, vnf.ID)
}

// VNFBGP returns the BGP session and RIB snapshot for a BGP VNF.
func (s *Server) VNFBGP(ctx context.Context, vnf store.VNF) (host.BGPStatus, error) {
	if s.vnfs == nil {
		return host.BGPStatus{}, errors.New("VNF runtime is not configured")
	}
	if vnf.Type != store.VNFTypeBGP {
		return host.BGPStatus{}, errors.New("BGP status is available for BGP VNFs only")
	}
	return s.vnfs.BGPStatus(ctx, vnf.ID)
}

// VNFSessions returns NAT44 sessions for a VNF, or an empty list when the NAT
// runtime is unavailable.
func (s *Server) VNFSessions(ctx context.Context, vnf store.VNF) ([]string, error) {
	if vnf.Type != store.VNFTypeNAT44 {
		return nil, errors.New("sessions are available for NAT44 VNFs only")
	}
	if s.nat == nil {
		return []string{}, nil
	}
	sessions, err := s.nat.NAT44Sessions(ctx)
	if err != nil {
		return nil, err
	}
	if sessions == nil {
		sessions = []string{}
	}
	return sessions, nil
}

// binaryBigEndianUint32 returns the IPv4 address as a host-order uint32 for
// numeric comparisons.
func binaryBigEndianUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	return uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
}

// bytesCompareIPv4 compares two IPv4 addresses numerically and returns -1, 0,
// or 1.
func bytesCompareIPv4(a, b net.IP) int {
	av := binaryBigEndianUint32(a)
	bv := binaryBigEndianUint32(b)
	switch {
	case av < bv:
		return -1
	case av > bv:
		return 1
	default:
		return 0
	}
}
