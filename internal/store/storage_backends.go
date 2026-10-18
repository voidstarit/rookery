package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// StorageBackendConfig contains driver-specific settings.
type StorageBackendConfig struct {
	VG       string `json:"vg,omitempty"`
	ThinPool string `json:"thin_pool,omitempty"`
}

// StorageBackend is a registered disk provisioning backend.
type StorageBackend struct {
	ID          int64                `json:"id"`
	Name        string               `json:"name"`
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Driver      string               `json:"driver"`
	Config      StorageBackendConfig `json:"config"`
	DiskCount   int                  `json:"disk_count"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

const storageBackendSelect = `
	SELECT b.id, b.name, b.title, b.description, b.driver, b.config,
	       (SELECT COUNT(*) FROM disks AS d WHERE d.storage_backend_id = b.id),
	       b.created_at, b.updated_at
	FROM storage_backends AS b`

// CreateStorageBackend inserts a storage backend.
func (s *Store) CreateStorageBackend(ctx context.Context, backend StorageBackend) (StorageBackend, error) {
	config, err := json.Marshal(backend.Config)
	if err != nil {
		return StorageBackend{}, fmt.Errorf("encode storage backend config: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO storage_backends (name, title, description, driver, config, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		backend.Name, backend.Title, backend.Description, backend.Driver, string(config), now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return StorageBackend{}, fmt.Errorf("%w: %q", ErrStorageBackendNameTaken, backend.Name)
		}
		return StorageBackend{}, fmt.Errorf("insert storage backend: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return StorageBackend{}, fmt.Errorf("read inserted storage backend id: %w", err)
	}
	return s.GetStorageBackend(ctx, id)
}

// ListStorageBackends returns all storage backends ordered by name.
func (s *Store) ListStorageBackends(ctx context.Context) ([]StorageBackend, error) {
	rows, err := s.db.QueryContext(ctx, storageBackendSelect+` ORDER BY b.name COLLATE NOCASE, b.id`)
	if err != nil {
		return nil, fmt.Errorf("query storage backends: %w", err)
	}
	defer rows.Close()
	backends := make([]StorageBackend, 0)
	for rows.Next() {
		backend, err := scanStorageBackend(rows)
		if err != nil {
			return nil, err
		}
		backends = append(backends, backend)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate storage backends: %w", err)
	}
	return backends, nil
}

// GetStorageBackend returns a storage backend by ID.
func (s *Store) GetStorageBackend(ctx context.Context, id int64) (StorageBackend, error) {
	row := s.db.QueryRowContext(ctx, storageBackendSelect+` WHERE b.id = ?`, id)
	backend, err := scanStorageBackend(row)
	if errors.Is(err, sql.ErrNoRows) {
		return StorageBackend{}, ErrStorageBackendNotFound
	}
	if err != nil {
		return StorageBackend{}, err
	}
	return backend, nil
}

// GetStorageBackendByName returns a storage backend by name.
func (s *Store) GetStorageBackendByName(ctx context.Context, name string) (StorageBackend, error) {
	row := s.db.QueryRowContext(ctx, storageBackendSelect+` WHERE b.name = ? COLLATE NOCASE`, name)
	backend, err := scanStorageBackend(row)
	if errors.Is(err, sql.ErrNoRows) {
		return StorageBackend{}, ErrStorageBackendNotFound
	}
	if err != nil {
		return StorageBackend{}, err
	}
	return backend, nil
}

// UpdateStorageBackend updates a storage backend's fields and config.
func (s *Store) UpdateStorageBackend(ctx context.Context, backend StorageBackend) (StorageBackend, error) {
	config, err := json.Marshal(backend.Config)
	if err != nil {
		return StorageBackend{}, fmt.Errorf("encode storage backend config: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE storage_backends
		SET name = ?, title = ?, description = ?, driver = ?, config = ?, updated_at = ?
		WHERE id = ?`,
		backend.Name, backend.Title, backend.Description, backend.Driver, string(config), now, backend.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return StorageBackend{}, fmt.Errorf("%w: %q", ErrStorageBackendNameTaken, backend.Name)
		}
		return StorageBackend{}, fmt.Errorf("update storage backend: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return StorageBackend{}, fmt.Errorf("read updated storage backend rows: %w", err)
	}
	if affected == 0 {
		return StorageBackend{}, ErrStorageBackendNotFound
	}
	return s.GetStorageBackend(ctx, backend.ID)
}

// DeleteStorageBackend removes a backend if it has no disks.
func (s *Store) DeleteStorageBackend(ctx context.Context, id int64) error {
	var diskID int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM disks WHERE storage_backend_id = ? LIMIT 1`, id).Scan(&diskID)
	switch {
	case err == nil:
		return ErrStorageBackendInUse
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check storage backend usage: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM storage_backends WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrStorageBackendInUse
		}
		return fmt.Errorf("delete storage backend: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted storage backend rows: %w", err)
	}
	if affected == 0 {
		return ErrStorageBackendNotFound
	}
	return nil
}

// EnsureDefaultStorageBackend creates the bootstrap LVM backend if necessary
// and associates existing disks that predate backend records.
func (s *Store) EnsureDefaultStorageBackend(ctx context.Context, vg, thinPool string) (StorageBackend, error) {
	backend, err := s.GetStorageBackendByName(ctx, "default")
	if errors.Is(err, ErrStorageBackendNotFound) {
		backend, err = s.CreateStorageBackend(ctx, StorageBackend{
			Name:        "default",
			Title:       "Default LVM thin pool",
			Description: "Bootstrapped from the API -vg and -thin-pool flags",
			Driver:      "lvm-thin",
			Config:      StorageBackendConfig{VG: vg, ThinPool: thinPool},
		})
	}
	if err != nil {
		return StorageBackend{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE disks SET storage_backend_id = ?
		WHERE storage_backend_id IS NULL AND vg = ? AND thin_pool = ?`,
		backend.ID, vg, thinPool,
	); err != nil {
		return StorageBackend{}, fmt.Errorf("associate existing disks with storage backend: %w", err)
	}
	return backend, nil
}

func scanStorageBackend(s scanner) (StorageBackend, error) {
	var (
		backend   StorageBackend
		config    string
		diskCount int64
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&backend.ID, &backend.Name, &backend.Title, &backend.Description,
		&backend.Driver, &config, &diskCount, &createdAt, &updatedAt,
	); err != nil {
		return StorageBackend{}, err
	}
	if err := json.Unmarshal([]byte(config), &backend.Config); err != nil {
		return StorageBackend{}, fmt.Errorf("parse storage backend config: %w", err)
	}
	backend.DiskCount = int(diskCount)
	backend.CreatedAt = time.UnixMilli(createdAt).UTC()
	backend.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return backend, nil
}
