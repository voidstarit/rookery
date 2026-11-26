package api

import (
	"context"
	"errors"
	"strings"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// VPPBlocked reports whether VPP-dependent lifecycle operations are blocked.
func (s *Server) VPPBlocked() (bool, string) { return s.vppBlocked() }

// VNFWritesReady checks runtime prerequisites for an enabled VNF.
func (s *Server) VNFWritesReady(vnfType string) error {
	if blocked, message := s.vppBlocked(); blocked {
		return errors.New(message)
	}
	if s.vnfs == nil || s.networks == nil {
		return errors.New("VNF runtime is not configured")
	}
	if vnfType == store.VNFTypeNAT44 && s.nat == nil {
		return errors.New("NAT44 runtime is not configured")
	}
	return nil
}

// ApplyNATGatewayToDHCPConfig applies the network NAT gateway and service bind
// address to a DHCPv4 config. Other families are left unchanged.
func (s *Server) ApplyNATGatewayToDHCPConfig(ctx context.Context, networkID int64, family string, config *store.DHCPVNFConfig) error {
	if family != store.VNFTypeDHCPv4 {
		return nil
	}
	network, err := s.store.GetNetwork(ctx, networkID)
	if err != nil {
		return err
	}
	return applyNATGatewayToDHCPConfig(network, config)
}

// ActivateVNF provisions the VNF's interface, starts its runtime over the
// effective transport, and returns the record updated with Enabled and the tap
// or memif runtime fields. It fails when the runtime is unconfigured or VPP is
// blocked.
func (s *Server) ActivateVNF(ctx context.Context, vnf store.VNF) (store.VNF, error) {
	return s.activateVNF(ctx, vnf)
}

// StopVNF stops the VNF runtime, removes its tap or memif (shared memifs are
// left to the network), optionally drops its leases, and joins the per-step
// errors.
func (s *Server) StopVNF(ctx context.Context, vnf store.VNF, removeLeases bool) error {
	return s.stopVNF(ctx, vnf, removeLeases)
}

// ActivateNAT44 wires the network BVI, outside host interface, NAT44
// translation, DNAT rules, and DHCPv4 gateway for the VNF, returning the
// updated record. It rolls the BVI and NAT44 state back when a later step fails.
func (s *Server) ActivateNAT44(ctx context.Context, vnf store.VNF) (store.VNF, error) {
	return s.activateNAT44(ctx, vnf)
}

// StopNAT44 removes the VNF's DNAT mappings and NAT44 translation and clears
// its running flag, returning nil when NAT44 is unconfigured or the network was
// already deleted.
func (s *Server) StopNAT44(ctx context.Context, vnf store.VNF) error {
	return s.stopNAT44(ctx, vnf)
}

// CleanupNetworkBVIAfterNATDelete deletes the network's NAT-owned BVI once no
// NAT44 VNF or NAT-adjusted DHCPv4 VNF still depends on it, then clears the
// stored BVI fields.
func (s *Server) CleanupNetworkBVIAfterNATDelete(ctx context.Context, networkID int64) error {
	return s.cleanupNetworkBVIAfterNATDelete(ctx, networkID)
}

// CleanupLegacyNetworkVNF removes the pre-VNF per-network DHCP tap and stored
// DHCP fields, then asks the VNF manager to drop legacy state for the network.
func (s *Server) CleanupLegacyNetworkVNF(ctx context.Context, network store.Network) error {
	return s.cleanupLegacyNetworkVNF(ctx, network)
}

// DeleteBVI removes the named BVI interface and returns nil without contacting
// VPP when the NAT44 runtime is not configured.
func (s *Server) DeleteBVI(ctx context.Context, name string) error {
	if s.nat == nil {
		return nil
	}
	return s.nat.DeleteBVIByName(ctx, name)
}

// CreateBridgeDomain creates a bridge domain with the given name while
// avoiding the reserved IDs, and returns the new bridge-domain ID; it fails
// when the network runtime is not configured.
func (s *Server) CreateBridgeDomain(ctx context.Context, name string, reserved []uint32) (uint32, error) {
	if s.networks == nil {
		return 0, errors.New("VPP network runtime is not configured")
	}
	return s.networks.CreateBridgeDomain(ctx, name, reserved)
}

// DeleteBridgeDomain removes the VPP bridge domain with the given ID, returning
// an error when the network runtime is not configured.
func (s *Server) DeleteBridgeDomain(ctx context.Context, bdID uint32) error {
	if s.networks == nil {
		return errors.New("VPP network runtime is not configured")
	}
	return s.networks.DeleteBridgeDomain(ctx, bdID)
}

// CreateNetworkPipe connects two bridge domains with a VPP pipe and returns the
// created pipe's runtime details, or an error when the network runtime is not
// configured.
func (s *Server) CreateNetworkPipe(ctx context.Context, bdA, bdB uint32) (host.PipeRuntime, error) {
	if s.networks == nil {
		return host.PipeRuntime{}, errors.New("VPP network runtime is not configured")
	}
	return s.networks.CreatePipe(ctx, bdA, bdB)
}

// DeleteNetworkPipe removes the pipe identified by its instance and parent
// software interface index, failing when the network runtime is not configured.
func (s *Server) DeleteNetworkPipe(ctx context.Context, instance, parentSwIfIndex uint32) error {
	if s.networks == nil {
		return errors.New("VPP network runtime is not configured")
	}
	return s.networks.DeletePipe(ctx, instance, parentSwIfIndex)
}

// Running reports whether a VNF runtime is currently up, using the tracked
// NAT44 flag for NAT44 and the daemon state for other types.
func (s *Server) Running(ctx context.Context, vnf store.VNF) bool {
	return s.vnfRunning(ctx, vnf)
}

// ReloadVNF swaps a running DHCP, DNS, or BGP runtime's configuration in place
// through the daemon. NAT44 has no daemon runtime and is applied by the API.
func (s *Server) ReloadVNF(ctx context.Context, vnf store.VNF) error {
	if vnf.Type == store.VNFTypeNAT44 {
		return errors.New("NAT44 has no daemon runtime to reload")
	}
	if s.vnfs == nil {
		return errors.New("VNF runtime is not configured")
	}
	return s.vnfs.ReloadVNF(ctx, vnf)
}

// ApplyNAT44DNATRules replaces a NAT44 VNF's VPP static mappings in place while
// NAT44 and the network BVI stay up.
func (s *Server) ApplyNAT44DNATRules(ctx context.Context, vnf store.VNF) error {
	if s.nat == nil {
		return errors.New("NAT44 runtime is not configured")
	}
	if vnf.Type != store.VNFTypeNAT44 {
		return errors.New("static mappings are supported for NAT44 VNFs only")
	}
	outside := "host-" + strings.TrimSpace(vnf.Config.OutsideInterface)
	if outside == "host-" {
		return errors.New("NAT44 outside interface is not configured")
	}
	if err := s.nat.RemoveNAT44StaticMappings(ctx, outside, vnf.ID); err != nil {
		return err
	}
	if err := s.nat.ApplyNAT44StaticMappings(ctx, outside, vnf.ID, vnf.Config.DNATRules); err != nil {
		_ = s.nat.RemoveNAT44StaticMappings(ctx, outside, vnf.ID)
		return err
	}
	return nil
}
