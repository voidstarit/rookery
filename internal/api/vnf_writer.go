package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// VNFError carries the HTTP status and public message for a VNF write. The
// grpc-gateway maps it back to a gRPC status.
type VNFError struct {
	Status  int
	Message string
	Err     error
}

func (e *VNFError) Error() string {
	switch {
	case e.Message != "":
		return e.Message
	case e.Err != nil:
		return e.Err.Error()
	default:
		return "VNF operation failed"
	}
}

func (e *VNFError) Unwrap() error { return e.Err }

// VNFStatus returns the HTTP status and message for gateway error mapping.
func (e *VNFError) VNFStatus() (int, string) {
	if e.Status >= 500 {
		return e.Status, "VNF operation failed"
	}
	return e.Status, e.Error()
}

func vnfInputError(message string) error {
	return &VNFError{Status: http.StatusBadRequest, Message: message}
}

func vnfConflictError(message string) error {
	return &VNFError{Status: http.StatusConflict, Message: message}
}

func vnfUnavailableError(message string) error {
	return &VNFError{Status: http.StatusServiceUnavailable, Message: message}
}

func vnfInternalError(err error) error {
	return &VNFError{Status: http.StatusInternalServerError, Err: err}
}

func (s *Server) CreateVNF(ctx context.Context, request *rookeryv1.CreateVNFRequest) (store.VNF, error) {
	name := request.GetName()
	networkID := request.GetNetworkId()
	vnfType := request.GetType()
	payload := vnfPayload{
		Name:      &name,
		NetworkID: &networkID,
		Type:      &vnfType,
		Enabled:   request.Enabled,
		Config:    vnfConfigPayloadFromProto(request.GetConfig()),
	}
	return s.createVNF(ctx, payload)
}

func (s *Server) UpdateVNF(ctx context.Context, request *rookeryv1.UpdateVNFRequest) (store.VNF, error) {
	payload := vnfUpdatePayload{Enabled: request.Enabled}
	payload.Name = request.Name
	if request.Config != nil {
		payload.Config = vnfConfigPayloadFromProto(request.Config)
	}
	return s.updateVNF(ctx, request.GetId(), payload)
}

func (s *Server) DeleteVNF(ctx context.Context, request *rookeryv1.DeleteVNFRequest) error {
	return s.deleteVNF(ctx, request.GetId())
}

func (s *Server) RestartVNF(ctx context.Context, request *rookeryv1.RestartVNFRequest) (store.VNF, error) {
	return s.restartVNF(ctx, request.GetId())
}

func (s *Server) createVNF(ctx context.Context, payload vnfPayload) (store.VNF, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	name, err := payload.toName()
	if err != nil {
		return store.VNF{}, vnfInputError(err.Error())
	}
	vnfType, err := payload.toType()
	if err != nil {
		return store.VNF{}, vnfInputError(err.Error())
	}
	if payload.NetworkID == nil || *payload.NetworkID < 1 {
		return store.VNF{}, vnfInputError("network_id is required")
	}
	if payload.Config == nil {
		return store.VNF{}, vnfInputError("config is required")
	}
	config, err := payload.Config.toConfig(vnfType)
	if err != nil {
		return store.VNF{}, vnfInputError(err.Error())
	}
	network, err := s.store.GetNetwork(ctx, *payload.NetworkID)
	switch {
	case errors.Is(err, store.ErrNetworkNotFound):
		return store.VNF{}, vnfInputError("network not found")
	case err != nil:
		return store.VNF{}, vnfInternalError(err)
	}
	existingVNFs, err := s.store.ListVNFsByNetwork(ctx, network.ID)
	if err != nil {
		return store.VNF{}, vnfInternalError(err)
	}
	for _, existing := range existingVNFs {
		if existing.Type == vnfType {
			return store.VNF{}, vnfConflictError(fmt.Sprintf("network already has a %s VNF", vnfType))
		}
	}
	if err := applyNATGatewayToDHCPConfig(network, vnfType, &config); err != nil {
		return store.VNF{}, vnfInputError(err.Error())
	}
	enabled := payload.Enabled != nil && *payload.Enabled
	if enabled {
		if blocked, message := s.vppBlocked(); blocked {
			return store.VNF{}, vnfUnavailableError(message)
		}
		if s.vnfs == nil || s.networks == nil {
			return store.VNF{}, vnfUnavailableError("VNF runtime is not configured")
		}
		if vnfType == store.VNFTypeNAT44 && s.nat == nil {
			return store.VNF{}, vnfUnavailableError("NAT44 runtime is not configured")
		}
	}
	created, err := s.store.CreateVNF(ctx, store.VNF{
		Name: name, NetworkID: network.ID, Type: vnfType, Enabled: false, Config: config,
	})
	if err != nil {
		if errors.Is(err, store.ErrVNFNameTaken) {
			return store.VNF{}, vnfConflictError("a VNF with that name already exists")
		}
		return store.VNF{}, vnfInternalError(err)
	}
	if !enabled {
		return created, nil
	}
	var activated store.VNF
	if vnfType == store.VNFTypeNAT44 {
		activated, err = s.activateNAT44(ctx, created)
	} else {
		activated, err = s.activateVNF(ctx, created)
	}
	if err != nil {
		_ = s.store.DeleteVNF(ctx, created.ID)
		return store.VNF{}, vnfInternalError(fmt.Errorf("could not start VNF: %w", err))
	}
	return activated, nil
}

