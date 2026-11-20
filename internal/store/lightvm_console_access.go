package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrLightVMConsoleAccessNotFound is returned when a console-access token
	// is unknown, expired, or no longer scoped to a live console.
	ErrLightVMConsoleAccessNotFound = errors.New("lightvm console access not found")
)

// LightVMConsoleAccess is a time-limited bearer token for one LightVM console.
// The raw token is never stored; callers keep the token returned at issue time.
type LightVMConsoleAccess struct {
	ID        int64
	LightVMID string
	ConsoleID int64
	ExpiresAt time.Time
	CreatedAt time.Time
}

// CreateLightVMConsoleAccess stores a hashed console-access token scoped to one
// console of a LightVM. Expired tokens are removed as part of the same
// transaction. The returned record does not contain the raw token.
func (s *Store) CreateLightVMConsoleAccess(ctx context.Context, lightVMID string, consoleID int64, tokenHash string, expiresAt time.Time) (LightVMConsoleAccess, error) {
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return LightVMConsoleAccess{}, errors.New("token hash is required")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	expiresAt = expiresAt.UTC().Truncate(time.Millisecond)
	if !expiresAt.After(now) {
		return LightVMConsoleAccess{}, errors.New("console access expiration must be in the future")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LightVMConsoleAccess{}, fmt.Errorf("begin console access transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM lightvm_console_access WHERE expires_at <= ?`, now.UnixMilli()); err != nil {
		return LightVMConsoleAccess{}, fmt.Errorf("delete expired console access: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO lightvm_console_access (lightvm_id, console_id, token_hash, expires_at, created_at)
		SELECT l.id, c.id, ?, ?, ?
		FROM lightvms AS l
		JOIN lightvm_consoles AS c ON c.lightvm_id = l.id
		WHERE l.id = ? AND c.id = ?`,
		tokenHash, expiresAt.UnixMilli(), now.UnixMilli(), lightVMID, consoleID)
	if err != nil {
		return LightVMConsoleAccess{}, fmt.Errorf("insert console access: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return LightVMConsoleAccess{}, fmt.Errorf("inspect console access insert: %w", err)
	}
	if affected == 0 {
		return LightVMConsoleAccess{}, ErrLightVMConsoleAccessNotFound
	}
	id, err := result.LastInsertId()
	if err != nil {
		return LightVMConsoleAccess{}, fmt.Errorf("read console access id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LightVMConsoleAccess{}, fmt.Errorf("commit console access: %w", err)
	}
	return LightVMConsoleAccess{
		ID:        id,
		LightVMID: lightVMID,
		ConsoleID: consoleID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}, nil
}

// GetLightVMConsoleAccessByTokenHash returns a live console-access record for a
// token hash. Expired records are never returned.
func (s *Store) GetLightVMConsoleAccessByTokenHash(ctx context.Context, tokenHash string, now time.Time) (LightVMConsoleAccess, error) {
	var (
		access    LightVMConsoleAccess
		expiresAt int64
		createdAt int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, lightvm_id, console_id, expires_at, created_at
		FROM lightvm_console_access
		WHERE token_hash = ? AND expires_at > ?`,
		strings.TrimSpace(tokenHash), now.UTC().UnixMilli()).Scan(
		&access.ID, &access.LightVMID, &access.ConsoleID, &expiresAt, &createdAt,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return LightVMConsoleAccess{}, ErrLightVMConsoleAccessNotFound
	case err != nil:
		return LightVMConsoleAccess{}, fmt.Errorf("query console access: %w", err)
	}
	access.ExpiresAt = time.UnixMilli(expiresAt).UTC()
	access.CreatedAt = time.UnixMilli(createdAt).UTC()
	return access, nil
}
