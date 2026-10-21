package gateway

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/netutil"
	"github.com/voidstarit/rookery/internal/store"
)

type routeTableService struct {
	rookeryv1.UnimplementedRouteTableServiceServer
	store     *store.Store
	runtime   ACLRuntime
	readiness Readiness
}

func (s *routeTableService) runtimeReady() error {
	if s.runtime == nil {
		return status.Error(codes.Unavailable, "VPP network runtime is not configured")
	}
	if s.readiness != nil && !s.readiness.Status().VPP.Ready {
		return status.Error(codes.Unavailable, "VPP is not ready")
	}
	return nil
}

func (s *routeTableService) ListRouteTables(ctx context.Context, _ *rookeryv1.ListRouteTablesRequest) (*rookeryv1.ListRouteTablesResponse, error) {
	tables, err := s.store.ListRouteTables(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list route tables")
	}
	response := &rookeryv1.ListRouteTablesResponse{
		RouteTables: []*rookeryv1.RouteTable{
			defaultRouteTable(false),
			defaultRouteTable(true),
		},
	}
	for _, table := range tables {
		response.RouteTables = append(response.RouteTables, routeTableToProto(table))
	}
	for _, table := range response.RouteTables {
		count, err := s.store.RouteTableInterfaceCount(ctx, table.TableId, table.Ipv6)
		if err == nil {
			table.InterfaceCount = count
		}
		routes, err := s.store.RouteCount(ctx, table.TableId, table.Ipv6)
		if err == nil {
			table.RouteCount = routes
		}
	}
	return response, nil
}

func defaultRouteTable(ipv6 bool) *rookeryv1.RouteTable {
	table := &rookeryv1.RouteTable{Id: 0, Name: "default", TableId: 0, Ipv6: ipv6, IsDefault: true}
	return table
}

func routeTableToProto(table store.RouteTable) *rookeryv1.RouteTable {
	return &rookeryv1.RouteTable{
		Id: table.ID, Name: table.Name, TableId: table.TableID, Ipv6: table.IPv6,
		NetworkId: table.NetworkID, Managed: table.NetworkID != 0,
		CreatedAt: timestamp(table.CreatedAt), UpdatedAt: timestamp(table.UpdatedAt),
	}
}

func (s *routeTableService) CreateRouteTable(ctx context.Context, request *rookeryv1.CreateRouteTableRequest) (*rookeryv1.RouteTable, error) {
	if err := s.runtimeReady(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(request.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if request.GetTableId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "table_id must be greater than zero")
	}
	if request.GetTableId() >= netutil.NetworkRouteTableBase {
		return nil, status.Errorf(codes.InvalidArgument, "table ids %d and above are reserved for network VRFs", netutil.NetworkRouteTableBase)
	}
	created, err := s.store.CreateRouteTable(ctx, store.RouteTable{
		Name: name, TableID: request.GetTableId(), IPv6: request.GetIpv6(),
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRouteTableIDTaken):
			return nil, status.Error(codes.AlreadyExists, "a route table with that table id and family already exists")
		case errors.Is(err, store.ErrRouteTableNameTaken):
			return nil, status.Error(codes.AlreadyExists, "a route table with that name and family already exists")
		default:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	if err := s.runtime.EnsureRouteTable(ctx, created.TableID, created.IPv6, created.Name); err != nil {
		_ = s.store.DeleteRouteTable(ctx, created.ID)
		return nil, status.Error(codes.Internal, "could not create VPP route table: "+err.Error())
	}
	return routeTableToProto(created), nil
}

func (s *routeTableService) DeleteRouteTable(ctx context.Context, request *rookeryv1.DeleteRouteTableRequest) (*emptypb.Empty, error) {
	if err := s.runtimeReady(); err != nil {
		return nil, err
	}
	table, err := s.store.GetRouteTable(ctx, request.GetId())
	if err != nil {
		return nil, routeTableStoreError(err)
	}
	if table.NetworkID != 0 {
		return nil, status.Error(codes.FailedPrecondition, "route table is managed by its network")
	}
	if err := s.store.DeleteRouteTable(ctx, table.ID); err != nil {
		return nil, routeTableStoreError(err)
	}
	if err := s.runtime.DeleteRouteTable(ctx, table.TableID, table.IPv6); err != nil {
		_, _ = s.store.CreateRouteTable(context.Background(), table)
		return nil, status.Error(codes.Internal, "could not delete VPP route table: "+err.Error())
	}
	return &emptypb.Empty{}, nil
}

func (s *routeTableService) SetInterfaceRouteTables(ctx context.Context, request *rookeryv1.SetInterfaceRouteTablesRequest) (*rookeryv1.VirtualInterface, error) {
	if err := s.runtimeReady(); err != nil {
		return nil, err
	}
	iface, err := s.store.GetInterface(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrInterfaceNotFound, "interface not found")
	}
	ipv4 := request.GetIpv4TableId()
	ipv6 := request.GetIpv6TableId()
	if err := s.applyInterfaceRouteTables(ctx, iface, ipv4, ipv6); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	updated, err := s.store.SetInterfaceRouteTables(ctx, iface.ID, ipv4, ipv6)
	if err != nil {
		_ = s.applyInterfaceRouteTables(ctx, iface, iface.IPv4TableID, iface.IPv6TableID)
		return nil, routeTableStoreError(err)
	}
	return interfaceToProto(updated), nil
}

