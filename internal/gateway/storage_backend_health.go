package gateway

import (
	"context"
	"errors"
	"time"

	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/kouja"
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

func (s *storageBackendService) ProbeKoujaBackend(ctx context.Context, request *rookeryv1.ProbeKoujaBackendRequest) (*rookeryv1.ProbeKoujaBackendResponse, error) {
	cfg := koujaConfigFromProto(request.GetConfig())
	parsed, err := kouja.ParseConnectionConfig(store.StorageBackendConfig{Kouja: cfg})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	factory := s.newKoujaClient
	if factory == nil {
		factory = kouja.NewClient
	}
	client, err := factory(parsed)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "could not connect to kouja: "+err.Error())
	}
	defer client.Close()
	info, err := client.GetInfo(ctx, &koujav1.GetInfoRequest{})
	if err != nil {
		return nil, status.Error(codes.Unavailable, "could not connect to kouja: "+err.Error())
	}
	classes, err := client.ListStorageClasses(ctx, &koujav1.ListStorageClassesRequest{})
	if err != nil {
		return nil, status.Error(codes.Unavailable, "could not list kouja storage classes: "+err.Error())
	}
	response := &rookeryv1.ProbeKoujaBackendResponse{
		Reachable: true, Ready: info.GetReady(), Owner: parsed.Owner,
		Detail: "connected",
	}
	if !info.GetReady() {
		response.Detail = "connected; kouja agent is not ready"
	}
	for _, protocol := range info.GetProtocols() {
		switch protocol {
		case koujav1.Protocol_PROTOCOL_ISCSI:
			response.Protocols = append(response.Protocols, string(blockdev.ProtocolISCSI))
		case koujav1.Protocol_PROTOCOL_NVME_TCP:
			response.Protocols = append(response.Protocols, string(blockdev.ProtocolNVMeTCP))
		default:
			response.Protocols = append(response.Protocols, protocol.String())
		}
	}
	for _, class := range classes.GetClasses() {
		if name := class.GetName(); name != "" {
			response.StorageClasses = append(response.StorageClasses, name)
		}
	}
	return response, nil
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
