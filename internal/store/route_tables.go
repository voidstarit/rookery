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
	ErrRouteTableNotFound  = errors.New("route table not found")
	ErrRouteTableInUse     = errors.New("route table is in use")
	ErrRouteTableNameTaken = errors.New("route table name already exists")
	ErrRouteTableIDTaken   = errors.New("route table id already exists")
)

// RouteTable is a custom VPP IP FIB/VRF definition. Table 0 is the implicit
// default and is not stored.
type RouteTable struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	TableID   uint32    `json:"table_id"`
	IPv6      bool      `json:"ipv6"`
	NetworkID int64     `json:"network_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const routeTableSelect = `
	SELECT id, name, table_id, is_ipv6, network_id, created_at, updated_at
	FROM route_tables`

// ListRouteTables returns custom route tables ordered by family and table id.
func (s *Store) ListRouteTables(ctx context.Context) ([]RouteTable, error) {
	rows, err := s.db.QueryContext(ctx, routeTableSelect+` ORDER BY is_ipv6, table_id`)
	if err != nil {
		return nil, fmt.Errorf("query route tables: %w", err)
	}
	defer rows.Close()
	tables := make([]RouteTable, 0)
	for rows.Next() {
		table, err := scanRouteTable(rows)
		if err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate route tables: %w", err)
	}
	return tables, nil
}

// GetRouteTable returns one custom route table by record id.
func (s *Store) GetRouteTable(ctx context.Context, id int64) (RouteTable, error) {
	row := s.db.QueryRowContext(ctx, routeTableSelect+` WHERE id = ?`, id)
	table, err := scanRouteTable(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RouteTable{}, ErrRouteTableNotFound
	}
	if err != nil {
		return RouteTable{}, err
	}
	return table, nil
}

// GetRouteTableByID returns a custom route table by VPP table id and family.
func (s *Store) GetRouteTableByID(ctx context.Context, tableID uint32, ipv6 bool) (RouteTable, error) {
	row := s.db.QueryRowContext(ctx, routeTableSelect+` WHERE table_id = ? AND is_ipv6 = ?`, tableID, ipv6)
	table, err := scanRouteTable(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RouteTable{}, ErrRouteTableNotFound
	}
	if err != nil {
		return RouteTable{}, err
	}
	return table, nil
}

// GetRouteTableByName returns a route table by name and address family.
func (s *Store) GetRouteTableByName(ctx context.Context, name string, ipv6 bool) (RouteTable, error) {
	row := s.db.QueryRowContext(ctx, routeTableSelect+` WHERE name = ? AND is_ipv6 = ?`, strings.TrimSpace(name), ipv6)
	table, err := scanRouteTable(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RouteTable{}, ErrRouteTableNotFound
	}
	if err != nil {
		return RouteTable{}, err
	}
	return table, nil
}

// ResolveRouteTableName maps a stored route-table name to its VPP table ID.
// "default" refers to the implicit table 0.
func (s *Store) ResolveRouteTableName(ctx context.Context, name string, ipv6 bool) (uint32, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "default") {
		return 0, nil
	}
	table, err := s.GetRouteTableByName(ctx, name, ipv6)
	if err != nil {
		return 0, err
	}
	return table.TableID, nil
}

// CreateRouteTable records a custom VPP IP table definition.
func (s *Store) CreateRouteTable(ctx context.Context, table RouteTable) (RouteTable, error) {
	table.Name = strings.TrimSpace(table.Name)
	if table.Name == "" {
		return RouteTable{}, errors.New("route table name is required")
	}
	if len(table.Name) > 63 {
		return RouteTable{}, errors.New("route table name must be at most 63 characters")
	}
	if strings.EqualFold(table.Name, "default") {
		return RouteTable{}, errors.New("route table name \"default\" is reserved for VPP table 0")
	}
	if table.TableID == 0 {
		return RouteTable{}, errors.New("route table id must be greater than zero")
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO route_tables (name, table_id, is_ipv6, network_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		table.Name, table.TableID, table.IPv6, table.NetworkID, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			if _, getErr := s.GetRouteTableByID(ctx, table.TableID, table.IPv6); getErr == nil {
				return RouteTable{}, ErrRouteTableIDTaken
			}
			return RouteTable{}, ErrRouteTableNameTaken
		}
		return RouteTable{}, fmt.Errorf("insert route table: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return RouteTable{}, fmt.Errorf("read inserted route table id: %w", err)
	}
	return s.GetRouteTable(ctx, id)
}

// DeleteRouteTable removes a custom route table if no interface uses it.
func (s *Store) DeleteRouteTable(ctx context.Context, id int64) error {
	table, err := s.GetRouteTable(ctx, id)
	if err != nil {
		return err
	}
	if table.NetworkID != 0 {
		return ErrRouteTableInUse
	}
	var used int64
	column := "ipv4_table_id"
	if table.IPv6 {
		column = "ipv6_table_id"
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT 1 FROM virtual_interfaces WHERE `+column+` = ? LIMIT 1`,
		table.TableID,
	).Scan(&used)
	switch {
	case err == nil:
		return ErrRouteTableInUse
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check route table usage: %w", err)
	}
	if count, err := s.RouteCount(ctx, table.TableID, table.IPv6); err != nil {
		return err
	} else if count > 0 {
		return ErrRouteTableInUse
	}
	query := `SELECT 1 FROM routes WHERE next_hop_table_name = ? AND is_ipv6 = ?`
	args := []any{table.Name, table.IPv6}
	if !table.IPv6 {
		query += ` UNION ALL SELECT 1 FROM routes WHERE ipv4_lookup_table_name = ?`
		args = append(args, table.Name)
	} else {
		query += ` UNION ALL SELECT 1 FROM routes WHERE ipv6_lookup_table_name = ?`
		args = append(args, table.Name)
	}
	query += ` LIMIT 1`
	err = s.db.QueryRowContext(ctx, query, args...).Scan(&used)
	switch {
	case err == nil:
		return ErrRouteTableInUse
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check route table option usage: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM route_tables WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete route table: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted route table rows: %w", err)
	}
	if affected == 0 {
		return ErrRouteTableNotFound
	}
	return nil
}

