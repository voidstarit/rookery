package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/voidstarit/rookery/internal/blockdev"
)

const (
	// DiskDeletionClaimLease is longer than any single delete RPC.
	DiskDeletionClaimLease = 10 * time.Minute
)

// MarkDiskDeleting queues a disk for deletion.
func (s *Store) MarkDiskDeleting(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE disks
		SET deletion_state = ?, deletion_error = '', deletion_attempts = 0,
		    deletion_next_attempt_at = ?, deletion_claimed_at = 0, updated_at = ?
		WHERE id = ?`,
		DiskDeletionDeleting, now.UnixMilli(), now.UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("mark disk deleting: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read marked disk rows: %w", err)
	}
	if affected == 0 {
		return ErrDiskNotFound
	}
	return nil
}

// SetDiskDeleteFailed records a terminal deletion failure.
func (s *Store) SetDiskDeleteFailed(ctx context.Context, id int64, message string) error {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE disks
		SET deletion_state = ?, deletion_error = ?, deletion_next_attempt_at = 0,
		    deletion_claimed_at = 0, updated_at = ?
		WHERE id = ?`,
		DiskDeletionFailed, message, now, id,
	)
	if err != nil {
		return fmt.Errorf("set disk delete failed: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read failed disk rows: %w", err)
	}
	if affected == 0 {
		return ErrDiskNotFound
	}
	return nil
}

// RecordDiskDeletionFailure schedules another delete attempt.
func (s *Store) RecordDiskDeletionFailure(ctx context.Context, id int64, attempts int, next time.Time, message string) error {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE disks
		SET deletion_state = ?, deletion_attempts = ?, deletion_next_attempt_at = ?,
		    deletion_error = ?, deletion_claimed_at = 0, updated_at = ?
		WHERE id = ?`,
		DiskDeletionDeleting, attempts, next.UTC().UnixMilli(), message, now, id,
	)
	if err != nil {
		return fmt.Errorf("record disk deletion failure: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deletion failure rows: %w", err)
	}
	if affected == 0 {
		return ErrDiskNotFound
	}
	return nil
}

// ResetDiskDeletion resets a failed disk to the queued state.
func (s *Store) ResetDiskDeletion(ctx context.Context, id int64) error {
	return s.MarkDiskDeleting(ctx, id)
}

// ReleaseDiskDeletionClaim releases a worker claim without changing state.
func (s *Store) ReleaseDiskDeletionClaim(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE disks SET deletion_claimed_at = 0 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("release disk deletion claim: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read released claim rows: %w", err)
	}
	if affected == 0 {
		return ErrDiskNotFound
	}
	return nil
}

// ClaimDueDiskDeletions atomically claims due provisioning/deleting rows.
func (s *Store) ClaimDueDiskDeletions(ctx context.Context, now time.Time, limit int) ([]Disk, error) {
	if limit < 1 {
		return nil, nil
	}
	now = now.UTC()
	nowMS := now.UnixMilli()
	staleBefore := now.Add(-DiskDeletionClaimLease).UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin claim disk deletions: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, diskSelect+`
		WHERE d.deletion_state IN (?, ?)
		  AND d.deletion_next_attempt_at <= ?
		  AND (d.deletion_claimed_at = 0 OR d.deletion_claimed_at < ?)
		ORDER BY d.deletion_next_attempt_at, d.id
		LIMIT ?`,
		DiskDeletionProvisioning, DiskDeletionDeleting, nowMS, staleBefore, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query due disk deletions: %w", err)
	}
	disks := make([]Disk, 0, limit)
	for rows.Next() {
		disk, err := scanDisk(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		disks = append(disks, disk)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate due disk deletions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close due disk deletions: %w", err)
	}

	for i := range disks {
		result, err := tx.ExecContext(ctx, `
			UPDATE disks SET deletion_claimed_at = ?
			WHERE id = ? AND deletion_state IN (?, ?)
			  AND (deletion_claimed_at = 0 OR deletion_claimed_at < ?)`,
			nowMS, disks[i].ID, DiskDeletionProvisioning, DiskDeletionDeleting, staleBefore,
		)
		if err != nil {
			return nil, fmt.Errorf("claim disk deletion %d: %w", disks[i].ID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read claimed disk rows: %w", err)
		}
		if affected == 0 {
			disks[i].DeletionClaimedAt = time.Time{}
			continue
		}
		disks[i].DeletionClaimedAt = now
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit claim disk deletions: %w", err)
	}

	claimed := disks[:0]
	for _, disk := range disks {
		if !disk.DeletionClaimedAt.IsZero() {
			claimed = append(claimed, disk)
		}
	}
	return claimed, nil
}

// FinalizeProvisioning atomically promotes a provisioning row to ready.
func (s *Store) FinalizeProvisioning(ctx context.Context, id int64, metadata DiskBackendMetadata, sizeBytes uint64, device string, preparedByScript bool) (Disk, bool, error) {
	encoded, err := EncodeDiskBackendMetadata(metadata)
	if err != nil {
		return Disk{}, false, err
	}
	protocol := string(metadata.Attachment.Protocol.Normalize())
	if metadata.Attachment.Protocol == "" {
		protocol = string(blockdev.ProtocolLocal)
	} else if metadata.Attachment.Protocol.Normalize() != blockdev.ProtocolLocal {
		if err := metadata.Attachment.ValidateComplete(); err != nil {
			return Disk{}, false, fmt.Errorf("finalize disk attachment: %w", err)
		}
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE disks
		SET backend_metadata = ?, attachment_protocol = ?, size_bytes = ?, device = ?,
		    prepared_by_script = ?, deletion_state = '', deletion_error = '',
		    deletion_attempts = 0, deletion_next_attempt_at = 0, deletion_claimed_at = 0,
		    updated_at = ?
		WHERE id = ? AND deletion_state = ? AND deletion_claimed_at = 0`,
		encoded, protocol, sizeBytes, device, preparedByScript, now, id, DiskDeletionProvisioning,
	)
	if err != nil {
		return Disk{}, false, fmt.Errorf("finalize disk provisioning: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Disk{}, false, fmt.Errorf("read finalized disk rows: %w", err)
	}
	if affected == 0 {
		return Disk{}, false, nil
	}
	disk, err := s.GetDisk(ctx, id)
	if err != nil {
		return Disk{}, false, err
	}
	return disk, true, nil
}

// DueDiskDeletionCount returns the current backlog and oldest due age.
func (s *Store) DueDiskDeletionCount(ctx context.Context, now time.Time) (int64, time.Duration, error) {
	var count int64
	var oldest sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(deletion_next_attempt_at)
		FROM disks
		WHERE deletion_state IN (?, ?) AND deletion_next_attempt_at <= ?`,
		DiskDeletionProvisioning, DiskDeletionDeleting, now.UTC().UnixMilli(),
	).Scan(&count, &oldest)
	if err != nil {
		return 0, 0, fmt.Errorf("query disk deletion backlog: %w", err)
	}
	if !oldest.Valid || oldest.Int64 <= 0 {
		return count, 0, nil
	}
	age := now.UTC().Sub(time.UnixMilli(oldest.Int64).UTC())
	if age < 0 {
		age = 0
	}
	return count, age, nil
}
