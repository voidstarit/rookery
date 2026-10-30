package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/cgroup"
)

// CgroupRuntime applies host-wide cgroup settings and reports availability.
type CgroupRuntime interface {
	Configure(settings cgroup.Settings) error
	Validate(settings cgroup.Settings) error
	Status() cgroup.Status
}

// GetCgroupSettings returns the stored host settings together with live cgroup
// v2 status.
func (s *systemService) GetCgroupSettings(ctx context.Context, _ *rookeryv1.GetCgroupSettingsRequest) (*rookeryv1.CgroupSettings, error) {
	stored, err := s.store.GetCgroupSettings(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not load cgroup settings")
	}
	return cgroupSettingsToProto(stored, s.cgroupStatus()), nil
}

// UpdateCgroupSettings validates, applies, and stores host-wide limits. If the
// host rejects the settings, the previous configuration is restored.
func (s *systemService) UpdateCgroupSettings(ctx context.Context, request *rookeryv1.UpdateCgroupSettingsRequest) (*rookeryv1.CgroupSettings, error) {
	settings := cgroup.NormalizeSettings(cgroup.Settings{
		Enabled:        request.GetEnabled(),
		CPUs:           request.GetCpus(),
		Mems:           request.GetMems(),
		MemoryMaxBytes: request.GetMemoryMaxBytes(),
		CPUWeight:      int(request.GetCpuWeight()),
		IOLimits:       cgroupIOLimitsFromProto(request.GetIoLimits()),
		Classes:        cgroupClassesFromProto(request.GetClasses()),
	})
	if s.cgroup != nil {
		if err := s.cgroup.Validate(settings); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	} else if err := cgroup.ValidateSettings(settings); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	previous, previousErr := s.store.GetCgroupSettings(ctx)
	if previousErr == nil && settings.Enabled {
		if err := s.validateClassAssignments(ctx, previous.Settings, settings); err != nil {
			return nil, err
		}
	}
	saved, err := s.store.SetCgroupSettings(ctx, settings)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not save cgroup settings")
	}
	if s.cgroup != nil {
		if err := s.cgroup.Configure(saved.Settings); err != nil {
			rollback := cgroup.Settings{}
			if previousErr == nil {
				rollback = previous.Settings
			}
			_ = s.cgroup.Configure(rollback)
			_, _ = s.store.SetCgroupSettings(ctx, rollback)
			if rollback.Enabled {
				_ = s.applyCgroupsToRunningVMs(context.Background())
			}
			return nil, status.Error(codes.FailedPrecondition, "could not apply cgroup settings: "+err.Error())
		}
	}
	if saved.Enabled {
		if problems := s.applyCgroupsToRunningVMs(ctx); len(problems) > 0 {
			return nil, status.Error(codes.FailedPrecondition,
				"cgroup settings saved, but running VMs could not be updated: "+strings.Join(problems, "; "))
		}
	}
	return cgroupSettingsToProto(saved, s.cgroupStatus()), nil
}

// applyCgroupsToRunningVMs re-places every live QEMU process according to the
// current host settings. It returns human-readable failures for VMs that could
// not be updated.
func (s *systemService) applyCgroupsToRunningVMs(ctx context.Context) []string {
	if s.vms == nil || s.cgroupUpdater == nil {
		return nil
	}
	vms, err := s.store.ListVMs(ctx)
	if err != nil {
		return []string{"could not list VMs: " + err.Error()}
	}
	problems := make([]string, 0)
	for _, vm := range vms {
		if !s.vms.Running(vm.ID) {
			continue
		}
		if err := s.cgroupUpdater.ApplyCgroup(ctx, vm); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", vm.Name, err))
		}
	}
	return problems
}

// validateClassAssignments rejects removing a class while any VM still refers
// to it, and rejects enabling settings while a VM points at an undefined class.
func (s *systemService) validateClassAssignments(ctx context.Context, before, after cgroup.Settings) error {
	kept := make(map[string]struct{}, len(after.Classes))
	for _, class := range after.Classes {
		kept[strings.TrimSpace(class.Name)] = struct{}{}
	}
	removed := make(map[string]struct{})
	for _, class := range before.Classes {
		name := strings.TrimSpace(class.Name)
		if _, ok := kept[name]; !ok {
			removed[name] = struct{}{}
		}
	}
	vms, err := s.store.ListVMs(ctx)
	if err != nil {
		return status.Error(codes.Internal, "could not list VMs")
	}
	for _, vm := range vms {
		className := strings.TrimSpace(vm.CgroupClass)
		if className == "" {
			continue
		}
		if _, ok := removed[className]; ok {
			return status.Errorf(codes.InvalidArgument,
				"cgroup class %q is assigned to VM %q; reassign the VM before removing the class",
				className, vm.Name)
		}
		if _, ok := kept[className]; !ok {
			return status.Errorf(codes.InvalidArgument,
				"cgroup class %q assigned to VM %q is not defined",
				className, vm.Name)
		}
		prefix := className + "/"
		for _, class := range after.Classes {
			if strings.HasPrefix(strings.TrimSpace(class.Name), prefix) {
				return status.Errorf(codes.InvalidArgument,
					"cgroup class %q assigned to VM %q has child classes; assign the VM to a leaf class",
					className, vm.Name)
			}
		}
	}
	return nil
}

