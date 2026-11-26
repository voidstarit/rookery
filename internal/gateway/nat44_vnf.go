package gateway

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// nat44VNFToProto converts a typed NAT44 parent row to its API representation.
func nat44VNFToProto(vnf store.NAT44VNF) *rookeryv1.NAT44VNF {
	return &rookeryv1.NAT44VNF{
		Id: vnf.ID, Name: vnf.Name, NetworkId: vnf.NetworkID, NetworkName: vnf.NetworkName,
		Enabled: vnf.Enabled, Gateway: vnf.Config.Gateway,
		OutsideInterface: vnf.Config.OutsideInterface, OutsideAddress: vnf.Config.OutsideAddress,
		OutsideGateway: vnf.Config.OutsideGateway, Nat44Pool: vnf.Config.NAT44Pool,
		DnatRuleCount: uint32(vnf.DNATRuleCount),
		TapSwIfIndex:  vnf.TapSwIfIndex, TapHostName: vnf.TapHostName,
		CreatedAt: timestamp(vnf.CreatedAt), UpdatedAt: timestamp(vnf.UpdatedAt),
	}
}

// nat44VNFView builds a typed NAT44 parent with live status.
func (s *vnfService) nat44VNFView(ctx context.Context, vnf store.NAT44VNF) *rookeryv1.NAT44VNF {
	result := nat44VNFToProto(vnf)
	statusValue, detail, _ := s.applyVNFStatus(ctx, identityStatusRecord(vnf.ID, vnf.Name, vnf.NetworkID, store.VNFTypeNAT44, vnf.Enabled))
	result.Status = statusValue
	result.StatusDetail = detail
	return result
}

// ListNAT44VNFs returns NAT44 VNFs, optionally filtered by network.
func (s *vnfService) ListNAT44VNFs(ctx context.Context, request *rookeryv1.ListNAT44VNFsRequest) (*rookeryv1.ListNAT44VNFsResponse, error) {
	vnfs, err := s.store.ListNAT44VNFs(ctx, request.NetworkId)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list NAT44 VNFs")
	}
	response := &rookeryv1.ListNAT44VNFsResponse{Vnfs: make([]*rookeryv1.NAT44VNF, 0, len(vnfs))}
	for _, vnf := range vnfs {
		response.Vnfs = append(response.Vnfs, s.nat44VNFView(ctx, vnf))
	}
	return response, nil
}

