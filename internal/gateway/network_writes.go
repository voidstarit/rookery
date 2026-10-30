package gateway

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/netutil"
	"github.com/voidstarit/rookery/internal/store"
)

// NetworkRuntime creates and deletes VPP bridge domains and pipes.
type NetworkRuntime interface {
	CreateBridgeDomain(ctx context.Context, name string, reserved []uint32) (uint32, error)
	DeleteBridgeDomain(ctx context.Context, bdID uint32) error
	CreatePipe(ctx context.Context, bdA, bdB uint32) (host.PipeRuntime, error)
	EnsurePipe(ctx context.Context, instance, bdA, bdB uint32) (host.PipeRuntime, error)
	DeletePipe(ctx context.Context, instance, parentSwIfIndex uint32) error
}

// NetworkWriter is the network lifecycle service implemented by internal/lifecycle.
type NetworkWriter interface {
	CreateNetwork(ctx context.Context, name, ipv4CIDR, ipv6CIDR string) (store.Network, error)
	DeleteNetwork(ctx context.Context, id int64) error
	CreateNetworkPipe(ctx context.Context, networkAID, networkBID int64) (store.NetworkPipe, error)
	DeleteNetworkPipe(ctx context.Context, id int64) error
}

func (s *networkService) ready() error {
	if s.runtime == nil {
		return status.Error(codes.Unavailable, "VPP network runtime is not configured")
	}
	if s.readiness != nil && !s.readiness.Status().VPP.Ready {
		return status.Error(codes.Unavailable, "VPP is not ready")
	}
	return nil
}

func (s *networkService) CreateNetwork(ctx context.Context, request *rookeryv1.CreateNetworkRequest) (*rookeryv1.Network, error) {
	name := strings.TrimSpace(request.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	ipv4CIDR, ipv6CIDR, err := netutil.NormalizeNetworkCIDRs(request.GetIpv4Cidr(), request.GetIpv6Cidr())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if s.writer != nil {
		created, err := s.writer.CreateNetwork(ctx, name, ipv4CIDR, ipv6CIDR)
		if err != nil {
			return nil, mapNetworkWriteError(err)
		}
		return networkToProto(created), nil
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.store.ListNetworks(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list networks")
	}
	reserved := make([]uint32, 0, len(existing))
	for _, network := range existing {
		reserved = append(reserved, network.BridgeDomainID)
	}
	bdID, err := s.runtime.CreateBridgeDomain(ctx, name, reserved)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not create VPP bridge domain: "+err.Error())
	}
	created, err := s.store.CreateNetwork(ctx, store.Network{
		Name: name, BridgeDomainID: bdID, IPv4CIDR: ipv4CIDR, IPv6CIDR: ipv6CIDR,
	})
	if err != nil {
		_ = s.runtime.DeleteBridgeDomain(context.Background(), bdID)
		if errors.Is(err, store.ErrNetworkNameTaken) {
			return nil, status.Error(codes.AlreadyExists, "a network with that name already exists")
		}
		return nil, status.Error(codes.Internal, "could not record network")
	}
	return networkToProto(created), nil
}

// UpdateNetwork changes a network's IPv4/IPv6 CIDRs. VPP address-bearing
// features that derive their addresses from the prefix must be removed first.
func (s *networkService) UpdateNetwork(ctx context.Context, request *rookeryv1.UpdateNetworkRequest) (*rookeryv1.Network, error) {
	ipv4CIDR, ipv6CIDR, err := netutil.NormalizeNetworkCIDRs(request.GetIpv4Cidr(), request.GetIpv6Cidr())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	network, err := s.store.GetNetwork(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrNetworkNotFound, "network not found")
	}
	if network.IPv4CIDR == ipv4CIDR && network.IPv6CIDR == ipv6CIDR {
		return networkToProto(network), nil
	}
	if network.BVIHostName != "" || network.IPv4Gateway != "" {
		return nil, status.Error(codes.Aborted, "remove the network BVI before changing network CIDRs")
	}
	if network.DHCPEnabled || network.DHCPTapHostName != "" || network.DHCPTapSwIfIndex != 0 {
		return nil, status.Error(codes.Aborted, "remove legacy DHCP configuration before changing network CIDRs")
	}
	vnfs, err := s.store.ListVNFsByNetwork(ctx, network.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list network VNFs")
	}
	if len(vnfs) > 0 {
		return nil, status.Error(codes.Aborted, "remove network VNFs before changing network CIDRs")
	}
	taps, err := s.store.ListNetworkHostTapsByNetwork(ctx, network.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list network host taps")
	}
	if len(taps) > 0 {
		return nil, status.Error(codes.Aborted, "remove network host taps before changing network CIDRs")
	}
	updated, err := s.store.SetNetworkCIDRs(ctx, network.ID, ipv4CIDR, ipv6CIDR)
	if err != nil {
		if errors.Is(err, store.ErrNetworkNotFound) {
			return nil, status.Error(codes.NotFound, "network not found")
		}
		return nil, status.Error(codes.Internal, "could not update network CIDRs")
	}
	return networkToProto(updated), nil
}