func (s *systemService) cgroupStatus() cgroup.Status {
	if s.cgroup != nil {
		return s.cgroup.Status()
	}
	current := cgroup.Status{
		Reason:    "cgroup manager is not configured",
		CheckedAt: time.Now().UTC(),
	}
	if cpus, err := cgroup.OnlineCPUs(); err == nil {
		current.OnlineCPUs = cpus
	}
	current.CPUModel = cgroup.CPUModel()
	return current
}

func cgroupSettingsToProto(stored cgroup.CgroupSettings, runtime cgroup.Status) *rookeryv1.CgroupSettings {
	result := &rookeryv1.CgroupSettings{
		Enabled:        stored.Enabled,
		Cpus:           stored.CPUs,
		Mems:           stored.Mems,
		MemoryMaxBytes: stored.MemoryMaxBytes,
		CpuWeight:      int32(stored.CPUWeight),
		IoLimits:       cgroupIOLimitsToProto(stored.IOLimits),
		Classes:        cgroupClassesToProto(stored.Classes),
		Available:      runtime.Available,
		Configured:     runtime.Configured,
		Reason:         runtime.Reason,
		Mount:          runtime.Mount,
		Root:           runtime.Root,
		CpuModel:       runtime.CPUModel,
		UpdatedAt:      timestamp(stored.UpdatedAt),
	}
	if runtime.LastError != "" {
		result.Reason = runtime.LastError
	}
	for _, cpu := range runtime.OnlineCPUs {
		result.OnlineCpus = append(result.OnlineCpus, int32(cpu))
	}
	return result
}

func cgroupClassesFromProto(classes []*rookeryv1.CgroupClass) []cgroup.Class {
	if len(classes) == 0 {
		return nil
	}
	result := make([]cgroup.Class, 0, len(classes))
	for _, class := range classes {
		if class == nil {
			continue
		}
		result = append(result, cgroup.Class{
			Name:           strings.TrimSpace(class.GetName()),
			CPUs:           strings.TrimSpace(class.GetCpus()),
			MemoryMaxBytes: class.GetMemoryMaxBytes(),
			CPUWeight:      int(class.GetCpuWeight()),
			IOLimits:       cgroupIOLimitsFromProto(class.GetIoLimits()),
		})
	}
	return result
}

func cgroupClassesToProto(classes []cgroup.Class) []*rookeryv1.CgroupClass {
	if len(classes) == 0 {
		return nil
	}
	result := make([]*rookeryv1.CgroupClass, 0, len(classes))
	for _, class := range classes {
		result = append(result, &rookeryv1.CgroupClass{
			Name:           class.Name,
			Cpus:           class.CPUs,
			MemoryMaxBytes: class.MemoryMaxBytes,
			CpuWeight:      int32(class.CPUWeight),
			IoLimits:       cgroupIOLimitsToProto(class.IOLimits),
		})
	}
	return result
}

func cgroupIOLimitsFromProto(limits []*rookeryv1.CgroupIOLimit) []cgroup.IOLimit {
	if len(limits) == 0 {
		return nil
	}
	result := make([]cgroup.IOLimit, 0, len(limits))
	for _, limit := range limits {
		if limit == nil {
			continue
		}
		result = append(result, cgroup.IOLimit{
			Device:    strings.TrimSpace(limit.GetDevice()),
			ReadBPS:   limit.GetReadBps(),
			WriteBPS:  limit.GetWriteBps(),
			ReadIOPS:  limit.GetReadIops(),
			WriteIOPS: limit.GetWriteIops(),
			Weight:    int(limit.GetWeight()),
		})
	}
	return result
}

func cgroupIOLimitsToProto(limits []cgroup.IOLimit) []*rookeryv1.CgroupIOLimit {
	if len(limits) == 0 {
		return nil
	}
	result := make([]*rookeryv1.CgroupIOLimit, 0, len(limits))
	for _, limit := range limits {
		result = append(result, &rookeryv1.CgroupIOLimit{
			Device:    limit.Device,
			ReadBps:   limit.ReadBPS,
			WriteBps:  limit.WriteBPS,
			ReadIops:  limit.ReadIOPS,
			WriteIops: limit.WriteIOPS,
			Weight:    int32(limit.Weight),
		})
	}
	return result
}
