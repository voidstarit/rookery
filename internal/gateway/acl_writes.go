package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// ACLRuntime applies ACL definitions and interface assignments to VPP.
type ACLRuntime interface {
	EnsureVPPACL(ctx context.Context, aclID int64, rules []store.ACLRule) (uint32, error)
	DeleteVPPACL(ctx context.Context, index uint32) error
	SetInterfaceACLs(ctx context.Context, swIfIndex uint32, ingress, egress []uint32) error
	EnsureRouteTable(ctx context.Context, tableID uint32, ipv6 bool, name string) error
	DeleteRouteTable(ctx context.Context, tableID uint32, ipv6 bool) error
	SetInterfaceRouteTable(ctx context.Context, swIfIndex uint32, ipv6 bool, tableID uint32) error
	AddRoute(ctx context.Context, tableID uint32, ipv6 bool, prefix, nextHop, egressName string, swIfIndex uint32, preference uint32) error
	AddRouteSpec(ctx context.Context, spec host.RouteSpec) error
	DeleteRoute(ctx context.Context, tableID uint32, ipv6 bool, prefix string) error
	DumpFIBRoutes(ctx context.Context, tableID uint32, ipv6 bool) ([]host.FIBRoute, error)
}

func (s *aclService) runtimeReady() error {
	if s.runtime == nil {
		return status.Error(codes.Unavailable, "VPP network runtime is not configured")
	}
	if s.readiness != nil && !s.readiness.Status().VPP.Ready {
		return status.Error(codes.Unavailable, "VPP is not ready")
	}
	return nil
}

func (s *aclService) CreateACL(ctx context.Context, request *rookeryv1.CreateACLRequest) (*rookeryv1.ACL, error) {
	if err := s.runtimeReady(); err != nil {
		return nil, err
	}
	acl, err := aclFromCreateRequest(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	created, err := s.store.CreateACL(ctx, acl)
	if err != nil {
		return nil, aclStoreError(err)
	}
	index, err := s.runtime.EnsureVPPACL(ctx, created.ID, created.Rules)
	if err != nil {
		_ = s.store.DeleteACL(ctx, created.ID)
		return nil, status.Error(codes.Internal, "could not configure VPP ACL: "+err.Error())
	}
	created, err = s.store.SetACLVPPIndex(ctx, created.ID, index)
	if err != nil {
		_ = s.runtime.DeleteVPPACL(ctx, index)
		_ = s.store.DeleteACL(ctx, created.ID)
		return nil, status.Error(codes.Internal, "could not record VPP ACL index")
	}
	return aclToProto(created), nil
}

func (s *aclService) UpdateACL(ctx context.Context, request *rookeryv1.UpdateACLRequest) (*rookeryv1.ACL, error) {
	if err := s.runtimeReady(); err != nil {
		return nil, err
	}
	existing, err := s.store.GetACL(ctx, request.GetId())
	if err != nil {
		return nil, aclStoreError(err)
	}
	acl, err := aclFromUpdateRequest(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	acl.ID = existing.ID
	index, err := s.runtime.EnsureVPPACL(ctx, existing.ID, acl.Rules)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not update VPP ACL: "+err.Error())
	}
	updated, err := s.store.UpdateACL(ctx, acl)
	if err != nil {
		return nil, aclStoreError(err)
	}
	updated, err = s.store.SetACLVPPIndex(ctx, updated.ID, index)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not record VPP ACL index")
	}
	if err := s.reconcileACLs(ctx); err != nil {
		// The definition is stored; interface assignments can be repaired by
		// startup reconciliation or a later update.
		_ = err
	}
	return aclToProto(updated), nil
}

func (s *aclService) DeleteACL(ctx context.Context, request *rookeryv1.DeleteACLRequest) (*emptypb.Empty, error) {
	acl, err := s.store.GetACL(ctx, request.GetId())
	if err != nil {
		return nil, aclStoreError(err)
	}
	if err := s.store.DeleteACL(ctx, request.GetId()); err != nil {
		return nil, aclStoreError(err)
	}
	if s.runtime != nil {
		_ = s.runtime.DeleteVPPACL(ctx, acl.VPPIndex)
	}
	return &emptypb.Empty{}, nil
}

