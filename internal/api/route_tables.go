package api

import (
	"context"
	"errors"
	"fmt"
)

// ReconcileRouteTables recreates custom IP FIB/VRF tables and reapplies each
// interface's IPv4/IPv6 table binding after VPP restarts.
func (s *Server) ReconcileRouteTables(ctx context.Context) error {
	if s.networks == nil {
		return nil
	}
	if blocked, _ := s.vppBlocked(); blocked {
		return nil
	}
	tables, err := s.store.ListRouteTables(ctx)
	if err != nil {
		return fmt.Errorf("list route tables: %w", err)
	}
	var errs []error
	for _, table := range tables {
		if err := s.networks.EnsureRouteTable(ctx, table.TableID, table.IPv6, table.Name); err != nil {
			errs = append(errs, fmt.Errorf("route table %s: %w", table.Name, err))
		}
	}
	interfaces, err := s.store.ListInterfaces(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	interfaceIndex := make(map[int64]uint32, len(interfaces))
	for _, iface := range interfaces {
		interfaceIndex[iface.ID] = iface.SwIfIndex
		if iface.SwIfIndex == 0 {
			continue
		}
		if err := s.networks.SetInterfaceRouteTable(ctx, iface.SwIfIndex, false, iface.IPv4TableID); err != nil {
			errs = append(errs, fmt.Errorf("interface %s IPv4 route table: %w", iface.Name, err))
		}
		if err := s.networks.SetInterfaceRouteTable(ctx, iface.SwIfIndex, true, iface.IPv6TableID); err != nil {
			errs = append(errs, fmt.Errorf("interface %s IPv6 route table: %w", iface.Name, err))
		}
	}
	routes, err := s.store.ListAllRoutes(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, route := range routes {
		swIfIndex := uint32(0)
		if route.InterfaceID != nil {
			swIfIndex = interfaceIndex[*route.InterfaceID]
		}
		_ = s.networks.DeleteRoute(ctx, route.TableID, route.IPv6, route.Prefix)
		if err := s.networks.AddRoute(ctx, route.TableID, route.IPv6, route.Prefix, route.NextHop, swIfIndex); err != nil {
			errs = append(errs, fmt.Errorf("route %s in table %d: %w", route.Prefix, route.TableID, err))
		}
	}
	return errors.Join(errs...)
}
