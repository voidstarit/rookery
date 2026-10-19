package api

import (
	"context"
	"errors"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// VPPBlocked reports whether VPP-dependent lifecycle operations are blocked.
func (s *Server) VPPBlocked() (bool, string) { return s.vppBlocked() }

// NormalizeVNFConfig validates and normalizes a protobuf VNF configuration.
func (s *Server) NormalizeVNFConfig(vnfType string, config *rookeryv1.VNFConfig) (store.VNFConfig, error) {
	payload := vnfConfigPayloadFromProto(config)
	if payload == nil {
		return store.VNFConfig{}, errors.New("config is required")
	}
	return payload.toConfig(vnfType)
}

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

// ApplyNATGatewayToDHCPConfig applies the network NAT gateway to a DHCP config.
func (s *Server) ApplyNATGatewayToDHCPConfig(ctx context.Context, networkID int64, vnfType string, config *store.VNFConfig) error {
	network, err := s.store.GetNetwork(ctx, networkID)
	if err != nil {
		return err
	}
	return applyNATGatewayToDHCPConfig(network, vnfType, config)
}

func (s *Server) ActivateVNF(ctx context.Context, vnf store.VNF) (store.VNF, error) {
	return s.activateVNF(ctx, vnf)
}

func (s *Server) StopVNF(ctx context.Context, vnf store.VNF, removeLeases bool) error {
	return s.stopVNF(ctx, vnf, removeLeases)
}

func (s *Server) ActivateNAT44(ctx context.Context, vnf store.VNF) (store.VNF, error) {
	return s.activateNAT44(ctx, vnf)
}

func (s *Server) StopNAT44(ctx context.Context, vnf store.VNF) error {
	return s.stopNAT44(ctx, vnf)
}

func (s *Server) CleanupNetworkBVIAfterNATDelete(ctx context.Context, networkID int64) error {
	return s.cleanupNetworkBVIAfterNATDelete(ctx, networkID)
}

func (s *Server) CleanupLegacyNetworkVNF(ctx context.Context, network store.Network) error {
	return s.cleanupLegacyNetworkVNF(ctx, network)
}

func (s *Server) DeleteBVI(ctx context.Context, name string) error {
	if s.nat == nil {
		return nil
	}
	return s.nat.DeleteBVIByName(ctx, name)
}

func (s *Server) CreateBridgeDomain(ctx context.Context, name string, reserved []uint32) (uint32, error) {
	if s.networks == nil {
		return 0, errors.New("VPP network runtime is not configured")
	}
	return s.networks.CreateBridgeDomain(ctx, name, reserved)
}

func (s *Server) DeleteBridgeDomain(ctx context.Context, bdID uint32) error {
	if s.networks == nil {
		return errors.New("VPP network runtime is not configured")
	}
	return s.networks.DeleteBridgeDomain(ctx, bdID)
}

func vnfConfigPayloadFromProto(config *rookeryv1.VNFConfig) *vnfConfigPayload {
	if config == nil {
		return nil
	}
	payload := &vnfConfigPayload{
		Gateway: config.GetGateway(), RangeStart: config.GetRangeStart(), RangeEnd: config.GetRangeEnd(),
		DNS: config.GetDns(), LeaseTime: config.GetLeaseTime(), BindIP: config.GetBindIp(),
		OutsideInterface: config.GetOutsideInterface(), OutsideAddress: config.GetOutsideAddress(),
		OutsideGateway: config.GetOutsideGateway(), NAT44Pool: config.GetNat44Pool(),
	}
	for _, assignment := range config.GetStaticAssignments() {
		payload.StaticAssignments = append(payload.StaticAssignments, dhcpStaticAssignmentPayload{MAC: assignment.GetMac(), IP: assignment.GetIp()})
	}
	for _, rule := range config.GetDnatRules() {
		payload.DNATRules = append(payload.DNATRules, dnatRulePayload{
			Protocol: rule.GetProtocol(), ExternalPort: int(rule.GetExternalPort()),
			LocalIP: rule.GetLocalIp(), LocalPort: int(rule.GetLocalPort()),
		})
	}
	return payload
}