// UpsertNetworkRouteTable creates or updates the route-table record owned by a
// network. Each network has an IPv4 and an IPv6 record with the same table ID.
func (s *Store) UpsertNetworkRouteTable(ctx context.Context, networkID int64, name string, tableID uint32, ipv6 bool) (RouteTable, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("network-%d", networkID)
	}
	existing, err := s.GetRouteTableByID(ctx, tableID, ipv6)
	switch {
	case err == nil:
		if existing.NetworkID != networkID {
			return RouteTable{}, ErrRouteTableIDTaken
		}
		if existing.Name != name {
			now := time.Now().UTC().UnixMilli()
			if _, err := s.db.ExecContext(ctx,
				`UPDATE route_tables SET name = ?, updated_at = ? WHERE id = ?`,
				name, now, existing.ID,
			); err != nil {
				return RouteTable{}, fmt.Errorf("rename network route table: %w", err)
			}
			return s.GetRouteTable(ctx, existing.ID)
		}
		return existing, nil
	case !errors.Is(err, ErrRouteTableNotFound):
		return RouteTable{}, err
	}
	created, err := s.CreateRouteTable(ctx, RouteTable{Name: name, TableID: tableID, IPv6: ipv6, NetworkID: networkID})
	if errors.Is(err, ErrRouteTableNameTaken) {
		name = fmt.Sprintf("%s-%d", name, networkID)
		created, err = s.CreateRouteTable(ctx, RouteTable{Name: name, TableID: tableID, IPv6: ipv6, NetworkID: networkID})
	}
	if err != nil {
		return RouteTable{}, err
	}
	return created, nil
}

// DeleteRouteTablesByNetwork removes the route-table records owned by a
// network.
func (s *Store) DeleteRouteTablesByNetwork(ctx context.Context, networkID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM route_tables WHERE network_id = ?`, networkID); err != nil {
		return fmt.Errorf("delete network route tables: %w", err)
	}
	return nil
}

// SetInterfaceRouteTables assigns the IPv4 and IPv6 FIB/VRF table ids.
func (s *Store) SetInterfaceRouteTables(ctx context.Context, id int64, ipv4TableID, ipv6TableID uint32) (VirtualInterface, error) {
	if _, err := s.GetInterface(ctx, id); err != nil {
		return VirtualInterface{}, err
	}
	for _, table := range []struct {
		id   uint32
		ipv6 bool
	}{{ipv4TableID, false}, {ipv6TableID, true}} {
		if table.id == 0 {
			continue
		}
		if _, err := s.GetRouteTableByID(ctx, table.id, table.ipv6); err != nil {
			return VirtualInterface{}, err
		}
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		UPDATE virtual_interfaces SET ipv4_table_id = ?, ipv6_table_id = ?, updated_at = ? WHERE id = ?`,
		ipv4TableID, ipv6TableID, now, id,
	)
	if err != nil {
		return VirtualInterface{}, fmt.Errorf("update interface route tables: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return VirtualInterface{}, fmt.Errorf("read updated rows: %w", err)
	}
	if affected == 0 {
		return VirtualInterface{}, ErrInterfaceNotFound
	}
	return s.GetInterface(ctx, id)
}

// RouteTableInterfaceCount counts interfaces attached to a table/family.
func (s *Store) RouteTableInterfaceCount(ctx context.Context, tableID uint32, ipv6 bool) (int64, error) {
	column := "ipv4_table_id"
	if ipv6 {
		column = "ipv6_table_id"
	}
	var count int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM virtual_interfaces WHERE `+column+` = ?)
		     + (SELECT COUNT(*) FROM vpp_interface_route_tables WHERE `+column+` = ?)`,
		tableID, tableID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count route table interfaces: %w", err)
	}
	return count, nil
}

func scanRouteTable(s scanner) (RouteTable, error) {
	var (
		table     RouteTable
		ipv6      int64
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(&table.ID, &table.Name, &table.TableID, &ipv6, &table.NetworkID, &createdAt, &updatedAt); err != nil {
		return RouteTable{}, err
	}
	table.IPv6 = ipv6 != 0
	table.CreatedAt = time.UnixMilli(createdAt).UTC()
	table.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return table, nil
}
