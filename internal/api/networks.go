package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/store"
)

const (
	maxNetworkNameLength = 64
	rollbackTimeout      = 5 * time.Second
)

type networkPayload struct {
	Name *string `json:"name"`
}

func (p networkPayload) toName() (string, error) {
	if p.Name == nil {
		return "", errors.New("name is required")
	}
	name := strings.TrimSpace(*p.Name)
	switch {
	case name == "":
		return "", errors.New("name is required")
	case len(name) > maxNetworkNameLength:
		return "", fmt.Errorf("name must be at most %d characters", maxNetworkNameLength)
	case !vmNamePattern.MatchString(name):
		return "", errors.New("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}
	return name, nil
}

type interfacePayload struct {
	Name         *string `json:"name"`
	NetworkID    *int64  `json:"network_id"`
	AttachedVMID *string `json:"attached_vm_id"`
}

type interfaceAttachmentPayload struct {
	AttachedVMID *string `json:"attached_vm_id"`
}

func broadcastIPv4(ipnet *net.IPNet) net.IP {
	ip := ipnet.IP.To4()
	out := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		out[i] = ip[i] | ^ipnet.Mask[i]
	}
	return out
}

func (s *Server) handleListNetworks(w http.ResponseWriter, r *http.Request) {
	networks, err := s.store.ListNetworks(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, networks)
}

func (s *Server) handleGetNetwork(w http.ResponseWriter, r *http.Request) {
	id, err := parseNetworkID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	network, err := s.store.GetNetwork(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNetworkNotFound):
		writeError(w, http.StatusNotFound, "network not found")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, network)
	}
}