func (s *networkService) DeleteNetwork(ctx context.Context, request *rookeryv1.DeleteNetworkRequest) (*emptypb.Empty, error) {
	if s.writer != nil {
		if err := s.writer.DeleteNetwork(ctx, request.GetId()); err != nil {
			return nil, mapNetworkWriteError(err)
		}
		return &emptypb.Empty{}, nil
	}
	network, err := s.store.GetNetwork(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrNetworkNotFound, "network not found")
	}
	if network.InterfaceCount > 0 {
		return nil, status.Error(codes.Aborted, "network still has virtual interfaces")
	}
	if count, err := s.store.CountNetworkPipesForNetwork(ctx, network.ID); err != nil {
		return nil, status.Error(codes.Internal, "could not check network pipes")
	} else if count > 0 {
		return nil, status.Error(codes.Aborted, "network still has bridge-domain pipes")
	}
	vnfs, err := s.store.ListVNFsByNetwork(ctx, network.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list network VNFs")
	}
	if len(vnfs) > 0 {
		return nil, status.Error(codes.Aborted, "delete the network's VNFs before deleting the network")
	}
	if network.BVIHostName != "" {
		return nil, status.Error(codes.Aborted, "delete the network's NAT44 VNF before deleting the network")
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.runtime.DeleteBridgeDomain(ctx, network.BridgeDomainID); err != nil {
		return nil, status.Error(codes.Internal, "could not delete VPP bridge domain: "+err.Error())
	}
	if err := s.store.DeleteNetwork(ctx, network.ID); err != nil {
		switch {
		case errors.Is(err, store.ErrNetworkNotFound):
			return nil, status.Error(codes.NotFound, "network not found")
		case errors.Is(err, store.ErrNetworkInUse):
			return nil, status.Error(codes.Aborted, "network still has virtual interfaces")
		default:
			return nil, status.Error(codes.Internal, "could not delete network")
		}
	}
	return &emptypb.Empty{}, nil
}

type networkStatusError interface {
	NetworkStatus() (int, string)
}

func mapNetworkWriteError(err error) error {
	var operationError networkStatusError
	if !errors.As(err, &operationError) {
		return status.Error(codes.Internal, "network operation failed")
	}
	httpStatus, message := operationError.NetworkStatus()
	if message == "" {
		message = "network operation failed"
	}
	switch httpStatus {
	case 400:
		return status.Error(codes.InvalidArgument, message)
	case 404:
		return status.Error(codes.NotFound, message)
	case 409:
		return status.Error(codes.Aborted, message)
	case 503:
		return status.Error(codes.Unavailable, message)
	default:
		return status.Error(codes.Internal, message)
	}
}

// ListNetworkPipes lists persistent bridge-domain pipes.
func (s *networkService) ListNetworkPipes(ctx context.Context, _ *rookeryv1.ListNetworkPipesRequest) (*rookeryv1.ListNetworkPipesResponse, error) {
	pipes, err := s.store.ListNetworkPipes(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list network pipes")
	}
	response := &rookeryv1.ListNetworkPipesResponse{Pipes: make([]*rookeryv1.NetworkPipe, 0, len(pipes))}
	for _, pipe := range pipes {
		response.Pipes = append(response.Pipes, networkPipeToProto(pipe))
	}
	return response, nil
}

