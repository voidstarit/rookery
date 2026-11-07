package gateway

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

func (s *storageBackendService) GetStorageBackendHealth(ctx context.Context, request *rookeryv1.GetStorageBackendHealthRequest) (*rookeryv1.StorageBackendHealth, error) {
	backend, err := s.store.GetStorageBackend(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrStorageBackendNotFound, "storage backend not found")
	}
	health := &rookeryv1.StorageBackendHealth{Id: backend.ID, Driver: backend.Driver, QemuReady: false}
	if backend.Driver == "lvm-thin" {
		if s.readiness != nil {
			status := s.readiness.Status()
			health.Reachable = true
			health.Ready = status.ThinPool.Ready
			health.Detail = status.ThinPool.Detail
			health.CheckedAt = timestamp(time.Now().UTC())
		}
		return s.withDeletionBacklog(ctx, health)
	}
	driver, ok := s.registry.Driver(backend.Driver)
	if !ok {
		health.Detail = "storage driver is not available"
		return s.withDeletionBacklog(ctx, health)
	}
	var sample *store.Disk
	disks, listErr := s.store.ListDisks(ctx)
	if listErr == nil {
		for i := range disks {
			if disks[i].StorageBackendID != nil && *disks[i].StorageBackendID == backend.ID {
				sample = &disks[i]
				break
			}
		}
	}
	result, healthErr := driver.Health(ctx, backend, sample)
	if healthErr != nil {
		health.Detail = healthErr.Error()
		return s.withDeletionBacklog(ctx, health)
	}
	health.Reachable = result.Reachable
	health.Ready = result.Ready
	health.Detail = result.Detail
	health.Protocols = result.Protocols
	health.CheckedAt = timestamp(result.CheckedAt)
	if backend.Config.Kouja != nil {
		health.StorageClass = backend.Config.Kouja.StorageClass
	}
	for _, class := range result.Classes {
		health.Classes = append(health.Classes, &rookeryv1.StorageClassCapacity{
			Name: class.Name, VgName: class.VGName, State: class.State,
			TotalBytes: class.TotalBytes, FreeBytes: class.FreeBytes,
			AllocatableBytes: class.AllocatableBytes, MaxVolumeBytes: class.MaxVolumeBytes,
		})
	}
	return s.withDeletionBacklog(ctx, health)
}

func (s *storageBackendService) TestStorageBackendDataPath(context.Context, *rookeryv1.TestStorageBackendDataPathRequest) (*rookeryv1.StorageBackendHealth, error) {
	return nil, status.Error(codes.Unimplemented, "deep storage data-path testing is not implemented yet")
}

func (s *storageBackendService) withDeletionBacklog(ctx context.Context, health *rookeryv1.StorageBackendHealth) (*rookeryv1.StorageBackendHealth, error) {
	if s.store == nil {
		return health, nil
	}
	count, age, err := s.store.DueDiskDeletionCount(ctx, time.Now().UTC())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return health, nil
	}
	health.PendingDeletions = count
	health.OldestPendingDeletionAgeSeconds = int64(age / time.Second)
	return health, nil
}
