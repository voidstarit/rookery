package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/voidstarit/rookery/internal/store"
)

// NetworkError carries the HTTP status and public message for a network
// deletion. The grpc-gateway maps it back to a gRPC status.
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

// NetworkStatus returns the HTTP status and public message for gateway mapping.
func (e *NetworkError) NetworkStatus() (int, string) {
	if e.Status >= 500 {
		return e.Status, "network operation failed"
	}
	return e.Status, e.Error()
}

// CreateNetwork validates a network name and provisions its VPP bridge domain.
func (s *Server) CreateNetwork(ctx context.Context, name string) (store.Network, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return store.Network{}, &NetworkError{Status: http.StatusBadRequest, Message: "name is required"}
	}
	if len(name) > maxNetworkNameLength {
		return store.Network{}, &NetworkError{Status: http.StatusBadRequest, Message: fmt.Sprintf("name must be at most %d characters", maxNetworkNameLength)}
	}
	if !vmNamePattern.MatchString(name) {
		return store.Network{}, &NetworkError{Status: http.StatusBadRequest, Message: "name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores"}
	}
	if s.networks == nil {
		return store.Network{}, &NetworkError{Status: http.StatusServiceUnavailable, Message: "VPP network runtime is not configured"}
	}
	if blocked, message := s.vppBlocked(); blocked {
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
	bdID, err := s.networks.CreateBridgeDomain(ctx, name, reserved)
	if err != nil {
		return store.Network{}, &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("could not create VPP bridge domain: %w", err)}
	}
	created, err := s.store.CreateNetwork(ctx, store.Network{Name: name, BridgeDomainID: bdID})
	if err != nil {
		s.rollbackBridgeDomain(bdID)
		if errors.Is(err, store.ErrNetworkNameTaken) {
			return store.Network{}, &NetworkError{Status: http.StatusConflict, Message: "a network with that name already exists"}
		}
		return store.Network{}, &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	return created, nil
}

// DeleteNetwork stops the network's VNFs, removes its BVI and bridge domain,
// then deletes the database record.
func (s *Server) DeleteNetwork(ctx context.Context, id int64) error {
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
	if blocked, message := s.vppBlocked(); blocked {
		return &NetworkError{Status: http.StatusServiceUnavailable, Message: message}
	}
	if err := s.cleanupLegacyNetworkVNF(ctx, network); err != nil {
		return &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("clean legacy DHCP before deleting the network: %w", err)}
	}
	vnfs, err := s.store.ListVNFsByNetwork(ctx, id)
	if err != nil {
		return &NetworkError{Status: http.StatusInternalServerError, Err: err}
	}
	for _, vnf := range vnfs {
		if vnf.Type == store.VNFTypeNAT44 {
			err = s.stopNAT44(ctx, vnf)
		} else {
			err = s.stopVNF(ctx, vnf, true)
		}
		if err != nil {
			return &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("stop network VNFs: %w", err)}
		}
	}
	if network.BVIHostName != "" && s.nat != nil {
		if err := s.nat.DeleteBVIByName(ctx, network.BVIHostName); err != nil {
			return &NetworkError{Status: http.StatusInternalServerError, Err: fmt.Errorf("delete network BVI: %w", err)}
		}
	}
	if err := s.networks.DeleteBridgeDomain(ctx, network.BridgeDomainID); err != nil {
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
