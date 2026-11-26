package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// validateVNFName trims a VNF name and enforces the shared name rule.
func validateVNFName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", errors.New("name is required")
	case len(name) > 64:
		return "", errors.New("name must be at most 64 characters")
	case !lvmNamePattern.MatchString(name):
		return "", errors.New("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}
	return name, nil
}

// isVNFType reports whether vnfType is one of the accepted types.
func isVNFType(vnfType string, types ...string) bool {
	for _, candidate := range types {
		if vnfType == candidate {
			return true
		}
	}
	return false
}

// vnfNotFound is the typed 404 returned when a typed route receives another
// VNF type or an unknown ID.
func vnfNotFound() error {
	return &VNFError{Status: http.StatusNotFound, Message: "vnf not found"}
}

// validateTypedVNF loads a VNF and returns it only when its stored type is one
// of types; another type is reported as not found so a wrong-type route cannot
// leak or mutate the record.
func (s *Service) validateTypedVNF(ctx context.Context, id int64, types ...string) (store.VNF, error) {
	vnf, err := s.store.GetVNF(ctx, id)
	if err != nil {
		return store.VNF{}, storeError(err)
	}
	if !isVNFType(vnf.Type, types...) {
		return store.VNF{}, vnfNotFound()
	}
	return vnf, nil
}

// validateDHCPDNSIntegration checks that a DHCP VNF's dns_vnf_id references an
// existing DNS VNF on the same network. A zero ID means no integration.
func (s *Service) validateDHCPDNSIntegration(ctx context.Context, networkID, dnsVNFID int64) error {
	if dnsVNFID == 0 {
		return nil
	}
	target, err := s.store.GetVNF(ctx, dnsVNFID)
	if err != nil {
		if errors.Is(err, store.ErrVNFNotFound) {
			return errors.New("selected DNS VNF not found")
		}
		return err
	}
	if target.Type != store.VNFTypeDNS {
		return errors.New("selected VNF is not a DNS VNF")
	}
	if target.NetworkID != networkID {
		return errors.New("selected DNS VNF must belong to the same network")
	}
	return nil
}

// dhcpVNFConfigFromRequest builds the typed scalar config of a DHCP create request.
func dhcpVNFConfigFromRequest(request *rookeryv1.CreateDHCPVNFRequest) store.DHCPVNFConfig {
	return store.DHCPVNFConfig{
		Gateway:    request.GetGateway(),
		RangeStart: request.GetRangeStart(),
		RangeEnd:   request.GetRangeEnd(),
		LeaseTime:  request.GetLeaseTime(),
		DNS:        request.GetDns(),
		BindIP:     request.GetBindIp(),
		Transport:  request.GetTransport(),
		DNSVNFID:   request.GetDnsVnfId(),
		DNSDomain:  request.GetDnsDomain(),
		DNSTTL:     request.GetDnsTtl(),
	}
}

// dhcpVNFConfigFromVNF converts a hydrated VNF config into the typed DHCP config.
func dhcpVNFConfigFromVNF(vnf store.VNF) store.DHCPVNFConfig {
	return store.DHCPVNFConfig{
		Gateway:    vnf.Config.Gateway,
		RangeStart: vnf.Config.RangeStart,
		RangeEnd:   vnf.Config.RangeEnd,
		LeaseTime:  vnf.Config.LeaseTime,
		DNS:        vnf.Config.DNS,
		BindIP:     vnf.Config.BindIP,
		Transport:  vnf.Config.Transport,
		DNSVNFID:   vnf.Config.DNSVNFID,
		DNSDomain:  vnf.Config.DNSDomain,
		DNSTTL:     vnf.Config.DNSTTL,
	}
}

