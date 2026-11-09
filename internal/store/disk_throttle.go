package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/voidstarit/rookery/internal/diskthrottle"
)

var (
	ErrDiskThrottleClassNotFound   = errors.New("disk throttle class not found")
	ErrDiskThrottleClassNameTaken  = errors.New("disk throttle class name already exists")
	ErrDiskThrottleClassInUse      = errors.New("disk throttle class is in use")
	ErrDiskThrottleClassOnTemplate = errors.New("template disks cannot have a throttle class")
	ErrDiskThrottleClassInvalid    = errors.New("disk throttle class limits are invalid")
	ErrDiskThrottleClassInDeletion = errors.New("disk is being deleted")
)

// DiskThrottleClass is a reusable QEMU disk throttling policy.
type DiskThrottleClass struct {
	ID              int64               `json:"id"`
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	Limits          diskthrottle.Limits `json:"limits"`
	DiskCount       int                 `json:"disk_count"`
	ValidationError string              `json:"validation_error,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

const diskThrottleClassSelect = `
	SELECT dtc.id, dtc.name, dtc.description, dtc.limits,
	       dtc.created_at, dtc.updated_at,
	       (SELECT COUNT(*) FROM disks AS d WHERE d.throttle_class_id = dtc.id)
	FROM disk_throttle_classes AS dtc`

// CreateDiskThrottleClass inserts a class and returns the stored record.
func (s *Store) CreateDiskThrottleClass(ctx context.Context, class DiskThrottleClass) (DiskThrottleClass, error) {
	class.Name = strings.TrimSpace(class.Name)
	class.Description = strings.TrimSpace(class.Description)
	if err := validateDiskThrottleClassName(class.Name); err != nil {
		return DiskThrottleClass{}, err
	}
	if utf8.RuneCountInString(class.Description) > 2000 {
		return DiskThrottleClass{}, errors.New("description must be at most 2000 characters")
	}
	if err := class.Limits.Validate(); err != nil {
		return DiskThrottleClass{}, fmt.Errorf("%w: %v", ErrDiskThrottleClassInvalid, err)
	}
	encoded, err := json.Marshal(class.Limits)
	if err != nil {
		return DiskThrottleClass{}, fmt.Errorf("encode disk throttle limits: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO disk_throttle_classes (name, description, limits, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`,
		class.Name, class.Description, string(encoded), now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return DiskThrottleClass{}, fmt.Errorf("%w: %q", ErrDiskThrottleClassNameTaken, class.Name)
		}
		return DiskThrottleClass{}, fmt.Errorf("insert disk throttle class: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return DiskThrottleClass{}, fmt.Errorf("read inserted disk throttle class id: %w", err)
	}
	return s.GetDiskThrottleClass(ctx, id)
}

// GetDiskThrottleClass returns one class. Invalid stored limits are reported in
// ValidationError rather than failing the read.
func (s *Store) GetDiskThrottleClass(ctx context.Context, id int64) (DiskThrottleClass, error) {
	row := s.db.QueryRowContext(ctx, diskThrottleClassSelect+` WHERE dtc.id = ?`, id)
	class, err := scanDiskThrottleClass(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DiskThrottleClass{}, ErrDiskThrottleClassNotFound
	}
	if err != nil {
		return DiskThrottleClass{}, err
	}
	return class, nil
}

// ListDiskThrottleClasses returns classes ordered by name.
func (s *Store) ListDiskThrottleClasses(ctx context.Context) ([]DiskThrottleClass, error) {
	rows, err := s.db.QueryContext(ctx, diskThrottleClassSelect+`
		ORDER BY dtc.name COLLATE NOCASE, dtc.id`)
	if err != nil {
		return nil, fmt.Errorf("query disk throttle classes: %w", err)
	}
	defer rows.Close()
	classes := make([]DiskThrottleClass, 0)
	for rows.Next() {
		class, err := scanDiskThrottleClass(rows)
		if err != nil {
			return nil, err
		}
		classes = append(classes, class)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate disk throttle classes: %w", err)
	}
	return classes, nil
}

// UpdateDiskThrottleClass replaces a class after validating its limits.
func (s *Store) UpdateDiskThrottleClass(ctx context.Context, class DiskThrottleClass) (DiskThrottleClass, error) {
	class.Name = strings.TrimSpace(class.Name)
	class.Description = strings.TrimSpace(class.Description)
	if err := validateDiskThrottleClassName(class.Name); err != nil {
		return DiskThrottleClass{}, err
	}
	if utf8.RuneCountInString(class.Description) > 2000 {
		return DiskThrottleClass{}, errors.New("description must be at most 2000 characters")
	}
	if err := class.Limits.Validate(); err != nil {
		return DiskThrottleClass{}, fmt.Errorf("%w: %v", ErrDiskThrottleClassInvalid, err)
	}
	encoded, err := json.Marshal(class.Limits)
	if err != nil {
		return DiskThrottleClass{}, fmt.Errorf("encode disk throttle limits: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE disk_throttle_classes
		SET name = ?, description = ?, limits = ?, updated_at = ?
		WHERE id = ?`,
		class.Name, class.Description, string(encoded), time.Now().UTC().UnixMilli(), class.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return DiskThrottleClass{}, fmt.Errorf("%w: %q", ErrDiskThrottleClassNameTaken, class.Name)
		}
		return DiskThrottleClass{}, fmt.Errorf("update disk throttle class: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return DiskThrottleClass{}, fmt.Errorf("read updated disk throttle class rows: %w", err)
	}
	if affected == 0 {
		return DiskThrottleClass{}, ErrDiskThrottleClassNotFound
	}
	return s.GetDiskThrottleClass(ctx, class.ID)
}

// DeleteDiskThrottleClass deletes an unused class.
func (s *Store) DeleteDiskThrottleClass(ctx context.Context, id int64) error {
	class, err := s.GetDiskThrottleClass(ctx, id)
	if err != nil {
		return err
	}
	if class.DiskCount > 0 {
		return ErrDiskThrottleClassInUse
	}
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM disk_throttle_classes
		WHERE id = ?
		  AND NOT EXISTS (SELECT 1 FROM disks WHERE throttle_class_id = ?)`, id, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrDiskThrottleClassInUse
		}
		return fmt.Errorf("delete disk throttle class: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted disk throttle class rows: %w", err)
	}
	if affected == 0 {
		if _, err := s.GetDiskThrottleClass(ctx, id); errors.Is(err, ErrDiskThrottleClassNotFound) {
			return ErrDiskThrottleClassNotFound
		}
		return ErrDiskThrottleClassInUse
	}
	return nil
}

