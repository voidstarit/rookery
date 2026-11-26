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

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/netutil"
	"github.com/voidstarit/rookery/internal/store"
)

// Backend exposes the VPP/NAT/DHCP runtime and the type-specific validators
// used by lifecycle operations.
type Backend interface {
	VPPBlocked() (bool, string)
	VNFWritesReady(vnfType string) error

	// Normalizers validate and canonicalize typed parent configuration.
	NormalizeDHCPVNFConfig(family string, config store.DHCPVNFConfig) (store.DHCPVNFConfig, error)
	NormalizeNAT44VNFConfig(config store.NAT44VNFConfig) (store.NAT44VNFConfig, error)
	NormalizeBGPVNFConfig(config store.BGPVNFConfig) (store.BGPVNFConfig, error)
	// ApplyNATGatewayToDHCPConfig rewrites the derived NAT gateway and bind
	// address of a DHCPv4 config, rejecting ranges outside the network CIDR.
	ApplyNATGatewayToDHCPConfig(ctx context.Context, networkID int64, family string, config *store.DHCPVNFConfig) error

	// Child validators check a new or updated row against its parent and the
	// sibling rows that remain.
	NormalizeDHCPStaticAssignment(ctx context.Context, vnf store.VNF, item store.DHCPStaticAssignment) (store.DHCPStaticAssignment, error)
	NormalizeDHCPOption(ctx context.Context, vnf store.VNF, item store.DHCPOption) (store.DHCPOption, error)
	NormalizeDNSRecord(ctx context.Context, vnf store.VNF, siblings []store.DNSRecord, item store.DNSRecord) (store.DNSRecord, error)
	NormalizeDNSForwarder(ctx context.Context, vnf store.VNF, siblings []store.DNSForwarder, item store.DNSForwarder) (store.DNSForwarder, error)
	NormalizeDNATRule(ctx context.Context, vnf store.VNF, siblings []store.DNATRule, item store.DNATRule) (store.DNATRule, error)
	NormalizeBGPPrefix(ctx context.Context, vnf store.VNF, prefix string) (store.BGPPrefix, error)

	ActivateVNF(ctx context.Context, vnf store.VNF) (store.VNF, error)
	StopVNF(ctx context.Context, vnf store.VNF, removeLeases bool) error
	// Running reports whether the VNF runtime is currently up. A child write on
	// an enabled but stopped runtime keeps the row and does not fail.
	Running(ctx context.Context, vnf store.VNF) bool
	// ReloadVNF swaps a running runtime's configuration in place. It is a
	// no-op for a VNF that is enabled but whose runtime is not running.
	ReloadVNF(ctx context.Context, vnf store.VNF) error
	ActivateNAT44(ctx context.Context, vnf store.VNF) (store.VNF, error)
	StopNAT44(ctx context.Context, vnf store.VNF) error
	// ApplyNAT44DNATRules replaces the VNF's VPP static mappings in place.
	ApplyNAT44DNATRules(ctx context.Context, vnf store.VNF) error
	CleanupNetworkBVIAfterNATDelete(ctx context.Context, networkID int64) error
	CleanupLegacyNetworkVNF(ctx context.Context, network store.Network) error
	DeleteBVI(ctx context.Context, name string) error
	CreateBridgeDomain(ctx context.Context, name string, reserved []uint32) (uint32, error)
	DeleteBridgeDomain(ctx context.Context, bdID uint32) error
	CreateNetworkPipe(ctx context.Context, bdA, bdB uint32) (host.PipeRuntime, error)
	DeleteNetworkPipe(ctx context.Context, instance, parentSwIfIndex uint32) error
	PrepareNetworkVNFs(ctx context.Context, network store.Network) (store.Network, error)
	CleanupNetworkVNFs(ctx context.Context, network store.Network) error
	DeleteNetworkRouteTables(ctx context.Context, network store.Network) error
	CreateNetworkHostTap(ctx context.Context, network store.Network, name, ipv4Address string) (store.NetworkHostTap, error)
	DeleteNetworkHostTap(ctx context.Context, tap store.NetworkHostTap) error
	CleanupNetworkHostTaps(ctx context.Context, network store.Network) error
}

