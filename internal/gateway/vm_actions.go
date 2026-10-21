package gateway

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

// VMController is the QEMU runtime subset used by the gateway.
type VMController interface {
	Start(ctx context.Context, vm store.VM) error
	Stop(ctx context.Context, vmID string) error
	Reset(ctx context.Context, vmID string) error
	SendCtrlAltDel(ctx context.Context, vmID string) error
	Running(vmID string) bool
	Metrics(vmID string) (qemu.Metrics, error)
}

// InterfaceCounterSource supplies VPP interface counters for VM metrics.
type InterfaceCounterSource interface {
	InterfaceCounters(ctx context.Context, names []string) (map[string]host.InterfaceCounters, error)
	ResetInterfaceCounters(ctx context.Context) error
}

func (s *vmService) runtimeUnavailable() error {
	if s.runtime == nil {
		return status.Error(codes.Unavailable, "VM runtime is not configured")
	}
	return nil
}

func (s *vmService) StartVM(ctx context.Context, request *rookeryv1.StartVMRequest) (*rookeryv1.VM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.runtimeUnavailable(); err != nil {
		return nil, err
	}
	vm, err := s.store.GetVM(ctx, request.GetId())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, status.Error(codes.NotFound, "vm not found")
	case err != nil:
		return nil, status.Error(codes.Internal, "could not read VM")
	}
	if len(vm.Disks) == 0 && vm.Disk == nil {
		return nil, status.Error(codes.Aborted, "vm has no disk")
	}
	if s.runtime.Running(vm.ID) {
		return nil, status.Error(codes.AlreadyExists, "vm is already running")
	}
	if err := s.runtime.Start(ctx, vm); err != nil {
		_, _ = s.store.SetVMStatus(ctx, vm.ID, store.StatusError)
		return nil, status.Error(codes.Internal, "could not start VM: "+err.Error())
	}
	updated, err := s.store.SetVMStatus(ctx, vm.ID, store.StatusRunning)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not update VM status")
	}
	return vmToProto(updated), nil
}

func (s *vmService) StopVM(ctx context.Context, request *rookeryv1.StopVMRequest) (*rookeryv1.VM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.runtimeUnavailable(); err != nil {
		return nil, err
	}
	if _, err := s.store.GetVM(ctx, request.GetId()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "vm not found")
		}
		return nil, status.Error(codes.Internal, "could not read VM")
	}
	if s.runtime.Running(request.GetId()) {
		if err := s.runtime.Stop(ctx, request.GetId()); err != nil {
			return nil, status.Error(codes.Internal, "could not stop VM: "+err.Error())
		}
	}
	updated, err := s.store.SetVMStatus(ctx, request.GetId(), store.StatusStopped)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not update VM status")
	}
	return vmToProto(updated), nil
}

func (s *vmService) ResetVM(ctx context.Context, request *rookeryv1.ResetVMRequest) (*rookeryv1.VM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.runtimeUnavailable(); err != nil {
		return nil, err
	}
	vm, err := s.store.GetVM(ctx, request.GetId())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, status.Error(codes.NotFound, "vm not found")
	case err != nil:
		return nil, status.Error(codes.Internal, "could not read VM")
	}
	if !s.runtime.Running(vm.ID) {
		return nil, status.Error(codes.Aborted, "vm is not running")
	}
	if err := s.runtime.Reset(ctx, vm.ID); err != nil {
		if !errors.Is(err, qemu.ErrMonitorUnavailable) {
			return nil, status.Error(codes.Internal, "could not reset VM: "+err.Error())
		}
		if stopErr := s.runtime.Stop(ctx, vm.ID); stopErr != nil {
			return nil, status.Error(codes.Internal, "could not stop VM for reset: "+stopErr.Error())
		}
		if startErr := s.runtime.Start(ctx, vm); startErr != nil {
			_, _ = s.store.SetVMStatus(ctx, vm.ID, store.StatusError)
			return nil, status.Error(codes.Internal, "could not restart VM: "+startErr.Error())
		}
		if _, err := s.store.SetVMStatus(ctx, vm.ID, store.StatusRunning); err != nil {
			return nil, status.Error(codes.Internal, "could not update VM status")
		}
	}
	return s.getVM(ctx, vm.ID)
}