func (s *routeTableService) applyInterfaceRouteTables(ctx context.Context, iface store.VirtualInterface, ipv4, ipv6 uint32) error {
	if ipv4 != 0 {
		if _, err := s.store.GetRouteTableByID(ctx, ipv4, false); err != nil {
			return err
		}
	}
	if ipv6 != 0 {
		if _, err := s.store.GetRouteTableByID(ctx, ipv6, true); err != nil {
			return err
		}
	}
	if err := s.runtime.SetInterfaceRouteTable(ctx, iface.SwIfIndex, false, ipv4); err != nil {
		return err
	}
	if err := s.runtime.SetInterfaceRouteTable(ctx, iface.SwIfIndex, true, ipv6); err != nil {
		_ = s.runtime.SetInterfaceRouteTable(ctx, iface.SwIfIndex, false, iface.IPv4TableID)
		return err
	}
	return nil
}

func routeTableStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrRouteTableNotFound):
		return status.Error(codes.NotFound, "route table not found")
	case errors.Is(err, store.ErrRouteTableInUse):
		return status.Error(codes.Aborted, "route table is in use")
	case errors.Is(err, store.ErrRouteTableNameTaken), errors.Is(err, store.ErrRouteTableIDTaken):
		return status.Error(codes.AlreadyExists, "route table already exists")
	case errors.Is(err, store.ErrInterfaceNotFound):
		return status.Error(codes.NotFound, "interface not found")
	default:
		return status.Error(codes.Internal, "route table operation failed")
	}
}

func (s *routeTableService) resolveRouteTable(ctx context.Context, id int64, ipv6 bool) (store.RouteTable, error) {
	if id == 0 {
		return store.RouteTable{Name: "default", TableID: 0, IPv6: ipv6}, nil
	}
	table, err := s.store.GetRouteTable(ctx, id)
	if err != nil {
		return store.RouteTable{}, err
	}
	return table, nil
}

func (s *routeTableService) ListRoutes(ctx context.Context, request *rookeryv1.ListRoutesRequest) (*rookeryv1.ListRoutesResponse, error) {
	table, err := s.resolveRouteTable(ctx, request.GetRouteTableId(), request.GetIpv6())
	if err != nil {
		return nil, routeTableStoreError(err)
	}
	routes, err := s.store.ListRoutes(ctx, table.TableID, table.IPv6)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list routes")
	}
	response := &rookeryv1.ListRoutesResponse{Routes: make([]*rookeryv1.Route, 0, len(routes))}
	for _, route := range routes {
		response.Routes = append(response.Routes, routeToProto(route, request.GetRouteTableId()))
	}
	return response, nil
}

func (s *routeTableService) CreateRoute(ctx context.Context, request *rookeryv1.CreateRouteRequest) (*rookeryv1.Route, error) {
	if err := s.runtimeReady(); err != nil {
		return nil, err
	}
	table, err := s.resolveRouteTable(ctx, request.GetRouteTableId(), request.GetIpv6())
	if err != nil {
		return nil, routeTableStoreError(err)
	}
	var interfaceID *int64
	swIfIndex := uint32(0)
	if request.GetInterfaceId() != 0 {
		id := request.GetInterfaceId()
		iface, err := s.store.GetInterface(ctx, id)
		if err != nil {
			return nil, storeReadError(err, store.ErrInterfaceNotFound, "interface not found")
		}
		interfaceID = &id
		swIfIndex = iface.SwIfIndex
	}
	egress := strings.TrimSpace(request.GetEgressInterface())
	if interfaceID != nil && egress != "" {
		return nil, status.Error(codes.InvalidArgument, "interface_id and egress_interface are mutually exclusive")
	}
	created, err := s.store.CreateRoute(ctx, store.Route{
		TableID: table.TableID, IPv6: table.IPv6, Prefix: request.GetPrefix(),
		NextHop: request.GetNextHop(), InterfaceID: interfaceID,
		EgressInterface: egress, Preference: request.GetPreference(),
	})
	if err != nil {
		return nil, routeStoreError(err)
	}
	if err := s.runtime.AddRoute(ctx, created.TableID, created.IPv6, created.Prefix, created.NextHop, created.EgressInterface, swIfIndex, created.Preference); err != nil {
		_ = s.store.DeleteRoute(ctx, created.ID)
		return nil, status.Error(codes.InvalidArgument, "could not install route: "+err.Error())
	}
	return routeToProto(created, request.GetRouteTableId()), nil
}

