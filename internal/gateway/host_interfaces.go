package gateway

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// VPPInterfaceSource lists the interfaces currently present in VPP.
type VPPInterfaceSource interface {
	VPPInterfaces(ctx context.Context) ([]host.VPPInterface, error)
}

// HostWriter takes host NICs into and out of VPP.
type HostWriter interface {
	EnsureHostInterface(ctx context.Context, nic string) (string, error)
	DeleteHostInterface(ctx context.Context, nic string) error
}

// HostTapWriter creates and deletes persistent taps between a Rookery
// Network and the Linux host.
type HostTapWriter interface {
	CreateNetworkHostTap(ctx context.Context, networkID int64, name, ipv4Address string) (store.NetworkHostTap, error)
	DeleteNetworkHostTap(ctx context.Context, id int64) error
}

func (s *hostService) ListHostBlockDevices(_ context.Context, _ *rookeryv1.ListHostBlockDevicesRequest) (*rookeryv1.ListHostBlockDevicesResponse, error) {
	devices, err := host.ListBlockDevices()
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list block devices")
	}
	response := &rookeryv1.ListHostBlockDevicesResponse{Devices: make([]*rookeryv1.HostBlockDevice, 0, len(devices))}
	for _, device := range devices {
		response.Devices = append(response.Devices, &rookeryv1.HostBlockDevice{
			Name: device.Name, Path: device.Path, Model: device.Model,
			SizeBytes: device.SizeBytes, Major: device.Major, Minor: device.Minor,
			Rotational: device.Rotational,
		})
	}
	return response, nil
}

func (s *hostService) listInterfaces(ctx context.Context) (*rookeryv1.ListHostInterfacesResponse, error) {
	interfaces, err := host.ListHostInterfaces()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]host.VPPInterface)
	vppChecked := false
	if s.source != nil {
		if vppInterfaces, err := s.source.VPPInterfaces(ctx); err == nil {
			vppChecked = true
			for _, iface := range vppInterfaces {
				byName[iface.Name] = iface
			}
		}
	}
	managed := make(map[string]bool)
	if s.store != nil {
		if records, err := s.store.ListHostInterfaceRecords(ctx); err == nil {
			for _, record := range records {
				managed[record.NIC] = true
			}
		}
	}
	response := &rookeryv1.ListHostInterfacesResponse{Interfaces: make([]*rookeryv1.HostInterface, 0, len(interfaces))}
	for _, iface := range interfaces {
		item := &rookeryv1.HostInterface{
			Name: iface.Name, Up: iface.Up, Addresses: iface.Addresses,
			DefaultRoute: iface.DefaultRoute, Physical: iface.Physical, Wireless: iface.Wireless,
			VppChecked: vppChecked, Managed: managed[iface.Name],
		}
		for _, name := range []string{"host-" + iface.Name, iface.Name} {
			if vppIface, ok := byName[name]; ok {
				item.InVpp = true
				item.VppName = vppIface.Name
				item.VppUp = vppIface.Up
				item.VppType = vppIface.Type
				item.VppSwIfIndex = &vppIface.SwIfIndex
				if s.addresses != nil && (s.readiness == nil || s.readiness.Status().VPP.Ready) {
					if addresses, err := s.addresses.InterfaceAddresses(ctx, vppIface.Name); err == nil {
						item.VppAddresses = addresses
					}
				}
				break
			}
		}
		response.Interfaces = append(response.Interfaces, item)
	}
	return response, nil
}

func (s *hostService) attachable(iface host.HostInterface) error {
	switch {
	case iface.Wireless:
		return status.Error(codes.InvalidArgument, "wireless interfaces cannot be attached")
	case iface.DefaultRoute:
		return status.Error(codes.InvalidArgument, "the interface carrying the host default route cannot be attached")
	case !iface.Physical:
		return status.Error(codes.InvalidArgument, "only physical host interfaces can be attached")
	}
	return nil
}

func (s *hostService) vppInterface(ctx context.Context, nic string) (host.VPPInterface, bool) {
	if s.source == nil {
		return host.VPPInterface{}, false
	}
	interfaces, err := s.source.VPPInterfaces(ctx)
	if err != nil {
		return host.VPPInterface{}, false
	}
	for _, iface := range interfaces {
		if iface.Name == "host-"+nic {
			return iface, true
		}
	}
	return host.VPPInterface{}, false
}

func (s *hostService) AttachHostInterface(ctx context.Context, request *rookeryv1.AttachHostInterfaceRequest) (*rookeryv1.HostInterface, error) {
	name := strings.TrimSpace(request.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "interface name is required")
	}
	if s.writer == nil {
		return nil, status.Error(codes.Unavailable, "VPP host-interface runtime is not configured")
	}
	interfaces, err := host.ListHostInterfaces()
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list host interfaces")
	}
	var target *host.HostInterface
	for i := range interfaces {
		if interfaces[i].Name == name {
			target = &interfaces[i]
			break
		}
	}
	if target == nil {
		return nil, status.Error(codes.NotFound, "host interface not found")
	}
	if err := s.attachable(*target); err != nil {
		return nil, err
	}
	if _, err := s.writer.EnsureHostInterface(ctx, name); err != nil {
		return nil, status.Error(codes.Internal, "could not attach host interface: "+err.Error())
	}
	record := store.HostInterfaceRecord{NIC: name, VPPName: "host-" + name, Purpose: "manual"}
	if iface, ok := s.vppInterface(ctx, name); ok {
		record.SwIfIndex = iface.SwIfIndex
	}
	if s.store != nil {
		if _, err := s.store.UpsertHostInterfaceRecord(ctx, record); err != nil {
			return nil, status.Error(codes.Internal, "could not persist host interface: "+err.Error())
		}
	}
	response, err := s.listInterfaces(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list host interfaces")
	}
	for _, item := range response.GetInterfaces() {
		if item.GetName() == name {
			return item, nil
		}
	}
	return &rookeryv1.HostInterface{Name: name, InVpp: true, VppName: "host-" + name, Managed: true}, nil
}

