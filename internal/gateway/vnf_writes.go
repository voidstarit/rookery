package gateway

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// VNFWriter is the native VNF lifecycle service implemented by internal/api.
type VNFWriter interface {
	CreateVNF(ctx context.Context, request *rookeryv1.CreateVNFRequest) (store.VNF, error)
	UpdateVNF(ctx context.Context, request *rookeryv1.UpdateVNFRequest) (store.VNF, error)
	DeleteVNF(ctx context.Context, request *rookeryv1.DeleteVNFRequest) error
	RestartVNF(ctx context.Context, request *rookeryv1.RestartVNFRequest) (store.VNF, error)
}

func (s *vnfService) writeAvailable() error {
	if s.writer == nil {
		return status.Error(codes.Unavailable, "VNF write runtime is not configured")
	}
	return nil
}

func (s *vnfService) CreateVNF(ctx context.Context, request *rookeryv1.CreateVNFRequest) (*rookeryv1.VNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	vnf, err := s.writer.CreateVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return s.vnfView(ctx, vnf), nil
}

func (s *vnfService) UpdateVNF(ctx context.Context, request *rookeryv1.UpdateVNFRequest) (*rookeryv1.VNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	vnf, err := s.writer.UpdateVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return s.vnfView(ctx, vnf), nil
}

func (s *vnfService) DeleteVNF(ctx context.Context, request *rookeryv1.DeleteVNFRequest) (*emptypb.Empty, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	if err := s.writer.DeleteVNF(ctx, request); err != nil {
		return nil, mapVNFWriteError(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *vnfService) RestartVNF(ctx context.Context, request *rookeryv1.RestartVNFRequest) (*rookeryv1.VNF, error) {
	if err := s.writeAvailable(); err != nil {
		return nil, err
	}
	vnf, err := s.writer.RestartVNF(ctx, request)
	if err != nil {
		return nil, mapVNFWriteError(err)
	}
	return s.vnfView(ctx, vnf), nil
}

type vnfStatusError interface {
	VNFStatus() (int, string)
}

func mapVNFWriteError(err error) error {
	var operationError vnfStatusError
	if !errors.As(err, &operationError) {
		return status.Error(codes.Internal, "VNF operation failed")
	}
	httpStatus, message := operationError.VNFStatus()
	if message == "" {
		message = "VNF operation failed"
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
		return status.Error(codes.Internal, "VNF operation failed")
	}
}