func (s *aclService) reconcileACLs(ctx context.Context) error {
	acls, err := s.store.ListACLs(ctx)
	if err != nil {
		return err
	}
	indices := make(map[int64]uint32, len(acls))
	var errs []error
	for _, acl := range acls {
		index, err := s.runtime.EnsureVPPACL(ctx, acl.ID, acl.Rules)
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
		var ingress, egress []uint32
		for _, acl := range iface.IngressACLs {
			if index, ok := indices[acl.ID]; ok {
				ingress = append(ingress, aclIndexReference(index))
			}
		}
		for _, acl := range iface.EgressACLs {
			if index, ok := indices[acl.ID]; ok {
				egress = append(egress, aclIndexReference(index))
			}
		}
		if err := s.runtime.SetInterfaceACLs(ctx, iface.SwIfIndex, ingress, egress); err != nil {
			errs = append(errs, fmt.Errorf("interface %s ACLs: %w", iface.Name, err))
		}
	}
	return errors.Join(errs...)
}

func aclIndexReference(index uint32) uint32 { return index + 1 }

func aclStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrACLNotFound):
		return status.Error(codes.NotFound, "acl not found")
	case errors.Is(err, store.ErrACLNameTaken):
		return status.Error(codes.AlreadyExists, "an ACL with that name already exists")
	case errors.Is(err, store.ErrACLInUse):
		return status.Error(codes.Aborted, "ACL is assigned to an interface")
	default:
		return status.Error(codes.Internal, "ACL operation failed")
	}
}

func aclFromCreateRequest(request *rookeryv1.CreateACLRequest) (store.ACL, error) {
	return aclFromFields(request.GetName(), request.GetTitle(), request.GetDescription(), request.GetRules())
}

func aclFromUpdateRequest(request *rookeryv1.UpdateACLRequest) (store.ACL, error) {
	return aclFromFields(request.GetName(), request.GetTitle(), request.GetDescription(), request.GetRules())
}

func aclFromFields(name, title, description string, rules []*rookeryv1.ACLRule) (store.ACL, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return store.ACL{}, errors.New("name is required")
	case len(name) > 64:
		return store.ACL{}, errors.New("name must be at most 64 characters")
	}
	title = strings.TrimSpace(title)
	if len(title) > 128 {
		return store.ACL{}, errors.New("title must be at most 128 characters")
	}
	description = strings.TrimSpace(description)
	if len(description) > 2000 {
		return store.ACL{}, errors.New("description must be at most 2000 characters")
	}
	if len(rules) == 0 {
		return store.ACL{}, errors.New("at least one rule is required")
	}
	if len(rules) > 64 {
		return store.ACL{}, errors.New("an ACL can have at most 64 rules")
	}
	normalized := make([]store.ACLRule, 0, len(rules))
	for i, rule := range rules {
		value, err := aclRuleFromProto(rule)
		if err != nil {
			return store.ACL{}, fmt.Errorf("rule %d: %w", i+1, err)
		}
		normalized = append(normalized, value)
	}
	return store.ACL{Name: name, Title: title, Description: description, Rules: normalized}, nil
}

func aclRuleFromProto(rule *rookeryv1.ACLRule) (store.ACLRule, error) {
	description := strings.TrimSpace(rule.GetDescription())
	if len(description) > 256 {
		return store.ACLRule{}, errors.New("description must be at most 256 characters")
	}
	action := strings.ToLower(strings.TrimSpace(rule.GetAction()))
	if action != "permit" && action != "deny" {
		return store.ACLRule{}, errors.New("action must be permit or deny")
	}
	protocol := strings.ToLower(strings.TrimSpace(rule.GetProtocol()))
	switch protocol {
	case "any", "tcp", "udp", "icmp", "icmpv6":
	case "":
		protocol = "any"
	default:
		return store.ACLRule{}, errors.New("protocol must be any, tcp, udp, icmp, or icmpv6")
	}
	source, sourceNet, err := net.ParseCIDR(strings.TrimSpace(rule.GetSource()))
	if err != nil {
		return store.ACLRule{}, errors.New("source must be a CIDR")
	}
	destination, destinationNet, err := net.ParseCIDR(strings.TrimSpace(rule.GetDestination()))
	if err != nil {
		return store.ACLRule{}, errors.New("destination must be a CIDR")
	}
	if (source.To4() == nil) != (destination.To4() == nil) {
		return store.ACLRule{}, errors.New("source and destination must use the same address family")
	}
	sourcePorts := [2]int{int(rule.GetSourcePortStart()), int(rule.GetSourcePortEnd())}
	destinationPorts := [2]int{int(rule.GetDestinationPortStart()), int(rule.GetDestinationPortEnd())}
	if protocol != "tcp" && protocol != "udp" {
		if sourcePorts != [2]int{0, 0} || destinationPorts != [2]int{0, 0} {
			return store.ACLRule{}, errors.New("ports are only valid for tcp and udp")
		}
		return store.ACLRule{
			Action: action, Description: description, Protocol: protocol,
			Source: sourceNet.String(), Destination: destinationNet.String(),
		}, nil
	}
	normalizedSource, err := normalizedPortRange(sourcePorts)
	if err != nil {
		return store.ACLRule{}, fmt.Errorf("source ports: %w", err)
	}
	normalizedDestination, err := normalizedPortRange(destinationPorts)
	if err != nil {
		return store.ACLRule{}, fmt.Errorf("destination ports: %w", err)
	}
	return store.ACLRule{
		Action: action, Description: description, Protocol: protocol,
		Source: sourceNet.String(), Destination: destinationNet.String(),
		SourcePortStart: normalizedSource[0], SourcePortEnd: normalizedSource[1],
		DestinationPortStart: normalizedDestination[0], DestinationPortEnd: normalizedDestination[1],
	}, nil
}

