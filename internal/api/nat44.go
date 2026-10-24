package api

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/voidstarit/rookery/internal/netutil"
	"github.com/voidstarit/rookery/internal/store"
)

func (s *Server) activateNAT44(ctx context.Context, vnf store.VNF) (store.VNF, error) {
	if s.nat == nil {
		return vnf, errors.New("NAT44 runtime is not configured")
	}
	if blocked, message := s.vppBlocked(); blocked {
		return vnf, errors.New(message)
	}
	network, err := s.store.GetNetwork(ctx, vnf.NetworkID)
	if err != nil {
		return vnf, fmt.Errorf("get NAT44 network: %w", err)
	}
	network, err = s.ensureNetworkCIDRs(ctx, network)
	if err != nil {
		return vnf, err
	}
	expectedGateway, err := netutil.HostCIDR(network.IPv4CIDR, 1)
	if err == nil && expectedGateway != "" && vnf.Config.Gateway != expectedGateway {
		return vnf, fmt.Errorf("NAT44 gateway must be the network BVI address %s", expectedGateway)
	}
	if err := s.validateVNFNetwork(vnf, network); err != nil {
		return vnf, err
	}

	outside, err := s.nat.EnsureHostInterface(ctx, vnf.Config.OutsideInterface)
	if err != nil {
		return vnf, err
	}
	s.recordHostInterface(ctx, vnf.Config.OutsideInterface, "nat44")

	priorBVI, priorIndex, priorGateway := network.BVIHostName, network.BVISwIfIndex, network.IPv4Gateway
	bviName, bviIndex, err := s.nat.EnsureBVI(ctx, network.ID, network.BridgeDomainID)
	if err != nil {
		return vnf, fmt.Errorf("ensure network BVI: %w", err)
	}
	if network.RouteTableID != 0 && s.networks != nil {
		if err := s.bindNetworkBVI(ctx, network, bviName, bviIndex, vnf.Config.Gateway); err != nil {
			return vnf, err
		}
	}
	rollbackBVI := func() {
		if priorBVI == "" {
			if err := s.nat.DeleteBVIByName(ctx, bviName); err != nil {
				s.logger.Error().Str("network", network.Name).Str("bvi", bviName).Err(err).Msg("rollback NAT44 BVI failed")
			}
			if _, err := s.store.SetNetworkBVI(ctx, network.ID, "", 0, ""); err != nil {
				s.logger.Error().Str("network", network.Name).Err(err).Msg("clear network BVI after failed NAT44 activation")
			}
			return
		}
		if _, err := s.store.SetNetworkBVI(ctx, network.ID, priorBVI, priorIndex, priorGateway); err != nil {
			s.logger.Error().Str("network", network.Name).Err(err).Msg("restore network BVI after failed NAT44 activation")
		}
	}
	network, err = s.store.SetNetworkBVI(ctx, network.ID, bviName, bviIndex, vnf.Config.Gateway)
	if err != nil {
		rollbackBVI()
		return vnf, fmt.Errorf("save network BVI: %w", err)
	}
	if err := s.nat.AddInterfaceIPv4(ctx, bviName, vnf.Config.Gateway); err != nil {
		rollbackBVI()
		return vnf, fmt.Errorf("assign NAT44 gateway to BVI: %w", err)
	}
	if err := s.nat.ApplyNAT44(ctx, bviName, outside, vnf.Config.OutsideAddress, vnf.Config.OutsideGateway, vnf.Config.NAT44Pool); err != nil {
		_ = s.nat.RemoveNAT44(ctx, bviName, outside, vnf.Config.NAT44Pool)
		rollbackBVI()
		return vnf, err
	}
	if err := s.nat.ApplyNAT44StaticMappings(ctx, outside, vnf.ID, vnf.Config.DNATRules); err != nil {
		_ = s.nat.RemoveNAT44StaticMappings(ctx, outside, vnf.ID)
		_ = s.nat.RemoveNAT44(ctx, bviName, outside, vnf.Config.NAT44Pool)
		rollbackBVI()
		return vnf, fmt.Errorf("apply NAT44 DNAT rules: %w", err)
	}
	if err := s.configureDHCPv4ForNAT(ctx, network, vnf.Config.Gateway); err != nil {
		_ = s.nat.RemoveNAT44StaticMappings(ctx, outside, vnf.ID)
		_ = s.nat.RemoveNAT44(ctx, bviName, outside, vnf.Config.NAT44Pool)
		return vnf, err
	}

	vnf.Enabled = true
	vnf.TapSwIfIndex = 0
	vnf.TapHostName = ""
	updated, err := s.store.UpdateVNF(ctx, vnf.ID, vnf)
	if err != nil {
		_ = s.nat.RemoveNAT44StaticMappings(ctx, outside, vnf.ID)
		_ = s.nat.RemoveNAT44(ctx, bviName, outside, vnf.Config.NAT44Pool)
		return vnf, err
	}
	s.setNAT44Running(vnf.ID, true)
	return updated, nil
}

