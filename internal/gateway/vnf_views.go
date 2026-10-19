package gateway

import (
	"context"
	"net"
	"net/netip"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// VNFRuntime supplies DHCP runtime state.
type VNFRuntime interface {
	Running(vnfID int64) bool
	Leases(ctx context.Context, vnfID int64) ([]store.VNFLease, error)
}

// VNFStatusProvider supplies the legacy API's status and NAT session views.
type VNFStatusProvider interface {
	VNFStatus(ctx context.Context, vnf store.VNF) (status, detail string, leaseCount int)
	VNFSessions(ctx context.Context, vnf store.VNF) ([]string, error)
}

func (s *vnfService) vnfView(ctx context.Context, vnf store.VNF) *rookeryv1.VNF {
	result := vnfToProto(vnf)
	if s.statusProvider != nil {
		statusValue, detail, leaseCount := s.statusProvider.VNFStatus(ctx, vnf)
		result.Status = statusValue
		result.StatusDetail = detail
		result.LeaseCount = int32(leaseCount)
	}
	return result
}

func (s *vnfService) ListVNFLeases(ctx context.Context, request *rookeryv1.ListVNFLeasesRequest) (*rookeryv1.ListVNFLeasesResponse, error) {
	vnf, err := s.store.GetVNF(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrVNFNotFound, "vnf not found")
	}
	response := &rookeryv1.ListVNFLeasesResponse{
		VnfId: vnf.ID, Type: vnf.Type, Leases: []*rookeryv1.VNFLease{},
	}
	if s.runtime == nil {
		return response, nil
	}
	leases, err := s.runtime.Leases(ctx, vnf.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read VNF leases")
	}
	for _, lease := range leases {
		if !leaseBelongsToVNF(vnf, lease.Address) {
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

func (s *vnfService) ListVNFSessions(ctx context.Context, request *rookeryv1.ListVNFSessionsRequest) (*rookeryv1.ListVNFSessionsResponse, error) {
	vnf, err := s.store.GetVNF(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrVNFNotFound, "vnf not found")
	}
	if vnf.Type != store.VNFTypeNAT44 {
		return nil, status.Error(codes.InvalidArgument, "sessions are available for NAT44 VNFs only")
	}
	sessions := []string{}
	if s.statusProvider != nil {
		sessions, err = s.statusProvider.VNFSessions(ctx, vnf)
		if err != nil {
			if vnf.Type != store.VNFTypeNAT44 {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			return nil, status.Error(codes.Internal, "could not read NAT44 sessions")
		}
	}
	return &rookeryv1.ListVNFSessionsResponse{VnfId: vnf.ID, Type: vnf.Type, Sessions: sessions}, nil
}

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

func binaryBigEndianUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	return uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
}