// Service provides VNF and network lifecycle operations.
type Service struct {
	store      *store.Store
	backend    Backend
	resourceMu *sync.Mutex
}

// New returns a Service that runs VNF and network operations against st and
// backend, serializing mutating work with resourceMu.
func New(st *store.Store, backend Backend, resourceMu *sync.Mutex) *Service {
	return &Service{store: st, backend: backend, resourceMu: resourceMu}
}

type VNFError struct {
	Status  int
	Message string
	Err     error
}

// Error returns Message when set, otherwise the wrapped error, falling back to
// a generic VNF operation failure message.
func (e *VNFError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "VNF operation failed"
}

// Unwrap returns the wrapped error so errors.Is and errors.As can inspect the
// underlying cause.
func (e *VNFError) Unwrap() error { return e.Err }

// VNFStatus returns the HTTP status and client-facing message, replacing the
// detail of server errors (status 500 and above) with a generic message.
func (e *VNFError) VNFStatus() (int, string) {
	if e.Status >= 500 {
		if e.Message != "" {
			return e.Status, e.Message
		}
		return e.Status, "VNF operation failed"
	}
	return e.Status, e.Error()
}

type NetworkError struct {
	Status  int
	Message string
	Err     error
}

// Error returns Message when set, otherwise the wrapped error, falling back to
// a generic network operation failure message.
func (e *NetworkError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "network operation failed"
}

// Unwrap returns the wrapped error so errors.Is and errors.As can inspect the
// underlying cause.
func (e *NetworkError) Unwrap() error { return e.Err }

// NetworkStatus returns the HTTP status and client-facing message. Server
// errors expose the wrapped error or Message, while client errors use Error.
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

// inputError wraps message in a VNFError with status 400 Bad Request.
func inputError(message string) error {
	return &VNFError{Status: http.StatusBadRequest, Message: message}
}

// conflictError wraps message in a VNFError with status 409 Conflict.
func conflictError(message string) error {
	return &VNFError{Status: http.StatusConflict, Message: message}
}

// unavailableError wraps message in a VNFError with status 503 Service
// Unavailable.
func unavailableError(message string) error {
	return &VNFError{Status: http.StatusServiceUnavailable, Message: message}
}

// internalError wraps err in a VNFError with status 500 Internal Server Error.
func internalError(err error) error {
	return &VNFError{Status: http.StatusInternalServerError, Err: err}
}

// networkInputError wraps message in a NetworkError with status 400 Bad Request.
func networkInputError(message string) error {
	return &NetworkError{Status: http.StatusBadRequest, Message: message}
}

// CreateNetwork validates the name and CIDRs, allocates a VPP bridge domain,
// and creates the network record. Per-network VNF preparation runs last; its
// failure rolls back the record, bridge domain, BVI, and route tables.
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