func (s *hostService) DetachHostInterface(ctx context.Context, request *rookeryv1.DetachHostInterfaceRequest) (*emptypb.Empty, error) {
	name := strings.TrimSpace(request.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "interface name is required")
	}
	if s.writer == nil {
		return nil, status.Error(codes.Unavailable, "VPP host-interface runtime is not configured")
	}
	if s.store != nil {
		vnfs, err := s.store.ListVNFs(ctx, nil)
		if err != nil {
			return nil, status.Error(codes.Internal, "could not read VNFs")
		}
		for _, vnf := range vnfs {
			if vnf.Type == store.VNFTypeNAT44 && vnf.Config.OutsideInterface == name {
				return nil, status.Error(codes.FailedPrecondition, "interface is in use by a NAT44 VNF")
			}
		}
	}
	if err := s.writer.DeleteHostInterface(ctx, name); err != nil {
		return nil, status.Error(codes.Internal, "could not detach host interface: "+err.Error())
	}
	if s.store != nil {
		if err := s.store.DeleteHostInterfaceRecord(ctx, name); err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.Internal, "could not remove host interface record")
		}
		_ = s.store.DeleteVPPInterfaceAddresses(ctx, "host-"+name)
	}
	return &emptypb.Empty{}, nil
}

func (s *hostService) ListHostTaps(ctx context.Context, request *rookeryv1.ListHostTapsRequest) (*rookeryv1.ListHostTapsResponse, error) {
	var networkID *int64
	if request.GetNetworkId() > 0 {
		id := request.GetNetworkId()
		if _, err := s.store.GetNetwork(ctx, id); err != nil {
			return nil, storeReadError(err, store.ErrNetworkNotFound, "network not found")
		}
		networkID = &id
	}
	taps, err := s.store.ListNetworkHostTaps(ctx, networkID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list host taps")
	}
	response := &rookeryv1.ListHostTapsResponse{Taps: make([]*rookeryv1.HostTap, 0, len(taps))}
	for _, tap := range taps {
		item := hostTapToProto(tap)
		if s.addresses != nil && (s.readiness == nil || s.readiness.Status().VPP.Ready) {
			if addresses, err := s.addresses.InterfaceAddresses(ctx, tap.Name); err == nil {
				item.VppAddresses = addresses
			}
		}
		response.Taps = append(response.Taps, item)
	}
	return response, nil
}

func (s *hostService) CreateHostTap(ctx context.Context, request *rookeryv1.CreateHostTapRequest) (*rookeryv1.HostTap, error) {
	if s.taps == nil {
		return nil, status.Error(codes.Unavailable, "host tap runtime is not configured")
	}
	tap, err := s.taps.CreateNetworkHostTap(ctx, request.GetNetworkId(), request.GetName(), request.GetIpv4Address())
	if err != nil {
		return nil, mapHostTapWriteError(err)
	}
	return hostTapToProto(tap), nil
}

func (s *hostService) DeleteHostTap(ctx context.Context, request *rookeryv1.DeleteHostTapRequest) (*emptypb.Empty, error) {
	if s.taps == nil {
		return nil, status.Error(codes.Unavailable, "host tap runtime is not configured")
	}
	if err := s.taps.DeleteNetworkHostTap(ctx, request.GetId()); err != nil {
		return nil, mapHostTapWriteError(err)
	}
	return &emptypb.Empty{}, nil
}

func hostTapToProto(tap store.NetworkHostTap) *rookeryv1.HostTap {
	return &rookeryv1.HostTap{
		Id:          tap.ID,
		NetworkId:   tap.NetworkID,
		NetworkName: tap.NetworkName,
		Name:        tap.Name,
		SwIfIndex:   tap.SwIfIndex,
		Ipv4Address: tap.IPv4Address,
		CreatedAt:   timestamp(tap.CreatedAt),
		UpdatedAt:   timestamp(tap.UpdatedAt),
	}
}

type hostTapStatusError interface {
	HostTapStatus() (int, string)
}

type hostTapNetworkStatusError interface {
	NetworkStatus() (int, string)
}

func mapHostTapWriteError(err error) error {
	var tapError hostTapStatusError
	if errors.As(err, &tapError) {
		return statusForHostTap(tapError.HostTapStatus())
	}
	var networkError hostTapNetworkStatusError
	if errors.As(err, &networkError) {
		return statusForHostTap(networkError.NetworkStatus())
	}
	return status.Error(codes.Internal, "host tap operation failed")
}

func statusForHostTap(httpStatus int, message string) error {
	if message == "" {
		message = "host tap operation failed"
	}
	switch httpStatus {
	case 400:
		return status.Error(codes.InvalidArgument, message)
	case 404:
		return status.Error(codes.NotFound, message)
	case 409:
		return status.Error(codes.Aborted, message)
	case 503:
		return status.Error(codes.Unavailable, message)
	default:
		return status.Error(codes.Internal, message)
	}
}
