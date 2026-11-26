package gateway

import (
	"context"
	"net"
	"net/netip"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// dhcpVNFToProto converts a typed DHCP parent row to its API representation.
func dhcpVNFToProto(vnf store.DHCPVNF) *rookeryv1.DHCPVNF {
	return &rookeryv1.DHCPVNF{
		Id: vnf.ID, Name: vnf.Name, NetworkId: vnf.NetworkID, NetworkName: vnf.NetworkName,
		Family: vnf.Family, Enabled: vnf.Enabled,
		Gateway: vnf.Config.Gateway, RangeStart: vnf.Config.RangeStart,
		RangeEnd: vnf.Config.RangeEnd, LeaseTime: vnf.Config.LeaseTime,
		Dns: vnf.Config.DNS, BindIp: vnf.Config.BindIP, Transport: vnf.Config.Transport,
		DnsVnfId: vnf.Config.DNSVNFID, DnsDomain: vnf.Config.DNSDomain, DnsTtl: vnf.Config.DNSTTL,
		StaticAssignmentCount: uint32(vnf.StaticAssignmentCount),
		OptionCount:           uint32(vnf.OptionCount),
		TapSwIfIndex:          vnf.TapSwIfIndex, TapHostName: vnf.TapHostName,
		CreatedAt: timestamp(vnf.CreatedAt), UpdatedAt: timestamp(vnf.UpdatedAt),
	}
}

// dhcpVNFView builds a typed DHCP parent with live status and lease count.
func (s *vnfService) dhcpVNFView(ctx context.Context, vnf store.DHCPVNF) *rookeryv1.DHCPVNF {
	result := dhcpVNFToProto(vnf)
	statusValue, detail, leaseCount := s.applyVNFStatus(ctx, dhcpStatusRecord(vnf))
	result.Status = statusValue
	result.StatusDetail = detail
	result.LeaseCount = uint32(leaseCount)
	return result
}

// ListDHCPVNFs returns DHCP VNFs, optionally filtered by network and family.
func (s *vnfService) ListDHCPVNFs(ctx context.Context, request *rookeryv1.ListDHCPVNFsRequest) (*rookeryv1.ListDHCPVNFsResponse, error) {
	vnfs, err := s.store.ListDHCPVNFs(ctx, request.NetworkId, request.GetFamily())
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list DHCP VNFs")
	}
	response := &rookeryv1.ListDHCPVNFsResponse{Vnfs: make([]*rookeryv1.DHCPVNF, 0, len(vnfs))}
	for _, vnf := range vnfs {
		response.Vnfs = append(response.Vnfs, s.dhcpVNFView(ctx, vnf))
	}
	return response, nil
}