// DeleteNetwork removes a network and its VPP state, refusing when virtual
// interfaces or bridge-domain pipes still reference it. It stops the network's
// VNFs and deletes taps, BVI, route tables, and the bridge domain before
// removing the record.
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
	if count, err := s.store.CountNetworkPipesForNetwork(ctx, id); err != nil {
		return &NetworkError{Status: http.StatusInternalServerError, Err: err}
	} else if count > 0 {
		return &NetworkError{Status: http.StatusConflict, Message: "network still has bridge-domain pipes"}
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
	if err := s.backend.CleanupNetworkHostTaps(ctx, network); err != nil {
		return &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("delete network host taps: %w", err)}
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

// CreateNetworkPipe creates a pipe between two bridge domains and records it.
func (s *Service) CreateNetworkPipe(ctx context.Context, networkAID, networkBID int64) (store.NetworkPipe, error) {
	if networkAID == networkBID {
		return store.NetworkPipe{}, &NetworkError{Status: http.StatusBadRequest, Message: "a pipe requires two different networks"}
	}
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	networkA, err := s.store.GetNetwork(ctx, networkAID)
	if err != nil {
		if errors.Is(err, store.ErrNetworkNotFound) {
			return store.NetworkPipe{}, &NetworkError{Status: http.StatusNotFound, Message: "network not found"}
		}
		return store.NetworkPipe{}, &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	networkB, err := s.store.GetNetwork(ctx, networkBID)
	if err != nil {
		if errors.Is(err, store.ErrNetworkNotFound) {
			return store.NetworkPipe{}, &NetworkError{Status: http.StatusNotFound, Message: "network not found"}
		}
		return store.NetworkPipe{}, &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	if networkA.ID > networkB.ID {
		networkA, networkB = networkB, networkA
	}
	if blocked, message := s.backend.VPPBlocked(); blocked {
		return store.NetworkPipe{}, &NetworkError{Status: http.StatusServiceUnavailable, Message: message}
	}
	runtime, err := s.backend.CreateNetworkPipe(ctx, networkA.BridgeDomainID, networkB.BridgeDomainID)
	if err != nil {
		return store.NetworkPipe{}, &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("create VPP pipe: %w", err)}
	}
	created, err := s.store.CreateNetworkPipe(ctx, store.NetworkPipe{
		NetworkAID: networkA.ID, NetworkBID: networkB.ID, Instance: runtime.Instance,
		ParentSwIfIndex: runtime.ParentSwIfIndex, EndpointASwIfIndex: runtime.EndpointASwIfIndex,
		EndpointBSwIfIndex: runtime.EndpointBSwIfIndex,
	})
	if err != nil {
		_ = s.backend.DeleteNetworkPipe(context.Background(), runtime.Instance, runtime.ParentSwIfIndex)
		if errors.Is(err, store.ErrNetworkPipeExists) {
			return store.NetworkPipe{}, &NetworkError{Status: http.StatusConflict, Message: "a pipe between those networks already exists"}
		}
		return store.NetworkPipe{}, &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	return created, nil
}

// DeleteNetworkPipe removes a pipe and its VPP runtime.
func (s *Service) DeleteNetworkPipe(ctx context.Context, id int64) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	pipe, err := s.store.GetNetworkPipe(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNetworkPipeNotFound) {
			return &NetworkError{Status: http.StatusNotFound, Message: "network pipe not found"}
		}
		return &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	if blocked, message := s.backend.VPPBlocked(); blocked {
		return &NetworkError{Status: http.StatusServiceUnavailable, Message: message}
	}
	if err := s.backend.DeleteNetworkPipe(ctx, pipe.Instance, pipe.ParentSwIfIndex); err != nil {
		return &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("delete VPP pipe: %w", err)}
	}
	if err := s.store.DeleteNetworkPipe(ctx, pipe.ID); err != nil {
		if errors.Is(err, store.ErrNetworkPipeNotFound) {
			return &NetworkError{Status: http.StatusNotFound, Message: "network pipe not found"}
		}
		return &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	return nil
}

// CreateNetworkHostTap creates a persistent host tap on a Rookery Network.
func (s *Service) CreateNetworkHostTap(ctx context.Context, networkID int64, name, ipv4Address string) (store.NetworkHostTap, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	network, err := s.store.GetNetwork(ctx, networkID)
	if err != nil {
		if errors.Is(err, store.ErrNetworkNotFound) {
			return store.NetworkHostTap{}, &NetworkError{Status: http.StatusNotFound, Message: "network not found"}
		}
		return store.NetworkHostTap{}, &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	if blocked, message := s.backend.VPPBlocked(); blocked {
		return store.NetworkHostTap{}, &NetworkError{Status: http.StatusServiceUnavailable, Message: message}
	}
	tap, err := s.backend.CreateNetworkHostTap(ctx, network, name, ipv4Address)
	if err != nil {
		return store.NetworkHostTap{}, err
	}
	return tap, nil
}

// DeleteNetworkHostTap removes a persistent host tap and its record.
func (s *Service) DeleteNetworkHostTap(ctx context.Context, id int64) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	tap, err := s.store.GetNetworkHostTap(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNetworkHostTapNotFound) {
			return &NetworkError{Status: http.StatusNotFound, Message: "host tap not found"}
		}
		return &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	if blocked, message := s.backend.VPPBlocked(); blocked {
		return &NetworkError{Status: http.StatusServiceUnavailable, Message: message}
	}
	if err := s.backend.DeleteNetworkHostTap(ctx, tap); err != nil {
		return err
	}
	return nil
}
