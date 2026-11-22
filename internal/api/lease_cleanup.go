package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/store"
)

// leaseCleanupInterval is how often orphaned DHCP leases are reconciled
// against the interfaces recorded for their network.
const leaseCleanupInterval = time.Minute

// leaseRuntime is the subset of the VNF manager used by orphaned-lease cleanup.
type leaseRuntime interface {
	Leases(ctx context.Context, vnfID int64) ([]store.VNFLease, error)
	RemoveLeases(ctx context.Context, vnfID int64, clientIDs []string) (int, error)
}

// runLeaseCleanup periodically drops DHCP leases whose client MAC no longer
// belongs to an interface on the VNF's network. Dynamic DNS records are derived
// from these lease files by the VNF daemon, so removing the lease removes the
// corresponding A/AAAA and PTR records as well.
func (s *Server) runLeaseCleanup(ctx context.Context) {
	if s.store == nil || s.vnfs == nil {
		return
	}
	ticker := time.NewTicker(leaseCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupOrphanedLeases(ctx)
		}
	}
}

// cleanupOrphanedLeases reconciles every enabled DHCPv4 VNF. Leases using an
// address-family client ID other than a MAC (DHCPv6 DUIDs) are left untouched.
func (s *Server) cleanupOrphanedLeases(ctx context.Context) {
	reconcileOrphanedLeases(ctx, s.store, s.vnfs, s.logger)
}

func reconcileOrphanedLeases(ctx context.Context, st *store.Store, runtime leaseRuntime, logger zerolog.Logger) {
	if st == nil || runtime == nil {
		return
	}
	interfaces, err := st.ListInterfaces(ctx)
	if err != nil {
		logger.Warn().Err(err).Msg("DHCP lease cleanup could not list interfaces")
		return
	}
	networks, err := st.ListNetworks(ctx)
	if err != nil {
		logger.Warn().Err(err).Msg("DHCP lease cleanup could not list networks")
		return
	}

	knownByNetwork := make(map[int64]map[string]struct{}, len(networks))
	for _, iface := range interfaces {
		mac, err := normalizeLeaseMAC(iface.MAC)
		if err != nil {
			continue
		}
		if knownByNetwork[iface.NetworkID] == nil {
			knownByNetwork[iface.NetworkID] = make(map[string]struct{})
		}
		knownByNetwork[iface.NetworkID][mac] = struct{}{}
	}

	for _, network := range networks {
		vnfs, err := st.ListVNFsByNetwork(ctx, network.ID)
		if err != nil {
			logger.Warn().Err(err).Int64("network_id", network.ID).Msg("DHCP lease cleanup could not list VNFs")
			continue
		}
		for _, vnf := range vnfs {
			if vnf.Type != store.VNFTypeDHCPv4 || !vnf.Enabled {
				continue
			}
			leases, err := runtime.Leases(ctx, vnf.ID)
			if err != nil {
				logger.Warn().Err(err).Int64("vnf_id", vnf.ID).Msg("DHCP lease cleanup could not list leases")
				continue
			}
			stale := staleLeaseClientIDs(leases, knownByNetwork[network.ID])
			if len(stale) == 0 {
				continue
			}
			removed, err := runtime.RemoveLeases(ctx, vnf.ID, stale)
			if err != nil {
				logger.Warn().Err(err).Int64("vnf_id", vnf.ID).Msg("DHCP lease cleanup could not remove leases")
				continue
			}
			logger.Info().
				Int64("vnf_id", vnf.ID).
				Int64("network_id", network.ID).
				Strs("client_ids", stale).
				Int("removed", removed).
				Msg("removed DHCP leases for interfaces that no longer exist")
		}
	}
}

// RemoveLeasesForInterface immediately drops DHCPv4 leases for a MAC after its
// interface was deleted. It is a no-op when another interface on the network
// still uses the same MAC; the periodic scan repeats the check in any case.
func (s *Server) RemoveLeasesForInterface(ctx context.Context, networkID int64, mac string) error {
	if networkID <= 0 || s.store == nil || s.vnfs == nil {
		return nil
	}
	normalized, err := normalizeLeaseMAC(mac)
	if err != nil {
		return nil
	}
	interfaces, err := s.store.ListInterfaces(ctx)
	if err != nil {
		return err
	}
	for _, iface := range interfaces {
		if iface.NetworkID != networkID {
			continue
		}
		if other, err := normalizeLeaseMAC(iface.MAC); err == nil && other == normalized {
			return nil
		}
	}
	vnfs, err := s.store.ListVNFsByNetwork(ctx, networkID)
	if err != nil {
		return err
	}
	var errs []error
	for _, vnf := range vnfs {
		if vnf.Type != store.VNFTypeDHCPv4 || !vnf.Enabled {
			continue
		}
		if _, err := s.vnfs.RemoveLeases(ctx, vnf.ID, []string{normalized}); err != nil {
			errs = append(errs, fmt.Errorf("vnf %d: %w", vnf.ID, err))
		}
	}
	return errors.Join(errs...)
}

func staleLeaseClientIDs(leases []store.VNFLease, known map[string]struct{}) []string {
	stale := make([]string, 0, len(leases))
	for _, lease := range leases {
		if !lease.Active {
			continue
		}
		mac, err := normalizeLeaseMAC(lease.ClientID)
		if err != nil {
			// DHCPv6 leases use DUIDs, which do not map to an interface MAC.
			continue
		}
		if _, ok := known[mac]; !ok {
			stale = append(stale, mac)
		}
	}
	return stale
}

func normalizeLeaseMAC(value string) (string, error) {
	address, err := net.ParseMAC(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	if len(address) != 6 {
		return "", fmt.Errorf("not an Ethernet MAC address: %q", value)
	}
	return strings.ToLower(address.String()), nil
}