// ListDiskNamesByThrottleClass returns up to limit assigned disk names.
func (s *Store) ListDiskNamesByThrottleClass(ctx context.Context, classID int64, limit int) ([]string, error) {
	if limit < 1 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT name FROM disks
		WHERE throttle_class_id = ?
		ORDER BY name COLLATE NOCASE, id
		LIMIT ?`, classID, limit)
	if err != nil {
		return nil, fmt.Errorf("query disks for throttle class: %w", err)
	}
	defer rows.Close()
	names := make([]string, 0, limit)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate disks for throttle class: %w", err)
	}
	return names, nil
}

// SetDiskThrottleClass assigns or clears a disk's throttling class.
func (s *Store) SetDiskThrottleClass(ctx context.Context, diskID int64, classID *int64) (Disk, error) {
	if classID != nil && *classID == 0 {
		classID = nil
	}
	now := time.Now().UTC().UnixMilli()
	if classID == nil {
		res, err := s.db.ExecContext(ctx, `
			UPDATE disks SET throttle_class_id = NULL, updated_at = ? WHERE id = ?`,
			now, diskID)
		if err != nil {
			return Disk{}, fmt.Errorf("clear disk throttle class: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return Disk{}, fmt.Errorf("read cleared disk throttle rows: %w", err)
		}
		if affected == 0 {
			return Disk{}, ErrDiskNotFound
		}
		return s.GetDisk(ctx, diskID)
	}
	if *classID < 0 {
		return Disk{}, ErrDiskThrottleClassInvalid
	}
	class, err := s.GetDiskThrottleClass(ctx, *classID)
	if err != nil {
		if errors.Is(err, ErrDiskThrottleClassNotFound) {
			return Disk{}, ErrDiskThrottleClassNotFound
		}
		return Disk{}, err
	}
	if class.ValidationError != "" {
		return Disk{}, fmt.Errorf("%w: %s", ErrDiskThrottleClassInvalid, class.ValidationError)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE disks
		SET throttle_class_id = ?, updated_at = ?
		WHERE id = ? AND is_template = 0
		  AND deletion_state NOT IN (?, ?)`,
		*classID, now, diskID, DiskDeletionDeleting, DiskDeletionFailed)
	if err != nil {
		if isForeignKeyViolation(err) {
			return Disk{}, ErrDiskThrottleClassNotFound
		}
		return Disk{}, fmt.Errorf("assign disk throttle class: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Disk{}, fmt.Errorf("read assigned disk throttle rows: %w", err)
	}
	if affected == 0 {
		disk, err := s.GetDisk(ctx, diskID)
		if err != nil {
			return Disk{}, err
		}
		if disk.IsTemplate {
			return Disk{}, ErrDiskThrottleClassOnTemplate
		}
		if disk.DeletionState == DiskDeletionDeleting || disk.DeletionState == DiskDeletionFailed {
			return Disk{}, ErrDiskThrottleClassInDeletion
		}
		return Disk{}, ErrDiskNotFound
	}
	return s.GetDisk(ctx, diskID)
}

func scanDiskThrottleClass(s scanner) (DiskThrottleClass, error) {
	var (
		class     DiskThrottleClass
		rawLimits string
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(&class.ID, &class.Name, &class.Description, &rawLimits,
		&createdAt, &updatedAt, &class.DiskCount); err != nil {
		return DiskThrottleClass{}, err
	}
	class.Limits, class.ValidationError = decodeDiskThrottleLimits(rawLimits)
	class.CreatedAt = time.UnixMilli(createdAt).UTC()
	class.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return class, nil
}

func decodeDiskThrottleLimits(raw string) (diskthrottle.Limits, string) {
	var limits diskthrottle.Limits
	if err := json.Unmarshal([]byte(raw), &limits); err != nil {
		return diskthrottle.Limits{}, fmt.Sprintf("decode limits: %v", err)
	}
	if err := limits.Validate(); err != nil {
		return diskthrottle.Limits{}, err.Error()
	}
	return limits, ""
}

func validateDiskThrottleClassName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if utf8.RuneCountInString(name) > 127 {
		return errors.New("name must be at most 127 characters")
	}
	for i, r := range name {
		if i == 0 {
			if !isASCIILetterOrDigit(r) {
				return errors.New("name must start with a letter or digit")
			}
			continue
		}
		if !isASCIILetterOrDigit(r) && r != '.' && r != '_' && r != '-' {
			return errors.New("name contains an invalid character")
		}
	}
	return nil
}

func isASCIILetterOrDigit(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