// applyDHCPVNFUpdate overlays the present optional fields of an update request.
func applyDHCPVNFUpdate(cfg *store.DHCPVNFConfig, request *rookeryv1.UpdateDHCPVNFRequest) {
	if request.Gateway != nil {
		cfg.Gateway = request.GetGateway()
	}
	if request.RangeStart != nil {
		cfg.RangeStart = request.GetRangeStart()
	}
	if request.RangeEnd != nil {
		cfg.RangeEnd = request.GetRangeEnd()
	}
	if request.LeaseTime != nil {
		cfg.LeaseTime = request.GetLeaseTime()
	}
	if request.Dns != nil {
		cfg.DNS = request.GetDns()
	}
	if request.BindIp != nil {
		cfg.BindIP = request.GetBindIp()
	}
	if request.Transport != nil {
		cfg.Transport = request.GetTransport()
	}
	if request.DnsVnfId != nil {
		cfg.DNSVNFID = request.GetDnsVnfId()
	}
	if request.DnsDomain != nil {
		cfg.DNSDomain = request.GetDnsDomain()
	}
	if request.DnsTtl != nil {
		cfg.DNSTTL = request.GetDnsTtl()
	}
}

// CreateDHCPVNF validates and stores a DHCPv4 or DHCPv6 VNF, starting its
// runtime when it is enabled. A failed start removes the new record.
func (s *Service) CreateDHCPVNF(ctx context.Context, request *rookeryv1.CreateDHCPVNFRequest) (store.VNF, error) {
	name, err := validateVNFName(request.GetName())
	if err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	family := strings.ToLower(strings.TrimSpace(request.GetFamily()))
	if family != store.VNFTypeDHCPv4 && family != store.VNFTypeDHCPv6 {
		return store.VNF{}, inputError("family must be dhcpv4 or dhcpv6")
	}
	if request.GetNetworkId() < 1 {
		return store.VNF{}, inputError("network_id is required")
	}
	cfg, err := s.backend.NormalizeDHCPVNFConfig(family, dhcpVNFConfigFromRequest(request))
	if err != nil {
		return store.VNF{}, inputError(err.Error())
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	network, err := s.store.GetNetwork(ctx, request.GetNetworkId())
	if err != nil {
		if errors.Is(err, store.ErrNetworkNotFound) {
			return store.VNF{}, inputError("network not found")
		}
		return store.VNF{}, internalError(err)
	}
	if err := s.validateDHCPDNSIntegration(ctx, network.ID, cfg.DNSVNFID); err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	if err := s.backend.ApplyNATGatewayToDHCPConfig(ctx, network.ID, family, &cfg); err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	enabled := request.Enabled != nil && request.GetEnabled()
	if enabled {
		if err := s.backend.VNFWritesReady(family); err != nil {
			return store.VNF{}, unavailableError(err.Error())
		}
	}
	created, err := s.store.CreateDHCPVNF(ctx, store.NewDHCPVNF{
		Name: name, NetworkID: network.ID, Family: family, Enabled: false, Config: cfg,
	})
	if err != nil {
		return store.VNF{}, storeError(err)
	}
	if !enabled {
		return created, nil
	}
	activated, err := s.backend.ActivateVNF(ctx, created)
	if err != nil {
		_ = s.store.DeleteVNF(ctx, created.ID)
		return store.VNF{}, internalError(fmt.Errorf("could not start VNF: %w", err))
	}
	return activated, nil
}

// UpdateDHCPVNF merges the present scalar fields of a DHCP update, stops the
// old runtime, stores the new configuration, and starts the runtime when the
// VNF is enabled.
func (s *Service) UpdateDHCPVNF(ctx context.Context, request *rookeryv1.UpdateDHCPVNFRequest) (store.VNF, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	existing, err := s.validateTypedVNF(ctx, request.GetId(), store.VNFTypeDHCPv4, store.VNFTypeDHCPv6)
	if err != nil {
		return store.VNF{}, err
	}
	name := existing.Name
	if request.Name != nil {
		name, err = validateVNFName(request.GetName())
		if err != nil {
			return store.VNF{}, inputError(err.Error())
		}
	}
	cfg := dhcpVNFConfigFromVNF(existing)
	applyDHCPVNFUpdate(&cfg, request)
	cfg, err = s.backend.NormalizeDHCPVNFConfig(existing.Type, cfg)
	if err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	if err := s.validateDHCPDNSIntegration(ctx, existing.NetworkID, cfg.DNSVNFID); err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	if err := s.backend.ApplyNATGatewayToDHCPConfig(ctx, existing.NetworkID, existing.Type, &cfg); err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	enabled := existing.Enabled
	if request.Enabled != nil {
		enabled = request.GetEnabled()
	}
	if enabled {
		if err := s.backend.VNFWritesReady(existing.Type); err != nil {
			return store.VNF{}, unavailableError(err.Error())
		}
	}
	if existing.Enabled || existing.TapSwIfIndex > 0 || existing.MemifSwIfIndex > 0 {
		if err := s.backend.StopVNF(ctx, existing, false); err != nil {
			return store.VNF{}, internalError(fmt.Errorf("could not stop VNF: %w", err))
		}
	}
	identity := existing
	identity.Name = name
	identity.Enabled = false
	identity.TapSwIfIndex = 0
	identity.TapHostName = ""
	identity.MemifSwIfIndex = 0
	identity.MemifSocket = ""
	identity.MemifHostName = ""
	saved, err := s.store.UpdateDHCPVNF(ctx, identity, cfg)
	if err != nil {
		return store.VNF{}, s.restoreAfterFailedStore(ctx, existing, err)
	}
	if !enabled {
		return saved, nil
	}
	activated, err := s.backend.ActivateVNF(ctx, saved)
	if err != nil {
		_, _ = s.store.UpdateVNFIdentity(ctx, saved)
		return store.VNF{}, internalError(fmt.Errorf("could not start VNF: %w", err))
	}
	return activated, nil
}

// CreateDNSVNF validates and stores a DNS VNF, starting it when enabled.
func (s *Service) CreateDNSVNF(ctx context.Context, request *rookeryv1.CreateDNSVNFRequest) (store.VNF, error) {
	name, err := validateVNFName(request.GetName())
	if err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	if request.GetNetworkId() < 1 {
		return store.VNF{}, inputError("network_id is required")
	}
	forward := request.Forward != nil && request.GetForward()
	enabled := request.Enabled != nil && request.GetEnabled()

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	network, err := s.store.GetNetwork(ctx, request.GetNetworkId())
	if err != nil {
		if errors.Is(err, store.ErrNetworkNotFound) {
			return store.VNF{}, inputError("network not found")
		}
		return store.VNF{}, internalError(err)
	}
	if enabled {
		if err := s.backend.VNFWritesReady(store.VNFTypeDNS); err != nil {
			return store.VNF{}, unavailableError(err.Error())
		}
	}
	created, err := s.store.CreateDNSVNF(ctx, store.NewDNSVNF{
		Name: name, NetworkID: network.ID, Enabled: false,
		Config: store.DNSVNFConfig{Forward: forward},
	})
	if err != nil {
		return store.VNF{}, storeError(err)
	}
	if !enabled {
		return created, nil
	}
	activated, err := s.backend.ActivateVNF(ctx, created)
	if err != nil {
		_ = s.store.DeleteVNF(ctx, created.ID)
		return store.VNF{}, internalError(fmt.Errorf("could not start VNF: %w", err))
	}
	return activated, nil
}

// UpdateDNSVNF applies a DNS parent update, restarting an enabled runtime.
func (s *Service) UpdateDNSVNF(ctx context.Context, request *rookeryv1.UpdateDNSVNFRequest) (store.VNF, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	existing, err := s.validateTypedVNF(ctx, request.GetId(), store.VNFTypeDNS)
	if err != nil {
		return store.VNF{}, err
	}
	name := existing.Name
	if request.Name != nil {
		name, err = validateVNFName(request.GetName())
		if err != nil {
			return store.VNF{}, inputError(err.Error())
		}
	}
	forward := existing.Config.DNSForward
	if request.Forward != nil {
		forward = request.GetForward()
	}
	enabled := existing.Enabled
	if request.Enabled != nil {
		enabled = request.GetEnabled()
	}
	if enabled {
		if err := s.backend.VNFWritesReady(store.VNFTypeDNS); err != nil {
			return store.VNF{}, unavailableError(err.Error())
		}
	}
	if existing.Enabled || existing.TapSwIfIndex > 0 || existing.MemifSwIfIndex > 0 {
		if err := s.backend.StopVNF(ctx, existing, false); err != nil {
			return store.VNF{}, internalError(fmt.Errorf("could not stop VNF: %w", err))
		}
	}
	identity := existing
	identity.Name = name
	identity.Enabled = false
	identity.TapSwIfIndex = 0
	identity.TapHostName = ""
	identity.MemifSwIfIndex = 0
	identity.MemifSocket = ""
	identity.MemifHostName = ""
	saved, err := s.store.UpdateDNSVNF(ctx, identity, store.DNSVNFConfig{Forward: forward})
	if err != nil {
		return store.VNF{}, s.restoreAfterFailedStore(ctx, existing, err)
	}
	if !enabled {
		return saved, nil
	}
	activated, err := s.backend.ActivateVNF(ctx, saved)
	if err != nil {
		_, _ = s.store.UpdateVNFIdentity(ctx, saved)
		return store.VNF{}, internalError(fmt.Errorf("could not start VNF: %w", err))
	}
	return activated, nil
}

// nat44VNFConfigFromRequest builds the typed scalar config of a NAT44 create request.
func nat44VNFConfigFromRequest(request *rookeryv1.CreateNAT44VNFRequest) store.NAT44VNFConfig {
	return store.NAT44VNFConfig{
		Gateway:          request.GetGateway(),
		OutsideInterface: request.GetOutsideInterface(),
		OutsideAddress:   request.GetOutsideAddress(),
		OutsideGateway:   request.GetOutsideGateway(),
		NAT44Pool:        request.GetNat44Pool(),
	}
}

// nat44VNFConfigFromVNF converts a hydrated VNF config into the typed NAT44 config.
func nat44VNFConfigFromVNF(vnf store.VNF) store.NAT44VNFConfig {
	return store.NAT44VNFConfig{
		Gateway:          vnf.Config.Gateway,
		OutsideInterface: vnf.Config.OutsideInterface,
		OutsideAddress:   vnf.Config.OutsideAddress,
		OutsideGateway:   vnf.Config.OutsideGateway,
		NAT44Pool:        vnf.Config.NAT44Pool,
	}
}

// applyNAT44VNFUpdate overlays the present optional fields of a NAT44 update.
func applyNAT44VNFUpdate(cfg *store.NAT44VNFConfig, request *rookeryv1.UpdateNAT44VNFRequest) {
	if request.Gateway != nil {
		cfg.Gateway = request.GetGateway()
	}
	if request.OutsideInterface != nil {
		cfg.OutsideInterface = request.GetOutsideInterface()
	}
	if request.OutsideAddress != nil {
		cfg.OutsideAddress = request.GetOutsideAddress()
	}
	if request.OutsideGateway != nil {
		cfg.OutsideGateway = request.GetOutsideGateway()
	}
	if request.Nat44Pool != nil {
		cfg.NAT44Pool = request.GetNat44Pool()
	}
}

// CreateNAT44VNF validates and stores a NAT44 VNF, activating it when enabled.
func (s *Service) CreateNAT44VNF(ctx context.Context, request *rookeryv1.CreateNAT44VNFRequest) (store.VNF, error) {
	name, err := validateVNFName(request.GetName())
	if err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	if request.GetNetworkId() < 1 {
		return store.VNF{}, inputError("network_id is required")
	}
	cfg, err := s.backend.NormalizeNAT44VNFConfig(nat44VNFConfigFromRequest(request))
	if err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	enabled := request.Enabled != nil && request.GetEnabled()

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	network, err := s.store.GetNetwork(ctx, request.GetNetworkId())
	if err != nil {
		if errors.Is(err, store.ErrNetworkNotFound) {
			return store.VNF{}, inputError("network not found")
		}
		return store.VNF{}, internalError(err)
	}
	if enabled {
		if err := s.backend.VNFWritesReady(store.VNFTypeNAT44); err != nil {
			return store.VNF{}, unavailableError(err.Error())
		}
	}
	created, err := s.store.CreateNAT44VNF(ctx, store.NewNAT44VNF{
		Name: name, NetworkID: network.ID, Enabled: false, Config: cfg,
	})
	if err != nil {
		return store.VNF{}, storeError(err)
	}
	if !enabled {
		return created, nil
	}
	activated, err := s.backend.ActivateNAT44(ctx, created)
	if err != nil {
		_ = s.store.DeleteVNF(ctx, created.ID)
		return store.VNF{}, internalError(fmt.Errorf("could not start NAT44: %w", err))
	}
	return activated, nil
}

// UpdateNAT44VNF applies a NAT44 parent update, restarting an enabled runtime.
func (s *Service) UpdateNAT44VNF(ctx context.Context, request *rookeryv1.UpdateNAT44VNFRequest) (store.VNF, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	existing, err := s.validateTypedVNF(ctx, request.GetId(), store.VNFTypeNAT44)
	if err != nil {
		return store.VNF{}, err
	}
	name := existing.Name
	if request.Name != nil {
		name, err = validateVNFName(request.GetName())
		if err != nil {
			return store.VNF{}, inputError(err.Error())
		}
	}
	cfg := nat44VNFConfigFromVNF(existing)
	applyNAT44VNFUpdate(&cfg, request)
	cfg, err = s.backend.NormalizeNAT44VNFConfig(cfg)
	if err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	enabled := existing.Enabled
	if request.Enabled != nil {
		enabled = request.GetEnabled()
	}
	if enabled {
		if err := s.backend.VNFWritesReady(store.VNFTypeNAT44); err != nil {
			return store.VNF{}, unavailableError(err.Error())
		}
	}
	if err := s.backend.StopNAT44(ctx, existing); err != nil {
		return store.VNF{}, internalError(fmt.Errorf("could not stop NAT44: %w", err))
	}
	identity := existing
	identity.Name = name
	identity.Enabled = false
	identity.TapSwIfIndex = 0
	identity.TapHostName = ""
	saved, err := s.store.UpdateNAT44VNF(ctx, identity, cfg)
	if err != nil {
		return store.VNF{}, s.restoreAfterFailedStore(ctx, existing, err)
	}
	if !enabled {
		return saved, nil
	}
	activated, err := s.backend.ActivateNAT44(ctx, saved)
	if err != nil {
		_, _ = s.store.UpdateVNFIdentity(ctx, saved)
		return store.VNF{}, internalError(fmt.Errorf("could not start NAT44: %w", err))
	}
	return activated, nil
}

// bgpVNFConfigFromRequest builds the typed scalar config of a BGP create request.
func bgpVNFConfigFromRequest(request *rookeryv1.CreateBGPVNFRequest) store.BGPVNFConfig {
	return store.BGPVNFConfig{
		Transport:     request.GetTransport(),
		LocalAS:       request.GetLocalAs(),
		RouterID:      request.GetRouterId(),
		PeerAS:        request.GetPeerAs(),
		PeerIP:        request.GetPeerIp(),
		LocalIP:       request.GetLocalIp(),
		PeerGateway:   request.GetPeerGateway(),
		MultihopTTL:   request.GetMultihopTtl(),
		Password:      request.GetPassword(),
		Passive:       request.GetPassive(),
		TargetTableID: request.GetTargetTableId(),
		NextHop:       request.GetNextHop(),
	}
}

// bgpVNFConfigFromVNF converts a hydrated VNF config into the typed BGP config,
// including the write-only password so an update that omits it keeps it.
func bgpVNFConfigFromVNF(vnf store.VNF) store.BGPVNFConfig {
	return store.BGPVNFConfig{
		Transport:     vnf.Config.Transport,
		LocalAS:       vnf.Config.LocalAS,
		RouterID:      vnf.Config.RouterID,
		PeerAS:        vnf.Config.PeerAS,
		PeerIP:        vnf.Config.PeerIP,
		LocalIP:       vnf.Config.LocalIP,
		PeerGateway:   vnf.Config.PeerGateway,
		MultihopTTL:   vnf.Config.MultihopTTL,
		Password:      vnf.Config.Password,
		Passive:       vnf.Config.Passive,
		TargetTableID: vnf.Config.TargetTableID,
		NextHop:       vnf.Config.NextHop,
	}
}

// applyBGPVNFUpdate overlays the present optional fields of a BGP update.
func applyBGPVNFUpdate(cfg *store.BGPVNFConfig, request *rookeryv1.UpdateBGPVNFRequest) {
	if request.Transport != nil {
		cfg.Transport = request.GetTransport()
	}
	if request.LocalAs != nil {
		cfg.LocalAS = request.GetLocalAs()
	}
	if request.RouterId != nil {
		cfg.RouterID = request.GetRouterId()
	}
	if request.PeerAs != nil {
		cfg.PeerAS = request.GetPeerAs()
	}
	if request.PeerIp != nil {
		cfg.PeerIP = request.GetPeerIp()
	}
	if request.LocalIp != nil {
		cfg.LocalIP = request.GetLocalIp()
	}
	if request.PeerGateway != nil {
		cfg.PeerGateway = request.GetPeerGateway()
	}
	if request.MultihopTtl != nil {
		cfg.MultihopTTL = request.GetMultihopTtl()
	}
	if request.Password != nil {
		cfg.Password = request.GetPassword()
	}
	if request.Passive != nil {
		cfg.Passive = request.GetPassive()
	}
	if request.TargetTableId != nil {
		cfg.TargetTableID = request.GetTargetTableId()
	}
	if request.NextHop != nil {
		cfg.NextHop = request.GetNextHop()
	}
}

// CreateBGPVNF validates and stores a BGP VNF, starting it when enabled.
func (s *Service) CreateBGPVNF(ctx context.Context, request *rookeryv1.CreateBGPVNFRequest) (store.VNF, error) {
	name, err := validateVNFName(request.GetName())
	if err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	if request.GetNetworkId() < 1 {
		return store.VNF{}, inputError("network_id is required")
	}
	cfg, err := s.backend.NormalizeBGPVNFConfig(bgpVNFConfigFromRequest(request))
	if err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	enabled := request.Enabled != nil && request.GetEnabled()

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	network, err := s.store.GetNetwork(ctx, request.GetNetworkId())
	if err != nil {
		if errors.Is(err, store.ErrNetworkNotFound) {
			return store.VNF{}, inputError("network not found")
		}
		return store.VNF{}, internalError(err)
	}
	if enabled {
		if err := s.backend.VNFWritesReady(store.VNFTypeBGP); err != nil {
			return store.VNF{}, unavailableError(err.Error())
		}
	}
	created, err := s.store.CreateBGPVNF(ctx, store.NewBGPVNF{
		Name: name, NetworkID: network.ID, Enabled: false, Config: cfg,
	})
	if err != nil {
		return store.VNF{}, storeError(err)
	}
	if !enabled {
		return created, nil
	}
	activated, err := s.backend.ActivateVNF(ctx, created)
	if err != nil {
		_ = s.store.DeleteVNF(ctx, created.ID)
		return store.VNF{}, internalError(fmt.Errorf("could not start VNF: %w", err))
	}
	return activated, nil
}

// UpdateBGPVNF applies a BGP parent update, restarting an enabled runtime.
func (s *Service) UpdateBGPVNF(ctx context.Context, request *rookeryv1.UpdateBGPVNFRequest) (store.VNF, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	existing, err := s.validateTypedVNF(ctx, request.GetId(), store.VNFTypeBGP)
	if err != nil {
		return store.VNF{}, err
	}
	name := existing.Name
	if request.Name != nil {
		name, err = validateVNFName(request.GetName())
		if err != nil {
			return store.VNF{}, inputError(err.Error())
		}
	}
	cfg := bgpVNFConfigFromVNF(existing)
	applyBGPVNFUpdate(&cfg, request)
	cfg, err = s.backend.NormalizeBGPVNFConfig(cfg)
	if err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	enabled := existing.Enabled
	if request.Enabled != nil {
		enabled = request.GetEnabled()
	}
	if enabled {
		if err := s.backend.VNFWritesReady(store.VNFTypeBGP); err != nil {
			return store.VNF{}, unavailableError(err.Error())
		}
	}
	if existing.Enabled || existing.TapSwIfIndex > 0 || existing.MemifSwIfIndex > 0 {
		if err := s.backend.StopVNF(ctx, existing, false); err != nil {
			return store.VNF{}, internalError(fmt.Errorf("could not stop VNF: %w", err))
		}
	}
	identity := existing
	identity.Name = name
	identity.Enabled = false
	identity.TapSwIfIndex = 0
	identity.TapHostName = ""
	identity.MemifSwIfIndex = 0
	identity.MemifSocket = ""
	identity.MemifHostName = ""
	saved, err := s.store.UpdateBGPVNF(ctx, identity, cfg)
	if err != nil {
		return store.VNF{}, s.restoreAfterFailedStore(ctx, existing, err)
	}
	if !enabled {
		return saved, nil
	}
	activated, err := s.backend.ActivateVNF(ctx, saved)
	if err != nil {
		_, _ = s.store.UpdateVNFIdentity(ctx, saved)
		return store.VNF{}, internalError(fmt.Errorf("could not start VNF: %w", err))
	}
	return activated, nil
}

// DeleteDHCPVNF removes a DHCP VNF after stopping its runtime.
func (s *Service) DeleteDHCPVNF(ctx context.Context, request *rookeryv1.DeleteDHCPVNFRequest) error {
	return s.deleteVNF(ctx, request.GetId(), store.VNFTypeDHCPv4, store.VNFTypeDHCPv6)
}

// DeleteDNSVNF removes a DNS VNF after checking DHCP references.
func (s *Service) DeleteDNSVNF(ctx context.Context, request *rookeryv1.DeleteDNSVNFRequest) error {
	return s.deleteVNF(ctx, request.GetId(), store.VNFTypeDNS)
}

// DeleteNAT44VNF removes a NAT44 VNF and cleans up the network BVI.
func (s *Service) DeleteNAT44VNF(ctx context.Context, request *rookeryv1.DeleteNAT44VNFRequest) error {
	return s.deleteVNF(ctx, request.GetId(), store.VNFTypeNAT44)
}

// DeleteBGPVNF removes a BGP VNF after stopping its runtime.
func (s *Service) DeleteBGPVNF(ctx context.Context, request *rookeryv1.DeleteBGPVNFRequest) error {
	return s.deleteVNF(ctx, request.GetId(), store.VNFTypeBGP)
}

// deleteVNF stops and deletes a VNF, refusing to delete a DNS VNF that a DHCP
// VNF still references and cleaning up the network BVI after NAT44 removal.
func (s *Service) deleteVNF(ctx context.Context, id int64, types ...string) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, id, types...)
	if err != nil {
		return err
	}
	if vnf.Type == store.VNFTypeDNS {
		vnfs, err := s.store.ListVNFs(ctx, nil)
		if err != nil {
			return internalError(err)
		}
		for _, item := range vnfs {
			if item.Config.DNSVNFID == vnf.ID {
				return conflictError(fmt.Sprintf("DNS VNF %s is used by DHCP VNF %s", vnf.Name, item.Name))
			}
		}
	}
	var stopErr error
	if vnf.Type == store.VNFTypeNAT44 {
		stopErr = s.backend.StopNAT44(ctx, vnf)
	} else {
		stopErr = s.backend.StopVNF(ctx, vnf, true)
	}
	if stopErr != nil {
		return internalError(fmt.Errorf("could not stop VNF: %w", stopErr))
	}
	if err := s.store.DeleteVNF(ctx, vnf.ID); err != nil {
		return storeError(err)
	}
	if vnf.Type == store.VNFTypeNAT44 {
		_ = s.backend.CleanupNetworkBVIAfterNATDelete(ctx, vnf.NetworkID)
	}
	return nil
}

