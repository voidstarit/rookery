package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/voidstarit/rookery/internal/netutil"
	"github.com/voidstarit/rookery/internal/store"
)

// HostTapError carries the HTTP status the gateway should return for a failed
// host-tap operation.
type HostTapError struct {
	Status  int
	Message string
	Err     error
}

func (e *HostTapError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "host tap operation failed"
}

func (e *HostTapError) Unwrap() error { return e.Err }

// HostTapStatus returns the HTTP status and message for the gateway.
func (e *HostTapError) HostTapStatus() (int, string) {
	if e.Status >= 500 {
		if e.Err != nil {
			return e.Status, e.Err.Error()
		}
		if e.Message != "" {
			return e.Status, e.Message
		}
		return e.Status, "host tap operation failed"
	}
	return e.Status, e.Error()
}

func hostTapInputError(message string) error {
	return &HostTapError{Status: http.StatusBadRequest, Message: message}
}

func hostTapConflictError(message string) error {
	return &HostTapError{Status: http.StatusConflict, Message: message}
}

func hostTapUnavailableError(message string) error {
	return &HostTapError{Status: http.StatusServiceUnavailable, Message: message}
}

func hostTapInternalError(err error) error {
	return &HostTapError{Status: http.StatusInternalServerError, Err: err}
}

