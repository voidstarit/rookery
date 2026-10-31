package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrLightVMKernelNotFound is returned when a LightVM kernel does not exist.
	ErrLightVMKernelNotFound = errors.New("lightvm kernel not found")
	// ErrLightVMKernelNameTaken is returned when a kernel name is already in use.
	ErrLightVMKernelNameTaken = errors.New("lightvm kernel name already exists")
	// ErrLightVMKernelInUse is returned when a LightVM still references a kernel.
	ErrLightVMKernelInUse = errors.New("lightvm kernel is used by a lightvm")
)

// LightVMKernel is an uploaded kernel image used by LightVMs.
type LightVMKernel struct {
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

const lightVMKernelSelect = `
	SELECT id, name, description, path, file_name, size_bytes, managed, created_at, updated_at
	FROM lightvm_kernels`

// ListLightVMKernels returns all kernel records ordered by name.
func (s *Store) ListLightVMKernels(ctx context.Context) ([]LightVMKernel, error) {
	rows, err := s.db.QueryContext(ctx, lightVMKernelSelect+` ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("query lightvm kernels: %w", err)
	}
	defer rows.Close()

	kernels := make([]LightVMKernel, 0)
	for rows.Next() {
		kernel, err := scanLightVMKernel(rows)
		if err != nil {
			return nil, err
		}
		kernels = append(kernels, kernel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lightvm kernels: %w", err)
	}
	return kernels, nil
}

// GetLightVMKernel returns a single kernel record by ID.
func (s *Store) GetLightVMKernel(ctx context.Context, id int64) (LightVMKernel, error) {
	row := s.db.QueryRowContext(ctx, lightVMKernelSelect+` WHERE id = ?`, id)
	kernel, err := scanLightVMKernel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LightVMKernel{}, ErrLightVMKernelNotFound
	}
	if err != nil {
		return LightVMKernel{}, err
	}
	return kernel, nil
}

// CreateLightVMKernel inserts a kernel record and returns it.
func (s *Store) CreateLightVMKernel(ctx context.Context, kernel LightVMKernel) (LightVMKernel, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO lightvm_kernels
			(name, description, path, file_name, size_bytes, managed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		kernel.Name, kernel.Description, kernel.Path, kernel.FileName,
		kernel.SizeBytes, kernel.Managed, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return LightVMKernel{}, fmt.Errorf("%w: %q", ErrLightVMKernelNameTaken, kernel.Name)
		}
		return LightVMKernel{}, fmt.Errorf("insert lightvm kernel: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return LightVMKernel{}, fmt.Errorf("read inserted lightvm kernel id: %w", err)
	}
	return s.GetLightVMKernel(ctx, id)
}

// UpdateLightVMKernel replaces the editable fields of a kernel record.
func (s *Store) UpdateLightVMKernel(ctx context.Context, kernel LightVMKernel) (LightVMKernel, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE lightvm_kernels
		SET name = ?, description = ?, path = ?, file_name = ?,
		    size_bytes = ?, managed = ?, updated_at = ?
		WHERE id = ?`,
		kernel.Name, kernel.Description, kernel.Path, kernel.FileName,
		kernel.SizeBytes, kernel.Managed, time.Now().UTC().UnixMilli(), kernel.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return LightVMKernel{}, fmt.Errorf("%w: %q", ErrLightVMKernelNameTaken, kernel.Name)
		}
		return LightVMKernel{}, fmt.Errorf("update lightvm kernel: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return LightVMKernel{}, fmt.Errorf("read updated lightvm kernel rows: %w", err)
	}
	if affected == 0 {
		return LightVMKernel{}, ErrLightVMKernelNotFound
	}
	return s.GetLightVMKernel(ctx, kernel.ID)
}

// DeleteLightVMKernel removes a kernel record and returns the deleted record so
// callers can clean up a managed file. LightVMs do not reference kernels yet.
func (s *Store) DeleteLightVMKernel(ctx context.Context, id int64) (LightVMKernel, error) {
	kernel, err := s.GetLightVMKernel(ctx, id)
	if err != nil {
		return LightVMKernel{}, err
	}
	var lightVMID string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM lightvms WHERE kernel_id = ? LIMIT 1`, id).Scan(&lightVMID)
	switch {
	case err == nil:
		return LightVMKernel{}, ErrLightVMKernelInUse
	case !errors.Is(err, sql.ErrNoRows):
		return LightVMKernel{}, fmt.Errorf("check lightvm kernel usage: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM lightvm_kernels WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return LightVMKernel{}, ErrLightVMKernelInUse
		}
		return LightVMKernel{}, fmt.Errorf("delete lightvm kernel: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return LightVMKernel{}, fmt.Errorf("read deleted lightvm kernel rows: %w", err)
	}
	if affected == 0 {
		return LightVMKernel{}, ErrLightVMKernelNotFound
	}
	return kernel, nil
}

func scanLightVMKernel(s scanner) (LightVMKernel, error) {
	var (
		kernel    LightVMKernel
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&kernel.ID,
		&kernel.Name,
		&kernel.Description,
		&kernel.Path,
		&kernel.FileName,
		&kernel.SizeBytes,
		&kernel.Managed,
		&createdAt,
		&updatedAt,
	); err != nil {
		return LightVMKernel{}, err
	}
	kernel.CreatedAt = time.UnixMilli(createdAt).UTC()
	kernel.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return kernel, nil
}