// RestartDHCPVNF stops and starts a DHCP VNF without dropping its leases.
func (s *Service) RestartDHCPVNF(ctx context.Context, request *rookeryv1.RestartDHCPVNFRequest) (store.VNF, error) {
	return s.restartVNF(ctx, request.GetId(), store.VNFTypeDHCPv4, store.VNFTypeDHCPv6)
}

// RestartDNSVNF stops and starts a DNS VNF.
func (s *Service) RestartDNSVNF(ctx context.Context, request *rookeryv1.RestartDNSVNFRequest) (store.VNF, error) {
	return s.restartVNF(ctx, request.GetId(), store.VNFTypeDNS)
}

// RestartNAT44VNF stops and starts a NAT44 VNF.
func (s *Service) RestartNAT44VNF(ctx context.Context, request *rookeryv1.RestartNAT44VNFRequest) (store.VNF, error) {
	return s.restartVNF(ctx, request.GetId(), store.VNFTypeNAT44)
}

// RestartBGPVNF stops and starts a BGP VNF.
func (s *Service) RestartBGPVNF(ctx context.Context, request *rookeryv1.RestartBGPVNFRequest) (store.VNF, error) {
	return s.restartVNF(ctx, request.GetId(), store.VNFTypeBGP)
}

// restartVNF stops a VNF without dropping its leases and starts it again,
// persisting Enabled=false when the restart fails.
func (s *Service) restartVNF(ctx context.Context, id int64, types ...string) (store.VNF, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.validateTypedVNF(ctx, id, types...)
	if err != nil {
		return store.VNF{}, err
	}
	if err := s.backend.VNFWritesReady(vnf.Type); err != nil {
		return store.VNF{}, unavailableError(err.Error())
	}
	var stopErr error
	if vnf.Type == store.VNFTypeNAT44 {
		stopErr = s.backend.StopNAT44(ctx, vnf)
	} else {
		stopErr = s.backend.StopVNF(ctx, vnf, false)
	}
	if stopErr != nil {
		return store.VNF{}, internalError(fmt.Errorf("could not stop VNF: %w", stopErr))
	}
	vnf.Enabled = false
	vnf.TapSwIfIndex = 0
	vnf.TapHostName = ""
	vnf.MemifSwIfIndex = 0
	vnf.MemifSocket = ""
	vnf.MemifHostName = ""
	saved, err := s.store.UpdateVNFIdentity(ctx, vnf)
	if err != nil {
		return store.VNF{}, storeError(err)
	}
	var activated store.VNF
	if saved.Type == store.VNFTypeNAT44 {
		activated, err = s.backend.ActivateNAT44(ctx, saved)
	} else {
		activated, err = s.backend.ActivateVNF(ctx, saved)
	}
	if err != nil {
		return store.VNF{}, internalError(fmt.Errorf("could not restart VNF: %w", err))
	}
	return activated, nil
}

