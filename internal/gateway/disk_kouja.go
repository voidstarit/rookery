package gateway

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/storage"
	"github.com/voidstarit/rookery/internal/store"
)

func (s *diskService) createKoujaDisk(ctx context.Context, request *rookeryv1.CreateDiskRequest, name string, backend store.StorageBackend, sizeGB int) (*rookeryv1.Disk, error) {
	if request.GetIsTemplate() || request.TemplateDiskId != nil {
		return nil, status.Error(codes.InvalidArgument, "kouja disks cannot be templates or template clones")
	}
	if strings.TrimSpace(request.GetProvisioningScript()) != "" ||
		strings.TrimSpace(request.GetProvisioningTimeout()) != "" || request.Provisioning != nil {
		return nil, status.Error(codes.InvalidArgument, "kouja disks do not support provisioning settings")
	}
	if s.registry == nil {
		return nil, status.Error(codes.Unavailable, "storage registry is not configured")
	}
	driver, ok := s.registry.Driver(backend.Driver)
	if !ok {
		return nil, status.Error(codes.Unavailable, "kouja storage driver is not configured")
	}
	volumeID := uuid.NewString()
	requestID := "create-" + volumeID
	protocol := blockdev.ProtocolISCSI
	storageClass := ""
	if backend.Config.Kouja != nil {
		if strings.TrimSpace(backend.Config.Kouja.Protocol) == "nvme-tcp" {
			protocol = blockdev.ProtocolNVMeTCP
		}
		storageClass = backend.Config.Kouja.StorageClass
	}
	readBPS, writeBPS := requestDiskBandwidth(request)
	disk := store.Disk{
		Name: name, SizeGB: sizeGB, ReadBPS: readBPS, WriteBPS: writeBPS,
		StorageBackendID: &backend.ID, StorageBackendName: backend.Name,
		Source: store.DiskSourceKouja, Protocol: string(protocol),
		BackendMetadata: store.DiskBackendMetadata{
			Version: store.DiskBackendMetadataVersion, Driver: store.DiskSourceKouja,
			Volume:     store.VolumeMetadata{VolumeID: volumeID, RequestID: requestID, StorageClass: storageClass},
			Attachment: blockdev.Reference{Protocol: protocol},
		},
		DeletionState:         store.DiskDeletionProvisioning,
		DeletionNextAttemptAt: time.Now().UTC().Add(20 * time.Minute),
	}

	s.mu.Lock()
	if _, err := s.store.GetDiskByName(ctx, name); err == nil {
		s.mu.Unlock()
		return nil, status.Error(codes.AlreadyExists, "a disk with that name already exists")
	} else if !errors.Is(err, store.ErrDiskNotFound) {
		s.mu.Unlock()
		return nil, status.Error(codes.Internal, "could not check disk name")
	}
	created, err := s.store.CreateDisk(ctx, disk)
	s.mu.Unlock()
	if err != nil {
		if errors.Is(err, store.ErrDiskNameTaken) {
			return nil, status.Error(codes.AlreadyExists, "a disk with that name already exists")
		}
		return nil, status.Error(codes.Internal, "could not record disk")
	}

	result, err := driver.CreateDisk(ctx, backend, storage.CreateDiskRequest{
		Disk: created, SizeBytes: int64(sizeGB) << 30, RequestID: requestID,
	})
	if err != nil {
		s.queueKoujaCleanup(ctx, created.ID)
		return nil, status.Error(codes.Internal, "could not create kouja volume: "+err.Error())
	}
	finalized, ok, err := s.store.FinalizeProvisioning(ctx, created.ID, result.Metadata, result.SizeBytes, result.Device, result.PreparedByScript)
	if err != nil {
		s.queueKoujaCleanup(ctx, created.ID)
		return nil, status.Error(codes.Internal, "could not finalize kouja disk: "+err.Error())
	}
	if !ok {
		if current, readErr := s.store.GetDisk(ctx, created.ID); readErr == nil && current.DeletionState != "" {
			return nil, status.Error(codes.Aborted, "disk was queued for cleanup while provisioning")
		}
		return nil, status.Error(codes.Internal, "could not finalize kouja disk")
	}
	return diskToProto(finalized), nil
}

func (s *diskService) queueKoujaCleanup(ctx context.Context, id int64) {
	if err := s.store.MarkDiskDeleting(ctx, id); err != nil && !errors.Is(err, store.ErrDiskNotFound) {
		s.logger.Warn().Err(err).Int64("disk_id", id).Msg("could not queue kouja cleanup")
	}
}
