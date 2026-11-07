package storage

import (
	"context"
	"fmt"
	"strings"

	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/kouja"
	"github.com/voidstarit/rookery/internal/store"
)

// KoujaDriver provisions disks through a kouja agent.
type KoujaDriver struct {
	newClient func(kouja.Config) (*kouja.Client, error)
}

// NewKoujaDriver creates a kouja driver.
func NewKoujaDriver() *KoujaDriver {
	return &KoujaDriver{newClient: kouja.NewClient}
}

// Kind identifies the kouja driver.
func (d *KoujaDriver) Kind() string { return store.DiskSourceKouja }

// CreateDisk creates and exports a kouja volume.
func (d *KoujaDriver) CreateDisk(ctx context.Context, backend store.StorageBackend, request CreateDiskRequest) (CreatedDisk, error) {
	cfg, err := kouja.ParseConfig(backend.Config)
	if err != nil {
		return CreatedDisk{}, err
	}
	client, err := d.client(cfg)
	if err != nil {
		return CreatedDisk{}, err
	}
	defer client.Close()
	volumeID := strings.TrimSpace(request.Disk.BackendMetadata.Volume.VolumeID)
	if volumeID == "" {
		return CreatedDisk{}, fmt.Errorf("kouja disk has no volume ID")
	}
	sizeBytes := request.SizeBytes
	if sizeBytes <= 0 {
		sizeBytes = int64(request.Disk.SizeGB) << 30
	}
	requestID := strings.TrimSpace(request.RequestID)
	if requestID == "" {
		requestID = "create-" + volumeID
	}
	provisioner := &kouja.Provisioner{API: client}
	created, err := provisioner.Create(ctx, cfg, kouja.CreateRequest{
		VolumeID: volumeID, RequestID: requestID, SizeBytes: sizeBytes,
	})
	if err != nil {
		return CreatedDisk{}, err
	}
	actual := int64(created.ActualSizeBytes)
	if actual <= 0 {
		actual = sizeBytes
	}
	metadata := store.DiskBackendMetadata{
		Version: store.DiskBackendMetadataVersion,
		Driver:  store.DiskSourceKouja,
		Volume: store.VolumeMetadata{
			VolumeID:     volumeID,
			RequestID:    requestID,
			ResourceID:   created.ResourceID,
			Owner:        created.Owner,
			StorageClass: cfg.StorageClass,
		},
		Attachment: created.Reference,
	}
	return CreatedDisk{Metadata: metadata, SizeBytes: uint64(actual)}, nil
}

// DeleteDisk unexports and deletes a kouja volume.
func (d *KoujaDriver) DeleteDisk(ctx context.Context, backend store.StorageBackend, disk store.Disk) error {
	cfg, err := kouja.ParseConfig(backend.Config)
	if err != nil {
		return err
	}
	client, err := d.client(cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	volumeID := strings.TrimSpace(disk.BackendMetadata.Volume.VolumeID)
	if volumeID == "" {
		return fmt.Errorf("kouja disk has no volume ID")
	}
	unexport := koujav1UnexportRequest(volumeID)
	if _, err := client.UnexportVolume(ctx, &unexport); err != nil && !kouja.IsNotFound(err) {
		return err
	}
	deleteRequest := koujav1DeleteRequest(volumeID)
	if _, err := client.DeleteVolume(ctx, &deleteRequest); err != nil {
		if kouja.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// DeleteOrphanVolume deletes a provisioning volume without unexport.
func (d *KoujaDriver) DeleteOrphanVolume(ctx context.Context, backend store.StorageBackend, disk store.Disk) error {
	cfg, err := kouja.ParseConfig(backend.Config)
	if err != nil {
		return err
	}
	client, err := d.client(cfg)
	if err != nil {
		return err
	}
	defer client.Close()
	volumeID := strings.TrimSpace(disk.BackendMetadata.Volume.VolumeID)
	if volumeID == "" {
		return nil
	}
	deleteRequest := koujav1DeleteRequest(volumeID)
	if _, err := client.DeleteVolume(ctx, &deleteRequest); err != nil {
		if kouja.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// PrepareStart fetches the live export and credential for a VM start.
func (d *KoujaDriver) PrepareStart(ctx context.Context, backend store.StorageBackend, disk store.Disk) (blockdev.Attachment, error) {
	cfg, err := kouja.ParseConfig(backend.Config)
	if err != nil {
		return blockdev.Attachment{}, err
	}
	client, err := d.client(cfg)
	if err != nil {
		return blockdev.Attachment{}, err
	}
	defer client.Close()
	preparer := &kouja.Preparer{API: client}
	return preparer.Prepare(ctx, cfg, disk)
}

// Health checks the kouja endpoint and optionally an existing disk.
func (d *KoujaDriver) Health(ctx context.Context, backend store.StorageBackend, sample *store.Disk) (Health, error) {
	cfg, err := kouja.ParseConfig(backend.Config)
	if err != nil {
		return Health{}, err
	}
	client, err := d.client(cfg)
	if err != nil {
		return Health{}, err
	}
	defer client.Close()
	checker := &kouja.Checker{API: client}
	health, err := checker.Check(ctx, cfg, sample)
	if err != nil {
		return Health{}, err
	}
	result := Health{
		Reachable: health.Reachable, Ready: health.Ready, Detail: health.Detail,
		Protocols: health.Protocols, CheckedAt: health.CheckedAt,
	}
	for _, class := range health.Classes {
		result.Classes = append(result.Classes, ClassCapacity{
			Name: class.Name, VGName: class.VGName, State: class.State,
			TotalBytes: class.TotalBytes, FreeBytes: class.FreeBytes,
			AllocatableBytes: class.AllocatableBytes, MaxVolumeBytes: class.MaxVolumeBytes,
		})
	}
	return result, nil
}

func (d *KoujaDriver) client(cfg kouja.Config) (*kouja.Client, error) {
	if d != nil && d.newClient != nil {
		return d.newClient(cfg)
	}
	return kouja.NewClient(cfg)
}

func koujav1UnexportRequest(volumeID string) koujav1.UnexportVolumeRequest {
	return koujav1.UnexportVolumeRequest{
		VolumeId: volumeID, Force: false, RequestId: "unexport-" + volumeID,
	}
}

func koujav1DeleteRequest(volumeID string) koujav1.DeleteVolumeRequest {
	return koujav1.DeleteVolumeRequest{
		VolumeId: volumeID, Force: false, RequestId: "delete-" + volumeID,
	}
}
