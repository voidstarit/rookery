package gateway

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// NetworkRuntime creates and deletes VPP bridge domains.
type NetworkRuntime interface {
	CreateBridgeDomain(ctx context.Context, name string, reserved []uint32) (uint32, error)
	DeleteBridgeDomain(ctx context.Context, bdID uint32) error
}

// NetworkWriter is the native network lifecycle service implemented by internal/api.
type NetworkWriter interface {
	CreateNetwork(ctx context.Context, name string) (store.Network, error)
	DeleteNetwork(ctx context.Context, id int64) error
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
	if s.writer != nil {
		created, err := s.writer.CreateNetwork(ctx, name)
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
	created, err := s.store.CreateNetwork(ctx, store.Network{Name: name, BridgeDomainID: bdID})
	if err != nil {
		_ = s.runtime.DeleteBridgeDomain(context.Background(), bdID)
		if errors.Is(err, store.ErrNetworkNameTaken) {
			return nil, status.Error(codes.AlreadyExists, "a network with that name already exists")
		}
		return nil, status.Error(codes.Internal, "could not record network")
	}
	return networkToProto(created), nil
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
		return status.Error(codes.Internal, "network operation failed")
	}
}
