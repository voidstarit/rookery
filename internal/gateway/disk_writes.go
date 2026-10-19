package gateway

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// DiskProvisioner creates and removes thin-LVM volumes.
type DiskProvisioner interface {
	CreateThinVolume(ctx context.Context, vg, thinPool, name string, sizeGB int) error
	DeleteThinVolume(ctx context.Context, vg, name string) error
	ListThinVolumes(ctx context.Context, vg, thinPool string) ([]host.ThinVolume, error)
}

const (
	defaultGatewayDiskGB = 10
	maxGatewayDiskGB     = 65_536
)

func (s *diskService) CreateDisk(ctx context.Context, request *rookeryv1.CreateDiskRequest) (*rookeryv1.Disk, error) {
	if s.runtime == nil {
		return nil, status.Error(codes.Unavailable, "disk runtime is not configured")
	}
	name := strings.TrimSpace(request.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	sizeGB := int(request.GetSizeGb())
	if sizeGB == 0 {
		sizeGB = defaultGatewayDiskGB
	}
	if sizeGB < 1 || sizeGB > maxGatewayDiskGB {
		return nil, status.Error(codes.InvalidArgument, "size_gb is out of range")
	}
	backend, err := s.resolveStorageBackend(ctx, request.StorageBackendId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if backend.Driver != "lvm-thin" || backend.Config.VG == "" || backend.Config.ThinPool == "" {
		return nil, status.Error(codes.FailedPrecondition, "selected storage backend is not a configured LVM thin backend")
	}
	if backend.Name == "default" && s.readiness != nil && !s.readiness.Status().ThinPool.Ready {
		return nil, status.Error(codes.Unavailable, "default thin pool is not ready")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.store.GetDiskByName(ctx, name); err == nil {
		return nil, status.Error(codes.AlreadyExists, "a disk with that name already exists")
	} else if !errors.Is(err, store.ErrDiskNotFound) {
		return nil, status.Error(codes.Internal, "could not check disk name")
	}
	disk := store.Disk{
		Name: name, SizeGB: sizeGB,
		StorageBackendID: &backend.ID, StorageBackendName: backend.Name,
		VG: backend.Config.VG, ThinPool: backend.Config.ThinPool,
	}
	if err := s.runtime.CreateThinVolume(ctx, disk.VG, disk.ThinPool, disk.Name, disk.SizeGB); err != nil {
		return nil, status.Error(codes.Internal, "could not create disk volume: "+err.Error())
	}
	created, err := s.store.CreateDisk(ctx, disk)
	if err != nil {
		_ = s.runtime.DeleteThinVolume(context.Background(), disk.VG, disk.Name)
		switch {
		case errors.Is(err, store.ErrDiskNameTaken):
			return nil, status.Error(codes.AlreadyExists, "a disk with that name already exists")
		default:
			return nil, status.Error(codes.Internal, "could not record disk")
		}
	}
	return diskToProto(created), nil
}

func (s *diskService) DeleteDisk(ctx context.Context, request *rookeryv1.DeleteDiskRequest) (*emptypb.Empty, error) {
	if s.runtime == nil {
		return nil, status.Error(codes.Unavailable, "disk runtime is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	disk, err := s.store.GetDisk(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrDiskNotFound, "disk not found")
	}
	if disk.AttachedVMID != nil {
		return nil, status.Error(codes.Aborted, "disk is attached to a VM; delete the VM first")
	}
	if err := s.runtime.DeleteThinVolume(ctx, disk.VG, disk.Name); err != nil {
		return nil, status.Error(codes.Internal, "could not remove disk volume: "+err.Error())
	}
	if err := s.store.DeleteDisk(ctx, request.GetId()); err != nil {
		switch {
		case errors.Is(err, store.ErrDiskNotFound):
			return nil, status.Error(codes.NotFound, "disk not found")
		case errors.Is(err, store.ErrDiskInUse):
			return nil, status.Error(codes.Aborted, "disk is attached to a VM; delete the VM first")
		default:
			return nil, status.Error(codes.Internal, "could not delete disk")
		}
	}
	return &emptypb.Empty{}, nil
}

func (s *diskService) resolveStorageBackend(ctx context.Context, id *int64) (store.StorageBackend, error) {
	if id != nil {
		backend, err := s.store.GetStorageBackend(ctx, *id)
		if err != nil {
			if errors.Is(err, store.ErrStorageBackendNotFound) {
				return store.StorageBackend{}, errors.New("storage backend not found")
			}
			return store.StorageBackend{}, err
		}
		return backend, nil
	}
	backend, err := s.store.GetStorageBackendByName(ctx, "default")
	if err == nil {
		return backend, nil
	}
	if !errors.Is(err, store.ErrStorageBackendNotFound) {
		return store.StorageBackend{}, err
	}
	backends, listErr := s.store.ListStorageBackends(ctx)
	if listErr != nil {
		return store.StorageBackend{}, listErr
	}
	for _, candidate := range backends {
		if candidate.Driver == "lvm-thin" && candidate.Config.VG != "" && candidate.Config.ThinPool != "" {
			return candidate, nil
		}
	}
	if s.readiness == nil {
		return store.StorageBackend{}, errors.New("no storage backend selected")
	}
	status := s.readiness.Status()
	if status.ThinPool.VG == "" || status.ThinPool.Name == "" {
		return store.StorageBackend{}, errors.New("no storage backend selected and no default backend is configured")
	}
	return s.store.CreateStorageBackend(ctx, store.StorageBackend{
		Name:        "default",
		Title:       "Default LVM thin pool",
		Description: "Bootstrapped from readiness configuration",
		Driver:      "lvm-thin",
		Config:      store.StorageBackendConfig{VG: status.ThinPool.VG, ThinPool: status.ThinPool.Name},
	})
}

func (s *storageBackendService) ListImportableDisks(ctx context.Context, request *rookeryv1.ListImportableDisksRequest) (*rookeryv1.ListImportableDisksResponse, error) {
	backend, err := s.store.GetStorageBackend(ctx, request.GetId())
	if err != nil {
		return nil, storageBackendError(err)
	}
	if backend.Driver != "lvm-thin" || backend.Config.VG == "" || backend.Config.ThinPool == "" {
		return nil, status.Error(codes.FailedPrecondition, "storage backend is not configured for LVM thin volumes")
	}
	if s.disks == nil {
		return nil, status.Error(codes.Unavailable, "disk runtime is not configured")
	}
	volumes, err := s.disks.ListThinVolumes(ctx, backend.Config.VG, backend.Config.ThinPool)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list thin volumes")
	}
	disks, err := s.store.ListDisks(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list disks")
	}
	registered := make(map[string]store.Disk, len(disks))
	for _, disk := range disks {
		registered[strings.ToLower(disk.Name)] = disk
	}
	items := make([]*rookeryv1.ImportableDisk, 0, len(volumes))
	for _, volume := range volumes {
		item := &rookeryv1.ImportableDisk{Name: volume.Name, SizeGb: int32(volume.SizeGB)}
		if disk, ok := registered[strings.ToLower(volume.Name)]; ok {
			item.Registered = true
			item.DiskId = &disk.ID
			item.AttachedVmId = disk.AttachedVMID
			item.AttachedVmName = disk.AttachedVMName
			item.StorageBackendName = disk.StorageBackendName
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := strings.ToLower(items[i].Name), strings.ToLower(items[j].Name)
		if left == right {
			return items[i].Name < items[j].Name
		}
		return left < right
	})
	return &rookeryv1.ListImportableDisksResponse{Disks: items}, nil
}

func (s *storageBackendService) ImportDisks(ctx context.Context, request *rookeryv1.ImportDisksRequest) (*rookeryv1.ImportDisksResponse, error) {
	backend, err := s.store.GetStorageBackend(ctx, request.GetId())
	if err != nil {
		return nil, storageBackendError(err)
	}
	if backend.Driver != "lvm-thin" || backend.Config.VG == "" || backend.Config.ThinPool == "" {
		return nil, status.Error(codes.FailedPrecondition, "storage backend is not configured for LVM thin volumes")
	}
	if s.disks == nil {
		return nil, status.Error(codes.Unavailable, "disk runtime is not configured")
	}
	names, err := normalizeImportNames(request.GetNames())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	volumes, err := s.disks.ListThinVolumes(ctx, backend.Config.VG, backend.Config.ThinPool)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list thin volumes")
	}
	byName := make(map[string]host.ThinVolume, len(volumes))
	for _, volume := range volumes {
		byName[volume.Name] = volume
	}
	imports := make([]store.ImportDisk, 0, len(names))
	for _, name := range names {
		volume, ok := byName[name]
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "thin volume %q was not found in the backend", name)
		}
		if _, err := s.store.GetDiskByName(ctx, name); err == nil {
			return nil, status.Errorf(codes.AlreadyExists, "disk %q is already registered", name)
		} else if !errors.Is(err, store.ErrDiskNotFound) {
			return nil, status.Error(codes.Internal, "could not check disk name")
		}
		imports = append(imports, store.ImportDisk{Name: volume.Name, SizeGB: volume.SizeGB})
	}
	created, err := s.store.ImportDisks(ctx, backend.ID, backend.Config.VG, backend.Config.ThinPool, imports)
	if err != nil {
		if errors.Is(err, store.ErrDiskNameTaken) {
			return nil, status.Error(codes.AlreadyExists, "a disk with that name already exists")
		}
		return nil, status.Error(codes.Internal, "could not import disks")
	}
	response := &rookeryv1.ImportDisksResponse{Disks: make([]*rookeryv1.Disk, 0, len(created))}
	for _, disk := range created {
		response.Disks = append(response.Disks, diskToProto(disk))
	}
	return response, nil
}

func normalizeImportNames(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, errors.New("at least one disk is required")
	}
	if len(names) > 256 {
		return nil, errors.New("at most 256 disks can be imported at once")
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if err := host.ValidateLVMName("logical volume", name); err != nil {
			return nil, err
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate disk name %q", name)
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}
