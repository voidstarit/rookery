package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrLightVMInitramfsNotFound is returned when an initramfs record does not exist.
	ErrLightVMInitramfsNotFound = errors.New("lightvm initramfs not found")
	// ErrLightVMInitramfsNameTaken is returned when an initramfs name is already in use.
	ErrLightVMInitramfsNameTaken = errors.New("lightvm initramfs name already exists")
	// ErrLightVMInitramfsInUse is returned when a LightVM still references an initramfs.
	ErrLightVMInitramfsInUse = errors.New("lightvm initramfs is used by a lightvm")
)

// LightVMInitramfs is an uploaded initramfs image used by LightVMs.
type LightVMInitramfs struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Path        string    `json:"path"`
	FileName    string    `json:"file_name"`
	SizeBytes   int64     `json:"size_bytes"`
	Managed     bool      `json:"managed"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const lightVMInitramfsSelect = `
	SELECT id, name, description, path, file_name, size_bytes, managed, created_at, updated_at
	FROM lightvm_initramfs`

// ListLightVMInitramfs returns all initramfs records ordered by name.
func (s *Store) ListLightVMInitramfs(ctx context.Context) ([]LightVMInitramfs, error) {
	rows, err := s.db.QueryContext(ctx, lightVMInitramfsSelect+` ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("query lightvm initramfs: %w", err)
	}
	defer rows.Close()

	images := make([]LightVMInitramfs, 0)
	for rows.Next() {
		image, err := scanLightVMInitramfs(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, image)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lightvm initramfs: %w", err)
	}
	return images, nil
}

// GetLightVMInitramfs returns a single initramfs record by ID.
func (s *Store) GetLightVMInitramfs(ctx context.Context, id int64) (LightVMInitramfs, error) {
	row := s.db.QueryRowContext(ctx, lightVMInitramfsSelect+` WHERE id = ?`, id)
	image, err := scanLightVMInitramfs(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LightVMInitramfs{}, ErrLightVMInitramfsNotFound
	}
	if err != nil {
		return LightVMInitramfs{}, err
	}
	return image, nil
}

// CreateLightVMInitramfs inserts an initramfs record and returns it.
func (s *Store) CreateLightVMInitramfs(ctx context.Context, image LightVMInitramfs) (LightVMInitramfs, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO lightvm_initramfs
			(name, description, path, file_name, size_bytes, managed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		image.Name, image.Description, image.Path, image.FileName,
		image.SizeBytes, image.Managed, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return LightVMInitramfs{}, fmt.Errorf("%w: %q", ErrLightVMInitramfsNameTaken, image.Name)
		}
		return LightVMInitramfs{}, fmt.Errorf("insert lightvm initramfs: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return LightVMInitramfs{}, fmt.Errorf("read inserted lightvm initramfs id: %w", err)
	}
	return s.GetLightVMInitramfs(ctx, id)
}

// UpdateLightVMInitramfs replaces the editable fields of an initramfs record.
func (s *Store) UpdateLightVMInitramfs(ctx context.Context, image LightVMInitramfs) (LightVMInitramfs, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE lightvm_initramfs
		SET name = ?, description = ?, path = ?, file_name = ?,
		    size_bytes = ?, managed = ?, updated_at = ?
		WHERE id = ?`,
		image.Name, image.Description, image.Path, image.FileName,
		image.SizeBytes, image.Managed, time.Now().UTC().UnixMilli(), image.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return LightVMInitramfs{}, fmt.Errorf("%w: %q", ErrLightVMInitramfsNameTaken, image.Name)
		}
		return LightVMInitramfs{}, fmt.Errorf("update lightvm initramfs: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return LightVMInitramfs{}, fmt.Errorf("read updated lightvm initramfs rows: %w", err)
	}
	if affected == 0 {
		return LightVMInitramfs{}, ErrLightVMInitramfsNotFound
	}
	return s.GetLightVMInitramfs(ctx, image.ID)
}

// DeleteLightVMInitramfs removes an initramfs record and returns the deleted
// record so callers can clean up a managed file. LightVMs do not reference
// initramfs records yet.
func (s *Store) DeleteLightVMInitramfs(ctx context.Context, id int64) (LightVMInitramfs, error) {
	image, err := s.GetLightVMInitramfs(ctx, id)
	if err != nil {
		return LightVMInitramfs{}, err
	}
	var lightVMID string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM lightvms WHERE initramfs_id = ? LIMIT 1`, id).Scan(&lightVMID)
	switch {
	case err == nil:
		return LightVMInitramfs{}, ErrLightVMInitramfsInUse
	case !errors.Is(err, sql.ErrNoRows):
		return LightVMInitramfs{}, fmt.Errorf("check lightvm initramfs usage: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM lightvm_initramfs WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return LightVMInitramfs{}, ErrLightVMInitramfsInUse
		}
		return LightVMInitramfs{}, fmt.Errorf("delete lightvm initramfs: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return LightVMInitramfs{}, fmt.Errorf("read deleted lightvm initramfs rows: %w", err)
	}
	if affected == 0 {
		return LightVMInitramfs{}, ErrLightVMInitramfsNotFound
	}
	return image, nil
}

func scanLightVMInitramfs(s scanner) (LightVMInitramfs, error) {
	var (
		image     LightVMInitramfs
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&image.ID,
		&image.Name,
		&image.Description,
		&image.Path,
		&image.FileName,
		&image.SizeBytes,
		&image.Managed,
		&createdAt,
		&updatedAt,
	); err != nil {
		return LightVMInitramfs{}, err
	}
	image.CreatedAt = time.UnixMilli(createdAt).UTC()
	image.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return image, nil
}
