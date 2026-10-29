package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrRouteNotFound = errors.New("route not found")
	ErrRouteExists   = errors.New("route already exists")
)

// routeNamePattern validates VPP interface names used as route egress.
var routeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// Route is a static IP FIB entry inside a route table/VRF.
type Route struct {
	ID                  int64     `json:"id"`
	TableID             uint32    `json:"table_id"`
	IPv6                bool      `json:"ipv6"`
	Prefix              string    `json:"prefix"`
	Description         string    `json:"description,omitempty"`
	NextHop             string    `json:"next_hop"`
	InterfaceID         *int64    `json:"interface_id,omitempty"`
	InterfaceName       string    `json:"interface_name,omitempty"`
	EgressInterface     string    `json:"egress_interface,omitempty"`
	Preference          *uint32   `json:"preference,omitempty"`
	Count               *uint32   `json:"count,omitempty"`
	NextHopTable        *string   `json:"next_hop_table,omitempty"`
	Weight              *uint32   `json:"weight,omitempty"`
	UDPEncap            *uint32   `json:"udp_encap,omitempty"`
	IPv4LookupTable     *string   `json:"ipv4_lookup_table,omitempty"`
	IPv6LookupTable     *string   `json:"ipv6_lookup_table,omitempty"`
	MPLSLookupTable     *uint32   `json:"mpls_lookup_table,omitempty"`
	ResolveViaHost      bool      `json:"resolve_via_host,omitempty"`
	ResolveViaConnected bool      `json:"resolve_via_connected,omitempty"`
	RxIPv4Interface     string    `json:"rx_ipv4_interface,omitempty"`
	RxIPv6Interface     string    `json:"rx_ipv6_interface,omitempty"`
	OutLabels           []uint32  `json:"out_labels,omitempty"`
	Drop                bool      `json:"drop,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

const routeSelect = `
	SELECT r.id, r.table_id, r.is_ipv6, r.prefix, r.description, r.next_hop,
	       r.interface_id, COALESCE(i.name, ''),
	       r.egress_interface, r.preference,
	       r.route_count, r.next_hop_table_name, r.weight, r.udp_encap,
	       r.ipv4_lookup_table_name, r.ipv6_lookup_table_name, r.mpls_lookup_table,
	       r.resolve_via_host, r.resolve_via_connected,
	       r.rx_ipv4_interface, r.rx_ipv6_interface, r.out_labels, r.is_drop,
	       r.created_at, r.updated_at,
	       r.preference_set, r.route_count_set, r.weight_set, r.udp_encap_set,
	       r.mpls_lookup_table_set
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
	route, err := s.validateRoute(ctx, route)
	if err != nil {
		return Route{}, err
	}
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO routes (
			table_id, is_ipv6, prefix, description, next_hop, interface_id, egress_interface,
			preference, route_count, next_hop_table_name, weight, udp_encap,
			ipv4_lookup_table_name, ipv6_lookup_table_name, mpls_lookup_table,
			resolve_via_host, resolve_via_connected,
			rx_ipv4_interface, rx_ipv6_interface, out_labels, is_drop,
			preference_set, route_count_set, weight_set, udp_encap_set, mpls_lookup_table_set,
			created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		route.TableID, route.IPv6, route.Prefix, route.Description, route.NextHop, route.InterfaceID,
		route.EgressInterface, optionalUint32Value(route.Preference), optionalUint32Value(route.Count),
		optionalStringValue(route.NextHopTable), optionalUint32Value(route.Weight),
		optionalUint32Value(route.UDPEncap), optionalStringValue(route.IPv4LookupTable),
		optionalStringValue(route.IPv6LookupTable), optionalUint32Value(route.MPLSLookupTable),
		route.ResolveViaHost, route.ResolveViaConnected,
		route.RxIPv4Interface, route.RxIPv6Interface, formatLabels(route.OutLabels),
		route.Drop, optionalUint32Set(route.Preference), optionalUint32Set(route.Count),
		optionalUint32Set(route.Weight), optionalUint32Set(route.UDPEncap),
		optionalUint32Set(route.MPLSLookupTable),
		now, now,
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

// UpdateRoute replaces the editable properties of a static route. The route's
// table and address family are immutable.
func (s *Store) UpdateRoute(ctx context.Context, id int64, route Route) (Route, error) {
	existing, err := s.GetRoute(ctx, id)
	if err != nil {
		return Route{}, err
	}
	route.ID = id
	route.TableID = existing.TableID
	route.IPv6 = existing.IPv6
	route, err = s.validateRoute(ctx, route)
	if err != nil {
		return Route{}, err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE routes SET
			prefix = ?, description = ?, next_hop = ?, interface_id = ?, egress_interface = ?,
			preference = ?, route_count = ?, next_hop_table_name = ?, weight = ?, udp_encap = ?,
			ipv4_lookup_table_name = ?, ipv6_lookup_table_name = ?, mpls_lookup_table = ?,
			resolve_via_host = ?, resolve_via_connected = ?,
			rx_ipv4_interface = ?, rx_ipv6_interface = ?, out_labels = ?, is_drop = ?,
			preference_set = ?, route_count_set = ?, weight_set = ?, udp_encap_set = ?,
			mpls_lookup_table_set = ?,
			next_hop_table = 0, ipv4_lookup_table = 0, ipv6_lookup_table = 0,
			next_hop_table_set = 0, ipv4_lookup_table_set = 0, ipv6_lookup_table_set = 0,
			updated_at = ?
		WHERE id = ?`,
		route.Prefix, route.Description, route.NextHop, route.InterfaceID, route.EgressInterface,
		optionalUint32Value(route.Preference), optionalUint32Value(route.Count),
		optionalStringValue(route.NextHopTable), optionalUint32Value(route.Weight),
		optionalUint32Value(route.UDPEncap), optionalStringValue(route.IPv4LookupTable),
		optionalStringValue(route.IPv6LookupTable), optionalUint32Value(route.MPLSLookupTable),
		route.ResolveViaHost, route.ResolveViaConnected,
		route.RxIPv4Interface, route.RxIPv6Interface, formatLabels(route.OutLabels), route.Drop,
		optionalUint32Set(route.Preference), optionalUint32Set(route.Count),
		optionalUint32Set(route.Weight), optionalUint32Set(route.UDPEncap),
		optionalUint32Set(route.MPLSLookupTable),
		time.Now().UTC().UnixMilli(), id,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Route{}, ErrRouteExists
		}
		return Route{}, fmt.Errorf("update route: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return Route{}, ErrRouteNotFound
	}
	return s.GetRoute(ctx, id)
}

func (s *Store) validateRoute(ctx context.Context, route Route) (Route, error) {
	route.Prefix = strings.TrimSpace(route.Prefix)
	route.Description = strings.TrimSpace(route.Description)
	route.NextHop = strings.TrimSpace(route.NextHop)
	route.EgressInterface = strings.TrimSpace(route.EgressInterface)
	route.RxIPv4Interface = strings.TrimSpace(route.RxIPv4Interface)
	route.RxIPv6Interface = strings.TrimSpace(route.RxIPv6Interface)
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
	if route.NextHop != "" {
		nextHop := net.ParseIP(route.NextHop)
		if nextHop == nil || (nextHop.To4() == nil) != route.IPv6 {
			return Route{}, errors.New("next-hop address family does not match the route table")
		}
	} else if route.InterfaceID == nil && route.EgressInterface == "" &&
		!route.Drop && route.RxIPv4Interface == "" && route.RxIPv6Interface == "" &&
		route.NextHopTable == nil && route.UDPEncap == nil &&
		route.IPv4LookupTable == nil && route.IPv6LookupTable == nil && route.MPLSLookupTable == nil {
		return Route{}, errors.New("next-hop, egress interface, rx interface, lookup option, or drop is required")
	}
	if route.Count != nil {
		if *route.Count == 0 {
			return Route{}, errors.New("count must be greater than zero")
		}
		if *route.Count > 100000 {
			return Route{}, errors.New("count must be at most 100000")
		}
	}
	if route.Weight != nil && *route.Weight > 255 {
		return Route{}, errors.New("weight must be at most 255")
	}
	for _, lookup := range []struct {
		name  *string
		ipv6  bool
		label string
	}{
		{route.NextHopTable, route.IPv6, "next-hop table"},
		{route.IPv4LookupTable, false, "IPv4 lookup table"},
		{route.IPv6LookupTable, true, "IPv6 lookup table"},
	} {
		if lookup.name == nil {
			continue
		}
		name := strings.TrimSpace(*lookup.name)
		if name == "" {
			*lookup.name = ""
			continue
		}
		*lookup.name = name
		if _, err := s.ResolveRouteTableName(ctx, name, lookup.ipv6); err != nil {
			return Route{}, fmt.Errorf("%s %q: %w", lookup.label, name, err)
		}
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
	if route.InterfaceID != nil && route.EgressInterface != "" {
		return Route{}, errors.New("interface_id and egress_interface are mutually exclusive")
	}
	if route.EgressInterface != "" {
		if len(route.EgressInterface) > 64 || !routeNamePattern.MatchString(route.EgressInterface) {
			return Route{}, fmt.Errorf("invalid egress interface name %q", route.EgressInterface)
		}
	}
	return route, nil
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

func optionalUint32Value(value *uint32) uint32 {
	if value == nil {
		return 0
	}
	return *value
}

func optionalStringValue(value *string) any {
	if value == nil {
		return nil
	}
	return strings.TrimSpace(*value)
}

func optionalUint32Set(value *uint32) int {
	if value == nil {
		return 0
	}
	return 1
}

func scanRoute(s scanner) (Route, error) {
	var (
		route            Route
		ipv6             int64
		ifaceID          sql.NullInt64
		preference       int64
		count            int64
		nextHopTable     sql.NullString
		weight           int64
		udpEncap         int64
		ipv4Lookup       sql.NullString
		ipv6Lookup       sql.NullString
		mplsLookup       int64
		resolveHost      int64
		resolveConnected int64
		rxIPv4           string
		rxIPv6           string
		outLabels        string
		drop             int64
		createdAt        int64
		updatedAt        int64
		preferenceSet    int64
		countSet         int64
		weightSet        int64
		udpEncapSet      int64
		mplsLookupSet    int64
	)
	if err := s.Scan(
		&route.ID, &route.TableID, &ipv6, &route.Prefix, &route.Description, &route.NextHop,
		&ifaceID, &route.InterfaceName, &route.EgressInterface, &preference,
		&count, &nextHopTable, &weight, &udpEncap,
		&ipv4Lookup, &ipv6Lookup, &mplsLookup,
		&resolveHost, &resolveConnected, &rxIPv4, &rxIPv6, &outLabels, &drop,
		&createdAt, &updatedAt,
		&preferenceSet, &countSet, &weightSet, &udpEncapSet, &mplsLookupSet,
	); err != nil {
		return Route{}, err
	}
	route.IPv6 = ipv6 != 0
	if ifaceID.Valid {
		id := ifaceID.Int64
		route.InterfaceID = &id
	}
	if preferenceSet != 0 {
		value := uint32(preference)
		route.Preference = &value
	}
	if countSet != 0 {
		value := uint32(count)
		route.Count = &value
	}
	if nextHopTable.Valid {
		value := nextHopTable.String
		route.NextHopTable = &value
	}
	if weightSet != 0 {
		value := uint32(weight)
		route.Weight = &value
	}
	if udpEncapSet != 0 {
		value := uint32(udpEncap)
		route.UDPEncap = &value
	}
	if ipv4Lookup.Valid {
		value := ipv4Lookup.String
		route.IPv4LookupTable = &value
	}
	if ipv6Lookup.Valid {
		value := ipv6Lookup.String
		route.IPv6LookupTable = &value
	}
	if mplsLookupSet != 0 {
		value := uint32(mplsLookup)
		route.MPLSLookupTable = &value
	}
	route.ResolveViaHost = resolveHost != 0
	route.ResolveViaConnected = resolveConnected != 0
	route.RxIPv4Interface = rxIPv4
	route.RxIPv6Interface = rxIPv6
	route.OutLabels = parseLabels(outLabels)
	route.Drop = drop != 0
	route.CreatedAt = time.UnixMilli(createdAt).UTC()
	route.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return route, nil
}

func formatLabels(labels []uint32) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for _, label := range labels {
		parts = append(parts, strconv.FormatUint(uint64(label), 10))
	}
	return strings.Join(parts, " ")
}

func parseLabels(value string) []uint32 {
	parts := strings.Fields(value)
	labels := make([]uint32, 0, len(parts))
	for _, part := range parts {
		label, err := strconv.ParseUint(part, 10, 32)
		if err == nil {
			labels = append(labels, uint32(label))
		}
	}
	return labels
}
