// Package lifecycle owns VNF and network provisioning lifecycle operations.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/netutil"
	"github.com/voidstarit/rookery/internal/store"
)

// Backend exposes the VPP/NAT/DHCP runtime used by lifecycle operations.
type Backend interface {
	VPPBlocked() (bool, string)
	NormalizeVNFConfig(vnfType string, config *rookeryv1.VNFConfig) (store.VNFConfig, error)
	VNFWritesReady(vnfType string) error
	ApplyNATGatewayToDHCPConfig(ctx context.Context, networkID int64, vnfType string, config *store.VNFConfig) error
	ActivateVNF(ctx context.Context, vnf store.VNF) (store.VNF, error)
	StopVNF(ctx context.Context, vnf store.VNF, removeLeases bool) error
	ActivateNAT44(ctx context.Context, vnf store.VNF) (store.VNF, error)
	StopNAT44(ctx context.Context, vnf store.VNF) error
	CleanupNetworkBVIAfterNATDelete(ctx context.Context, networkID int64) error
	CleanupLegacyNetworkVNF(ctx context.Context, network store.Network) error
	DeleteBVI(ctx context.Context, name string) error
	CreateBridgeDomain(ctx context.Context, name string, reserved []uint32) (uint32, error)
	DeleteBridgeDomain(ctx context.Context, bdID uint32) error
	PrepareNetworkVNFs(ctx context.Context, network store.Network) (store.Network, error)
	CleanupNetworkVNFs(ctx context.Context, network store.Network) error
	DeleteNetworkRouteTables(ctx context.Context, network store.Network) error
}

// Service provides VNF and network lifecycle operations.
type Service struct {
	store      *store.Store
	backend    Backend
	resourceMu *sync.Mutex
}

func New(st *store.Store, backend Backend, resourceMu *sync.Mutex) *Service {
	return &Service{store: st, backend: backend, resourceMu: resourceMu}
}

type VNFError struct {
	Status  int
	Message string
	Err     error
}

func (e *VNFError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "VNF operation failed"
}
func (e *VNFError) Unwrap() error { return e.Err }
func (e *VNFError) VNFStatus() (int, string) {
	if e.Status >= 500 {
		return e.Status, "VNF operation failed"
	}
	return e.Status, e.Error()
}

type NetworkError struct {
	Status  int
	Message string
	Err     error
}

func (e *NetworkError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "network operation failed"
}
func (e *NetworkError) Unwrap() error { return e.Err }
func (e *NetworkError) NetworkStatus() (int, string) {
	if e.Status >= 500 {
		if e.Err != nil {
			return e.Status, e.Err.Error()
		}
		if e.Message != "" {
			return e.Status, e.Message
		}
		return e.Status, "network operation failed"
	}
	return e.Status, e.Error()
}

var lvmNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func inputError(message string) error {
	return &VNFError{Status: http.StatusBadRequest, Message: message}
}
func conflictError(message string) error {
	return &VNFError{Status: http.StatusConflict, Message: message}
}
func unavailableError(message string) error {
	return &VNFError{Status: http.StatusServiceUnavailable, Message: message}
}
func internalError(err error) error {
	return &VNFError{Status: http.StatusInternalServerError, Err: err}
}

