package gateway

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// bgpVNFToProto converts a typed BGP parent row to its API representation. The
// TCP MD5 password is never returned.
func bgpVNFToProto(vnf store.BGPVNF) *rookeryv1.BGPVNF {
	return &rookeryv1.BGPVNF{
		Id: vnf.ID, Name: vnf.Name, NetworkId: vnf.NetworkID, NetworkName: vnf.NetworkName,
		Enabled: vnf.Enabled, Transport: vnf.Config.Transport,
		LocalAs: vnf.Config.LocalAS, RouterId: vnf.Config.RouterID,
		PeerAs: vnf.Config.PeerAS, PeerIp: vnf.Config.PeerIP,
		LocalIp: vnf.Config.LocalIP, PeerGateway: vnf.Config.PeerGateway,
		MultihopTtl: vnf.Config.MultihopTTL, Passive: vnf.Config.Passive,
		TargetTableId: vnf.Config.TargetTableID, NextHop: vnf.Config.NextHop,
		AdvertisedPrefixCount: uint32(vnf.AdvertisedPrefixCount),
		TapSwIfIndex:          vnf.TapSwIfIndex, TapHostName: vnf.TapHostName,
		CreatedAt: timestamp(vnf.CreatedAt), UpdatedAt: timestamp(vnf.UpdatedAt),
	}
}

// bgpVNFView builds a typed BGP parent with live status.
func (s *vnfService) bgpVNFView(ctx context.Context, vnf store.BGPVNF) *rookeryv1.BGPVNF {
	result := bgpVNFToProto(vnf)
	statusValue, detail, _ := s.applyVNFStatus(ctx, identityStatusRecord(vnf.ID, vnf.Name, vnf.NetworkID, store.VNFTypeBGP, vnf.Enabled))
	result.Status = statusValue
	result.StatusDetail = detail
	return result
}

// ListBGPVNFs returns BGP VNFs, optionally filtered by network.
func (s *vnfService) ListBGPVNFs(ctx context.Context, request *rookeryv1.ListBGPVNFsRequest) (*rookeryv1.ListBGPVNFsResponse, error) {
	vnfs, err := s.store.ListBGPVNFs(ctx, request.NetworkId)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list BGP VNFs")
	}
	response := &rookeryv1.ListBGPVNFsResponse{Vnfs: make([]*rookeryv1.BGPVNF, 0, len(vnfs))}
	for _, vnf := range vnfs {
		response.Vnfs = append(response.Vnfs, s.bgpVNFView(ctx, vnf))
	}
	return response, nil
}