func normalizedPortRange(ports [2]int) ([2]int, error) {
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

func (s *interfaceService) runtimeReady() error {
	if s.runtime == nil {
		return status.Error(codes.Unavailable, "VPP network runtime is not configured")
	}
	if s.readiness != nil && !s.readiness.Status().VPP.Ready {
		return status.Error(codes.Unavailable, "VPP is not ready")
	}
	return nil
}

func (s *interfaceService) SetInterfaceACLs(ctx context.Context, request *rookeryv1.SetInterfaceACLsRequest) (*rookeryv1.VirtualInterface, error) {
	if err := s.runtimeReady(); err != nil {
		return nil, err
	}
	iface, err := s.store.GetInterface(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrInterfaceNotFound, "interface not found")
	}

	ingressNames := request.GetIngressAcls()
	if len(ingressNames) == 0 && strings.TrimSpace(request.GetIngressAcl()) != "" {
		ingressNames = []string{request.GetIngressAcl()}
	}
	egressNames := request.GetEgressAcls()
	if len(egressNames) == 0 && strings.TrimSpace(request.GetEgressAcl()) != "" {
		egressNames = []string{request.GetEgressAcl()}
	}

	ingressIDs, ingressRefs, err := s.resolveInterfaceACLs(ctx, ingressNames)
	if err != nil {
		return nil, err
	}
	egressIDs, egressRefs, err := s.resolveInterfaceACLs(ctx, egressNames)
	if err != nil {
		return nil, err
	}

	if err := s.runtime.SetInterfaceACLs(ctx, iface.SwIfIndex, ingressRefs, egressRefs); err != nil {
		return nil, status.Error(codes.Internal, "could not apply interface ACLs: "+err.Error())
	}
	updated, err := s.store.SetInterfaceACLs(ctx, iface.ID, ingressIDs, egressIDs)
	if err != nil {
		oldIngress := s.interfaceACLRefs(ctx, iface.IngressACLs, iface.IngressACLID)
		oldEgress := s.interfaceACLRefs(ctx, iface.EgressACLs, iface.EgressACLID)
		_ = s.runtime.SetInterfaceACLs(ctx, iface.SwIfIndex, oldIngress, oldEgress)
		return nil, aclStoreError(err)
	}
	return interfaceToProto(updated), nil
}

func (s *interfaceService) resolveInterfaceACLs(ctx context.Context, names []string) ([]int64, []uint32, error) {
	ids := make([]int64, 0, len(names))
	refs := make([]uint32, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(name)]; ok {
			continue
		}
		seen[strings.ToLower(name)] = struct{}{}
		acl, err := s.store.GetACLByName(ctx, name)
		if err != nil {
			return nil, nil, aclStoreError(err)
		}
		index, err := s.runtime.EnsureVPPACL(ctx, acl.ID, acl.Rules)
		if err != nil {
			return nil, nil, status.Error(codes.Internal, "could not configure ACL "+acl.Name+": "+err.Error())
		}
		acl, err = s.store.SetACLVPPIndex(ctx, acl.ID, index)
		if err != nil {
			return nil, nil, status.Error(codes.Internal, "could not record ACL index for "+acl.Name)
		}
		ids = append(ids, acl.ID)
		refs = append(refs, aclIndexReference(acl.VPPIndex))
	}
	return ids, refs, nil
}

func (s *interfaceService) interfaceACLRefs(ctx context.Context, refs []store.InterfaceACL, legacy *int64) []uint32 {
	ids := make([]int64, 0, len(refs)+1)
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	if len(ids) == 0 && legacy != nil {
		ids = append(ids, *legacy)
	}
	result := make([]uint32, 0, len(ids))
	for _, id := range ids {
		acl, err := s.store.GetACL(ctx, id)
		if err != nil {
			continue
		}
		result = append(result, aclIndexReference(acl.VPPIndex))
	}
	return result
}
