package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// HostInterfaceRecord persists a host NIC that Rookery has brought into VPP.
// It is used to recreate the VPP host-interface after a VPP restart.
type HostInterfaceRecord struct {
	NIC       string    `json:"nic"`
	VPPName   string    `json:"vpp_name"`
	MAC       string    `json:"mac,omitempty"`
	SwIfIndex uint32    `json:"sw_if_index,omitempty"`
	Purpose   string    `json:"purpose,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListHostInterfaceRecords returns all persisted host-interface takeovers.
func (s *Store) ListHostInterfaceRecords(ctx context.Context) ([]HostInterfaceRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT nic, vpp_name, mac, sw_if_index, purpose, created_at, updated_at
		FROM host_interfaces ORDER BY nic`)
	if err != nil {
		return nil, fmt.Errorf("query host interfaces: %w", err)
	}
	defer rows.Close()

	records := make([]HostInterfaceRecord, 0)
	for rows.Next() {
		record, err := scanHostInterface(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate host interfaces: %w", err)
	}
	return records, nil
}

// GetHostInterfaceRecord returns one persisted host-interface takeover.
func (s *Store) GetHostInterfaceRecord(ctx context.Context, nic string) (HostInterfaceRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT nic, vpp_name, mac, sw_if_index, purpose, created_at, updated_at
		FROM host_interfaces WHERE nic = ?`, nic)
	record, err := scanHostInterface(row)
	if errors.Is(err, sql.ErrNoRows) {
		return HostInterfaceRecord{}, ErrNotFound
	}
	if err != nil {
		return HostInterfaceRecord{}, err
	}
	return record, nil
}

// UpsertHostInterfaceRecord records or updates a host-interface takeover.
func (s *Store) UpsertHostInterfaceRecord(ctx context.Context, record HostInterfaceRecord) (HostInterfaceRecord, error) {
	now := time.Now().UTC().UnixMilli()
	if record.Purpose == "" {
		record.Purpose = "manual"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO host_interfaces (nic, vpp_name, mac, sw_if_index, purpose, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(nic) DO UPDATE SET
			vpp_name = excluded.vpp_name,
			mac = excluded.mac,
			sw_if_index = excluded.sw_if_index,
			purpose = excluded.purpose,
			updated_at = excluded.updated_at`,
		record.NIC, record.VPPName, record.MAC, record.SwIfIndex, record.Purpose, now, now,
	)
	if err != nil {
		return HostInterfaceRecord{}, fmt.Errorf("save host interface: %w", err)
	}
	return s.GetHostInterfaceRecord(ctx, record.NIC)
}

// SetHostInterfaceRuntime updates the VPP interface index after reconciliation.
func (s *Store) SetHostInterfaceRuntime(ctx context.Context, nic string, swIfIndex uint32) error {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE host_interfaces SET sw_if_index = ?, updated_at = ? WHERE nic = ?`,
		swIfIndex, now, nic,
	)
	if err != nil {
		return fmt.Errorf("update host interface runtime: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteHostInterfaceRecord removes a host-interface takeover record.
func (s *Store) DeleteHostInterfaceRecord(ctx context.Context, nic string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM host_interfaces WHERE nic = ?`, nic); err != nil {
		return fmt.Errorf("delete host interface: %w", err)
	}
	return nil
}

func scanHostInterface(s scanner) (HostInterfaceRecord, error) {
	var (
		record    HostInterfaceRecord
		swIfIndex int64
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&record.NIC, &record.VPPName, &record.MAC, &swIfIndex,
		&record.Purpose, &createdAt, &updatedAt,
	); err != nil {
		return HostInterfaceRecord{}, err
	}
	record.SwIfIndex = uint32(swIfIndex)
	record.CreatedAt = time.UnixMilli(createdAt).UTC()
	record.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return record, nil
}
