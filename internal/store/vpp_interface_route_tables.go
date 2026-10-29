package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// VPPInterfaceRouteTables is the persisted IPv4/IPv6 FIB binding for a
// Rookery-managed VPP interface other than a vhost-user interface. Vhost-user
// bindings live on virtual_interfaces so the interface API can return them
// alongside VM attachment state.
type VPPInterfaceRouteTables struct {
	InterfaceName string `json:"interface_name"`
	IPv4TableID   uint32 `json:"ipv4_table_id"`
	IPv6TableID   uint32 `json:"ipv6_table_id"`
	IPv4TableName string `json:"ipv4_table_name"`
	IPv6TableName string `json:"ipv6_table_name"`
	// Configured distinguishes an explicit row (even 0/0) from an interface
	// that follows its automatically assigned network VRF.
	Configured bool `json:"configured"`
}

const vppInterfaceRouteTablesSelect = `
	SELECT t.interface_name, t.ipv4_table_id, COALESCE(v4.name, ''),
	       t.ipv6_table_id, COALESCE(v6.name, '')
	FROM vpp_interface_route_tables AS t
	LEFT JOIN route_tables AS v4 ON v4.table_id = t.ipv4_table_id AND v4.is_ipv6 = 0
	LEFT JOIN route_tables AS v6 ON v6.table_id = t.ipv6_table_id AND v6.is_ipv6 = 1`

// GetVPPInterfaceRouteTables returns the persisted table binding for an
// interface. A missing row means the default table for both families.
func (s *Store) GetVPPInterfaceRouteTables(ctx context.Context, name string) (VPPInterfaceRouteTables, error) {
	row := s.db.QueryRowContext(ctx, vppInterfaceRouteTablesSelect+` WHERE t.interface_name = ?`, name)
	tables, err := scanVPPInterfaceRouteTables(row)
	if errors.Is(err, sql.ErrNoRows) {
		return VPPInterfaceRouteTables{InterfaceName: name}, nil
	}
	if err != nil {
		return VPPInterfaceRouteTables{}, err
	}
	return tables, nil
}

// ListAllVPPInterfaceRouteTables returns every persisted non-vhost binding.
func (s *Store) ListAllVPPInterfaceRouteTables(ctx context.Context) (map[string]VPPInterfaceRouteTables, error) {
	rows, err := s.db.QueryContext(ctx, vppInterfaceRouteTablesSelect+` ORDER BY t.interface_name`)
	if err != nil {
		return nil, fmt.Errorf("query VPP interface route tables: %w", err)
	}
	defer rows.Close()
	result := make(map[string]VPPInterfaceRouteTables)
	for rows.Next() {
		tables, err := scanVPPInterfaceRouteTables(rows)
		if err != nil {
			return nil, err
		}
		result[tables.InterfaceName] = tables
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate VPP interface route tables: %w", err)
	}
	return result, nil
}

// SetVPPInterfaceRouteTables persists a table binding after validating both
// table IDs. The caller is responsible for applying the binding to VPP.
func (s *Store) SetVPPInterfaceRouteTables(ctx context.Context, name string, ipv4, ipv6 uint32) (VPPInterfaceRouteTables, error) {
	if ipv4 != 0 {
		if _, err := s.GetRouteTableByID(ctx, ipv4, false); err != nil {
			return VPPInterfaceRouteTables{}, err
		}
	}
	if ipv6 != 0 {
		if _, err := s.GetRouteTableByID(ctx, ipv6, true); err != nil {
			return VPPInterfaceRouteTables{}, err
		}
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO vpp_interface_route_tables
			(interface_name, ipv4_table_id, ipv6_table_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(interface_name) DO UPDATE SET
			ipv4_table_id = excluded.ipv4_table_id,
			ipv6_table_id = excluded.ipv6_table_id,
			updated_at = excluded.updated_at`,
		name, ipv4, ipv6, now, now); err != nil {
		return VPPInterfaceRouteTables{}, fmt.Errorf("set VPP interface route tables: %w", err)
	}
	return s.GetVPPInterfaceRouteTables(ctx, name)
}

// DeleteVPPInterfaceRouteTables removes the persisted table binding for an
// interface.
func (s *Store) DeleteVPPInterfaceRouteTables(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM vpp_interface_route_tables WHERE interface_name = ?`, name); err != nil {
		return fmt.Errorf("delete VPP interface route tables: %w", err)
	}
	return nil
}

// IsVirtualInterfaceName reports whether name belongs to a vhost-user
// interface. Those interfaces use virtual_interfaces.ipv4_table_id/ipv6_table_id
// through the dedicated route-table API instead of the generic binding table.
func (s *Store) IsVirtualInterfaceName(ctx context.Context, name string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM virtual_interfaces WHERE name = ? LIMIT 1`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check virtual interface name: %w", err)
	}
	return true, nil
}

func scanVPPInterfaceRouteTables(row scanner) (VPPInterfaceRouteTables, error) {
	var (
		tables VPPInterfaceRouteTables
		ipv4   int64
		ipv6   int64
	)
	if err := row.Scan(&tables.InterfaceName, &ipv4, &tables.IPv4TableName, &ipv6, &tables.IPv6TableName); err != nil {
		return VPPInterfaceRouteTables{}, err
	}
	tables.IPv4TableID = uint32(ipv4)
	tables.IPv6TableID = uint32(ipv6)
	tables.Configured = true
	return tables, nil
}
