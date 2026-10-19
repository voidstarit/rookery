package gateway

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

type storageReadiness interface {
	StorageConfig() (vg, thinPool string)
	SetStorage(vg, thinPool string)
}

func (s *storageBackendService) CreateStorageBackend(ctx context.Context, request *rookeryv1.CreateStorageBackendRequest) (*rookeryv1.StorageBackend, error) {
	backend, err := normalizeStorageBackend(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	created, err := s.store.CreateStorageBackend(ctx, backend)
	if err != nil {
		return nil, storageBackendError(err)
	}
	s.configureStorageReadiness(created)
	return storageBackendToProto(created), nil
}

func (s *storageBackendService) UpdateStorageBackend(ctx context.Context, request *rookeryv1.UpdateStorageBackendRequest) (*rookeryv1.StorageBackend, error) {
	existing, err := s.store.GetStorageBackend(ctx, request.GetId())
	if err != nil {
		return nil, storageBackendError(err)
	}
	backend, err := normalizeStorageBackendUpdate(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	backend.ID = existing.ID
	if existing.DiskCount > 0 && (backend.Driver != existing.Driver || backend.Config != existing.Config) {
		return nil, status.Error(codes.FailedPrecondition, "storage backend configuration cannot change while disks use it")
	}
	updated, err := s.store.UpdateStorageBackend(ctx, backend)
	if err != nil {
		return nil, storageBackendError(err)
	}
	s.configureStorageReadiness(updated)
	return storageBackendToProto(updated), nil
}

func (s *storageBackendService) DeleteStorageBackend(ctx context.Context, request *rookeryv1.DeleteStorageBackendRequest) (*emptypb.Empty, error) {
	existing, err := s.store.GetStorageBackend(ctx, request.GetId())
	if err != nil {
		return nil, storageBackendError(err)
	}
	if err := s.store.DeleteStorageBackend(ctx, request.GetId()); err != nil {
		return nil, storageBackendError(err)
	}
	s.configureStorageReadinessAfterDelete(existing)
	return &emptypb.Empty{}, nil
}

func normalizeStorageBackend(request *rookeryv1.CreateStorageBackendRequest) (store.StorageBackend, error) {
	return normalizeStorageBackendFields(
		request.GetName(),
		request.GetTitle(),
		request.GetDescription(),
		request.GetDriver(),
		request.GetConfig(),
	)
}

func normalizeStorageBackendUpdate(request *rookeryv1.UpdateStorageBackendRequest) (store.StorageBackend, error) {
	return normalizeStorageBackendFields(
		request.GetName(),
		request.GetTitle(),
		request.GetDescription(),
		request.GetDriver(),
		request.GetConfig(),
	)
}

func normalizeStorageBackendFields(name, title, description, driver string, config *rookeryv1.StorageBackendConfig) (store.StorageBackend, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return store.StorageBackend{}, errors.New("name is required")
	}
	if len(name) > 64 {
		return store.StorageBackend{}, errors.New("name must be at most 64 characters")
	}
	title = strings.TrimSpace(title)
	if len(title) > 128 {
		return store.StorageBackend{}, errors.New("title must be at most 128 characters")
	}
	description = strings.TrimSpace(description)
	if len(description) > 2000 {
		return store.StorageBackend{}, errors.New("description must be at most 2000 characters")
	}
	driver = strings.ToLower(strings.TrimSpace(driver))
	if driver == "" {
		driver = "lvm-thin"
	}
	if driver != "lvm-thin" {
		return store.StorageBackend{}, fmt.Errorf("unsupported storage driver %q", driver)
	}
	if config == nil {
		config = &rookeryv1.StorageBackendConfig{}
	}
	vg := strings.TrimSpace(config.GetVg())
	thinPool := strings.TrimSpace(config.GetThinPool())
	if err := host.ValidateLVMName("volume group", vg); err != nil {
		return store.StorageBackend{}, err
	}
	if err := host.ValidateLVMName("thin pool", thinPool); err != nil {
		return store.StorageBackend{}, err
	}
	return store.StorageBackend{
		Name: name, Title: title, Description: description, Driver: driver,
		Config: store.StorageBackendConfig{VG: vg, ThinPool: thinPool},
	}, nil
}

func storageBackendError(err error) error {
	switch {
	case errors.Is(err, store.ErrStorageBackendNotFound):
		return status.Error(codes.NotFound, "storage backend not found")
	case errors.Is(err, store.ErrStorageBackendNameTaken):
		return status.Error(codes.AlreadyExists, "a storage backend with that name already exists")
	case errors.Is(err, store.ErrStorageBackendInUse):
		return status.Error(codes.FailedPrecondition, "storage backend still has disks")
	default:
		return status.Error(codes.Internal, "storage backend operation failed")
	}
}

func (s *storageBackendService) configureStorageReadiness(backend store.StorageBackend) {
	readiness, ok := s.readiness.(storageReadiness)
	if !ok || backend.Driver != "lvm-thin" {
		return
	}
	vg, _ := readiness.StorageConfig()
	if vg == "" || backend.Name == "default" {
		readiness.SetStorage(backend.Config.VG, backend.Config.ThinPool)
	}
}

func (s *storageBackendService) configureStorageReadinessAfterDelete(backend store.StorageBackend) {
	readiness, ok := s.readiness.(storageReadiness)
	if !ok {
		return
	}
	vg, thinPool := readiness.StorageConfig()
	if vg != backend.Config.VG || thinPool != backend.Config.ThinPool {
		return
	}
	backends, err := s.store.ListStorageBackends(context.Background())
	if err != nil {
		return
	}
	for _, candidate := range backends {
		if candidate.Driver == "lvm-thin" && candidate.Config.VG != "" && candidate.Config.ThinPool != "" {
			readiness.SetStorage(candidate.Config.VG, candidate.Config.ThinPool)
			return
		}
	}
	readiness.SetStorage("", "")
}

func (s *mediaService) CreateMedia(ctx context.Context, request *rookeryv1.CreateMediaRequest) (*rookeryv1.Media, error) {
	name := strings.TrimSpace(request.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if len(name) > 127 {
		return nil, status.Error(codes.InvalidArgument, "name must be at most 127 characters")
	}
	path := strings.TrimSpace(request.GetPath())
	if path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}
	if !filepath.IsAbs(path) {
		return nil, status.Error(codes.InvalidArgument, "path must be absolute")
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, status.Error(codes.InvalidArgument, "media file not found")
	case err != nil:
		return nil, status.Errorf(codes.InvalidArgument, "inspect media file: %v", err)
	case !info.Mode().IsRegular():
		return nil, status.Error(codes.InvalidArgument, "media path must point to a regular file")
	}
	created, err := s.store.CreateMedia(ctx, store.Media{Name: name, Path: path, SizeBytes: info.Size()})
	if err != nil {
		if errors.Is(err, store.ErrMediaNameTaken) {
			return nil, status.Error(codes.AlreadyExists, "media with that name already exists")
		}
		return nil, status.Error(codes.Internal, "could not create media")
	}
	return mediaToProto(created), nil
}

func (s *mediaService) DeleteMedia(ctx context.Context, request *rookeryv1.DeleteMediaRequest) (*emptypb.Empty, error) {
	if err := s.store.DeleteMedia(ctx, request.GetId()); err != nil {
		switch {
		case errors.Is(err, store.ErrMediaNotFound):
			return nil, status.Error(codes.NotFound, "media not found")
		case errors.Is(err, store.ErrMediaInUse):
			return nil, status.Error(codes.FailedPrecondition, "media is used as boot media by a VM; clear it first")
		default:
			return nil, status.Error(codes.Internal, "could not delete media")
		}
	}
	return &emptypb.Empty{}, nil
}
