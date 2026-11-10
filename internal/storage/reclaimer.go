package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/kouja"
	"github.com/voidstarit/rookery/internal/store"
)

const (
	reclaimerInterval = 30 * time.Second
	reclaimerBatch    = 20
	reclaimerMaxTries = 8
)

// Reclaimer retries remote disk deletions.
type Reclaimer struct {
	store    *store.Store
	registry *Registry
	logger   zerolog.Logger
	now      func() time.Time
}

// NewReclaimer creates a deletion worker.
func NewReclaimer(st *store.Store, registry *Registry, logger zerolog.Logger) *Reclaimer {
	return &Reclaimer{store: st, registry: registry, logger: logger, now: time.Now}
}

// Run processes due deletions until ctx is cancelled.
func (r *Reclaimer) Run(ctx context.Context) {
	ticker := time.NewTicker(reclaimerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = r.Tick(ctx)
		}
	}
}

// Tick processes one batch of due deletions.
func (r *Reclaimer) Tick(ctx context.Context) (int, error) {
	if r == nil || r.store == nil || r.registry == nil {
		return 0, errors.New("disk reclaimer is not configured")
	}
	now := r.now().UTC()
	disks, err := r.store.ClaimDueDiskDeletions(ctx, now, reclaimerBatch)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, disk := range disks {
		if store.DiskAttachedToAnyWorkload(disk) {
			_ = r.store.ReleaseDiskDeletionClaim(ctx, disk.ID)
			continue
		}
		processed++
		if err := r.process(ctx, disk); err != nil {
			r.logger.Warn().Err(err).Int64("disk_id", disk.ID).Str("disk", disk.Name).Msg("disk deletion attempt failed")
		}
	}
	return processed, nil
}

func (r *Reclaimer) process(ctx context.Context, disk store.Disk) error {
	if disk.StorageBackendID == nil {
		return r.fail(ctx, disk, "disk has no storage backend")
	}
	backend, err := r.store.GetStorageBackend(ctx, *disk.StorageBackendID)
	if err != nil {
		return r.fail(ctx, disk, "could not read storage backend: "+err.Error())
	}
	driver, ok := r.registry.Driver(backend.Driver)
	if !ok {
		return r.fail(ctx, disk, fmt.Sprintf("storage driver %q is not available", backend.Driver))
	}
	var deleteErr error
	if disk.DeletionState == store.DiskDeletionProvisioning {
		if cleaner, ok := driver.(OrphanCleaner); ok {
			deleteErr = cleaner.DeleteOrphanVolume(ctx, backend, disk)
		} else {
			deleteErr = driver.DeleteDisk(ctx, backend, disk)
		}
	} else {
		deleteErr = driver.DeleteDisk(ctx, backend, disk)
	}
	if deleteErr == nil {
		if err := r.store.DeleteDisk(ctx, disk.ID); err != nil && !errors.Is(err, store.ErrDiskNotFound) {
			return err
		}
		return nil
	}
	switch kouja.ClassifyCleanup(deleteErr) {
	case kouja.CleanupSuccess:
		if err := r.store.DeleteDisk(ctx, disk.ID); err != nil && !errors.Is(err, store.ErrDiskNotFound) {
			return err
		}
		return nil
	case kouja.CleanupRetry:
		attempts := disk.DeletionAttempts + 1
		if attempts >= reclaimerMaxTries {
			return r.fail(ctx, disk, deleteErr.Error())
		}
		next := r.now().UTC().Add(deletionBackoff(attempts))
		if err := r.store.RecordDiskDeletionFailure(ctx, disk.ID, attempts, next, deleteErr.Error()); err != nil {
			return err
		}
		return deleteErr
	default:
		return r.fail(ctx, disk, deleteErr.Error())
	}
}

func (r *Reclaimer) fail(ctx context.Context, disk store.Disk, message string) error {
	err := r.store.SetDiskDeleteFailed(ctx, disk.ID, message)
	if err != nil {
		return err
	}
	r.logger.Error().Int64("disk_id", disk.ID).Str("disk", disk.Name).Str("detail", message).Msg("disk deletion failed permanently")
	return errors.New(message)
}

func deletionBackoff(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return 30 * time.Second
	case attempt == 2:
		return time.Minute
	case attempt == 3:
		return 5 * time.Minute
	case attempt == 4:
		return 15 * time.Minute
	case attempt == 5:
		return 30 * time.Minute
	default:
		return time.Hour
	}
}
