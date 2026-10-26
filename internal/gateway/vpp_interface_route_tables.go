package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// GetVPPInterfaceRouteTables returns the configured IPv4/IPv6 FIB binding for
// a host interface or host tap. Vhost-user interfaces use the dedicated
// virtual-interface route-table API.
func (s *interfaceService) GetVPPInterfaceRouteTables(ctx context.Context, request *rookeryv1.GetVPPInterfaceRouteTablesRequest) (*rookeryv1.VPPInterfaceRouteTables, error) {
	name := strings.TrimSpace(request.GetName())
	if err := s.validateGenericRouteTableTarget(ctx, name); err != nil {
		return nil, err
	}
	tables, err := s.effectiveVPPInterfaceRouteTables(ctx, name)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load interface route tables")
	}
	return vppInterfaceRouteTablesToProto(tables), nil
}

// UpdateVPPInterfaceRouteTables applies and persists IPv4/IPv6 FIB bindings for
// a host interface or host tap.
func (s *interfaceService) UpdateVPPInterfaceRouteTables(ctx context.Context, request *rookeryv1.UpdateVPPInterfaceRouteTablesRequest) (*rookeryv1.VPPInterfaceRouteTables, error) {
	name := strings.TrimSpace(request.GetName())
	if s.addresses == nil {
		return nil, status.Error(codes.Unavailable, "VPP interface runtime is not configured")
	}
	if s.readiness != nil && !s.readiness.Status().VPP.Ready {
		return nil, status.Error(codes.Unavailable, "VPP is not ready")
	}
	if err := s.validateGenericRouteTableTarget(ctx, name); err != nil {
		return nil, err
	}
	ipv4 := request.GetIpv4TableId()
	ipv6 := request.GetIpv6TableId()
	for _, table := range []struct {
		id   uint32
		ipv6 bool
	}{{ipv4, false}, {ipv6, true}} {
		if table.id == 0 {
			continue
		}
		if _, err := s.store.GetRouteTableByID(ctx, table.id, table.ipv6); err != nil {
			if errors.Is(err, store.ErrRouteTableNotFound) {
				return nil, status.Error(codes.InvalidArgument, "route table not found")
			}
			return nil, status.Error(codes.Internal, "could not validate route table")
		}
	}

	interfaces, err := s.addresses.VPPInterfaces(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "could not query VPP interfaces")
	}
	var swIfIndex uint32
	found := false
	for _, iface := range interfaces {
		if iface.Name == name {
			swIfIndex = iface.SwIfIndex
			found = true
			break
		}
	}
	if !found {
		return nil, status.Error(codes.FailedPrecondition, "VPP interface does not exist")
	}

	if s.resourceMu != nil {
		s.resourceMu.Lock()
		defer s.resourceMu.Unlock()
	}
	previous, err := s.effectiveVPPInterfaceRouteTables(ctx, name)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load interface route tables")
	}
	if err := s.addresses.SetInterfaceRouteTable(ctx, swIfIndex, false, ipv4); err != nil {
		return nil, status.Error(codes.Internal, "could not apply IPv4 route table: "+err.Error())
	}
	if err := s.addresses.SetInterfaceRouteTable(ctx, swIfIndex, true, ipv6); err != nil {
		_ = s.addresses.SetInterfaceRouteTable(context.Background(), swIfIndex, false, previous.IPv4TableID)
		return nil, status.Error(codes.Internal, "could not apply IPv6 route table: "+err.Error())
	}
	updated, err := s.store.SetVPPInterfaceRouteTables(ctx, name, ipv4, ipv6)
	if err != nil {
		_ = s.addresses.SetInterfaceRouteTable(context.Background(), swIfIndex, false, previous.IPv4TableID)
		_ = s.addresses.SetInterfaceRouteTable(context.Background(), swIfIndex, true, previous.IPv6TableID)
		if errors.Is(err, store.ErrRouteTableNotFound) {
			return nil, status.Error(codes.InvalidArgument, "route table not found")
		}
		return nil, status.Error(codes.Internal, "could not save interface route tables")
	}
	return vppInterfaceRouteTablesToProto(updated), nil
}

// effectiveVPPInterfaceRouteTables returns the explicit binding when one is
// stored. With no explicit row, a host tap follows its network VRF, so report
// that table instead of incorrectly showing the default VRF.
func (s *interfaceService) effectiveVPPInterfaceRouteTables(ctx context.Context, name string) (store.VPPInterfaceRouteTables, error) {
	tables, err := s.store.GetVPPInterfaceRouteTables(ctx, name)
	if err != nil || tables.Configured {
		return tables, err
	}
	tap, err := s.store.GetNetworkHostTapByName(ctx, name)
	if errors.Is(err, store.ErrNetworkHostTapNotFound) {
		return tables, nil
	}
	if err != nil {
		return store.VPPInterfaceRouteTables{}, err
	}
	network, err := s.store.GetNetwork(ctx, tap.NetworkID)
	if err != nil {
		return store.VPPInterfaceRouteTables{}, err
	}
	tables.IPv4TableID = network.RouteTableID
	tables.IPv6TableID = network.RouteTableID
	tables.IPv4TableName = s.routeTableName(ctx, network.RouteTableID, false)
	tables.IPv6TableName = s.routeTableName(ctx, network.RouteTableID, true)
	return tables, nil
}

func (s *interfaceService) routeTableName(ctx context.Context, tableID uint32, ipv6 bool) string {
	if tableID == 0 {
		return "default"
	}
	table, err := s.store.GetRouteTableByID(ctx, tableID, ipv6)
	if err != nil {
		return fmt.Sprintf("table %d", tableID)
	}
	return table.Name
}

func (s *interfaceService) validateGenericRouteTableTarget(ctx context.Context, name string) error {
	if name == "" {
		return status.Error(codes.InvalidArgument, "interface name is required")
	}
	managed, err := s.store.IsManagedVPPInterface(ctx, name)
	if err != nil {
		return status.Error(codes.Internal, "could not check VPP interface")
	}
	if !managed {
		return status.Error(codes.NotFound, "VPP interface is not managed by Rookery")
	}
	isVirtual, err := s.store.IsVirtualInterfaceName(ctx, name)
	if err != nil {
		return status.Error(codes.Internal, "could not check virtual interface")
	}
	if isVirtual {
		return status.Error(codes.FailedPrecondition, "use the virtual interface route-tables endpoint for vhost-user interfaces")
	}
	return nil
}

func vppInterfaceRouteTablesToProto(tables store.VPPInterfaceRouteTables) *rookeryv1.VPPInterfaceRouteTables {
	ipv4Name := tables.IPv4TableName
	if tables.IPv4TableID == 0 {
		ipv4Name = "default"
	} else if ipv4Name == "" {
		ipv4Name = fmt.Sprintf("table %d", tables.IPv4TableID)
	}
	ipv6Name := tables.IPv6TableName
	if tables.IPv6TableID == 0 {
		ipv6Name = "default"
	} else if ipv6Name == "" {
		ipv6Name = fmt.Sprintf("table %d", tables.IPv6TableID)
	}
	return &rookeryv1.VPPInterfaceRouteTables{
		Name: tables.InterfaceName, Ipv4TableId: tables.IPv4TableID, Ipv6TableId: tables.IPv6TableID,
		Ipv4TableName: ipv4Name, Ipv6TableName: ipv6Name,
	}
}