// CreateNetworkPipe creates a VPP pipe between two bridge domains.
func (s *networkService) CreateNetworkPipe(ctx context.Context, request *rookeryv1.CreateNetworkPipeRequest) (*rookeryv1.NetworkPipe, error) {
	if request.GetNetworkAId() == request.GetNetworkBId() {
		return nil, status.Error(codes.InvalidArgument, "a pipe requires two different networks")
	}
	if s.writer != nil {
		created, err := s.writer.CreateNetworkPipe(ctx, request.GetNetworkAId(), request.GetNetworkBId())
		if err != nil {
			return nil, mapNetworkWriteError(err)
		}
		return networkPipeToProto(created), nil
	}
	networkA, err := s.store.GetNetwork(ctx, request.GetNetworkAId())
	if err != nil {
		return nil, storeReadError(err, store.ErrNetworkNotFound, "network not found")
	}
	networkB, err := s.store.GetNetwork(ctx, request.GetNetworkBId())
	if err != nil {
		return nil, storeReadError(err, store.ErrNetworkNotFound, "network not found")
	}
	if networkA.ID > networkB.ID {
		networkA, networkB = networkB, networkA
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, err := s.runtime.CreatePipe(ctx, networkA.BridgeDomainID, networkB.BridgeDomainID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not create VPP pipe: "+err.Error())
	}
	created, err := s.store.CreateNetworkPipe(ctx, store.NetworkPipe{
		NetworkAID: networkA.ID, NetworkBID: networkB.ID, Instance: runtime.Instance,
		ParentSwIfIndex:    runtime.ParentSwIfIndex,
		EndpointASwIfIndex: runtime.EndpointASwIfIndex,
		EndpointBSwIfIndex: runtime.EndpointBSwIfIndex,
	})
	if err != nil {
		_ = s.runtime.DeletePipe(context.Background(), runtime.Instance, runtime.ParentSwIfIndex)
		if errors.Is(err, store.ErrNetworkPipeExists) {
			return nil, status.Error(codes.AlreadyExists, "a pipe between those networks already exists")
		}
		return nil, status.Error(codes.Internal, "could not record network pipe")
	}
	return networkPipeToProto(created), nil
}

// DeleteNetworkPipe removes a bridge-domain pipe.
func (s *networkService) DeleteNetworkPipe(ctx context.Context, request *rookeryv1.DeleteNetworkPipeRequest) (*emptypb.Empty, error) {
	if s.writer != nil {
		if err := s.writer.DeleteNetworkPipe(ctx, request.GetId()); err != nil {
			return nil, mapNetworkWriteError(err)
		}
		return &emptypb.Empty{}, nil
	}
	pipe, err := s.store.GetNetworkPipe(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrNetworkPipeNotFound, "network pipe not found")
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.runtime.DeletePipe(ctx, pipe.Instance, pipe.ParentSwIfIndex); err != nil {
		return nil, status.Error(codes.Internal, "could not delete VPP pipe: "+err.Error())
	}
	if err := s.store.DeleteNetworkPipe(ctx, pipe.ID); err != nil {
		return nil, storeReadError(err, store.ErrNetworkPipeNotFound, "network pipe not found")
	}
	return &emptypb.Empty{}, nil
}

func networkPipeToProto(pipe store.NetworkPipe) *rookeryv1.NetworkPipe {
	return &rookeryv1.NetworkPipe{
		Id: pipe.ID, NetworkAId: pipe.NetworkAID, NetworkAName: pipe.NetworkAName,
		NetworkBId: pipe.NetworkBID, NetworkBName: pipe.NetworkBName,
		Instance: pipe.Instance, ParentSwIfIndex: pipe.ParentSwIfIndex,
		EndpointASwIfIndex: pipe.EndpointASwIfIndex, EndpointBSwIfIndex: pipe.EndpointBSwIfIndex,
		CreatedAt: timestamp(pipe.CreatedAt), UpdatedAt: timestamp(pipe.UpdatedAt),
	}
}
