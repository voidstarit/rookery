package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
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
	for _, iface := range interfaces {
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
		_ = s.networks.DeleteRoute(ctx, route.TableID, route.IPv6, route.Prefix)
		if err := s.networks.AddRouteSpec(ctx, routeSpecFromStore(route)); err != nil {
			errs = append(errs, fmt.Errorf("route %s in table %d: %w", route.Prefix, route.TableID, err))
		}
	}
	return errors.Join(errs...)
}

func routeSpecFromStore(route store.Route) host.RouteSpec {
	egress := route.EgressInterface
	if egress == "" {
		egress = route.InterfaceName
	}
	return host.RouteSpec{
		TableID: route.TableID, IPv6: route.IPv6, Prefix: route.Prefix,
		NextHop: route.NextHop, EgressInterface: egress, Preference: route.Preference,
		Count: route.Count, NextHopTable: route.NextHopTable, Weight: route.Weight,
		UDPEncap: route.UDPEncap, IPv4LookupTable: route.IPv4LookupTable,
		IPv6LookupTable: route.IPv6LookupTable, MPLSLookupTable: route.MPLSLookupTable,
		ResolveViaHost: route.ResolveViaHost, ResolveViaConnected: route.ResolveViaConnected,
		RxIPv4Interface: route.RxIPv4Interface, RxIPv6Interface: route.RxIPv6Interface,
		OutLabels: route.OutLabels, Drop: route.Drop,
	}
}