// CreateNetworkHostTap creates a persistent VPP tap between a network bridge
// domain and the Linux host. The record is written before VPP provisioning so
// a partial failure can be rolled back and a crash leaves something to
// reconcile.
func (s *Server) CreateNetworkHostTap(ctx context.Context, network store.Network, name, ipv4Address string) (store.NetworkHostTap, error) {
	if s.networks == nil {
		return store.NetworkHostTap{}, hostTapUnavailableError("VPP network runtime is not configured")
	}
	if blocked, message := s.vppBlocked(); blocked {
		return store.NetworkHostTap{}, hostTapUnavailableError(message)
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("hostnet%d", network.ID)
	}
	switch {
	case len(name) > 15:
		return store.NetworkHostTap{}, hostTapInputError("name must be at most 15 characters; it becomes the Linux interface name")
	case !vmNamePattern.MatchString(name):
		return store.NetworkHostTap{}, hostTapInputError("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}
	if len(network.IPv4CIDR) == 0 {
		var err error
		network, err = s.ensureNetworkCIDRs(ctx, network)
		if err != nil {
			return store.NetworkHostTap{}, hostTapInternalError(err)
		}
	}
	address, err := normalizeHostTapIPv4(network, ipv4Address)
	if err != nil {
		return store.NetworkHostTap{}, hostTapInputError(err.Error())
	}
	if err := s.hostTapAddressAvailable(ctx, network, address, 0); err != nil {
		return store.NetworkHostTap{}, err
	}
	if existing, err := s.store.GetNetworkHostTapByName(ctx, name); err == nil {
		return store.NetworkHostTap{}, hostTapConflictError(fmt.Sprintf("host interface %s is already used by network %s", name, existing.NetworkName))
	} else if !errors.Is(err, store.ErrNetworkHostTapNotFound) {
		return store.NetworkHostTap{}, hostTapInternalError(err)
	}
	if _, err := net.InterfaceByName(name); err == nil {
		return store.NetworkHostTap{}, hostTapConflictError(fmt.Sprintf("Linux interface %s already exists", name))
	}

	network, err = s.ensureNetworkRouteTable(ctx, network)
	if err != nil {
		return store.NetworkHostTap{}, hostTapInternalError(err)
	}
	if err := s.networks.EnsureBridgeDomain(ctx, network.BridgeDomainID, network.Name); err != nil {
		return store.NetworkHostTap{}, hostTapInternalError(fmt.Errorf("ensure network bridge domain: %w", err))
	}

	created, err := s.store.CreateNetworkHostTap(ctx, store.NetworkHostTap{
		NetworkID: network.ID, Name: name, IPv4Address: address,
	})
	if err != nil {
		if errors.Is(err, store.ErrNetworkHostTapNameTaken) {
			return store.NetworkHostTap{}, hostTapConflictError("a host tap with that name already exists")
		}
		return store.NetworkHostTap{}, hostTapInternalError(err)
	}

	rollback := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		_ = s.networks.DeleteHostTapByName(cleanupCtx, created.Name)
		_ = s.store.DeleteNetworkHostTap(cleanupCtx, created.ID)
	}

	swIfIndex, err := s.networks.EnsureHostTap(ctx, created.Name, network.BridgeDomainID, address)
	if err != nil {
		rollback()
		return store.NetworkHostTap{}, hostTapInternalError(fmt.Errorf("create host tap: %w", err))
	}
	if err := s.bindNetworkInterface(ctx, network, swIfIndex); err != nil {
		rollback()
		return store.NetworkHostTap{}, hostTapInternalError(err)
	}
	if swIfIndex != created.SwIfIndex {
		created, err = s.store.SetNetworkHostTapRuntime(ctx, created.ID, swIfIndex)
		if err != nil {
			rollback()
			return store.NetworkHostTap{}, hostTapInternalError(err)
		}
	}
	s.logger.Info().
		Str("network", network.Name).
		Str("interface", created.Name).
		Str("ipv4_address", created.IPv4Address).
		Uint32("sw_if_index", created.SwIfIndex).
		Msg("host tap created")
	return created, nil
}

// DeleteNetworkHostTap removes the VPP tap and its persisted record.
func (s *Server) DeleteNetworkHostTap(ctx context.Context, tap store.NetworkHostTap) error {
	if s.networks == nil {
		return hostTapUnavailableError("VPP network runtime is not configured")
	}
	if blocked, message := s.vppBlocked(); blocked {
		return hostTapUnavailableError(message)
	}
	if err := s.networks.DeleteHostTapByName(ctx, tap.Name); err != nil {
		return hostTapInternalError(fmt.Errorf("delete host tap: %w", err))
	}
	if err := s.store.DeleteNetworkHostTap(ctx, tap.ID); err != nil {
		if errors.Is(err, store.ErrNetworkHostTapNotFound) {
			return &HostTapError{Status: http.StatusNotFound, Message: "host tap not found"}
		}
		return hostTapInternalError(err)
	}
	_ = s.store.DeleteVPPInterfaceAddresses(ctx, tap.Name)
	s.logger.Info().
		Str("network", tap.NetworkName).
		Str("interface", tap.Name).
		Msg("host tap deleted")
	return nil
}

// CleanupNetworkHostTaps removes every host tap attached to a network. It is
// called before the network bridge domain is deleted.
func (s *Server) CleanupNetworkHostTaps(ctx context.Context, network store.Network) error {
	taps, err := s.store.ListNetworkHostTapsByNetwork(ctx, network.ID)
	if err != nil {
		return err
	}
	for _, tap := range taps {
		if err := s.DeleteNetworkHostTap(ctx, tap); err != nil {
			return err
		}
	}
	return nil
}

// ReconcileNetworkHostTaps recreates persisted host taps after an API or VPP
// restart and refreshes their sw_if_index.
func (s *Server) ReconcileNetworkHostTaps(ctx context.Context) error {
	if s.networks == nil {
		return nil
	}
	if blocked, _ := s.vppBlocked(); blocked {
		return nil
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	taps, err := s.store.ListNetworkHostTaps(ctx, nil)
	if err != nil {
		return fmt.Errorf("list host taps for reconciliation: %w", err)
	}
	var errs []error
	for _, tap := range taps {
		network, err := s.store.GetNetwork(ctx, tap.NetworkID)
		if err != nil {
			errs = append(errs, fmt.Errorf("host tap %s: %w", tap.Name, err))
			continue
		}
		network, err = s.ensureNetworkCIDRs(ctx, network)
		if err != nil {
			errs = append(errs, fmt.Errorf("host tap %s: %w", tap.Name, err))
			continue
		}
		network, err = s.ensureNetworkRouteTable(ctx, network)
		if err != nil {
			errs = append(errs, fmt.Errorf("host tap %s: %w", tap.Name, err))
			continue
		}
		if err := s.networks.EnsureBridgeDomain(ctx, network.BridgeDomainID, network.Name); err != nil {
			errs = append(errs, fmt.Errorf("host tap %s: %w", tap.Name, err))
			continue
		}
		swIfIndex, err := s.networks.EnsureHostTap(ctx, tap.Name, network.BridgeDomainID, tap.IPv4Address)
		if err != nil {
			errs = append(errs, fmt.Errorf("host tap %s: %w", tap.Name, err))
			continue
		}
		if err := s.bindNetworkInterface(ctx, network, swIfIndex); err != nil {
			errs = append(errs, fmt.Errorf("host tap %s: %w", tap.Name, err))
			continue
		}
		if swIfIndex != tap.SwIfIndex {
			updated, err := s.store.SetNetworkHostTapRuntime(ctx, tap.ID, swIfIndex)
			if err != nil {
				errs = append(errs, fmt.Errorf("host tap %s: update runtime metadata: %w", tap.Name, err))
				continue
			}
			tap = updated
		}
		s.logger.Info().
			Str("network", network.Name).
			Str("interface", tap.Name).
			Str("ipv4_address", tap.IPv4Address).
			Uint32("sw_if_index", tap.SwIfIndex).
			Msg("host tap restored")
	}
	return errors.Join(errs...)
}

// normalizeHostTapIPv4 accepts a bare address or a CIDR. The prefix length
// must match the network prefix so the Linux connected route covers the whole
// network.
func normalizeHostTapIPv4(network store.Network, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	networkPrefix, err := netip.ParsePrefix(strings.TrimSpace(network.IPv4CIDR))
	if err != nil {
		return "", fmt.Errorf("network has no valid IPv4 CIDR")
	}
	networkPrefix = networkPrefix.Masked()
	bits := networkPrefix.Bits()

	var addr netip.Addr
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return "", errors.New("ipv4_address must be a valid IPv4 address or CIDR")
		}
		if prefix.Bits() != bits {
			return "", fmt.Errorf("ipv4_address prefix must be /%d to match the network", bits)
		}
		addr = prefix.Addr()
	} else {
		addr, err = netip.ParseAddr(value)
		if err != nil || !addr.Is4() || addr.Is4In6() {
			return "", errors.New("ipv4_address must be a valid IPv4 address")
		}
	}
	if !addr.Is4() || addr.Is4In6() {
		return "", errors.New("ipv4_address must be a valid IPv4 address")
	}
	if !networkPrefix.Contains(addr) {
		return "", fmt.Errorf("ipv4_address %s is outside network %s", addr, networkPrefix)
	}
	if addr == networkPrefix.Addr() {
		return "", errors.New("ipv4_address cannot be the network address")
	}
	if last := lastIPv4(networkPrefix); addr == last {
		return "", errors.New("ipv4_address cannot be the broadcast address")
	}
	if netutil.IsReserved(network.IPv4CIDR, addr.String()) {
		return "", fmt.Errorf("ipv4_address %s is reserved for Rookery", addr)
	}
	return netip.PrefixFrom(addr, bits).String(), nil
}

