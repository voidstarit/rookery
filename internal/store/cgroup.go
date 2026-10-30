package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/voidstarit/rookery/internal/cgroup"
)

// GetCgroupSettings returns the singleton host-wide cgroup settings.
func (s *Store) GetCgroupSettings(ctx context.Context) (cgroup.CgroupSettings, error) {
	var (
		config    string
		updatedAt int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT config, updated_at FROM cgroup_settings WHERE id = 1`,
	).Scan(&config, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return cgroup.CgroupSettings{}, nil
	}
	if err != nil {
		return cgroup.CgroupSettings{}, fmt.Errorf("read cgroup settings: %w", err)
	}
	var settings cgroup.Settings
	if strings.TrimSpace(config) != "" {
		if err := json.Unmarshal([]byte(config), &settings); err != nil {
			return cgroup.CgroupSettings{}, fmt.Errorf("decode cgroup settings: %w", err)
		}
	}
	result := cgroup.CgroupSettings{Settings: settings}
	if updatedAt > 0 {
		result.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	}
	return result, nil
}

// SetCgroupSettings persists the singleton host-wide cgroup settings.
func (s *Store) SetCgroupSettings(ctx context.Context, settings cgroup.Settings) (cgroup.CgroupSettings, error) {
	config, err := json.Marshal(settings)
	if err != nil {
		return cgroup.CgroupSettings{}, fmt.Errorf("encode cgroup settings: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO cgroup_settings (id, config, updated_at)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET config = excluded.config, updated_at = excluded.updated_at`,
		string(config), now,
	); err != nil {
		return cgroup.CgroupSettings{}, fmt.Errorf("write cgroup settings: %w", err)
	}
	return s.GetCgroupSettings(ctx)
}
