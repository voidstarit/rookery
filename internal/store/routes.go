package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

var (
	ErrRouteNotFound = errors.New("route not found")
	ErrRouteExists   = errors.New("route already exists")
)

// Route is a static IP FIB entry inside a route table/VRF.
type Route struct {
	ID            int64     `json:"id"`
	TableID       uint32    `json:"table_id"`
	IPv6          bool      `json:"ipv6"`
	Prefix        string    `json:"prefix"`
	NextHop       string    `json:"next_hop"`
	InterfaceID   *int64    `json:"interface_id,omitempty"`
	InterfaceName string    `json:"interface_name,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

const routeSelect = `
	SELECT r.id, r.table_id, r.is_ipv6, r.prefix, r.next_hop,
	       r.interface_id, COALESCE(i.name, ''),
	       r.created_at, r.updated_at
	FROM routes AS r
	LEFT JOIN virtual_interfaces AS i ON i.id = r.interface_id`

// ListRoutes returns static routes for one table/family.
func (s *Store) ListRoutes(ctx context.Context, tableID uint32, ipv6 bool) ([]Route, error) {
	rows, err := s.db.QueryContext(ctx, routeSelect+`
		WHERE r.table_id = ? AND r.is_ipv6 = ?
		ORDER BY r.prefix`, tableID, ipv6)
	if err != nil {
		return nil, fmt.Errorf("query routes: %w", err)
	}
	defer rows.Close()
	routes := make([]Route, 0)
	for rows.Next() {
		route, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate routes: %w", err)
	}
	return routes, nil
}

// ListAllRoutes returns every static route, used during VPP reconciliation.
func (s *Store) ListAllRoutes(ctx context.Context) ([]Route, error) {
	rows, err := s.db.QueryContext(ctx, routeSelect+` ORDER BY r.table_id, r.is_ipv6, r.prefix`)
	if err != nil {
		return nil, fmt.Errorf("query all routes: %w", err)
	}
	defer rows.Close()
	routes := make([]Route, 0)
	for rows.Next() {
		route, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate all routes: %w", err)
	}
	return routes, nil
}

// GetRoute returns one static route by record id.
func (s *Store) GetRoute(ctx context.Context, id int64) (Route, error) {
	row := s.db.QueryRowContext(ctx, routeSelect+` WHERE r.id = ?`, id)
	route, err := scanRoute(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Route{}, ErrRouteNotFound
	}
	if err != nil {
		return Route{}, err
	}
	return route, nil
}

// CreateRoute records a static route if its table and interface are valid.
func (s *Store) CreateRoute(ctx context.Context, route Route) (Route, error) {
	route.Prefix = strings.TrimSpace(route.Prefix)
	route.NextHop = strings.TrimSpace(route.NextHop)
	ip, network, err := net.ParseCIDR(route.Prefix)
	if err != nil {
		return Route{}, fmt.Errorf("invalid route prefix %q: %w", route.Prefix, err)
	}
	if ip.To4() == nil && !route.IPv6 {
		return Route{}, errors.New("IPv4 route table cannot contain an IPv6 prefix")
	}
	if ip.To4() != nil && route.IPv6 {
		return Route{}, errors.New("IPv6 route table cannot contain an IPv4 prefix")
	}
	route.Prefix = network.String()
	nextHop := net.ParseIP(route.NextHop)
	if nextHop == nil || (nextHop.To4() == nil) != route.IPv6 {
		return Route{}, errors.New("next-hop address family does not match the route table")
	}
	if route.TableID != 0 {
		if _, err := s.GetRouteTableByID(ctx, route.TableID, route.IPv6); err != nil {
			return Route{}, err
		}
	}
	if route.InterfaceID != nil {
		if _, err := s.GetInterface(ctx, *route.InterfaceID); err != nil {
			return Route{}, err
		}
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO routes (table_id, is_ipv6, prefix, next_hop, interface_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		route.TableID, route.IPv6, route.Prefix, route.NextHop, route.InterfaceID, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Route{}, ErrRouteExists
		}
		return Route{}, fmt.Errorf("insert route: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Route{}, fmt.Errorf("read inserted route id: %w", err)
	}
	return s.GetRoute(ctx, id)
}

// DeleteRoute removes one static route.
func (s *Store) DeleteRoute(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM routes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete route: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted route rows: %w", err)
	}
	if affected == 0 {
		return ErrRouteNotFound
	}
	return nil
}

// RouteCount returns the number of static routes in a table/family.
func (s *Store) RouteCount(ctx context.Context, tableID uint32, ipv6 bool) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM routes WHERE table_id = ? AND is_ipv6 = ?`, tableID, ipv6).Scan(&count); err != nil {
		return 0, fmt.Errorf("count routes: %w", err)
	}
	return count, nil
}

func scanRoute(s scanner) (Route, error) {
	var (
		route     Route
		ipv6      int64
		ifaceID   sql.NullInt64
		createdAt int64
		updatedAt int64
	)
	if err := s.Scan(
		&route.ID, &route.TableID, &ipv6, &route.Prefix, &route.NextHop,
		&ifaceID, &route.InterfaceName, &createdAt, &updatedAt,
	); err != nil {
		return Route{}, err
	}
	route.IPv6 = ipv6 != 0
	if ifaceID.Valid {
		id := ifaceID.Int64
		route.InterfaceID = &id
	}
	route.CreatedAt = time.UnixMilli(createdAt).UTC()
	route.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return route, nil
}
