package gateway

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/kouja"
	"github.com/voidstarit/rookery/internal/store"
)

type storageReadiness interface {
	StorageConfig() (vg, thinPool string)
	SetStorage(vg, thinPool string)
}

// CreateStorageBackend validates the request, stores the backend, and points readiness at it when
// it is lvm-thin and readiness has no volume group or the backend is the default one.
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

// UpdateStorageBackend validates the request and updates an existing backend, refusing driver,
// volume-group, or thin-pool changes while disks still use it.
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
	if existing.DiskCount > 0 && (backend.Driver != existing.Driver ||
		backend.Config.VG != existing.Config.VG || backend.Config.ThinPool != existing.Config.ThinPool) {
		return nil, status.Error(codes.Aborted, "storage backend volume group and thin pool cannot change while disks use it")
	}
	updated, err := s.store.UpdateStorageBackend(ctx, backend)
	if err != nil {
		return nil, storageBackendError(err)
	}
	s.configureStorageReadiness(updated)
	return storageBackendToProto(updated), nil
}

// DeleteStorageBackend loads and deletes a backend, then re-points readiness when the deleted
// backend was the one readiness used.
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

// normalizeStorageBackend validates and trims the fields of a create request, returning the
// store record to insert.
func normalizeStorageBackend(request *rookeryv1.CreateStorageBackendRequest) (store.StorageBackend, error) {
	return normalizeStorageBackendFields(
		request.GetName(),
		request.GetTitle(),
		request.GetDescription(),
		request.GetDriver(),
		request.GetConfig(),
	)
}

// normalizeStorageBackendUpdate validates and trims the fields of an update request, returning
// the store record to write.
func normalizeStorageBackendUpdate(request *rookeryv1.UpdateStorageBackendRequest) (store.StorageBackend, error) {
	return normalizeStorageBackendFields(
		request.GetName(),
		request.GetTitle(),
		request.GetDescription(),
		request.GetDriver(),
		request.GetConfig(),
	)
}

// normalizeStorageBackendFields trims and validates the common fields, defaulting an empty
// driver to lvm-thin. Driver-specific configuration is validated too: kouja credentials are
// parsed, and lvm-thin requires a volume group, thin pool, and preparation settings.
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
	if driver == "kouja" {
		if config == nil || config.GetKouja() == nil {
			return store.StorageBackend{}, errors.New("kouja configuration is required")
		}
		cfg := koujaConfigFromProto(config.GetKouja())
		if _, err := kouja.ParseConfig(store.StorageBackendConfig{Kouja: cfg}); err != nil {
			return store.StorageBackend{}, err
		}
		return store.StorageBackend{Name: name, Title: title, Description: description, Driver: driver,
			Config: store.StorageBackendConfig{Kouja: cfg}}, nil
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
	script := strings.TrimSpace(config.GetPreparationScript())
	if script != "" && !filepath.IsAbs(script) {
		return store.StorageBackend{}, errors.New("preparation_script must be an absolute path")
	}
	timeout := strings.TrimSpace(config.GetPreparationTimeout())
	if timeout == "" {
		timeout = "5m"
	}
	duration, err := time.ParseDuration(timeout)
	if err != nil || duration <= 0 {
		return store.StorageBackend{}, errors.New("preparation_timeout must be a positive duration such as 5m")
	}
	if config.GetSupportsTemplates() && script == "" {
		return store.StorageBackend{}, errors.New("preparation_script is required when template support is enabled")
	}
	return store.StorageBackend{
		Name: name, Title: title, Description: description, Driver: driver,
		Config: store.StorageBackendConfig{
			VG: vg, ThinPool: thinPool,
			SupportsTemplates:             config.GetSupportsTemplates(),
			PreparationScript:             script,
			PreparationTimeout:            duration.String(),
			AllowTemplateDeleteWithClones: config.GetAllowTemplateDeleteWithClones(),
		},
	}, nil
}

// storageBackendError maps store sentinel errors for storage backends to NotFound, AlreadyExists,
// Aborted, or Internal gRPC codes.
func storageBackendError(err error) error {
	switch {
	case errors.Is(err, store.ErrStorageBackendNotFound):
		return status.Error(codes.NotFound, "storage backend not found")
	case errors.Is(err, store.ErrStorageBackendNameTaken):
		return status.Error(codes.AlreadyExists, "a storage backend with that name already exists")
	case errors.Is(err, store.ErrStorageBackendInUse):
		return status.Error(codes.Aborted, "storage backend still has disks")
	default:
		return status.Error(codes.Internal, "storage backend operation failed")
	}
}

// configureStorageReadiness points the readiness checker at an lvm-thin backend's volume group
// and thin pool when none is configured yet or the backend is the default one.
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

// configureStorageReadinessAfterDelete clears or replaces the readiness volume group and thin
// pool only when the deleted backend was the configured one, preferring the first remaining
// complete lvm-thin backend.
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

// CreateMedia registers an existing absolute regular file as boot media and records its size,
// rejecting missing files, non-regular paths, and duplicate names.
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

// DeleteMedia removes a media record, reporting NotFound when it does not exist and Aborted when
// a VM still uses it as boot media.
func (s *mediaService) DeleteMedia(ctx context.Context, request *rookeryv1.DeleteMediaRequest) (*emptypb.Empty, error) {
	if err := s.store.DeleteMedia(ctx, request.GetId()); err != nil {
		switch {
		case errors.Is(err, store.ErrMediaNotFound):
			return nil, status.Error(codes.NotFound, "media not found")
		case errors.Is(err, store.ErrMediaInUse):
			return nil, status.Error(codes.Aborted, "media is used as boot media by a VM; clear it first")
		default:
			return nil, status.Error(codes.Internal, "could not delete media")
		}
	}
	return &emptypb.Empty{}, nil
}

// koujaConfigFromProto copies a proto Kouja configuration into the store type with every string
// trimmed, returning nil for a nil input.
func koujaConfigFromProto(raw *rookeryv1.KoujaConfig) *store.KoujaConfig {
	if raw == nil {
		return nil
	}
	return &store.KoujaConfig{
		Endpoint: strings.TrimSpace(raw.GetEndpoint()), Protocol: strings.TrimSpace(raw.GetProtocol()),
		StorageClass: strings.TrimSpace(raw.GetStorageClass()), Initiator: strings.TrimSpace(raw.GetInitiator()),
		HostNQN: strings.TrimSpace(raw.GetHostNqn()), CAFile: strings.TrimSpace(raw.GetCaFile()),
		ClientCertFile: strings.TrimSpace(raw.GetClientCertFile()), ClientKeyFile: strings.TrimSpace(raw.GetClientKeyFile()),
		CAPEM: strings.TrimSpace(raw.GetCaPem()), ClientCertPEM: strings.TrimSpace(raw.GetClientCertPem()),
		ClientKeyPEM: strings.TrimSpace(raw.GetClientKeyPem()),
		ServerName:   strings.TrimSpace(raw.GetServerName()), Portal: strings.TrimSpace(raw.GetPortal()),
	}
}
