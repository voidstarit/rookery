package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// ListVPPInterfaceAddresses returns the Rookery-managed addresses for one VPP
// interface.
func (s *Store) ListVPPInterfaceAddresses(ctx context.Context, name string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT address FROM vpp_interface_addresses
		WHERE interface_name = ? ORDER BY address`, name)
	if err != nil {
		return nil, fmt.Errorf("query VPP interface addresses: %w", err)
	}
	defer rows.Close()

	addresses := make([]string, 0)
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return nil, fmt.Errorf("scan VPP interface address: %w", err)
		}
		addresses = append(addresses, address)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate VPP interface addresses: %w", err)
	}
	sort.Strings(addresses)
	return addresses, nil
}

// ListAllVPPInterfaceAddresses returns managed addresses grouped by interface
// name.
func (s *Store) ListAllVPPInterfaceAddresses(ctx context.Context) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT interface_name, address FROM vpp_interface_addresses
		ORDER BY interface_name, address`)
	if err != nil {
		return nil, fmt.Errorf("query all VPP interface addresses: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]string)
	for rows.Next() {
		var name, address string
		if err := rows.Scan(&name, &address); err != nil {
			return nil, fmt.Errorf("scan VPP interface address: %w", err)
		}
		result[name] = append(result[name], address)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate VPP interface addresses: %w", err)
	}
	return result, nil
}

// ReplaceVPPInterfaceAddresses replaces the managed address set for an
// interface. The caller is responsible for applying the same set to VPP.
func (s *Store) ReplaceVPPInterfaceAddresses(ctx context.Context, name string, addresses []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin VPP interface address update: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM vpp_interface_addresses WHERE interface_name = ?`, name); err != nil {
		return fmt.Errorf("clear VPP interface addresses: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	for _, address := range addresses {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO vpp_interface_addresses (interface_name, address, created_at)
			VALUES (?, ?, ?)`, name, address, now); err != nil {
			return fmt.Errorf("insert VPP interface address %s: %w", address, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit VPP interface address update: %w", err)
	}
	return nil
}

// DeleteVPPInterfaceAddresses removes all managed addresses, DHCP, and
// route-table state for an interface.
func (s *Store) DeleteVPPInterfaceAddresses(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM vpp_interface_addresses WHERE interface_name = ?`, name); err != nil {
		return fmt.Errorf("delete VPP interface addresses: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM vpp_interface_dhcp_clients WHERE interface_name = ?`, name); err != nil {
		return fmt.Errorf("delete VPP interface DHCP state: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM vpp_interface_route_tables WHERE interface_name = ?`, name); err != nil {
		return fmt.Errorf("delete VPP interface route tables: %w", err)
	}
	return nil
}

// IsManagedVPPInterface reports whether an interface name belongs to a VPP
// interface created by Rookery (vhost-user, host tap, host interface, or VNF
// tap).
func (s *Store) IsManagedVPPInterface(ctx context.Context, name string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM virtual_interfaces WHERE name = ?
		UNION ALL SELECT 1 FROM network_host_taps WHERE name = ?
		UNION ALL SELECT 1 FROM host_interfaces WHERE vpp_name = ?
		UNION ALL SELECT 1 FROM vnfs WHERE tap_host_name = ?
		UNION ALL SELECT 1 FROM network_pipes
			WHERE ? = ('pipe' || instance || '.0') OR ? = ('pipe' || instance || '.1')
		LIMIT 1`, name, name, name, name, name, name).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check managed VPP interface: %w", err)
	}
	return true, nil
}

// VPPInterfaceDHCPClient reports whether the DHCP client is enabled for one
// VPP interface.
func (s *Store) VPPInterfaceDHCPClient(ctx context.Context, name string) (bool, error) {
	var enabled int
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled FROM vpp_interface_dhcp_clients WHERE interface_name = ?`, name).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get VPP interface DHCP state: %w", err)
	}
	return enabled != 0, nil
}

// SetVPPInterfaceDHCPClient stores the desired DHCP client state for one VPP
// interface.
func (s *Store) SetVPPInterfaceDHCPClient(ctx context.Context, name string, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO vpp_interface_dhcp_clients (interface_name, enabled, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(interface_name) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
		name, value, time.Now().UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("set VPP interface DHCP state: %w", err)
	}
	return nil
}

// ListVPPInterfaceDHCPClients returns enabled DHCP client state by interface
// name.
func (s *Store) ListVPPInterfaceDHCPClients(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT interface_name, enabled FROM vpp_interface_dhcp_clients WHERE enabled != 0`)
	if err != nil {
		return nil, fmt.Errorf("query VPP interface DHCP state: %w", err)
	}
	defer rows.Close()
	result := make(map[string]bool)
	for rows.Next() {
		var name string
		var enabled int
		if err := rows.Scan(&name, &enabled); err != nil {
			return nil, fmt.Errorf("scan VPP interface DHCP state: %w", err)
		}
		result[name] = enabled != 0
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate VPP interface DHCP state: %w", err)
	}
	return result, nil
}

// SetVPPInterfaceConfig replaces addresses and stores the DHCP client state for
// one interface in a single transaction.
func (s *Store) SetVPPInterfaceConfig(ctx context.Context, name string, addresses []string, dhcpEnabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin VPP interface config update: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM vpp_interface_addresses WHERE interface_name = ?`, name); err != nil {
		return fmt.Errorf("clear VPP interface addresses: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	for _, address := range addresses {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO vpp_interface_addresses (interface_name, address, created_at)
			VALUES (?, ?, ?)`, name, address, now); err != nil {
			return fmt.Errorf("insert VPP interface address %s: %w", address, err)
		}
	}
	dhcpValue := 0
	if dhcpEnabled {
		dhcpValue = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO vpp_interface_dhcp_clients (interface_name, enabled, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(interface_name) DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
		name, dhcpValue, now); err != nil {
		return fmt.Errorf("store VPP interface DHCP state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit VPP interface config update: %w", err)
	}
	return nil
}