// GetNAT44VNF returns one NAT44 VNF.
func (s *vnfService) GetNAT44VNF(ctx context.Context, request *rookeryv1.GetNAT44VNFRequest) (*rookeryv1.NAT44VNF, error) {
	vnf, err := s.store.GetNAT44VNF(ctx, request.GetId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.nat44VNFView(ctx, vnf), nil
}

// CreateNAT44VNF delegates to the lifecycle writer.
func (s *vnfService) CreateNAT44VNF(ctx context.Context, request *rookeryv1.CreateNAT44VNFRequest) (*rookeryv1.NAT44VNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	created, err := s.writer.CreateNAT44VNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetNAT44VNF(ctx, created.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.nat44VNFView(ctx, vnf), nil
}

// UpdateNAT44VNF delegates to the lifecycle writer.
func (s *vnfService) UpdateNAT44VNF(ctx context.Context, request *rookeryv1.UpdateNAT44VNFRequest) (*rookeryv1.NAT44VNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	updated, err := s.writer.UpdateNAT44VNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetNAT44VNF(ctx, updated.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.nat44VNFView(ctx, vnf), nil
}

// DeleteNAT44VNF delegates removal to the lifecycle writer.
func (s *vnfService) DeleteNAT44VNF(ctx context.Context, request *rookeryv1.DeleteNAT44VNFRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	if err := s.writer.DeleteNAT44VNF(ctx, request); err != nil {
		return nil, mapVNFWriteError(err)
	}
	return &emptypb.Empty{}, nil
}

// RestartNAT44VNF restarts a NAT44 VNF and returns the refreshed parent.
func (s *vnfService) RestartNAT44VNF(ctx context.Context, request *rookeryv1.RestartNAT44VNFRequest) (*rookeryv1.NAT44VNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	restarted, err := s.writer.RestartNAT44VNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	vnf, err := s.store.GetNAT44VNF(ctx, restarted.ID)
	if err != nil {
		return nil, vnfReadError(err)
	}
	return s.nat44VNFView(ctx, vnf), nil
}

// ListNAT44VNFSessions returns the current VPP NAT44 translations.
func (s *vnfService) ListNAT44VNFSessions(ctx context.Context, request *rookeryv1.ListNAT44VNFSessionsRequest) (*rookeryv1.ListNAT44VNFSessionsResponse, error) {
	vnf, err := s.store.GetNAT44VNF(ctx, request.GetId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	sessions := []string{}
	if s.statusProvider != nil {
		sessions, err = s.statusProvider.VNFSessions(ctx, identityStatusRecord(vnf.ID, vnf.Name, vnf.NetworkID, store.VNFTypeNAT44, vnf.Enabled))
		if err != nil {
			return nil, status.Error(codes.Internal, "could not read NAT44 sessions")
		}
	}
	return &rookeryv1.ListNAT44VNFSessionsResponse{VnfId: vnf.ID, Sessions: sessions}, nil
}

// dnatRuleToProto converts one DNAT rule.
func dnatRuleToProto(item store.DNATRule) *rookeryv1.DNATRule {
	return &rookeryv1.DNATRule{
		Id: item.ID, VnfId: item.VNFID, Protocol: item.Protocol,
		ExternalPort: int32(item.ExternalPort), LocalIp: item.LocalIP, LocalPort: int32(item.LocalPort),
	}
}

// ListNAT44DNATRules returns the DNAT rules of a NAT44 VNF.
func (s *vnfService) ListNAT44DNATRules(ctx context.Context, request *rookeryv1.ListNAT44DNATRulesRequest) (*rookeryv1.ListNAT44DNATRulesResponse, error) {
	items, err := s.store.ListNAT44DNATRules(ctx, request.GetVnfId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	response := &rookeryv1.ListNAT44DNATRulesResponse{Rules: make([]*rookeryv1.DNATRule, 0, len(items))}
	for _, item := range items {
		response.Rules = append(response.Rules, dnatRuleToProto(item))
	}
	return response, nil
}

// GetNAT44DNATRule returns one DNAT rule.
func (s *vnfService) GetNAT44DNATRule(ctx context.Context, request *rookeryv1.GetNAT44DNATRuleRequest) (*rookeryv1.DNATRule, error) {
	item, err := s.store.GetNAT44DNATRule(ctx, request.GetVnfId(), request.GetRuleId())
	if err != nil {
		return nil, vnfReadError(err)
	}
	return dnatRuleToProto(item), nil
}

// CreateNAT44DNATRule delegates to the lifecycle writer.
func (s *vnfService) CreateNAT44DNATRule(ctx context.Context, request *rookeryv1.CreateNAT44DNATRuleRequest) (*rookeryv1.DNATRule, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.CreateNAT44DNATRule(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return dnatRuleToProto(item), nil
}

// UpdateNAT44DNATRule delegates to the lifecycle writer.
func (s *vnfService) UpdateNAT44DNATRule(ctx context.Context, request *rookeryv1.UpdateNAT44DNATRuleRequest) (*rookeryv1.DNATRule, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	item, err := s.writer.UpdateNAT44DNATRule(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return dnatRuleToProto(item), nil
}

// DeleteNAT44DNATRule delegates removal to the lifecycle writer.
func (s *vnfService) DeleteNAT44DNATRule(ctx context.Context, request *rookeryv1.DeleteNAT44DNATRuleRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	if err := s.writer.DeleteNAT44DNATRule(ctx, request); err != nil {
		return nil, mapVNFWriteError(err)
	}
	return &emptypb.Empty{}, nil
}
