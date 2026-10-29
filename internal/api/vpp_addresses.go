package api

import (
	"context"
	"errors"
	"fmt"
)

// ReconcileVPPInterfaceAddresses reapplies Rookery-managed addresses and DHCP
// client state after VPP interfaces are recreated following an API or VPP
// restart.
func (s *Server) ReconcileVPPInterfaceAddresses(ctx context.Context) error {
	if s.networks == nil {
		return nil
	}
	byInterface, err := s.store.ListAllVPPInterfaceAddresses(ctx)
	if err != nil {
		return fmt.Errorf("list VPP interface addresses: %w", err)
	}
	dhcpClients, err := s.store.ListVPPInterfaceDHCPClients(ctx)
	if err != nil {
		return fmt.Errorf("list VPP interface DHCP state: %w", err)
	}
	if len(byInterface) == 0 && len(dhcpClients) == 0 {
		return nil
	}
	interfaces, err := s.networks.VPPInterfaces(ctx)
	if err != nil {
		return fmt.Errorf("list VPP interfaces for addresses: %w", err)
	}
	present := make(map[string]bool, len(interfaces))
	for _, iface := range interfaces {
		present[iface.Name] = true
	}
	var errs []error
	for name := range dhcpClients {
		if !present[name] {
			s.logger.Warn().Str("interface", name).Msg("VPP interface for stored DHCP client is missing")
			continue
		}
		if err := s.networks.EnableDHCPClient(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("enable DHCP client on %s: %w", name, err))
		}
	}
	for name, addresses := range byInterface {
		if !present[name] {
			s.logger.Warn().Str("interface", name).Msg("VPP interface for stored addresses is missing")
			continue
		}
		for _, address := range addresses {
			if err := s.networks.AddInterfaceAddress(ctx, name, address); err != nil {
				errs = append(errs, fmt.Errorf("apply %s address %s: %w", name, address, err))
			}
		}
	}
	return errors.Join(errs...)
}