func (s *Service) CreateVNF(ctx context.Context, request *rookeryv1.CreateVNFRequest) (store.VNF, error) {
	name := strings.TrimSpace(request.GetName())
	switch {
	case name == "":
		return store.VNF{}, inputError("name is required")
	case len(name) > 64:
		return store.VNF{}, inputError("name must be at most 64 characters")
	case !lvmNamePattern.MatchString(name):
		return store.VNF{}, inputError("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}
	vnfType := strings.ToLower(strings.TrimSpace(request.GetType()))
	if !store.ValidVNFType(vnfType) {
		return store.VNF{}, inputError("unsupported VNF type")
	}
	if request.GetNetworkId() < 1 {
		return store.VNF{}, inputError("network_id is required")
	}
	config, err := s.backend.NormalizeVNFConfig(vnfType, request.GetConfig())
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
	existing, err := s.store.ListVNFsByNetwork(ctx, network.ID)
	if err != nil {
		return store.VNF{}, internalError(err)
	}
	for _, item := range existing {
		if item.Type == vnfType {
			return store.VNF{}, conflictError(fmt.Sprintf("network already has a %s VNF", vnfType))
		}
	}
	if err := s.backend.ApplyNATGatewayToDHCPConfig(ctx, network.ID, vnfType, &config); err != nil {
		return store.VNF{}, inputError(err.Error())
	}
	enabled := request.Enabled != nil && request.GetEnabled()
	if enabled {
		if err := s.backend.VNFWritesReady(vnfType); err != nil {
			return store.VNF{}, unavailableError(err.Error())
		}
	}
	created, err := s.store.CreateVNF(ctx, store.VNF{Name: name, NetworkID: network.ID, Type: vnfType, Enabled: false, Config: config})
	if err != nil {
		if errors.Is(err, store.ErrVNFNameTaken) {
			return store.VNF{}, conflictError("a VNF with that name already exists")
		}
		return store.VNF{}, internalError(err)
	}
	if !enabled {
		return created, nil
	}
	if vnfType == store.VNFTypeNAT44 {
		created, err = s.backend.ActivateNAT44(ctx, created)
	} else {
		created, err = s.backend.ActivateVNF(ctx, created)
	}
	if err != nil {
		_ = s.store.DeleteVNF(ctx, created.ID)
		return store.VNF{}, internalError(fmt.Errorf("could not start VNF: %w", err))
	}
	return created, nil
}

func (s *Service) UpdateVNF(ctx context.Context, request *rookeryv1.UpdateVNFRequest) (store.VNF, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	existing, err := s.store.GetVNF(ctx, request.GetId())
	if err != nil {
		if errors.Is(err, store.ErrVNFNotFound) {
			return store.VNF{}, &VNFError{Status: http.StatusNotFound, Message: "vnf not found"}
		}
		return store.VNF{}, internalError(err)
	}
	name := existing.Name
	if request.Name != nil {
		name = strings.TrimSpace(request.GetName())
		switch {
		case name == "":
			return store.VNF{}, inputError("name is required")
		case len(name) > 64:
			return store.VNF{}, inputError("name must be at most 64 characters")
		case !lvmNamePattern.MatchString(name):
			return store.VNF{}, inputError("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
		}
	}
	config := existing.Config
	if request.Config != nil {
		config, err = s.backend.NormalizeVNFConfig(existing.Type, request.Config)
		if err != nil {
			return store.VNF{}, inputError(err.Error())
		}
	}
	if existing.Type == store.VNFTypeDHCPv4 {
		if err := s.backend.ApplyNATGatewayToDHCPConfig(ctx, existing.NetworkID, existing.Type, &config); err != nil {
			return store.VNF{}, inputError(err.Error())
		}
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
	updated := existing
	updated.Name = name
	updated.Config = config
	updated.Enabled = enabled
	if existing.Type == store.VNFTypeNAT44 {
		if err := s.backend.StopNAT44(ctx, existing); err != nil {
			return store.VNF{}, internalError(fmt.Errorf("could not stop NAT44: %w", err))
		}
		updated.Enabled = false
		updated.TapSwIfIndex = 0
		updated.TapHostName = ""
		if enabled {
			activated, err := s.backend.ActivateNAT44(ctx, updated)
			if err != nil {
				_, _ = s.store.UpdateVNF(ctx, existing.ID, updated)
				return store.VNF{}, internalError(fmt.Errorf("could not start NAT44: %w", err))
			}
			return activated, nil
		}
		saved, err := s.store.UpdateVNF(ctx, existing.ID, updated)
		if err != nil {
			return store.VNF{}, storeError(err)
		}
		return saved, nil
	}
	if existing.Enabled || existing.TapSwIfIndex > 0 {
		if err := s.backend.StopVNF(ctx, existing, false); err != nil {
			return store.VNF{}, internalError(fmt.Errorf("could not stop VNF: %w", err))
		}
		updated.TapSwIfIndex = 0
		updated.TapHostName = ""
	}
	if enabled {
		activated, err := s.backend.ActivateVNF(ctx, updated)
		if err != nil {
			updated.Enabled = false
			updated.TapSwIfIndex = 0
			updated.TapHostName = ""
			_, _ = s.store.UpdateVNF(ctx, existing.ID, updated)
			return store.VNF{}, internalError(fmt.Errorf("could not start VNF: %w", err))
		}
		return activated, nil
	}
	saved, err := s.store.UpdateVNF(ctx, existing.ID, updated)
	if err != nil {
		return store.VNF{}, storeError(err)
	}
	return saved, nil
}

func (s *Service) DeleteVNF(ctx context.Context, request *rookeryv1.DeleteVNFRequest) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.store.GetVNF(ctx, request.GetId())
	if err != nil {
		if errors.Is(err, store.ErrVNFNotFound) {
			return &VNFError{Status: http.StatusNotFound, Message: "vnf not found"}
		}
		return internalError(err)
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
		if errors.Is(err, store.ErrVNFNotFound) {
			return &VNFError{Status: http.StatusNotFound, Message: "vnf not found"}
		}
		return internalError(err)
	}
	if vnf.Type == store.VNFTypeNAT44 {
		_ = s.backend.CleanupNetworkBVIAfterNATDelete(ctx, vnf.NetworkID)
	}
	return nil
}

func (s *Service) RestartVNF(ctx context.Context, request *rookeryv1.RestartVNFRequest) (store.VNF, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	vnf, err := s.store.GetVNF(ctx, request.GetId())
	if err != nil {
		if errors.Is(err, store.ErrVNFNotFound) {
			return store.VNF{}, &VNFError{Status: http.StatusNotFound, Message: "vnf not found"}
		}
		return store.VNF{}, internalError(err)
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
	vnf.Enabled = true
	vnf.TapSwIfIndex = 0
	vnf.TapHostName = ""
	var activated store.VNF
	if vnf.Type == store.VNFTypeNAT44 {
		activated, err = s.backend.ActivateNAT44(ctx, vnf)
	} else {
		activated, err = s.backend.ActivateVNF(ctx, vnf)
	}
	if err != nil {
		vnf.Enabled = false
		_, _ = s.store.UpdateVNF(ctx, vnf.ID, vnf)
		return store.VNF{}, internalError(fmt.Errorf("could not restart VNF: %w", err))
	}
	return activated, nil
}

func storeError(err error) error {
	switch {
	case errors.Is(err, store.ErrVNFNameTaken):
		return conflictError("a VNF with that name already exists")
	case errors.Is(err, store.ErrVNFNotFound):
		return &VNFError{Status: http.StatusNotFound, Message: "vnf not found"}
	default:
		return internalError(err)
	}
}

func networkInputError(message string) error {
	return &NetworkError{Status: http.StatusBadRequest, Message: message}
}

func (s *Service) CreateNetwork(ctx context.Context, name, ipv4CIDR, ipv6CIDR string) (store.Network, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return store.Network{}, networkInputError("name is required")
	case len(name) > 64:
		return store.Network{}, networkInputError("name must be at most 64 characters")
	case !lvmNamePattern.MatchString(name):
		return store.Network{}, networkInputError("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}
	ipv4CIDR, ipv6CIDR, err := netutil.NormalizeNetworkCIDRs(ipv4CIDR, ipv6CIDR)
	if err != nil {
		return store.Network{}, networkInputError(err.Error())
	}
	if blocked, message := s.backend.VPPBlocked(); blocked {
		return store.Network{}, &NetworkError{Status: http.StatusServiceUnavailable, Message: message}
	}
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	existing, err := s.store.ListNetworks(ctx)
	if err != nil {
		return store.Network{}, &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	reserved := make([]uint32, 0, len(existing))
	for _, network := range existing {
		reserved = append(reserved, network.BridgeDomainID)
	}
	bdID, err := s.backend.CreateBridgeDomain(ctx, name, reserved)
	if err != nil {
		return store.Network{}, &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("could not create VPP bridge domain: %w", err)}
	}
	created, err := s.store.CreateNetwork(ctx, store.Network{
		Name: name, BridgeDomainID: bdID, IPv4CIDR: ipv4CIDR, IPv6CIDR: ipv6CIDR,
	})
	if err != nil {
		_ = s.backend.DeleteBridgeDomain(context.Background(), bdID)
		if errors.Is(err, store.ErrNetworkNameTaken) {
			return store.Network{}, &NetworkError{Status: http.StatusConflict, Message: "a network with that name already exists"}
		}
		return store.Network{}, &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	prepared, err := s.backend.PrepareNetworkVNFs(ctx, created)
	if err != nil {
		if current, getErr := s.store.GetNetwork(ctx, created.ID); getErr == nil {
			_ = s.backend.CleanupNetworkVNFs(ctx, current)
			if current.BVIHostName != "" {
				_ = s.backend.DeleteBVI(ctx, current.BVIHostName)
			}
			_ = s.backend.DeleteNetworkRouteTables(ctx, current)
		}
		_ = s.store.DeleteNetwork(ctx, created.ID)
		_ = s.backend.DeleteBridgeDomain(context.Background(), bdID)
		return store.Network{}, &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("prepare network VNF resources: %w", err)}
	}
	return prepared, nil
}

func (s *Service) DeleteNetwork(ctx context.Context, id int64) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	network, err := s.store.GetNetwork(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNetworkNotFound) {
			return &NetworkError{Status: http.StatusNotFound, Message: "network not found"}
		}
		return &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	if network.InterfaceCount > 0 {
		return &NetworkError{Status: http.StatusConflict, Message: "network still has virtual interfaces"}
	}
	if blocked, message := s.backend.VPPBlocked(); blocked {
		return &NetworkError{Status: http.StatusServiceUnavailable, Message: message}
	}
	if err := s.backend.CleanupLegacyNetworkVNF(ctx, network); err != nil {
		return &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("clean legacy DHCP before deleting the network: %w", err)}
	}
	vnfs, err := s.store.ListVNFsByNetwork(ctx, id)
	if err != nil {
		return &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	for _, vnf := range vnfs {
		if vnf.Type == store.VNFTypeNAT44 {
			err = s.backend.StopNAT44(ctx, vnf)
		} else {
			err = s.backend.StopVNF(ctx, vnf, true)
		}
		if err != nil {
			return &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("stop network VNFs: %w", err)}
		}
	}
	if err := s.backend.CleanupNetworkVNFs(ctx, network); err != nil {
		return &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("delete network VNF resources: %w", err)}
	}
	if network.BVIHostName != "" {
		if err := s.backend.DeleteBVI(ctx, network.BVIHostName); err != nil {
			return &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("delete network BVI: %w", err)}
		}
	}
	if err := s.backend.DeleteNetworkRouteTables(ctx, network); err != nil {
		return &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("delete network route tables: %w", err)}
	}
	if err := s.backend.DeleteBridgeDomain(ctx, network.BridgeDomainID); err != nil {
		return &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("delete VPP bridge domain: %w", err)}
	}
	err = s.store.DeleteNetwork(ctx, id)
	switch {
	case errors.Is(err, store.ErrNetworkNotFound):
		return &NetworkError{Status: http.StatusNotFound, Message: "network not found"}
	case errors.Is(err, store.ErrNetworkInUse):
		return &NetworkError{Status: http.StatusConflict, Message: "network still has virtual interfaces"}
	case err != nil:
		return &NetworkError{Status: http.StatusInternalServerError, Err: err}
	default:
		return nil
	}
}