func (s *Server) handleCreateNetwork(w http.ResponseWriter, r *http.Request) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	if blocked, message := s.vppBlocked(); blocked {
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}

	var payload networkPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name, err := payload.toName()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	existing, err := s.store.ListNetworks(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	reserved := make([]uint32, 0, len(existing))
	for _, network := range existing {
		reserved = append(reserved, network.BridgeDomainID)
	}

	bdID, err := s.networks.CreateBridgeDomain(r.Context(), name, reserved)
	if err != nil {
		s.logger.Error("create bridge domain failed", "network", name, "error", err)
		writeError(w, http.StatusInternalServerError, "could not create VPP bridge domain: "+err.Error())
		return
	}

	created, err := s.store.CreateNetwork(r.Context(), store.Network{
		Name:           name,
		BridgeDomainID: bdID,
	})
	if err != nil {
		s.rollbackBridgeDomain(bdID)
		switch {
		case errors.Is(err, store.ErrNetworkNameTaken):
			writeError(w, http.StatusConflict, "a network with that name already exists")
		default:
			s.internalError(w, r, err)
		}
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/networks/%d", created.ID))
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleDeleteNetwork(w http.ResponseWriter, r *http.Request) {
	id, err := parseNetworkID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	network, err := s.store.GetNetwork(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNetworkNotFound):
		writeError(w, http.StatusNotFound, "network not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	if network.InterfaceCount > 0 {
		writeError(w, http.StatusConflict, "network still has virtual interfaces")
		return
	}
	if blocked, message := s.vppBlocked(); blocked {
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}

	if err := s.cleanupLegacyNetworkVNF(r.Context(), network); err != nil {
		s.logger.Error("clean legacy network DHCP before delete failed", "network", network.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "could not clean legacy DHCP before deleting the network: "+err.Error())
		return
	}
	vnfs, err := s.store.ListVNFsByNetwork(r.Context(), id)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, vnf := range vnfs {
		var err error
		if vnf.Type == store.VNFTypeNAT44 {
			err = s.stopNAT44(r.Context(), vnf)
		} else {
			err = s.stopVNF(r.Context(), vnf, true)
		}
		if err != nil {
			s.logger.Error("stop VNF before network delete failed", "vnf", vnf.Name, "network", network.Name, "error", err)
			writeError(w, http.StatusInternalServerError, "could not stop network VNFs: "+err.Error())
			return
		}
	}
	if network.BVIHostName != "" && s.nat != nil {
		if err := s.nat.DeleteBVIByName(r.Context(), network.BVIHostName); err != nil {
			s.logger.Error("delete network BVI failed", "network", network.Name, "bvi", network.BVIHostName, "error", err)
			writeError(w, http.StatusInternalServerError, "could not delete network BVI: "+err.Error())
			return
		}
	}

	if err := s.networks.DeleteBridgeDomain(r.Context(), network.BridgeDomainID); err != nil {
		s.logger.Error("delete bridge domain failed", "network", network.Name, "bd_id", network.BridgeDomainID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not delete VPP bridge domain: "+err.Error())
		return
	}

	err = s.store.DeleteNetwork(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNetworkNotFound):
		writeError(w, http.StatusNotFound, "network not found")
	case errors.Is(err, store.ErrNetworkInUse):
		writeError(w, http.StatusConflict, "network still has virtual interfaces")
	case err != nil:
		s.internalError(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleListInterfaces(w http.ResponseWriter, r *http.Request) {
	interfaces, err := s.store.ListInterfaces(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, interfaces)
}

func (s *Server) handleGetInterface(w http.ResponseWriter, r *http.Request) {
	id, err := parseInterfaceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	iface, err := s.store.GetInterface(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrInterfaceNotFound):
		writeError(w, http.StatusNotFound, "interface not found")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, iface)
	}
}

func (s *Server) handleCreateInterface(w http.ResponseWriter, r *http.Request) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	s.runMu.Lock()
	defer s.runMu.Unlock()

	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	if blocked, message := s.vppBlocked(); blocked {
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}

	var payload interfacePayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name, vmID, err := payload.toInterface()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	network, err := s.store.GetNetwork(r.Context(), *payload.NetworkID)
	switch {
	case errors.Is(err, store.ErrNetworkNotFound):
		writeError(w, http.StatusBadRequest, "network not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	if vmID != nil {
		if _, err := s.store.GetVM(r.Context(), *vmID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusBadRequest, "vm not found")
			} else {
				s.internalError(w, r, err)
			}
			return
		}
		if s.vms != nil && s.vms.Running(*vmID) {
			writeError(w, http.StatusConflict, "stop the VM before attaching a network interface")
			return
		}
	}

	mac, err := newMAC()
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	socket, swIfIndex, err := s.networks.CreateVhostUser(r.Context(), name, mac, network.BridgeDomainID)
	if err != nil {
		s.logger.Error("create vhost-user interface failed", "interface", name, "network", network.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "could not create VPP vhost-user interface: "+err.Error())
		return
	}

	created, err := s.store.CreateInterface(r.Context(), store.VirtualInterface{
		Name:         name,
		NetworkID:    network.ID,
		SocketPath:   socket,
		MAC:          mac,
		SwIfIndex:    swIfIndex,
		AttachedVMID: vmID,
	})
	if err != nil {
		s.rollbackVhostUser(swIfIndex)
		switch {
		case errors.Is(err, store.ErrInterfaceNameTaken):
			writeError(w, http.StatusConflict, "an interface with that name already exists")
		case errors.Is(err, store.ErrNetworkNotFound):
			writeError(w, http.StatusBadRequest, "network not found")
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusBadRequest, "vm not found")
		default:
			s.internalError(w, r, err)
		}
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/interfaces/%d", created.ID))
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateInterface(w http.ResponseWriter, r *http.Request) {
	id, err := parseInterfaceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	s.runMu.Lock()
	defer s.runMu.Unlock()

	iface, err := s.store.GetInterface(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrInterfaceNotFound):
		writeError(w, http.StatusNotFound, "interface not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	var payload interfaceAttachmentPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	vmID, err := normalizeOptionalVMID(payload.AttachedVMID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if iface.AttachedVMID != nil && s.vms != nil && s.vms.Running(*iface.AttachedVMID) {
		writeError(w, http.StatusConflict, "stop the current VM before detaching its network interface")
		return
	}
	if vmID != nil {
		if s.vms != nil && s.vms.Running(*vmID) {
			writeError(w, http.StatusConflict, "stop the target VM before attaching a network interface")
			return
		}
	}

	updated, err := s.store.SetInterfaceAttachment(r.Context(), id, vmID)
	switch {
	case errors.Is(err, store.ErrInterfaceNotFound):
		writeError(w, http.StatusNotFound, "interface not found")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusBadRequest, "vm not found")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, updated)
	}
}