// GetDHCPVNF returns one DHCP VNF.
func (s *vnfService) GetDHCPVNF(ctx context.Context, request *rookeryv1.GetDHCPVNFRequest) (*rookeryv1.DHCPVNF, error) {
	vnf, err := s.store.GetDHCPVNF(ctx, request.GetId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.dhcpVNFView(ctx, vnf), nil
}

// CreateDHCPVNF delegates to the lifecycle writer and returns the stored parent.
func (s *vnfService) CreateDHCPVNF(ctx context.Context, request *rookeryv1.CreateDHCPVNFRequest) (*rookeryv1.DHCPVNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	created, err := s.writer.CreateDHCPVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetDHCPVNF(ctx, created.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.dhcpVNFView(ctx, vnf), nil
}

// UpdateDHCPVNF delegates to the lifecycle writer and returns the stored parent.
func (s *vnfService) UpdateDHCPVNF(ctx context.Context, request *rookeryv1.UpdateDHCPVNFRequest) (*rookeryv1.DHCPVNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	updated, err := s.writer.UpdateDHCPVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetDHCPVNF(ctx, updated.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.dhcpVNFView(ctx, vnf), nil
}

// DeleteDHCPVNF delegates removal to the lifecycle writer.
func (s *vnfService) DeleteDHCPVNF(ctx context.Context, request *rookeryv1.DeleteDHCPVNFRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	if err := s.writer.DeleteDHCPVNF(ctx, request); err != nil {
		return nil, mapVNFWriteError(err)
	}
	return &emptypb.Empty{}, nil
}

// RestartDHCPVNF restarts a DHCP VNF and returns the refreshed parent.
func (s *vnfService) RestartDHCPVNF(ctx context.Context, request *rookeryv1.RestartDHCPVNFRequest) (*rookeryv1.DHCPVNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	restarted, err := s.writer.RestartDHCPVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetDHCPVNF(ctx, restarted.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.dhcpVNFView(ctx, vnf), nil
}

// ListDHCPVNFLeases returns the leases inside the VNF's configured range.
func (s *vnfService) ListDHCPVNFLeases(ctx context.Context, request *rookeryv1.ListDHCPVNFLeasesRequest) (*rookeryv1.ListDHCPVNFLeasesResponse, error) {
	vnf, err := s.store.GetDHCPVNF(ctx, request.GetId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	response := &rookeryv1.ListDHCPVNFLeasesResponse{VnfId: vnf.ID, Leases: []*rookeryv1.VNFLease{}}
	if s.runtime == nil {
		return response, nil
	}
	leases, err := s.runtime.Leases(ctx, vnf.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read VNF leases")
	}
	for _, lease := range leases {
		if !leaseBelongsToDHCPVNF(vnf, lease.Address) {
			continue
		}
		item := &rookeryv1.VNFLease{
			ClientId: lease.ClientID, Address: lease.Address, Hostname: lease.Hostname, Active: lease.Active,
		}
		if !lease.ExpiresAt.IsZero() {
			item.ExpiresAt = timestamp(lease.ExpiresAt)
		}
		response.Leases = append(response.Leases, item)
	}
	return response, nil
}

// leaseBelongsToDHCPVNF reports whether an address is inside a DHCP VNF's range.
func leaseBelongsToDHCPVNF(vnf store.DHCPVNF, address string) bool {
	switch vnf.Family {
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
		value := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
		return value >= binaryBigEndianIPv4(start) && value <= binaryBigEndianIPv4(end)
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

// binaryBigEndianIPv4 returns the numeric value of an IPv4 address.
func binaryBigEndianIPv4(ip net.IP) uint32 {
	ip4 := ip.To4()
	return uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
}

// dhcpStaticAssignmentToProto converts one child row.
func dhcpStaticAssignmentToProto(item store.DHCPStaticAssignment) *rookeryv1.DHCPStaticAssignment {
	return &rookeryv1.DHCPStaticAssignment{Id: item.ID, VnfId: item.VNFID, Mac: item.MAC, Ip: item.IP}
}

// dhcpOptionToProto converts one child row.
func dhcpOptionToProto(item store.DHCPOption) *rookeryv1.DHCPOption {
	return &rookeryv1.DHCPOption{
		Id: item.ID, VnfId: item.VNFID, Code: uint32(item.Code), Format: item.Format, Value: item.Value,
	}
}

// ListDHCPStaticAssignments returns the static assignments of a DHCPv4 VNF.
func (s *vnfService) ListDHCPStaticAssignments(ctx context.Context, request *rookeryv1.ListDHCPStaticAssignmentsRequest) (*rookeryv1.ListDHCPStaticAssignmentsResponse, error) {
	items, err := s.store.ListDHCPStaticAssignments(ctx, request.GetVnfId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	response := &rookeryv1.ListDHCPStaticAssignmentsResponse{
		Assignments: make([]*rookeryv1.DHCPStaticAssignment, 0, len(items)),
	}
	for _, item := range items {
		response.Assignments = append(response.Assignments, dhcpStaticAssignmentToProto(item))
	}
	return response, nil
}

// GetDHCPStaticAssignment returns one static assignment.
func (s *vnfService) GetDHCPStaticAssignment(ctx context.Context, request *rookeryv1.GetDHCPStaticAssignmentRequest) (*rookeryv1.DHCPStaticAssignment, error) {
	item, err := s.store.GetDHCPStaticAssignment(ctx, request.GetVnfId(), request.GetAssignmentId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	return dhcpStaticAssignmentToProto(item), nil
}

// CreateDHCPStaticAssignment delegates to the lifecycle writer.
func (s *vnfService) CreateDHCPStaticAssignment(ctx context.Context, request *rookeryv1.CreateDHCPStaticAssignmentRequest) (*rookeryv1.DHCPStaticAssignment, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.CreateDHCPStaticAssignment(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return dhcpStaticAssignmentToProto(item), nil
}

// UpdateDHCPStaticAssignment delegates to the lifecycle writer.
func (s *vnfService) UpdateDHCPStaticAssignment(ctx context.Context, request *rookeryv1.UpdateDHCPStaticAssignmentRequest) (*rookeryv1.DHCPStaticAssignment, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.UpdateDHCPStaticAssignment(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return dhcpStaticAssignmentToProto(item), nil
}

// DeleteDHCPStaticAssignment delegates removal to the lifecycle writer.
func (s *vnfService) DeleteDHCPStaticAssignment(ctx context.Context, request *rookeryv1.DeleteDHCPStaticAssignmentRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	if err := s.writer.DeleteDHCPStaticAssignment(ctx, request); err != nil {
		return nil, mapVNFWriteError(err)
	}
	return &emptypb.Empty{}, nil
}

// ListDHCPOptions returns the extra options of a DHCP VNF.
func (s *vnfService) ListDHCPOptions(ctx context.Context, request *rookeryv1.ListDHCPOptionsRequest) (*rookeryv1.ListDHCPOptionsResponse, error) {
	items, err := s.store.ListDHCPOptions(ctx, request.GetVnfId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	response := &rookeryv1.ListDHCPOptionsResponse{Options: make([]*rookeryv1.DHCPOption, 0, len(items))}
	for _, item := range items {
		response.Options = append(response.Options, dhcpOptionToProto(item))
	}
	return response, nil
}

// GetDHCPOption returns one DHCP option.
func (s *vnfService) GetDHCPOption(ctx context.Context, request *rookeryv1.GetDHCPOptionRequest) (*rookeryv1.DHCPOption, error) {
	item, err := s.store.GetDHCPOption(ctx, request.GetVnfId(), request.GetOptionId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	return dhcpOptionToProto(item), nil
}

// CreateDHCPOption delegates to the lifecycle writer.
func (s *vnfService) CreateDHCPOption(ctx context.Context, request *rookeryv1.CreateDHCPOptionRequest) (*rookeryv1.DHCPOption, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.CreateDHCPOption(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return dhcpOptionToProto(item), nil
}

// UpdateDHCPOption delegates to the lifecycle writer.
func (s *vnfService) UpdateDHCPOption(ctx context.Context, request *rookeryv1.UpdateDHCPOptionRequest) (*rookeryv1.DHCPOption, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.UpdateDHCPOption(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return dhcpOptionToProto(item), nil
}

// DeleteDHCPOption delegates removal to the lifecycle writer.
func (s *vnfService) DeleteDHCPOption(ctx context.Context, request *rookeryv1.DeleteDHCPOptionRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	if err := s.writer.DeleteDHCPOption(ctx, request); err != nil {
		return nil, mapVNFWriteError(err)
	}
	return &emptypb.Empty{}, nil
}
