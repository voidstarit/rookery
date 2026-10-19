package gateway

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// InterfaceRuntime creates and removes VPP vhost-user interfaces.
type InterfaceRuntime interface {
	CreateVhostUser(ctx context.Context, name, mac string, bdID uint32) (socketPath string, swIfIndex uint32, err error)
	DeleteVhostUser(ctx context.Context, swIfIndex uint32) error
}

func (s *interfaceService) vppReady() error {
	if s.attachments == nil {
		return status.Error(codes.Unavailable, "VPP network runtime is not configured")
	}
	if s.readiness != nil && !s.readiness.Status().VPP.Ready {
		return status.Error(codes.Unavailable, "VPP is not ready")
	}
	return nil
}

func (s *interfaceService) CreateInterface(ctx context.Context, request *rookeryv1.CreateInterfaceRequest) (*rookeryv1.VirtualInterface, error) {
	name := strings.TrimSpace(request.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if request.GetNetworkId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "network_id is required")
	}
	if err := s.vppReady(); err != nil {
		return nil, err
	}
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	s.runMu.Lock()
	defer s.runMu.Unlock()

	network, err := s.store.GetNetwork(ctx, request.GetNetworkId())
	if err != nil {
		if errors.Is(err, store.ErrNetworkNotFound) {
			return nil, status.Error(codes.InvalidArgument, "network not found")
		}
		return nil, status.Error(codes.Internal, "could not read network")
	}
	vmID := request.AttachedVmId
	if vmID != nil && strings.TrimSpace(*vmID) == "" {
		vmID = nil
	}
	if vmID != nil {
		if _, err := s.store.GetVM(ctx, *vmID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, status.Error(codes.InvalidArgument, "vm not found")
			}
			return nil, status.Error(codes.Internal, "could not read VM")
		}
		if s.vms != nil && s.vms.Running(*vmID) {
			return nil, status.Error(codes.Aborted, "stop the VM before attaching a network interface")
		}
	}
	mac, err := gatewayMAC()
	if err != nil {
		return nil, status.Error(codes.Internal, "could not generate MAC address")
	}
	socket, swIfIndex, err := s.attachments.CreateVhostUser(ctx, name, mac, network.BridgeDomainID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not create VPP vhost-user interface: "+err.Error())
	}
	created, err := s.store.CreateInterface(ctx, store.VirtualInterface{
		Name: name, NetworkID: network.ID, SocketPath: socket, MAC: mac,
		SwIfIndex: swIfIndex, AttachedVMID: vmID,
	})
	if err != nil {
		_ = s.attachments.DeleteVhostUser(context.Background(), swIfIndex)
		switch {
		case errors.Is(err, store.ErrInterfaceNameTaken):
			return nil, status.Error(codes.AlreadyExists, "an interface with that name already exists")
		case errors.Is(err, store.ErrNetworkNotFound):
			return nil, status.Error(codes.InvalidArgument, "network not found")
		case errors.Is(err, store.ErrNotFound):
			return nil, status.Error(codes.InvalidArgument, "vm not found")
		default:
			return nil, status.Error(codes.Internal, "could not record interface")
		}
	}
	return interfaceToProto(created), nil
}

func (s *interfaceService) UpdateInterface(ctx context.Context, request *rookeryv1.UpdateInterfaceRequest) (*rookeryv1.VirtualInterface, error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	s.runMu.Lock()
	defer s.runMu.Unlock()

	iface, err := s.store.GetInterface(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrInterfaceNotFound, "interface not found")
	}
	vmID := request.AttachedVmId
	if vmID != nil && strings.TrimSpace(*vmID) == "" {
		vmID = nil
	}
	if iface.AttachedVMID != nil && s.vms != nil && s.vms.Running(*iface.AttachedVMID) {
		return nil, status.Error(codes.Aborted, "stop the current VM before detaching its network interface")
	}
	if vmID != nil && s.vms != nil && s.vms.Running(*vmID) {
		return nil, status.Error(codes.Aborted, "stop the target VM before attaching a network interface")
	}
	updated, err := s.store.SetInterfaceAttachment(ctx, iface.ID, vmID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInterfaceNotFound):
			return nil, status.Error(codes.NotFound, "interface not found")
		case errors.Is(err, store.ErrNotFound):
			return nil, status.Error(codes.InvalidArgument, "vm not found")
		default:
			return nil, status.Error(codes.Internal, "could not update interface")
		}
	}
	return interfaceToProto(updated), nil
}

func (s *interfaceService) DeleteInterface(ctx context.Context, request *rookeryv1.DeleteInterfaceRequest) (*emptypb.Empty, error) {
	if err := s.vppReady(); err != nil {
		return nil, err
	}
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	iface, err := s.store.GetInterface(ctx, request.GetId())
	if err != nil {
		return nil, storeReadError(err, store.ErrInterfaceNotFound, "interface not found")
	}
	if iface.AttachedVMID != nil {
		return nil, status.Error(codes.Aborted, "interface is attached to a VM; detach it first")
	}
	if err := s.attachments.DeleteVhostUser(ctx, iface.SwIfIndex); err != nil {
		return nil, status.Error(codes.Internal, "could not delete VPP vhost-user interface: "+err.Error())
	}
	_ = os.Remove(iface.SocketPath)
	if err := s.store.DeleteInterface(ctx, iface.ID); err != nil {
		switch {
		case errors.Is(err, store.ErrInterfaceNotFound):
			return nil, status.Error(codes.NotFound, "interface not found")
		case errors.Is(err, store.ErrInterfaceInUse):
			return nil, status.Error(codes.Aborted, "interface is attached to a VM; detach it first")
		default:
			return nil, status.Error(codes.Internal, "could not delete interface")
		}
	}
	return &emptypb.Empty{}, nil
}

func gatewayMAC() (string, error) {
	address := make([]byte, 6)
	if _, err := rand.Read(address); err != nil {
		return "", err
	}
	address[0] &= 0xfe
	address[0] |= 0x02
	return net.HardwareAddr(address).String(), nil
}
