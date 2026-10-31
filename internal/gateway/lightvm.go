package gateway

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/lightvm"
	"github.com/voidstarit/rookery/internal/store"
)

type lightVMKernelService struct {
	rookeryv1.UnimplementedLightVMKernelServiceServer
	store      *store.Store
	storageDir string
}

// ListKernels returns every kernel record ordered by name.
func (s *lightVMKernelService) ListKernels(ctx context.Context, _ *rookeryv1.ListKernelsRequest) (*rookeryv1.ListKernelsResponse, error) {
	kernels, err := s.store.ListLightVMKernels(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list kernels")
	}
	response := &rookeryv1.ListKernelsResponse{Kernels: make([]*rookeryv1.LightVMKernel, 0, len(kernels))}
	for _, kernel := range kernels {
		response.Kernels = append(response.Kernels, lightVMKernelToProto(kernel))
	}
	return response, nil
}

// GetKernel returns one kernel record.
func (s *lightVMKernelService) GetKernel(ctx context.Context, request *rookeryv1.GetKernelRequest) (*rookeryv1.LightVMKernel, error) {
	kernel, err := s.store.GetLightVMKernel(ctx, request.GetId())
	if err != nil {
		return nil, lightVMKernelError(err, "kernel not found")
	}
	return lightVMKernelToProto(kernel), nil
}

// CreateKernel registers an uploaded or existing kernel file.
func (s *lightVMKernelService) CreateKernel(ctx context.Context, request *rookeryv1.CreateKernelRequest) (*rookeryv1.LightVMKernel, error) {
	kernel, err := s.kernelFromRequest(0, request.GetName(), request.GetDescription(), request.GetPath(), request.GetFileName())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	created, err := s.store.CreateLightVMKernel(ctx, kernel)
	if err != nil {
		return nil, lightVMKernelError(err, "kernel not found")
	}
	return lightVMKernelToProto(created), nil
}

// UpdateKernel changes the editable fields of a kernel record and cleans up a
// replaced managed file after the database update succeeds.
func (s *lightVMKernelService) UpdateKernel(ctx context.Context, request *rookeryv1.UpdateKernelRequest) (*rookeryv1.LightVMKernel, error) {
	existing, err := s.store.GetLightVMKernel(ctx, request.GetId())
	if err != nil {
		return nil, lightVMKernelError(err, "kernel not found")
	}
	kernel, err := s.kernelFromRequest(existing.ID, request.GetName(), request.GetDescription(), request.GetPath(), request.GetFileName())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	updated, err := s.store.UpdateLightVMKernel(ctx, kernel)
	if err != nil {
		return nil, lightVMKernelError(err, "kernel not found")
	}
	if existing.Managed && existing.Path != updated.Path {
		if err := os.Remove(existing.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Warn().Err(err).Str("path", existing.Path).Msg("could not remove replaced kernel file")
		}
	}
	return lightVMKernelToProto(updated), nil
}

// DeleteKernel removes a kernel record and its managed file.
func (s *lightVMKernelService) DeleteKernel(ctx context.Context, request *rookeryv1.DeleteKernelRequest) (*emptypb.Empty, error) {
	kernel, err := s.store.DeleteLightVMKernel(ctx, request.GetId())
	if err != nil {
		return nil, lightVMKernelError(err, "kernel not found")
	}
	if kernel.Managed {
		if err := os.Remove(kernel.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Warn().Err(err).Str("path", kernel.Path).Msg("could not remove deleted kernel file")
		}
	}
	return &emptypb.Empty{}, nil
}

func (s *lightVMKernelService) kernelFromRequest(id int64, name, description, path, fileName string) (store.LightVMKernel, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return store.LightVMKernel{}, errors.New("name is required")
	}
	description = strings.TrimSpace(description)
	if len(description) > 2000 {
		return store.LightVMKernel{}, errors.New("description must be at most 2000 characters")
	}

	path, size, err := inspectLightVMFile(path, "kernel")
	if err != nil {
		return store.LightVMKernel{}, err
	}
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		fileName = filepath.Base(path)
	}
	if fileName == "." || fileName == "/" {
		fileName = filepath.Base(path)
	}

	return store.LightVMKernel{
		ID:          id,
		Name:        name,
		Description: description,
		Path:        path,
		FileName:    fileName,
		SizeBytes:   size,
		Managed:     lightvm.IsManaged(s.storageDir, path),
	}, nil
}

func inspectLightVMFile(path, kind string) (string, int64, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", 0, errors.New("path is required")
	}
	if !filepath.IsAbs(path) {
		return "", 0, errors.New("path must be absolute")
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", 0, fmt.Errorf("%s file not found", kind)
	case err != nil:
		return "", 0, fmt.Errorf("inspect %s file: %w", kind, err)
	case !info.Mode().IsRegular():
		return "", 0, fmt.Errorf("%s path must point to a regular file", kind)
	case info.Size() == 0:
		return "", 0, fmt.Errorf("%s file is empty", kind)
	}
	return path, info.Size(), nil
}

func lightVMKernelToProto(kernel store.LightVMKernel) *rookeryv1.LightVMKernel {
	return &rookeryv1.LightVMKernel{
		Id:          kernel.ID,
		Name:        kernel.Name,
		Description: kernel.Description,
		Path:        kernel.Path,
		FileName:    kernel.FileName,
		SizeBytes:   kernel.SizeBytes,
		Managed:     kernel.Managed,
		CreatedAt:   timestamp(kernel.CreatedAt),
		UpdatedAt:   timestamp(kernel.UpdatedAt),
	}
}

func lightVMKernelError(err error, notFoundMessage string) error {
	switch {
	case errors.Is(err, store.ErrLightVMKernelNotFound):
		return status.Error(codes.NotFound, notFoundMessage)
	case errors.Is(err, store.ErrLightVMKernelNameTaken):
		return status.Error(codes.AlreadyExists, "a kernel with that name already exists")
	case errors.Is(err, store.ErrLightVMKernelInUse):
		return status.Error(codes.Aborted, "kernel is used by a LightVM; remove or update the LightVM first")
	default:
		return status.Error(codes.Internal, "could not save kernel")
	}
}