func (s *Server) updateVNF(ctx context.Context, id int64, payload vnfUpdatePayload) (store.VNF, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	existing, err := s.store.GetVNF(ctx, id)
	switch {
	case errors.Is(err, store.ErrVNFNotFound):
		return store.VNF{}, &VNFError{Status: http.StatusNotFound, Message: "vnf not found"}
	case err != nil:
		return store.VNF{}, vnfInternalError(err)
	}
	name := existing.Name
	if payload.Name != nil {
		name, err = vnfUpdateName(payload.Name)
		if err != nil {
			return store.VNF{}, vnfInputError(err.Error())
		}
	}
	config := existing.Config
	if payload.Config != nil {
		config, err = payload.Config.toConfig(existing.Type)
		if err != nil {
			return store.VNF{}, vnfInputError(err.Error())
		}
	}
	if existing.Type == store.VNFTypeDHCPv4 {
		if network, getErr := s.store.GetNetwork(ctx, existing.NetworkID); getErr == nil {
			if err := applyNATGatewayToDHCPConfig(network, existing.Type, &config); err != nil {
				return store.VNF{}, vnfInputError(err.Error())
			}
		}
	}
	enabled := existing.Enabled
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	if enabled {
		if blocked, message := s.vppBlocked(); blocked {
			return store.VNF{}, vnfUnavailableError(message)
		}
		if s.vnfs == nil || s.networks == nil {
			return store.VNF{}, vnfUnavailableError("VNF runtime is not configured")
		}
		if existing.Type == store.VNFTypeNAT44 && s.nat == nil {
			return store.VNF{}, vnfUnavailableError("NAT44 runtime is not configured")
		}
	}
	updated := existing
	updated.Name = name
	updated.Config = config
	updated.Enabled = enabled
	if existing.Type == store.VNFTypeNAT44 {
		if err := s.stopNAT44(ctx, existing); err != nil {
			return store.VNF{}, vnfInternalError(fmt.Errorf("could not stop NAT44: %w", err))
		}
		updated.Enabled = false
		updated.TapSwIfIndex = 0
		updated.TapHostName = ""
		if enabled {
			activated, err := s.activateNAT44(ctx, updated)
			if err != nil {
				_, _ = s.store.UpdateVNF(ctx, id, updated)
				return store.VNF{}, vnfInternalError(fmt.Errorf("could not start NAT44: %w", err))
			}
			return activated, nil
		}
		saved, err := s.store.UpdateVNF(ctx, id, updated)
		if err != nil {
			return store.VNF{}, vnfStoreError(err)
		}
		return saved, nil
	}
	if existing.Enabled || existing.TapSwIfIndex > 0 {
		if err := s.stopVNF(ctx, existing, false); err != nil {
			return store.VNF{}, vnfInternalError(fmt.Errorf("could not stop VNF: %w", err))
		}
		updated.TapSwIfIndex = 0
		updated.TapHostName = ""
	}
	if enabled {
		activated, err := s.activateVNF(ctx, updated)
		if err != nil {
			updated.Enabled = false
			updated.TapSwIfIndex = 0
			updated.TapHostName = ""
			_, _ = s.store.UpdateVNF(ctx, id, updated)
			return store.VNF{}, vnfInternalError(fmt.Errorf("could not start VNF: %w", err))
		}
		return activated, nil
	}
	saved, err := s.store.UpdateVNF(ctx, id, updated)
	if err != nil {
		return store.VNF{}, vnfStoreError(err)
	}
	return saved, nil
}

