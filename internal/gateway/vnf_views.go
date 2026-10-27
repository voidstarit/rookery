package gateway

import (
	"context"
	"net"
	"net/netip"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// VNFRuntime supplies DHCP runtime state.
type VNFRuntime interface {
	Leases(ctx context.Context, vnfID int64) ([]store.VNFLease, error)
}

// VNFStatusProvider supplies the legacy API's status, NAT session, and BGP
// runtime views.
type VNFStatusProvider interface {
	VNFStatus(ctx context.Context, vnf store.VNF) (status, detail string, leaseCount int)
	VNFSessions(ctx context.Context, vnf store.VNF) ([]string, error)
	VNFBGP(ctx context.Context, vnf store.VNF) (host.BGPStatus, error)
	VNFDNSRecords(ctx context.Context, vnf store.VNF) ([]host.DNSRecordView, error)
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

// GetVNFBGP returns the BGP session state, advertised prefixes, and received
// prefixes for a BGP VNF.
func (s *vnfService) GetVNFBGP(ctx context.Context, request *rookeryv1.GetVNFBGPRequest) (*rookeryv1.VNFBGPStatus, error) {
	vnf, err := s.store.GetVNF(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrVNFNotFound, "vnf not found")
	}
	if vnf.Type != store.VNFTypeBGP {
		return nil, status.Error(codes.InvalidArgument, "BGP status is available for BGP VNFs only")
	}
	result := &rookeryv1.VNFBGPStatus{VnfId: vnf.ID, Type: vnf.Type}
	if s.statusProvider == nil {
		return result, nil
	}
	snapshot, err := s.statusProvider.VNFBGP(ctx, vnf)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read BGP status: "+err.Error())
	}
	result.Running = snapshot.Running
	result.Established = snapshot.Established
	result.SessionState = snapshot.SessionState
	result.PeerAddress = snapshot.PeerAddress
	result.PeerAs = snapshot.PeerAS
	result.LocalAs = snapshot.LocalAS
	result.Advertised = append([]string(nil), snapshot.Advertised...)
	for _, prefix := range snapshot.Received {
		result.Received = append(result.Received, &rookeryv1.VNFBGPReceivedPrefix{
			Prefix:  prefix.Prefix,
			NextHop: prefix.NextHop,
			AsPath:  append([]uint32(nil), prefix.ASPath...),
		})
	}
	return result, nil
}

// ListVNFDNSRecords returns configured and lease-derived DNS records for a DNS
// VNF. Static records remain available when the daemon is unreachable; the
// dynamic runtime error is reported separately.
func (s *vnfService) ListVNFDNSRecords(ctx context.Context, request *rookeryv1.ListVNFDNSRecordsRequest) (*rookeryv1.ListVNFDNSRecordsResponse, error) {
	vnf, err := s.store.GetVNF(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrVNFNotFound, "vnf not found")
	}
	if vnf.Type != store.VNFTypeDNS {
		return nil, status.Error(codes.InvalidArgument, "DNS records are available for DNS VNFs only")
	}
	response := &rookeryv1.ListVNFDNSRecordsResponse{VnfId: vnf.ID, Type: vnf.Type, Records: []*rookeryv1.VNFDNSRecord{}}
	for _, record := range vnf.Config.DNSRecords {
		response.Records = append(response.Records, &rookeryv1.VNFDNSRecord{
			Name: record.Name, Type: record.Type, Value: record.Value, Ttl: record.TTL, Source: "static",
		})
	}
	if s.statusProvider != nil {
		dynamic, err := s.statusProvider.VNFDNSRecords(ctx, vnf)
		if err != nil {
			response.DynamicError = err.Error()
		} else {
			for _, record := range dynamic {
				response.Records = append(response.Records, &rookeryv1.VNFDNSRecord{
					Name: record.Name, Type: record.Type, Value: record.Value, Ttl: record.TTL,
					Source: "dhcp", SourceVnfId: record.SourceVNFID,
				})
			}
		}
	}
	sort.Slice(response.Records, func(i, j int) bool {
		left, right := response.Records[i], response.Records[j]
		if left.GetName() != right.GetName() {
			return left.GetName() < right.GetName()
		}
		if left.GetType() != right.GetType() {
			return left.GetType() < right.GetType()
		}
		return left.GetValue() < right.GetValue()
	})
	return response, nil
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