func (s *Server) handleDeleteInterface(w http.ResponseWriter, r *http.Request) {
	id, err := parseInterfaceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	iface, err := s.store.GetInterface(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrInterfaceNotFound):
		writeError(w, http.StatusNotFound, "interface not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	if iface.AttachedVMID != nil {
		writeError(w, http.StatusConflict, "interface is attached to a VM; detach it first")
		return
	}
	if blocked, message := s.vppBlocked(); blocked {
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}

	if err := s.networks.DeleteVhostUser(r.Context(), iface.SwIfIndex); err != nil {
		s.logger.Error("delete vhost-user interface failed", "interface", iface.Name, "sw_if_index", iface.SwIfIndex, "error", err)
		writeError(w, http.StatusInternalServerError, "could not delete VPP vhost-user interface: "+err.Error())
		return
	}
	_ = os.Remove(iface.SocketPath)

	err = s.store.DeleteInterface(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrInterfaceNotFound):
		writeError(w, http.StatusNotFound, "interface not found")
	case errors.Is(err, store.ErrInterfaceInUse):
		writeError(w, http.StatusConflict, "interface is attached to a VM; detach it first")
	case err != nil:
		s.internalError(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) vppBlocked() (bool, string) {
	status := s.readiness.Status()
	if status.VPP.Ready {
		return false, ""
	}
	return true, "VPP is not ready: " + componentReason(status.VPP.ComponentStatus)
}

func (s *Server) rollbackBridgeDomain(bdID uint32) {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	if err := s.networks.DeleteBridgeDomain(ctx, bdID); err != nil {
		s.logger.Error("rollback bridge domain failed", "bd_id", bdID, "error", err)
	}
}

func (s *Server) rollbackVhostUser(swIfIndex uint32) {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	if err := s.networks.DeleteVhostUser(ctx, swIfIndex); err != nil {
		s.logger.Error("rollback vhost-user interface failed", "sw_if_index", swIfIndex, "error", err)
	}
}

func (p interfacePayload) toInterface() (name string, vmID *string, err error) {
	if p.Name == nil {
		return "", nil, errors.New("name is required")
	}
	name = strings.TrimSpace(*p.Name)
	switch {
	case name == "":
		return "", nil, errors.New("name is required")
	case len(name) > maxNetworkNameLength:
		return "", nil, fmt.Errorf("name must be at most %d characters", maxNetworkNameLength)
	case !vmNamePattern.MatchString(name):
		return "", nil, errors.New("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}
	if p.NetworkID == nil || *p.NetworkID < 1 {
		return "", nil, errors.New("network_id is required")
	}
	vmID, err = normalizeOptionalVMID(p.AttachedVMID)
	if err != nil {
		return "", nil, err
	}
	return name, vmID, nil
}

func normalizeOptionalVMID(value *string) (*string, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(strings.TrimSpace(*value))
	if err != nil {
		return nil, errors.New("attached_vm_id must be a UUID")
	}
	id := parsed.String()
	return &id, nil
}

func parseNetworkID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid network id")
	}
	return id, nil
}

func parseInterfaceID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid interface id")
	}
	return id, nil
}

func newMAC() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate MAC address: %w", err)
	}
	b[0] = (b[0] & 0xfc) | 0x02
	return net.HardwareAddr(b[:]).String(), nil
}