func lastIPv4(prefix netip.Prefix) netip.Addr {
	addr := prefix.Masked().Addr().As4()
	bits := prefix.Bits()
	for i := bits; i < 32; i++ {
		addr[i/8] |= 1 << (7 - uint(i%8))
	}
	return netip.AddrFrom4(addr)
}

// hostTapAddressAvailable rejects addresses already used by another host tap
// or handed out by the network's DHCP server.
func (s *Server) hostTapAddressAvailable(ctx context.Context, network store.Network, address string, ignoreID int64) error {
	if address == "" {
		return nil
	}
	addr, err := netutil.ParseHost(address)
	if err != nil {
		return hostTapInputError("ipv4_address must be a valid IPv4 address")
	}
	taps, err := s.store.ListNetworkHostTapsByNetwork(ctx, network.ID)
	if err != nil {
		return hostTapInternalError(err)
	}
	for _, tap := range taps {
		if tap.ID == ignoreID || tap.IPv4Address == "" {
			continue
		}
		other, err := netutil.ParseHost(tap.IPv4Address)
		if err == nil && other == addr {
			return hostTapConflictError(fmt.Sprintf("ipv4_address %s is already used by host tap %s", addr, tap.Name))
		}
	}
	vnfs, err := s.store.ListVNFsByNetwork(ctx, network.ID)
	if err != nil {
		return hostTapInternalError(err)
	}
	for _, vnf := range vnfs {
		if vnf.Type != store.VNFTypeDHCPv4 {
			continue
		}
		start, startErr := netutil.ParseHost(vnf.Config.RangeStart + "/32")
		end, endErr := netutil.ParseHost(vnf.Config.RangeEnd + "/32")
		if startErr == nil && endErr == nil {
			if start.Compare(addr) <= 0 && addr.Compare(end) <= 0 {
				return hostTapConflictError(fmt.Sprintf("ipv4_address %s is inside the DHCP range of %s", addr, vnf.Name))
			}
		}
		for _, assignment := range vnf.Config.StaticAssignments {
			assigned, err := netutil.ParseHost(assignment.IP + "/32")
			if err == nil && assigned == addr {
				return hostTapConflictError(fmt.Sprintf("ipv4_address %s is assigned to %s by %s", addr, assignment.MAC, vnf.Name))
			}
		}
	}
	return nil
}
