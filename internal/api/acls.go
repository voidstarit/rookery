package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/voidstarit/rookery/internal/store"
)

type aclRulePayload struct {
	Action               string `json:"action"`
	Description          string `json:"description"`
	Protocol             string `json:"protocol"`
	Source               string `json:"source"`
	Destination          string `json:"destination"`
	SourcePortStart      int    `json:"source_port_start"`
	SourcePortEnd        int    `json:"source_port_end"`
	DestinationPortStart int    `json:"destination_port_start"`
	DestinationPortEnd   int    `json:"destination_port_end"`
}

type aclPayload struct {
	Name        string           `json:"name"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Rules       []aclRulePayload `json:"rules"`
}

func (p aclPayload) toACL() (store.ACL, error) {
	name := strings.TrimSpace(p.Name)
	switch {
	case name == "":
		return store.ACL{}, errors.New("name is required")
	case len(name) > 64:
		return store.ACL{}, errors.New("name must be at most 64 characters")
	case !vmNamePattern.MatchString(name):
		return store.ACL{}, errors.New("name must contain only letters, digits, dots, dashes, and underscores")
	}
	title := strings.TrimSpace(p.Title)
	if len(title) > 128 {
		return store.ACL{}, errors.New("title must be at most 128 characters")
	}
	description := strings.TrimSpace(p.Description)
	if len(description) > 2000 {
		return store.ACL{}, errors.New("description must be at most 2000 characters")
	}
	if len(p.Rules) == 0 {
		return store.ACL{}, errors.New("at least one rule is required")
	}
	if len(p.Rules) > 64 {
		return store.ACL{}, errors.New("an ACL can have at most 64 rules")
	}
	rules := make([]store.ACLRule, 0, len(p.Rules))
	for i, rule := range p.Rules {
		normalized, err := normalizeACLRule(rule)
		if err != nil {
			return store.ACL{}, fmt.Errorf("rule %d: %w", i+1, err)
		}
		rules = append(rules, normalized)
	}
	return store.ACL{Name: name, Title: title, Description: description, Rules: rules}, nil
}

func normalizeACLRule(rule aclRulePayload) (store.ACLRule, error) {
	description := strings.TrimSpace(rule.Description)
	if len(description) > 256 {
		return store.ACLRule{}, errors.New("description must be at most 256 characters")
	}
	action := strings.ToLower(strings.TrimSpace(rule.Action))
	if action != "permit" && action != "deny" {
		return store.ACLRule{}, errors.New("action must be permit or deny")
	}
	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	switch protocol {
	case "any", "tcp", "udp", "icmp", "icmpv6":
	case "":
		protocol = "any"
	default:
		return store.ACLRule{}, errors.New("protocol must be any, tcp, udp, icmp, or icmpv6")
	}
	source, sourceNet, err := net.ParseCIDR(strings.TrimSpace(rule.Source))
	if err != nil {
		return store.ACLRule{}, errors.New("source must be a CIDR")
	}
	destination, destinationNet, err := net.ParseCIDR(strings.TrimSpace(rule.Destination))
	if err != nil {
		return store.ACLRule{}, errors.New("destination must be a CIDR")
	}
	if (source.To4() == nil) != (destination.To4() == nil) {
		return store.ACLRule{}, errors.New("source and destination must use the same address family")
	}
	sourcePorts := [2]int{rule.SourcePortStart, rule.SourcePortEnd}
	destinationPorts := [2]int{rule.DestinationPortStart, rule.DestinationPortEnd}
	if protocol != "tcp" && protocol != "udp" {
		if sourcePorts != [2]int{0, 0} || destinationPorts != [2]int{0, 0} {
			return store.ACLRule{}, errors.New("ports are only valid for tcp and udp")
		}
		return store.ACLRule{
			Action:      action,
			Description: description,
			Protocol:    protocol,
			Source:      sourceNet.String(),
			Destination: destinationNet.String(),
		}, nil
	}
	normalizedSource, err := normalizePortRange(sourcePorts)
	if err != nil {
		return store.ACLRule{}, fmt.Errorf("source ports: %w", err)
	}
	normalizedDestination, err := normalizePortRange(destinationPorts)
	if err != nil {
		return store.ACLRule{}, fmt.Errorf("destination ports: %w", err)
	}
	return store.ACLRule{
		Action:               action,
		Description:          description,
		Protocol:             protocol,
		Source:               sourceNet.String(),
		Destination:          destinationNet.String(),
		SourcePortStart:      normalizedSource[0],
		SourcePortEnd:        normalizedSource[1],
		DestinationPortStart: normalizedDestination[0],
		DestinationPortEnd:   normalizedDestination[1],
	}, nil
}

func normalizePortRange(ports [2]int) ([2]int, error) {
	start, end := ports[0], ports[1]
	if start == 0 && end == 0 {
		return [2]int{0, 0}, nil
	}
	if start < 0 || start > 65535 || end < 0 || end > 65535 {
		return [2]int{}, errors.New("ports must be between 0 and 65535")
	}
	if start == 0 {
		return [2]int{}, errors.New("start port must be set when an end port is set")
	}
	if end == 0 {
		end = start
	}
	if start > end {
		return [2]int{}, errors.New("start port must not exceed end port")
	}
	return [2]int{start, end}, nil
}

func (s *Server) handleListACLs(w http.ResponseWriter, r *http.Request) {
	acls, err := s.store.ListACLs(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, acls)
}

func (s *Server) handleGetACL(w http.ResponseWriter, r *http.Request) {
	id, err := parseACLID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	acl, err := s.store.GetACL(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrACLNotFound):
		writeError(w, http.StatusNotFound, "acl not found")
	case err != nil:
		s.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, acl)
	}
}

func (s *Server) handleCreateACL(w http.ResponseWriter, r *http.Request) {
	if blocked, message := s.vppBlocked(); blocked {
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}
	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	var payload aclPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	acl, err := payload.toACL()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.store.CreateACL(r.Context(), acl)
	if err != nil {
		writeACLStoreError(w, r, s, err)
		return
	}
	index, err := s.networks.EnsureVPPACL(r.Context(), created.ID, created.Rules)
	if err != nil {
		_ = s.store.DeleteACL(r.Context(), created.ID)
		s.logger.Error("configure VPP ACL failed", "acl", created.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "could not configure VPP ACL: "+err.Error())
		return
	}
	created, err = s.store.SetACLVPPIndex(r.Context(), created.ID, index)
	if err != nil {
		_ = s.networks.DeleteVPPACL(r.Context(), index)
		_ = s.store.DeleteACL(r.Context(), created.ID)
		s.internalError(w, r, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/acls/%d", created.ID))
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateACL(w http.ResponseWriter, r *http.Request) {
	id, err := parseACLID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if blocked, message := s.vppBlocked(); blocked {
		writeError(w, http.StatusServiceUnavailable, message)
		return
	}
	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	existing, err := s.store.GetACL(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrACLNotFound):
		writeError(w, http.StatusNotFound, "acl not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	var payload aclPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	acl, err := payload.toACL()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	acl.ID = existing.ID
	index, err := s.networks.EnsureVPPACL(r.Context(), existing.ID, acl.Rules)
	if err != nil {
		s.logger.Error("update VPP ACL failed", "acl", existing.Name, "error", err)
		writeError(w, http.StatusInternalServerError, "could not update VPP ACL: "+err.Error())
		return
	}
	updated, err := s.store.UpdateACL(r.Context(), acl)
	if err != nil {
		writeACLStoreError(w, r, s, err)
		return
	}
	updated, err = s.store.SetACLVPPIndex(r.Context(), updated.ID, index)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.ReconcileACLs(r.Context()); err != nil {
		s.logger.Warn("reapply interface ACLs after ACL update failed", "acl", updated.Name, "error", err)
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteACL(w http.ResponseWriter, r *http.Request) {
	id, err := parseACLID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	acl, err := s.store.GetACL(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrACLNotFound):
		writeError(w, http.StatusNotFound, "acl not found")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	if err := s.store.DeleteACL(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrACLInUse) {
			writeError(w, http.StatusConflict, "ACL is assigned to an interface")
			return
		}
		s.internalError(w, r, err)
		return
	}
	if s.networks != nil {
		if err := s.networks.DeleteVPPACL(r.Context(), acl.VPPIndex); err != nil {
			s.logger.Warn("delete VPP ACL after database delete failed", "acl", acl.Name, "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func aclIndexRef(index uint32) uint32 {
	return index + 1
}

func parseACLID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid ACL id")
	}
	return id, nil
}

func writeACLStoreError(w http.ResponseWriter, r *http.Request, s *Server, err error) {
	switch {
	case errors.Is(err, store.ErrACLNameTaken):
		writeError(w, http.StatusConflict, "an ACL with that name already exists")
	case errors.Is(err, store.ErrACLNotFound):
		writeError(w, http.StatusNotFound, "acl not found")
	case errors.Is(err, store.ErrACLInUse):
		writeError(w, http.StatusConflict, "ACL is assigned to an interface")
	default:
		s.internalError(w, r, err)
	}
}

type interfaceACLPayload struct {
	IngressACL string `json:"ingress_acl"`
	EgressACL  string `json:"egress_acl"`
}

func (s *Server) handleSetInterfaceACLs(w http.ResponseWriter, r *http.Request) {
	id, err := parseInterfaceID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.networks == nil {
		writeError(w, http.StatusServiceUnavailable, "VPP network runtime is not configured")
		return
	}
	if blocked, message := s.vppBlocked(); blocked {
		writeError(w, http.StatusServiceUnavailable, message)
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
	var payload interfaceACLPayload
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var ingressID, egressID *int64
	var ingress, egress store.ACL
	if strings.TrimSpace(payload.IngressACL) != "" {
		ingress, err = s.store.GetACLByName(r.Context(), strings.TrimSpace(payload.IngressACL))
		if err != nil {
			writeACLStoreError(w, r, s, err)
			return
		}
		ingressID = &ingress.ID
	}
	if strings.TrimSpace(payload.EgressACL) != "" {
		egress, err = s.store.GetACLByName(r.Context(), strings.TrimSpace(payload.EgressACL))
		if err != nil {
			writeACLStoreError(w, r, s, err)
			return
		}
		egressID = &egress.ID
	}
	var ingressRef, egressRef uint32
	if ingressID != nil {
		index, err := s.networks.EnsureVPPACL(r.Context(), ingress.ID, ingress.Rules)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not configure ingress ACL: "+err.Error())
			return
		}
		ingress, err = s.store.SetACLVPPIndex(r.Context(), ingress.ID, index)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		ingressRef = aclIndexRef(ingress.VPPIndex)
	}
	if egressID != nil {
		index, err := s.networks.EnsureVPPACL(r.Context(), egress.ID, egress.Rules)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not configure egress ACL: "+err.Error())
			return
		}
		egress, err = s.store.SetACLVPPIndex(r.Context(), egress.ID, index)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		egressRef = aclIndexRef(egress.VPPIndex)
	}
	if err := s.networks.SetInterfaceACLs(r.Context(), iface.SwIfIndex, ingressRef, egressRef); err != nil {
		writeError(w, http.StatusInternalServerError, "could not apply interface ACLs: "+err.Error())
		return
	}
	updated, err := s.store.SetInterfaceACLs(r.Context(), id, ingressID, egressID)
	if err != nil {
		// Best-effort rollback of the VPP assignment.
		var oldIngress, oldEgress uint32
		if iface.IngressACLID != nil {
			if old, getErr := s.store.GetACL(r.Context(), *iface.IngressACLID); getErr == nil {
				oldIngress = old.VPPIndex
			}
		}
		if iface.EgressACLID != nil {
			if old, getErr := s.store.GetACL(r.Context(), *iface.EgressACLID); getErr == nil {
				oldEgress = old.VPPIndex
			}
		}
		_ = s.networks.SetInterfaceACLs(r.Context(), iface.SwIfIndex, aclIndexRef(oldIngress), aclIndexRef(oldEgress))
		writeACLStoreError(w, r, s, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) ReconcileACLs(ctx context.Context) error {
	if s.networks == nil {
		return nil
	}
	if blocked, _ := s.vppBlocked(); blocked {
		return nil
	}
	acls, err := s.store.ListACLs(ctx)
	if err != nil {
		return fmt.Errorf("list ACLs for reconciliation: %w", err)
	}
	indices := make(map[int64]uint32, len(acls))
	var errs []error
	for _, acl := range acls {
		index, err := s.networks.EnsureVPPACL(ctx, acl.ID, acl.Rules)
		if err != nil {
			errs = append(errs, fmt.Errorf("ACL %s: %w", acl.Name, err))
			continue
		}
		indices[acl.ID] = index
		if index != acl.VPPIndex {
			if _, err := s.store.SetACLVPPIndex(ctx, acl.ID, index); err != nil {
				errs = append(errs, fmt.Errorf("ACL %s: update VPP index: %w", acl.Name, err))
			}
		}
	}
	interfaces, err := s.store.ListInterfaces(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, iface := range interfaces {
		var ingress, egress uint32
		if iface.IngressACLID != nil {
			ingress = aclIndexRef(indices[*iface.IngressACLID])
		}
		if iface.EgressACLID != nil {
			egress = aclIndexRef(indices[*iface.EgressACLID])
		}
		if err := s.networks.SetInterfaceACLs(ctx, iface.SwIfIndex, ingress, egress); err != nil {
			errs = append(errs, fmt.Errorf("interface %s ACLs: %w", iface.Name, err))
		}
	}
	return errors.Join(errs...)
}
