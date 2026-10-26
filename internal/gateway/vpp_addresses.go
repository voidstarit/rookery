package gateway

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
)

// InterfaceAddressRuntime applies IPv4/IPv6 addresses to VPP interfaces.
type InterfaceAddressRuntime interface {
	AddInterfaceAddress(ctx context.Context, name, cidr string) error
	DelInterfaceAddress(ctx context.Context, name, cidr string) error
	EnableDHCPClient(ctx context.Context, name string) error
	DisableDHCPClient(ctx context.Context, name string) error
	InterfaceAddresses(ctx context.Context, name string) ([]string, error)
	SetInterfaceRouteTable(ctx context.Context, swIfIndex uint32, ipv6 bool, tableID uint32) error
	DHCPClientStatus(ctx context.Context, name string) (bool, string, error)
	VPPInterfaces(ctx context.Context) ([]host.VPPInterface, error)
}

func (s *interfaceService) GetVPPInterfaceAddresses(ctx context.Context, request *rookeryv1.GetVPPInterfaceAddressesRequest) (*rookeryv1.VPPInterfaceAddresses, error) {
	name := strings.TrimSpace(request.GetName())
	managed, err := s.store.IsManagedVPPInterface(ctx, name)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not check VPP interface")
	}
	if !managed {
		return nil, status.Error(codes.NotFound, "VPP interface is not managed by Rookery")
	}
	addresses, err := s.store.ListVPPInterfaceAddresses(ctx, name)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load VPP interface addresses")
	}
	dhcpEnabled, err := s.store.VPPInterfaceDHCPClient(ctx, name)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load VPP interface DHCP state")
	}
	return s.vppInterfaceAddressResponse(ctx, name, addresses, dhcpEnabled), nil
}

func (s *interfaceService) vppInterfaceAddressResponse(ctx context.Context, name string, addresses []string, dhcpEnabled bool) *rookeryv1.VPPInterfaceAddresses {
	response := &rookeryv1.VPPInterfaceAddresses{
		Name: name, Addresses: addresses, DhcpClientEnabled: dhcpEnabled,
	}
	if s.addresses == nil {
		response.StatusDetail = "VPP address runtime is not configured"
		return response
	}
	if s.readiness != nil && !s.readiness.Status().VPP.Ready {
		response.StatusDetail = "VPP is not ready"
		return response
	}
	if live, err := s.addresses.InterfaceAddresses(ctx, name); err != nil {
		response.StatusDetail = "Could not read live VPP addresses: " + err.Error()
	} else {
		response.LiveAddresses = live
	}
	if configured, detail, err := s.addresses.DHCPClientStatus(ctx, name); err != nil {
		response.DhcpClientStatus = detail
		if response.StatusDetail == "" {
			response.StatusDetail = "Could not read DHCP client status: " + err.Error()
		}
	} else {
		response.DhcpClientConfigured = configured
		response.DhcpClientStatus = detail
	}
	return response
}

