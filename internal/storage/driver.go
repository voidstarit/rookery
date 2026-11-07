package storage

import (
	"context"
	"time"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/store"
)

// CreateDiskRequest is the driver-neutral create request.
type CreateDiskRequest struct {
	Disk      store.Disk
	SizeBytes int64
	RequestID string
}

// CreatedDisk is the driver-neutral create result.
type CreatedDisk struct {
	Metadata         store.DiskBackendMetadata
	Device           string
	SizeBytes        uint64
	PreparedByScript bool
}

// Health is a driver-neutral backend health result.
type Health struct {
	Reachable bool
	Ready     bool
	Detail    string
	Protocols []string
	Classes   []ClassCapacity
	CheckedAt time.Time
}

// ClassCapacity describes one storage class or pool.
type ClassCapacity struct {
	Name             string
	VGName           string
	State            string
	TotalBytes       int64
	FreeBytes        int64
	AllocatableBytes int64
	MaxVolumeBytes   int64
}

// Driver is the minimal provisioning contract.
type Driver interface {
	Kind() string
	CreateDisk(context.Context, store.StorageBackend, CreateDiskRequest) (CreatedDisk, error)
	DeleteDisk(context.Context, store.StorageBackend, store.Disk) error
	Health(context.Context, store.StorageBackend, *store.Disk) (Health, error)
}

// StartPreparer is an optional capability for remote attachments.
type StartPreparer interface {
	PrepareStart(context.Context, store.StorageBackend, store.Disk) (blockdev.Attachment, error)
}

// OrphanCleaner is an optional capability for provisioning cleanup.
type OrphanCleaner interface {
	DeleteOrphanVolume(context.Context, store.StorageBackend, store.Disk) error
}