func (s *Server) stopNAT44(ctx context.Context, vnf store.VNF) error {
	if s.nat == nil {
		return nil
	}
	network, err := s.store.GetNetwork(ctx, vnf.NetworkID)
	if errors.Is(err, store.ErrNetworkNotFound) {
		s.setNAT44Running(vnf.ID, false)
		return nil
	}
	if err != nil {
		return err
	}
	outside := ""
	if strings.TrimSpace(vnf.Config.OutsideInterface) != "" {
		outside = "host-" + strings.TrimSpace(vnf.Config.OutsideInterface)
	}
	if outside != "" && (vnf.Enabled || network.BVIHostName != "") {
		if err := s.nat.RemoveNAT44StaticMappings(ctx, outside, vnf.ID); err != nil {
			return err
		}
	}
	if network.BVIHostName != "" && outside != "" {
		if err := s.nat.RemoveNAT44(ctx, network.BVIHostName, outside, vnf.Config.NAT44Pool); err != nil {
			return err
		}
	}
	s.setNAT44Running(vnf.ID, false)
	return nil
}

// cleanupNetworkBVIAfterNATDelete removes a NAT-owned BVI once no NAT44 VNF
// and no NAT-adjusted DHCPv4 VNF depends on it. This also cleans up a BVI left
// behind by a failed NAT44 activation.
func (s *Server) cleanupNetworkBVIAfterNATDelete(ctx context.Context, networkID int64) error {
	if s.nat == nil {
		return nil
	}
	network, err := s.store.GetNetwork(ctx, networkID)
	if errors.Is(err, store.ErrNetworkNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if network.BVIHostName == "" {
		return nil
	}
	vnfs, err := s.store.ListVNFsByNetwork(ctx, networkID)
	if err != nil {
		return err
	}
	for _, other := range vnfs {
		if other.Type == store.VNFTypeNAT44 {
			return nil
		}
		if other.Type == store.VNFTypeDHCPv4 && strings.TrimSpace(other.Config.BindIP) != "" {
			return nil
		}
	}
	if err := s.nat.DeleteBVIByName(ctx, network.BVIHostName); err != nil {
		return fmt.Errorf("delete network BVI: %w", err)
	}
	if _, err := s.store.SetNetworkBVI(ctx, network.ID, "", 0, ""); err != nil {
		return fmt.Errorf("clear network BVI: %w", err)
	}
	return nil
}

func (s *Server) recordHostInterface(ctx context.Context, nic, purpose string) {
	nic = strings.TrimSpace(nic)
	if nic == "" || s.store == nil {
		return
	}
	record := store.HostInterfaceRecord{NIC: nic, VPPName: "host-" + nic, Purpose: purpose}
	if s.networks != nil {
		if interfaces, err := s.networks.VPPInterfaces(ctx); err == nil {
			for _, iface := range interfaces {
				if iface.Name == record.VPPName {
					record.SwIfIndex = iface.SwIfIndex
					break
				}
			}
		}
	}
	if _, err := s.store.UpsertHostInterfaceRecord(ctx, record); err != nil {
		s.logger.Warn().Str("nic", nic).Err(err).Msg("could not persist host interface takeover")
	}
}

func (s *Server) configureDHCPv4ForNAT(ctx context.Context, network store.Network, natGateway string) error {
	vnfs, err := s.store.ListVNFsByNetwork(ctx, network.ID)
	if err != nil {
		return err
	}
	natIP, natSubnet, err := net.ParseCIDR(natGateway)
	if err != nil {
		return fmt.Errorf("parse NAT44 gateway: %w", err)
	}
	natIP4 := natIP.To4()
	if natIP4 == nil {
		return errors.New("NAT44 gateway is not IPv4")
	}
	bindCIDR, err := nextIPv4CIDR(natGateway)
	if err != nil {
		return err
	}

	for _, vnf := range vnfs {
		if vnf.Type != store.VNFTypeDHCPv4 {
			continue
		}
		start := net.ParseIP(vnf.Config.RangeStart).To4()
		end := net.ParseIP(vnf.Config.RangeEnd).To4()
		if start == nil || end == nil || !natSubnet.Contains(start) || !natSubnet.Contains(end) {
			return fmt.Errorf("DHCPv4 VNF %s range must be inside NAT44 subnet %s", vnf.Name, natSubnet)
		}
		bindIP, _, _ := net.ParseCIDR(bindCIDR)
		if bytesCompareIPv4(start, bindIP) <= 0 && bytesCompareIPv4(end, bindIP) >= 0 {
			return fmt.Errorf("DHCPv4 VNF %s range cannot include the NAT service address %s", vnf.Name, bindIP)
		}
		changed := vnf.Config.Gateway != natGateway || vnf.Config.BindIP != bindCIDR
		vnf.Config.Gateway = natGateway
		vnf.Config.BindIP = bindCIDR
		if !changed {
			if vnf.Enabled && !s.vnfs.Running(ctx, vnf.ID) {
				if vnf.TapHostName != "" {
					if err := s.vnfs.Start(ctx, vnf, vnf.TapHostName); err == nil {
						continue
					}
				}
				if _, err := s.activateVNF(ctx, vnf); err != nil {
					return fmt.Errorf("start DHCPv4 VNF %s: %w", vnf.Name, err)
				}
			}
			continue
		}

		if vnf.Enabled {
			if err := s.stopVNF(ctx, vnf, false); err != nil {
				return fmt.Errorf("stop DHCPv4 VNF %s for NAT: %w", vnf.Name, err)
			}
			vnf.TapSwIfIndex = 0
			vnf.TapHostName = ""
		}
		saved, err := s.store.UpdateVNF(ctx, vnf.ID, vnf)
		if err != nil {
			return fmt.Errorf("save DHCPv4 VNF %s for NAT: %w", vnf.Name, err)
		}
		if saved.Enabled {
			if _, err := s.activateVNF(ctx, saved); err != nil {
				return fmt.Errorf("restart DHCPv4 VNF %s for NAT: %w", vnf.Name, err)
			}
		}
	}
	return nil
}

func nextIPv4CIDR(gatewayCIDR string) (string, error) {
	gateway, subnet, err := net.ParseCIDR(gatewayCIDR)
	if err != nil {
		return "", fmt.Errorf("parse NAT44 gateway: %w", err)
	}
	gateway4 := gateway.To4()
	if gateway4 == nil {
		return "", errors.New("NAT44 gateway is not IPv4")
	}
	ones, bits := subnet.Mask.Size()
	if bits != 32 || ones > 30 {
		return "", errors.New("NAT44 subnet must be /30 or larger")
	}
	next := binary.BigEndian.Uint32(gateway4) + 1
	ip := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(ip, next)
	if !subnet.Contains(ip) {
		return "", errors.New("NAT44 subnet has no service address")
	}
	if ip.Equal(broadcastIPv4(subnet)) {
		return "", errors.New("NAT44 subnet has no service address")
	}
	return fmt.Sprintf("%s/%d", ip.String(), ones), nil
}

func vnfTapCIDR(vnf store.VNF) string {
	if vnf.Type == store.VNFTypeBGP {
		return strings.TrimSpace(vnf.Config.LocalIP)
	}
	if strings.TrimSpace(vnf.Config.BindIP) != "" {
		return strings.TrimSpace(vnf.Config.BindIP)
	}
	return strings.TrimSpace(vnf.Config.Gateway)
}