// storeError maps store sentinel errors to typed VNFErrors.
func storeError(err error) error {
	switch {
	case errors.Is(err, store.ErrVNFNameTaken):
		return conflictError("a VNF with that name already exists")
	case errors.Is(err, store.ErrVNFTypeTaken):
		return conflictError("network already has a VNF of that type")
	case errors.Is(err, store.ErrVNFNotFound), errors.Is(err, store.ErrVNFTypeMismatch):
		return vnfNotFound()
	case errors.Is(err, store.ErrVNFChildTaken):
		return conflictError(err.Error())
	case errors.Is(err, store.ErrVNFChildNotFound):
		return &VNFError{Status: http.StatusNotFound, Message: "vnf child not found"}
	default:
		return internalError(err)
	}
}

// restoreAfterFailedStore brings the previous runtime back when a parent update
// stopped it and the store write then failed (for example a 409 name conflict).
// Without this the row still says enabled=true while the runtime is down and
// nothing repairs it until the next API start.
func (s *Service) restoreAfterFailedStore(ctx context.Context, previous store.VNF, saveErr error) error {
	if !previous.Enabled {
		return storeError(saveErr)
	}
	var restoreErr error
	if previous.Type == store.VNFTypeNAT44 {
		_, restoreErr = s.backend.ActivateNAT44(ctx, previous)
	} else {
		_, restoreErr = s.backend.ActivateVNF(ctx, previous)
	}
	if restoreErr != nil {
		return &VNFError{Status: 500, Message: fmt.Sprintf(
			"could not save the VNF: %v; could not restore the previous runtime: %v", saveErr, restoreErr)}
	}
	return storeError(saveErr)
}
