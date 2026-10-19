package api

import (
	"context"
	"errors"
	"fmt"
)

func aclIndexRef(index uint32) uint32 { return index + 1 }

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