func (s *vmService) CtrlAltDel(ctx context.Context, request *rookeryv1.CtrlAltDelRequest) (*rookeryv1.VM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.runtimeUnavailable(); err != nil {
		return nil, err
	}
	if _, err := s.store.GetVM(ctx, request.GetId()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "vm not found")
		}
		return nil, status.Error(codes.Internal, "could not read VM")
	}
	if !s.runtime.Running(request.GetId()) {
		return nil, status.Error(codes.Aborted, "vm is not running")
	}
	if err := s.runtime.SendCtrlAltDel(ctx, request.GetId()); err != nil {
		if errors.Is(err, qemu.ErrMonitorUnavailable) {
			return nil, status.Error(codes.Aborted, "VM was started without a QEMU monitor; use Reset or stop/start the VM once")
		}
		return nil, status.Error(codes.Internal, "could not send Ctrl+Alt+Del: "+err.Error())
	}
	return s.getVM(ctx, request.GetId())
}

func (s *vmService) GetVMMetrics(ctx context.Context, request *rookeryv1.GetVMMetricsRequest) (*rookeryv1.VMMetrics, error) {
	if err := s.runtimeUnavailable(); err != nil {
		return nil, err
	}
	vm, err := s.store.GetVM(ctx, request.GetId())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, status.Error(codes.NotFound, "vm not found")
	case err != nil:
		return nil, status.Error(codes.Internal, "could not read VM")
	}
	metrics, err := s.runtime.Metrics(vm.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read VM metrics: "+err.Error())
	}
	result := &rookeryv1.VMMetrics{
		Running:             metrics.Running,
		Pid:                 int32(metrics.PID),
		UptimeSeconds:       metrics.UptimeSeconds,
		CpuSecondsTotal:     metrics.CPUSecondsTotal,
		MemoryRssBytes:      metrics.RSSBytes,
		MemoryVirtualBytes:  metrics.VirtualBytes,
		DiskReadBytesTotal:  metrics.ReadBytesTotal,
		DiskWriteBytesTotal: metrics.WriteBytesTotal,
		VmId:                vm.ID,
		Name:                vm.Name,
		Vcpus:               int32(vm.VCPUs),
		MemoryMb:            int32(vm.MemoryMB),
		SampledAt:           timestamppb.New(time.Now().UTC()),
	}
	if metrics.Running && s.counters != nil && len(vm.Interfaces) > 0 {
		names := make([]string, 0, len(vm.Interfaces))
		for _, iface := range vm.Interfaces {
			names = append(names, iface.Name)
		}
		if counters, counterErr := s.counters.InterfaceCounters(ctx, names); counterErr == nil {
			for _, counter := range counters {
				result.NetworkRxBytesTotal += counter.RXBytes
				result.NetworkTxBytesTotal += counter.TXBytes
				result.NetworkRxPacketsTotal += counter.RXPackets
				result.NetworkTxPacketsTotal += counter.TXPackets
			}
		}
	}
	return result, nil
}

func (s *vmService) getVM(ctx context.Context, id string) (*rookeryv1.VM, error) {
	vm, err := s.store.GetVM(ctx, id)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read VM")
	}
	return vmToProto(vm), nil
}

func (s *vmService) DeleteVM(ctx context.Context, request *rookeryv1.DeleteVMRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vm, err := s.store.GetVM(ctx, request.GetId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "vm not found")
		}
		return nil, status.Error(codes.Internal, "could not read VM")
	}
	if s.runtime != nil {
		if err := s.runtime.Stop(ctx, vm.ID); err != nil {
			return nil, status.Error(codes.Internal, "could not stop VM before deleting it: "+err.Error())
		}
	}
	if err := s.store.DeleteVM(ctx, vm.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "vm not found")
		}
		return nil, status.Error(codes.Internal, "could not delete VM")
	}
	return &emptypb.Empty{}, nil
}
