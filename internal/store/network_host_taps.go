package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// NetworkHostTap persists a VPP tap that connects a Rookery Network bridge
// domain to the Linux host. It is recreated after an API or VPP restart.
type NetworkHostTap struct {
	ID          int64     `json:"id"`
	NetworkID   int64     `json:"network_id"`
	NetworkName string    `json:"network_name"`
	Name        string    `json:"name"`
	SwIfIndex   uint32    `json:"sw_if_index"`
	IPv4Address string    `json:"ipv4_address,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const networkHostTapSelect = `
	SELECT t.id, t.network_id, n.name, t.name, t.sw_if_index, t.ipv4_address,
	       t.created_at, t.updated_at
	FROM network_host_taps AS t
	JOIN networks AS n ON n.id = t.network_id`

// ListNetworkHostTaps returns host taps ordered by name, optionally filtered
// by network.
func (s *Store) ListNetworkHostTaps(ctx context.Context, networkID *int64) ([]NetworkHostTap, error) {
	query := networkHostTapSelect
	args := make([]any, 0, 1)
	if networkID != nil {
		query += ` WHERE t.network_id = ?`
		args = append(args, *networkID)
	}
	query += ` ORDER BY t.name COLLATE NOCASE, t.id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query network host taps: %w", err)
	}
	defer rows.Close()

	taps := make([]NetworkHostTap, 0)
	for rows.Next() {
		tap, err := scanNetworkHostTap(rows)
		if err != nil {
			return nil, err
		}
		taps = append(taps, tap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate network host taps: %w", err)
	}
	return taps, nil
}

// ListNetworkHostTapsByNetwork returns the host taps attached to one network.
func (s *Store) ListNetworkHostTapsByNetwork(ctx context.Context, networkID int64) ([]NetworkHostTap, error) {
	return s.ListNetworkHostTaps(ctx, &networkID)
}

// GetNetworkHostTap returns one host tap by ID.
func (s *Store) GetNetworkHostTap(ctx context.Context, id int64) (NetworkHostTap, error) {
	row := s.db.QueryRowContext(ctx, networkHostTapSelect+`
		WHERE t.id = ?`, id)
	tap, err := scanNetworkHostTap(row)
	if errors.Is(err, sql.ErrNoRows) {
		return NetworkHostTap{}, ErrNetworkHostTapNotFound
	}
	if err != nil {
		return NetworkHostTap{}, err
	}
	return tap, nil
}

// GetNetworkHostTapByName returns one host tap by its Linux interface name.
func (s *Store) GetNetworkHostTapByName(ctx context.Context, name string) (NetworkHostTap, error) {
	row := s.db.QueryRowContext(ctx, networkHostTapSelect+`
		WHERE t.name = ? COLLATE NOCASE`, name)
	tap, err := scanNetworkHostTap(row)
	if errors.Is(err, sql.ErrNoRows) {
		return NetworkHostTap{}, ErrNetworkHostTapNotFound
	}
	if err != nil {
		return NetworkHostTap{}, err
	}
	return tap, nil
}

// CreateNetworkHostTap inserts a host tap record.
func (s *Store) CreateNetworkHostTap(ctx context.Context, tap NetworkHostTap) (NetworkHostTap, error) {
	if _, err := s.GetNetwork(ctx, tap.NetworkID); err != nil {
		return NetworkHostTap{}, err
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO network_host_taps
			(network_id, name, sw_if_index, ipv4_address, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		tap.NetworkID, tap.Name, tap.SwIfIndex, tap.IPv4Address, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return NetworkHostTap{}, fmt.Errorf("%w: %q", ErrNetworkHostTapNameTaken, tap.Name)
		}
		return NetworkHostTap{}, fmt.Errorf("insert network host tap: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return NetworkHostTap{}, fmt.Errorf("read inserted network host tap id: %w", err)
	}
	return s.GetNetworkHostTap(ctx, id)
}

// SetNetworkHostTapRuntime updates the VPP interface index after creation or
// reconciliation.
func (s *Store) SetNetworkHostTapRuntime(ctx context.Context, id int64, swIfIndex uint32) (NetworkHostTap, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE network_host_taps SET sw_if_index = ?, updated_at = ? WHERE id = ?`,
		swIfIndex, now, id,
	)
	if err != nil {
		return NetworkHostTap{}, fmt.Errorf("update network host tap runtime: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return NetworkHostTap{}, fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return NetworkHostTap{}, ErrNetworkHostTapNotFound
	}
	return s.GetNetworkHostTap(ctx, id)
}

// SetNetworkHostTapAddress updates the host-side IPv4 address.
func (s *Store) SetNetworkHostTapAddress(ctx context.Context, id int64, address string) (NetworkHostTap, error) {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE network_host_taps SET ipv4_address = ?, updated_at = ? WHERE id = ?`,
		address, now, id,
	)
	if err != nil {
		return NetworkHostTap{}, fmt.Errorf("update network host tap address: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return NetworkHostTap{}, fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return NetworkHostTap{}, ErrNetworkHostTapNotFound
	}
	return s.GetNetworkHostTap(ctx, id)
}

// DeleteNetworkHostTap removes a host tap record.
func (s *Store) DeleteNetworkHostTap(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM network_host_taps WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete network host tap: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted rows: %w", err)
	}
	if affected == 0 {
		return ErrNetworkHostTapNotFound
	}
	return nil
}

func scanNetworkHostTap(s scanner) (NetworkHostTap, error) {
	var (
		tap       NetworkHostTap
		swIfIndex int64
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&tap.ID, &tap.NetworkID, &tap.NetworkName, &tap.Name, &swIfIndex,
		&tap.IPv4Address, &createdAt, &updatedAt,
	); err != nil {
		return NetworkHostTap{}, err
	}
	tap.SwIfIndex = uint32(swIfIndex)
	tap.CreatedAt = time.UnixMilli(createdAt).UTC()
	tap.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return tap, nil
}