// GetBGPVNF returns one BGP VNF.
func (s *vnfService) GetBGPVNF(ctx context.Context, request *rookeryv1.GetBGPVNFRequest) (*rookeryv1.BGPVNF, error) {
	vnf, err := s.store.GetBGPVNF(ctx, request.GetId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.bgpVNFView(ctx, vnf), nil
}

// CreateBGPVNF delegates to the lifecycle writer.
func (s *vnfService) CreateBGPVNF(ctx context.Context, request *rookeryv1.CreateBGPVNFRequest) (*rookeryv1.BGPVNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	created, err := s.writer.CreateBGPVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetBGPVNF(ctx, created.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.bgpVNFView(ctx, vnf), nil
}

// UpdateBGPVNF delegates to the lifecycle writer.
func (s *vnfService) UpdateBGPVNF(ctx context.Context, request *rookeryv1.UpdateBGPVNFRequest) (*rookeryv1.BGPVNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	updated, err := s.writer.UpdateBGPVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetBGPVNF(ctx, updated.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.bgpVNFView(ctx, vnf), nil
}

// DeleteBGPVNF delegates removal to the lifecycle writer.
func (s *vnfService) DeleteBGPVNF(ctx context.Context, request *rookeryv1.DeleteBGPVNFRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	if err := s.writer.DeleteBGPVNF(ctx, request); err != nil {
		return nil, mapVNFWriteError(err)
	}
	return &emptypb.Empty{}, nil
}

// RestartBGPVNF restarts a BGP VNF and returns the refreshed parent.
func (s *vnfService) RestartBGPVNF(ctx context.Context, request *rookeryv1.RestartBGPVNFRequest) (*rookeryv1.BGPVNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	restarted, err := s.writer.RestartBGPVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetBGPVNF(ctx, restarted.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.bgpVNFView(ctx, vnf), nil
}

// GetBGPVNFStatus returns the live BGP session state, advertised prefixes, and
// received prefixes.
func (s *vnfService) GetBGPVNFStatus(ctx context.Context, request *rookeryv1.GetBGPVNFStatusRequest) (*rookeryv1.BGPStatus, error) {
	vnf, err := s.store.GetBGPVNF(ctx, request.GetId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	result := &rookeryv1.BGPStatus{VnfId: vnf.ID}
	if s.statusProvider == nil {
		return result, nil
	}
	snapshot, err := s.statusProvider.VNFBGP(ctx, identityStatusRecord(vnf.ID, vnf.Name, vnf.NetworkID, store.VNFTypeBGP, vnf.Enabled))
	if err != nil {
		s.logger.Error().Err(err).Int64("vnf_id", vnf.ID).Msg("could not read BGP status")
		return nil, status.Error(codes.Internal, "could not read BGP status")
	}
	result.Running = snapshot.Running
	result.Established = snapshot.Established
	result.SessionState = snapshot.SessionState
	result.PeerAddress = snapshot.PeerAddress
	result.PeerAs = snapshot.PeerAS
	result.LocalAs = snapshot.LocalAS
	result.Advertised = append([]string(nil), snapshot.Advertised...)
	for _, prefix := range snapshot.Received {
		result.Received = append(result.Received, &rookeryv1.BGPReceivedPrefix{
			Prefix:  prefix.Prefix,
			NextHop: prefix.NextHop,
			AsPath:  append([]uint32(nil), prefix.ASPath...),
		})
	}
	return result, nil
}

// bgpPrefixToProto converts one advertised prefix.
func bgpPrefixToProto(item store.BGPPrefix) *rookeryv1.BGPPrefix {
	return &rookeryv1.BGPPrefix{Id: item.ID, VnfId: item.VNFID, Prefix: item.Prefix}
}

// ListBGPVNFAdvertisedPrefixes returns the advertised prefixes of a BGP VNF.
func (s *vnfService) ListBGPVNFAdvertisedPrefixes(ctx context.Context, request *rookeryv1.ListBGPVNFAdvertisedPrefixesRequest) (*rookeryv1.ListBGPVNFAdvertisedPrefixesResponse, error) {
	items, err := s.store.ListBGPVNFAdvertisedPrefixes(ctx, request.GetVnfId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	response := &rookeryv1.ListBGPVNFAdvertisedPrefixesResponse{
		Prefixes: make([]*rookeryv1.BGPPrefix, 0, len(items)),
	}
	for _, item := range items {
		response.Prefixes = append(response.Prefixes, bgpPrefixToProto(item))
	}
	return response, nil
}

// GetBGPVNFAdvertisedPrefix returns one advertised prefix.
func (s *vnfService) GetBGPVNFAdvertisedPrefix(ctx context.Context, request *rookeryv1.GetBGPVNFAdvertisedPrefixRequest) (*rookeryv1.BGPPrefix, error) {
	item, err := s.store.GetBGPVNFAdvertisedPrefix(ctx, request.GetVnfId(), request.GetPrefixId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	return bgpPrefixToProto(item), nil
}

// CreateBGPVNFAdvertisedPrefix delegates to the lifecycle writer.
func (s *vnfService) CreateBGPVNFAdvertisedPrefix(ctx context.Context, request *rookeryv1.CreateBGPVNFAdvertisedPrefixRequest) (*rookeryv1.BGPPrefix, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.CreateBGPVNFAdvertisedPrefix(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return bgpPrefixToProto(item), nil
}

// UpdateBGPVNFAdvertisedPrefix delegates to the lifecycle writer.
func (s *vnfService) UpdateBGPVNFAdvertisedPrefix(ctx context.Context, request *rookeryv1.UpdateBGPVNFAdvertisedPrefixRequest) (*rookeryv1.BGPPrefix, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.UpdateBGPVNFAdvertisedPrefix(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return bgpPrefixToProto(item), nil
}

// DeleteBGPVNFAdvertisedPrefix delegates removal to the lifecycle writer.
func (s *vnfService) DeleteBGPVNFAdvertisedPrefix(ctx context.Context, request *rookeryv1.DeleteBGPVNFAdvertisedPrefixRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	if err := s.writer.DeleteBGPVNFAdvertisedPrefix(ctx, request); err != nil {
		return nil, mapVNFWriteError(err)
	}
	return &emptypb.Empty{}, nil
}
