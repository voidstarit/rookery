package api

import (
	"context"
	"errors"
	"fmt"
)

// ReconcileVPPInterfaceRouteTables reapplies persisted FIB/VRF bindings for
// host interfaces and host taps after an API or VPP restart. Vhost-user
// bindings are restored by ReconcileRouteTables from virtual_interfaces.
func (s *Server) ReconcileVPPInterfaceRouteTables(ctx context.Context) error {
	if s.networks == nil {
		return nil
	}
	configs, err := s.store.ListAllVPPInterfaceRouteTables(ctx)
	if err != nil {
		return fmt.Errorf("list interface route tables: %w", err)
	}
	if len(configs) == 0 {
		return nil
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	virtuals, err := s.store.ListInterfaces(ctx)
	if err != nil {
		return fmt.Errorf("list virtual interfaces for route tables: %w", err)
	}
	virtualNames := make(map[string]struct{}, len(virtuals))
	for _, iface := range virtuals {
		virtualNames[iface.Name] = struct{}{}
	}
	interfaces, err := s.networks.VPPInterfaces(ctx)
	if err != nil {
		return fmt.Errorf("list VPP interfaces for route tables: %w", err)
	}
	indices := make(map[string]uint32, len(interfaces))
	for _, iface := range interfaces {
		indices[iface.Name] = iface.SwIfIndex
	}

	var errs []error
	for name, config := range configs {
		if _, virtual := virtualNames[name]; virtual {
			continue
		}
		swIfIndex, present := indices[name]
		if !present {
			s.logger.Warn().Str("interface", name).Msg("VPP interface for stored route tables is missing")
			continue
		}
		if err := s.applyInterfaceRouteTablesByIndex(ctx, swIfIndex, config.IPv4TableID, config.IPv6TableID); err != nil {
			errs = append(errs, fmt.Errorf("apply route tables to %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) applyInterfaceRouteTablesByIndex(ctx context.Context, swIfIndex uint32, ipv4, ipv6 uint32) error {
	if err := s.networks.SetInterfaceRouteTable(ctx, swIfIndex, false, ipv4); err != nil {
		return err
	}
	if err := s.networks.SetInterfaceRouteTable(ctx, swIfIndex, true, ipv6); err != nil {
		return err
	}
	return nil
}
