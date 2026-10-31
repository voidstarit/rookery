package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/voidstarit/rookery/internal/readiness"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

const (
	gatewayDefaultVCPUs       = 1
	gatewayDefaultMemoryMB    = 512
	gatewayMaxDescriptionSize = 2000
	gatewayMaxVCPUs           = 128
	gatewayMaxMemoryMB        = 1_048_576
	gatewayMaxVMDisks         = 32
	gatewayMaxVMInterfaces    = 32
)

func (s *vmService) CreateVM(ctx context.Context, request *rookeryv1.CreateVMRequest) (*rookeryv1.VM, error) {
	if s.readiness != nil {
		current := s.readiness.Status()
		if !current.Ready {
			return nil, status.Error(codes.Unavailable, vmCreationBlockedMessage(current))
		}
	}
	vm, err := vmFromCreateRequest(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.validateCgroupClass(ctx, vm.CgroupClass); err != nil {
		return nil, err
	}
	interfaceIDs, err := normalizeGatewayIDs(request.GetInterfaceIds(), gatewayMaxVMInterfaces, "interface_ids")
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	for _, interfaceID := range interfaceIDs {
		iface, err := s.store.GetInterface(ctx, interfaceID)
		if err != nil {
			if errors.Is(err, store.ErrInterfaceNotFound) {
				return nil, status.Errorf(codes.InvalidArgument, "interface %d not found", interfaceID)
			}
			return nil, status.Error(codes.Internal, "could not read interface")
		}
		if iface.AttachedVMID != nil {
			return nil, status.Errorf(codes.Aborted, "interface %q is already attached to a VM", iface.Name)
		}
	}
	created, err := s.store.CreateVM(ctx, vm)
	if err != nil {
		return nil, vmStoreError(err, "create")
	}
	for _, interfaceID := range interfaceIDs {
		if _, err := s.store.SetInterfaceAttachment(ctx, interfaceID, &created.ID); err != nil {
			_ = s.store.DeleteVM(context.Background(), created.ID)
			switch {
			case errors.Is(err, store.ErrInterfaceNotFound):
				return nil, status.Errorf(codes.InvalidArgument, "interface %d not found", interfaceID)
			case errors.Is(err, store.ErrInterfaceInUse):
				return nil, status.Error(codes.FailedPrecondition, "interface is already attached to a VM")
			default:
				return nil, status.Error(codes.Internal, "could not attach interface")
			}
		}
	}
	if len(interfaceIDs) > 0 {
		created, err = s.store.GetVM(ctx, created.ID)
		if err != nil {
			_ = s.store.DeleteVM(context.Background(), created.ID)
			return nil, status.Error(codes.Internal, "could not reload VM")
		}
	}
	created, err = s.startCreatedVM(ctx, created)
	if err != nil {
		return nil, err
	}
	return vmToProto(created), nil
}

// startCreatedVM starts a freshly created VM. Rookery creates VMs running;
// stopping one is always an explicit action. A VM that cannot start is removed
// again, along with any partial QEMU process, so callers never see a
// half-created VM and the disks are released for a retry. Gateways built
// without a VM controller keep the record as created (stopped).
func (s *vmService) startCreatedVM(ctx context.Context, vm store.VM) (store.VM, error) {
	if s.runtime == nil {
		return vm, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.runtime.Start(ctx, vm); err != nil {
		s.rollbackCreatedVM(vm.ID)
		return store.VM{}, status.Error(codes.Internal, "could not start VM: "+err.Error())
	}
	started, err := s.store.SetVMStatus(ctx, vm.ID, store.StatusRunning)
	if err != nil {
		s.rollbackCreatedVM(vm.ID)
		return store.VM{}, status.Error(codes.Internal, "could not update VM status")
	}
	started, err = s.store.SetVMDesiredState(ctx, vm.ID, store.DesiredRunning)
	if err != nil {
		s.rollbackCreatedVM(vm.ID)
		return store.VM{}, status.Error(codes.Internal, "could not update VM desired state")
	}
	return started, nil
}

// rollbackCreatedVM stops a partially started QEMU process and removes the VM
// record after a failed create. Cleanup uses a fresh context so a cancelled
// request cannot leave the record behind.
func (s *vmService) rollbackCreatedVM(vmID string) {
	cleanupCtx := context.Background()
	if err := s.runtime.Stop(cleanupCtx, vmID); err != nil {
		s.logger.Warn().Err(err).Str("vm_id", vmID).Msg("could not stop VM after failed create")
	}
	if err := s.store.DeleteVM(cleanupCtx, vmID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.logger.Warn().Err(err).Str("vm_id", vmID).Msg("could not delete VM after failed create")
	}
}

func (s *vmService) UpdateVM(ctx context.Context, request *rookeryv1.UpdateVMRequest) (*rookeryv1.VM, error) {
	existing, err := s.store.GetVM(ctx, request.GetId())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "vm not found")
		}
		return nil, status.Error(codes.Internal, "could not read VM")
	}
	vm, err := vmFromUpdateRequest(existing, request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.validateCgroupClass(ctx, vm.CgroupClass); err != nil {
		return nil, err
	}
	// Empty repeated fields mean "leave the current set unchanged" unless the
	// caller explicitly requests replacement.
	changeInterfaces := request.GetReplaceInterfaceIds() || len(request.GetInterfaceIds()) > 0
	changeDisks := request.GetReplaceDiskIds() || len(request.GetDiskIds()) > 0
	var interfaceIDs, diskIDs []int64
	if changeInterfaces {
		interfaceIDs, err = normalizeGatewayIDs(request.GetInterfaceIds(), gatewayMaxVMInterfaces, "interface_ids")
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	if changeDisks {
		diskIDs, err = normalizeGatewayIDs(request.GetDiskIds(), gatewayMaxVMDisks, "disk_ids")
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		if len(diskIDs) == 0 {
			return nil, status.Error(codes.InvalidArgument, "at least one disk is required")
		}
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if changeInterfaces && s.runtime != nil && s.runtime.Running(existing.ID) {
		return nil, status.Error(codes.Aborted, "stop the VM before changing its network interfaces")
	}
	if changeDisks && s.runtime != nil && s.runtime.Running(existing.ID) {
		return nil, status.Error(codes.Aborted, "stop the VM before changing its disks")
	}
	for _, interfaceID := range interfaceIDs {
		iface, err := s.store.GetInterface(ctx, interfaceID)
		if err != nil {
			if errors.Is(err, store.ErrInterfaceNotFound) {
				return nil, status.Errorf(codes.InvalidArgument, "interface %d not found", interfaceID)
			}
			return nil, status.Error(codes.Internal, "could not read interface")
		}
		if iface.AttachedVMID != nil && *iface.AttachedVMID != existing.ID {
			return nil, status.Errorf(codes.Aborted, "interface %q is already attached to another VM", iface.Name)
		}
	}
	for _, diskID := range diskIDs {
		disk, err := s.store.GetDisk(ctx, diskID)
		if err != nil {
			if errors.Is(err, store.ErrDiskNotFound) {
				return nil, status.Errorf(codes.InvalidArgument, "disk %d not found", diskID)
			}
			return nil, status.Error(codes.Internal, "could not read disk")
		}
		if disk.AttachedVMID != nil && *disk.AttachedVMID != existing.ID {
			return nil, status.Errorf(codes.Aborted, "disk %q is already attached to another VM", disk.Name)
		}
	}
	var cgroupUpdater CgroupUpdater
	applyCgroup := request.CgroupClass != nil && vm.CgroupClass != existing.CgroupClass &&
		s.runtime != nil && s.runtime.Running(existing.ID)
	if applyCgroup {
		cgroupUpdater, _ = s.runtime.(CgroupUpdater)
		applyCgroup = cgroupUpdater != nil
	}
	if applyCgroup {
		if err := cgroupUpdater.ApplyCgroup(ctx, vm); err != nil {
			return nil, status.Error(codes.FailedPrecondition, "could not apply VM cgroup limits: "+err.Error())
		}
	}
	updated, err := s.store.UpdateVM(ctx, vm)
	if err != nil {
		if applyCgroup {
			if revertErr := cgroupUpdater.ApplyCgroup(context.Background(), existing); revertErr != nil {
				s.logger.Warn().Err(revertErr).Str("vm_id", existing.ID).Msg("could not restore VM cgroup after update failure")
			}
		}
		return nil, vmStoreError(err, "update")
	}
	if changeInterfaces {
		updated, err = s.store.SetVMInterfaces(ctx, existing.ID, interfaceIDs)
		if err != nil {
			return nil, vmStoreError(err, "update interfaces")
		}
	}
	if changeDisks {
		updated, err = s.store.SetVMDisks(ctx, existing.ID, diskIDs)
		if err != nil {
			return nil, vmStoreError(err, "update disks")
		}
	}
	return vmToProto(updated), nil
}

func vmFromCreateRequest(request *rookeryv1.CreateVMRequest) (store.VM, error) {
	vm := store.VM{Status: store.StatusStopped, VCPUs: gatewayDefaultVCPUs, MemoryMB: gatewayDefaultMemoryMB}
	if err := applyVMName(&vm, request.Name, true); err != nil {
		return store.VM{}, err
	}
	if err := applyVMDescription(&vm, request.Description); err != nil {
		return store.VM{}, err
	}
	if request.Status != nil {
		if err := applyVMStatus(&vm, *request.Status); err != nil {
			return store.VM{}, err
		}
	}
	if request.Vcpus != nil {
		if err := applyVMVCPUs(&vm, int(*request.Vcpus)); err != nil {
			return store.VM{}, err
		}
	}
	if request.MemoryMb != nil {
		if err := applyVMMemory(&vm, int(*request.MemoryMb)); err != nil {
			return store.VM{}, err
		}
	}
	vm.BootMediaID = request.BootMediaId
	if request.ConsoleEnabled != nil {
		vm.ConsoleEnabled = request.GetConsoleEnabled()
	}
	vm.CgroupClass = strings.TrimSpace(request.GetCgroupClass())
	diskIDs := request.GetDiskIds()
	if len(diskIDs) == 0 && request.DiskId != nil {
		diskIDs = []int64{request.GetDiskId()}
	}
	normalized, err := normalizeGatewayIDs(diskIDs, gatewayMaxVMDisks, "disk_ids")
	if err != nil {
		return store.VM{}, err
	}
	if len(normalized) == 0 {
		return store.VM{}, errors.New("at least one disk is required")
	}
	vm.DiskIDs = normalized
	vm.DiskID = &normalized[0]
	return vm, nil
}

func vmFromUpdateRequest(existing store.VM, request *rookeryv1.UpdateVMRequest) (store.VM, error) {
	vm := existing
	if request.Name != nil {
		if err := applyVMName(&vm, *request.Name, false); err != nil {
			return store.VM{}, err
		}
	}
	if request.Description != nil {
		if err := applyVMDescription(&vm, *request.Description); err != nil {
			return store.VM{}, err
		}
	}
	if request.Status != nil {
		if err := applyVMStatus(&vm, *request.Status); err != nil {
			return store.VM{}, err
		}
	}
	if request.Vcpus != nil {
		if err := applyVMVCPUs(&vm, int(*request.Vcpus)); err != nil {
			return store.VM{}, err
		}
	}
	if request.MemoryMb != nil {
		if err := applyVMMemory(&vm, int(*request.MemoryMb)); err != nil {
			return store.VM{}, err
		}
	}
	vm.BootMediaID = request.BootMediaId
	if request.ConsoleEnabled != nil {
		vm.ConsoleEnabled = request.GetConsoleEnabled()
	}
	if request.CgroupClass != nil {
		vm.CgroupClass = strings.TrimSpace(request.GetCgroupClass())
	}
	return vm, nil
}

func (s *vmService) validateCgroupClass(ctx context.Context, className string) error {
	className = strings.TrimSpace(className)
	if className == "" {
		return nil
	}
	settings, err := s.store.GetCgroupSettings(ctx)
	if err != nil {
		return status.Error(codes.Internal, "could not read cgroup settings")
	}
	found := false
	for _, class := range settings.Classes {
		if strings.TrimSpace(class.Name) == className {
			found = true
			break
		}
	}
	if !found {
		return status.Errorf(codes.InvalidArgument, "cgroup class %q not found", className)
	}
	prefix := className + "/"
	for _, class := range settings.Classes {
		if strings.HasPrefix(strings.TrimSpace(class.Name), prefix) {
			return status.Errorf(codes.InvalidArgument,
				"cgroup class %q has child classes; assign the VM to a leaf class", className)
		}
	}
	return nil
}

func applyVMName(vm *store.VM, value string, required bool) error {
	name := strings.TrimSpace(value)
	switch {
	case name == "" && required:
		return errors.New("name is required")
	case name == "":
		return nil
	case len(name) > 64:
		return errors.New("name must be at most 64 characters")
	}
	vm.Name = name
	return nil
}

func applyVMDescription(vm *store.VM, value string) error {
	description := strings.TrimSpace(value)
	if utf8.RuneCountInString(description) > gatewayMaxDescriptionSize {
		return fmt.Errorf("description must be at most %d characters", gatewayMaxDescriptionSize)
	}
	vm.Description = description
	return nil
}

func applyVMStatus(vm *store.VM, value string) error {
	value = strings.TrimSpace(value)
	if !store.ValidStatus(value) {
		return fmt.Errorf("status must be one of: %s", strings.Join(store.Statuses(), ", "))
	}
	vm.Status = value
	return nil
}

func applyVMVCPUs(vm *store.VM, value int) error {
	if value < 1 || value > gatewayMaxVCPUs {
		return fmt.Errorf("vcpus must be between 1 and %d", gatewayMaxVCPUs)
	}
	vm.VCPUs = value
	return nil
}

func applyVMMemory(vm *store.VM, value int) error {
	if value < 128 || value > gatewayMaxMemoryMB {
		return fmt.Errorf("memory_mb must be between 128 and %d", gatewayMaxMemoryMB)
	}
	vm.MemoryMB = value
	return nil
}

func normalizeGatewayIDs(ids []int64, max int, field string) ([]int64, error) {
	if len(ids) > max {
		return nil, fmt.Errorf("at most %d values are allowed in %s", max, field)
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id < 1 {
			return nil, fmt.Errorf("%s must contain positive integers", field)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func vmStoreError(err error, operation string) error {
	switch {
	case errors.Is(err, store.ErrNameTaken):
		return status.Error(codes.AlreadyExists, "a VM with that name already exists")
	case errors.Is(err, store.ErrDiskNotFound):
		return status.Error(codes.InvalidArgument, "disk not found")
	case errors.Is(err, store.ErrDiskInUse):
		return status.Error(codes.Aborted, "disk is already attached to another VM")
	case errors.Is(err, store.ErrDiskIsTemplate):
		return status.Error(codes.Aborted, "template disks cannot be attached to a VM")
	case errors.Is(err, store.ErrMediaNotFound):
		return status.Error(codes.InvalidArgument, "boot media not found")
	case errors.Is(err, store.ErrInterfaceNotFound):
		return status.Error(codes.InvalidArgument, "interface not found")
	case errors.Is(err, store.ErrInterfaceInUse):
		return status.Error(codes.Aborted, "interface is already attached to another VM")
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, "vm not found")
	default:
		return status.Error(codes.Internal, "could not "+operation+" VM")
	}
}

func vmCreationBlockedMessage(status readiness.Status) string {
	var problems []string
	if !status.ThinPool.Ready {
		problems = append(problems, "thin pool: "+componentReason(status.ThinPool.ComponentStatus))
	}
	if !status.VPP.Ready {
		problems = append(problems, "FD.io VPP: "+componentReason(status.VPP.ComponentStatus))
	}
	if len(problems) == 0 {
		return "VM creation is blocked until prerequisites are ready"
	}
	return "VM creation is blocked: " + strings.Join(problems, "; ")
}

func componentReason(status readiness.ComponentStatus) string {
	if status.Detail != "" {
		return status.Detail
	}
	if status.State != "" {
		return status.State
	}
	return "not ready"
}