func (s *interfaceService) UpdateVPPInterfaceAddresses(ctx context.Context, request *rookeryv1.UpdateVPPInterfaceAddressesRequest) (*rookeryv1.VPPInterfaceAddresses, error) {
	name := strings.TrimSpace(request.GetName())
	if s.addresses == nil {
		return nil, status.Error(codes.Unavailable, "VPP interface address runtime is not configured")
	}
	if s.readiness != nil && !s.readiness.Status().VPP.Ready {
		return nil, status.Error(codes.Unavailable, "VPP is not ready")
	}
	managed, err := s.store.IsManagedVPPInterface(ctx, name)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not check VPP interface")
	}
	if !managed {
		return nil, status.Error(codes.NotFound, "VPP interface is not managed by Rookery")
	}
	interfaces, err := s.addresses.VPPInterfaces(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "could not query VPP interfaces")
	}
	found := false
	for _, iface := range interfaces {
		if iface.Name == name {
			found = true
			break
		}
	}
	if !found {
		return nil, status.Error(codes.FailedPrecondition, "VPP interface does not exist")
	}
	addresses, err := normalizeVPPAddresses(request.GetAddresses())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if s.resourceMu != nil {
		s.resourceMu.Lock()
		defer s.resourceMu.Unlock()
	}
	if s.runMu != nil {
		s.runMu.Lock()
		defer s.runMu.Unlock()
	}

	previous, err := s.store.ListVPPInterfaceAddresses(ctx, name)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load VPP interface addresses")
	}
	previousDHCP, err := s.store.VPPInterfaceDHCPClient(ctx, name)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load VPP interface DHCP state")
	}
	dhcpEnabled := request.GetDhcpClientEnabled()
	before := make(map[string]bool, len(previous))
	for _, address := range previous {
		before[address] = true
	}
	after := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		after[address] = true
	}
	added := make([]string, 0)
	for _, address := range addresses {
		if !before[address] {
			added = append(added, address)
		}
	}
	removed := make([]string, 0)
	for _, address := range previous {
		if !after[address] {
			removed = append(removed, address)
		}
	}

	for _, address := range added {
		if err := s.addresses.AddInterfaceAddress(ctx, name, address); err != nil {
			for _, undo := range added {
				_ = s.addresses.DelInterfaceAddress(ctx, name, undo)
			}
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("could not add address %s: %v", address, err))
		}
	}
	if dhcpEnabled != previousDHCP {
		var dhcpErr error
		if dhcpEnabled {
			dhcpErr = s.addresses.EnableDHCPClient(ctx, name)
		} else {
			dhcpErr = s.addresses.DisableDHCPClient(ctx, name)
		}
		if dhcpErr != nil {
			for _, undo := range added {
				_ = s.addresses.DelInterfaceAddress(ctx, name, undo)
			}
			return nil, status.Error(codes.Internal, fmt.Sprintf("could not update DHCP client: %v", dhcpErr))
		}
	}
	for _, address := range removed {
		if err := s.addresses.DelInterfaceAddress(ctx, name, address); err != nil {
			for _, undo := range added {
				_ = s.addresses.DelInterfaceAddress(ctx, name, undo)
			}
			for _, restore := range removed {
				_ = s.addresses.AddInterfaceAddress(ctx, name, restore)
			}
			if dhcpEnabled != previousDHCP {
				if previousDHCP {
					_ = s.addresses.EnableDHCPClient(ctx, name)
				} else {
					_ = s.addresses.DisableDHCPClient(ctx, name)
				}
			}
			return nil, status.Error(codes.Internal, fmt.Sprintf("could not remove address %s: %v", address, err))
		}
	}
	if err := s.store.SetVPPInterfaceConfig(ctx, name, addresses, dhcpEnabled); err != nil {
		for _, undo := range added {
			_ = s.addresses.DelInterfaceAddress(ctx, name, undo)
		}
		for _, restore := range removed {
			_ = s.addresses.AddInterfaceAddress(ctx, name, restore)
		}
		if dhcpEnabled != previousDHCP {
			if previousDHCP {
				_ = s.addresses.EnableDHCPClient(ctx, name)
			} else {
				_ = s.addresses.DisableDHCPClient(ctx, name)
			}
		}
		return nil, status.Error(codes.Internal, "could not save VPP interface configuration")
	}
	return s.vppInterfaceAddressResponse(ctx, name, addresses, dhcpEnabled), nil
}

func normalizeVPPAddresses(values []string) ([]string, error) {
	seen := make(map[string]bool, len(values))
	addresses := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("address %q must be an IPv4 or IPv6 CIDR", value)
		}
		if prefix.Bits() == 0 {
			return nil, fmt.Errorf("address %q must have a nonzero prefix length", value)
		}
		if !prefix.Addr().Is4() && !prefix.Addr().Is6() {
			return nil, fmt.Errorf("address %q is not an IPv4 or IPv6 address", value)
		}
		canonical := prefix.String()
		if !seen[canonical] {
			seen[canonical] = true
			addresses = append(addresses, canonical)
		}
	}
	if len(addresses) > 32 {
		return nil, fmt.Errorf("at most 32 addresses are allowed")
	}
	sort.Strings(addresses)
	return addresses, nil
}