func (s *Server) deleteVNF(ctx context.Context, id int64) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	vnf, err := s.store.GetVNF(ctx, id)
	switch {
	case errors.Is(err, store.ErrVNFNotFound):
		return &VNFError{Status: http.StatusNotFound, Message: "vnf not found"}
	case err != nil:
		return vnfInternalError(err)
	}
	var stopErr error
	if vnf.Type == store.VNFTypeNAT44 {
		stopErr = s.stopNAT44(ctx, vnf)
	} else {
		stopErr = s.stopVNF(ctx, vnf, true)
	}
	if stopErr != nil {
		return vnfInternalError(fmt.Errorf("could not stop VNF: %w", stopErr))
	}
	if err := s.store.DeleteVNF(ctx, id); err != nil {
		if errors.Is(err, store.ErrVNFNotFound) {
			return &VNFError{Status: http.StatusNotFound, Message: "vnf not found"}
		}
		return vnfInternalError(err)
	}
	if vnf.Type == store.VNFTypeNAT44 {
		_ = s.cleanupNetworkBVIAfterNATDelete(ctx, vnf.NetworkID)
	}
	return nil
}

func (s *Server) restartVNF(ctx context.Context, id int64) (store.VNF, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	vnf, err := s.store.GetVNF(ctx, id)
	switch {
	case errors.Is(err, store.ErrVNFNotFound):
		return store.VNF{}, &VNFError{Status: http.StatusNotFound, Message: "vnf not found"}
	case err != nil:
		return store.VNF{}, vnfInternalError(err)
	}
	if s.vnfs == nil || s.networks == nil {
		return store.VNF{}, vnfUnavailableError("VNF runtime is not configured")
	}
	if blocked, message := s.vppBlocked(); blocked {
		return store.VNF{}, vnfUnavailableError(message)
	}
	if vnf.Type == store.VNFTypeNAT44 && s.nat == nil {
		return store.VNF{}, vnfUnavailableError("NAT44 runtime is not configured")
	}
	var stopErr error
	if vnf.Type == store.VNFTypeNAT44 {
		stopErr = s.stopNAT44(ctx, vnf)
	} else {
		stopErr = s.stopVNF(ctx, vnf, false)
	}
	if stopErr != nil {
		return store.VNF{}, vnfInternalError(fmt.Errorf("could not stop VNF: %w", stopErr))
	}
	vnf.Enabled = true
	vnf.TapSwIfIndex = 0
	vnf.TapHostName = ""
	var activated store.VNF
	if vnf.Type == store.VNFTypeNAT44 {
		activated, err = s.activateNAT44(ctx, vnf)
	} else {
		activated, err = s.activateVNF(ctx, vnf)
	}
	if err != nil {
		vnf.Enabled = false
		_, _ = s.store.UpdateVNF(ctx, id, vnf)
		return store.VNF{}, vnfInternalError(fmt.Errorf("could not restart VNF: %w", err))
	}
	return activated, nil
}

func vnfStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrVNFNameTaken):
		return vnfConflictError("a VNF with that name already exists")
	case errors.Is(err, store.ErrVNFNotFound):
		return &VNFError{Status: http.StatusNotFound, Message: "vnf not found"}
	default:
		return vnfInternalError(err)
	}
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