func (s *routeTableService) DeleteRoute(ctx context.Context, request *rookeryv1.DeleteRouteRequest) (*emptypb.Empty, error) {
	if err := s.runtimeReady(); err != nil {
		return nil, err
	}
	route, err := s.store.GetRoute(ctx, request.GetId())
	if err != nil {
		return nil, routeStoreError(err)
	}
	if err := s.store.DeleteRoute(ctx, route.ID); err != nil {
		return nil, routeStoreError(err)
	}
	if err := s.runtime.DeleteRoute(ctx, route.TableID, route.IPv6, route.Prefix); err != nil {
		_, _ = s.store.CreateRoute(context.Background(), route)
		return nil, status.Error(codes.Internal, "could not remove VPP route: "+err.Error())
	}
	return &emptypb.Empty{}, nil
}

func (s *routeTableService) ListFIBRoutes(ctx context.Context, request *rookeryv1.ListFIBRoutesRequest) (*rookeryv1.ListFIBRoutesResponse, error) {
	if err := s.runtimeReady(); err != nil {
		return nil, err
	}
	table, err := s.resolveRouteTable(ctx, request.GetRouteTableId(), request.GetIpv6())
	if err != nil {
		return nil, routeTableStoreError(err)
	}
	routes, err := s.runtime.DumpFIBRoutes(ctx, table.TableID, table.IPv6)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read VPP FIB: "+err.Error())
	}
	response := &rookeryv1.ListFIBRoutesResponse{Routes: make([]*rookeryv1.FIBRoute, 0, len(routes))}
	for _, route := range routes {
		entry := &rookeryv1.FIBRoute{
			Prefix: route.Prefix, TableId: route.TableID, Ipv6: route.IPv6,
			Source: uint32(route.Source), SourceName: route.SourceName, Dynamic: route.Dynamic,
			StatsIndex: route.StatsIndex, Paths: make([]*rookeryv1.FIBPath, 0, len(route.Paths)),
		}
		for _, path := range route.Paths {
			entry.Paths = append(entry.Paths, &rookeryv1.FIBPath{
				SwIfIndex: path.SwIfIndex, InterfaceName: path.InterfaceName,
				NextHop: path.NextHop, Type: path.Type, Weight: uint32(path.Weight),
				Preference: uint32(path.Preference), Proto: path.Proto,
			})
		}
		response.Routes = append(response.Routes, entry)
	}
	return response, nil
}

func routeToProto(route store.Route, routeTableID int64) *rookeryv1.Route {
	return &rookeryv1.Route{
		Id: route.ID, RouteTableId: routeTableID, TableId: route.TableID, Ipv6: route.IPv6,
		Prefix: route.Prefix, NextHop: route.NextHop, InterfaceId: route.InterfaceID,
		InterfaceName: route.InterfaceName, EgressInterface: route.EgressInterface,
		Preference: route.Preference, CreatedAt: timestamp(route.CreatedAt),
		UpdatedAt: timestamp(route.UpdatedAt),
	}
}

func routeStoreError(err error) error {
	switch {
	case errors.Is(err, store.ErrRouteNotFound):
		return status.Error(codes.NotFound, "route not found")
	case errors.Is(err, store.ErrRouteExists):
		return status.Error(codes.AlreadyExists, "a route with that prefix already exists in the table")
	case errors.Is(err, store.ErrRouteTableNotFound):
		return status.Error(codes.NotFound, "route table not found")
	case errors.Is(err, store.ErrInterfaceNotFound):
		return status.Error(codes.NotFound, "interface not found")
	default:
		return status.Error(codes.InvalidArgument, err.Error())
	}
}
