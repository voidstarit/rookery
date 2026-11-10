package gateway

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/kouja"
	"github.com/voidstarit/rookery/internal/lightvm"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

const (
	lightVMDefaultVCPUs    = 1
	lightVMDefaultMemoryMB = 512
)

type lightVMService struct {
	rookeryv1.UnimplementedLightVMServiceServer
	store            *store.Store
	runtime          LightVMRuntime
	mu               *sync.Mutex
	resourceMu       *sync.Mutex
	planner          LightVMDiskPlanner
	startCoordinator *lightvm.StartCoordinator
}

// LightVMDiskPlanner prepares remote disk attachments for LightVM starts.
type LightVMDiskPlanner interface {
	PlanDisks(ctx context.Context, disks []store.VMDisk) (qemu.StartPlan, error)
}

// LightVMRuntime owns LightVM QEMU, virtiofsd, and ephemeral containers.
type LightVMRuntime interface {
	PrepareLightVM(ctx context.Context, vm store.LightVM) (lightvm.PreparedLightVM, error)
	StartPreparedLightVM(ctx context.Context, prepared *lightvm.PreparedLightVM, vm store.LightVM, plan qemu.StartPlan) error
	CleanupPreparedLightVM(ctx context.Context, prepared lightvm.PreparedLightVM) error
	StopQEMULightVM(ctx context.Context, id string) error
	CleanupStoppedLightVM(ctx context.Context, id string) error
	RunState(ctx context.Context, id string) (string, error)
	RunningLightVM(id string) bool
	LightVMConsoleSocket(id string, consoleID int64) string
}

func (s *lightVMService) runtimeUnavailable() error {
	if s.runtime == nil {
		return status.Error(codes.Unavailable, "LightVM runtime is not configured")
	}
	return nil
}

// ListLightVMs returns every LightVM definition ordered by name.
func (s *lightVMService) ListLightVMs(ctx context.Context, _ *rookeryv1.ListLightVMsRequest) (*rookeryv1.ListLightVMsResponse, error) {
	vms, err := s.store.ListLightVMs(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not list lightvms")
	}
	response := &rookeryv1.ListLightVMsResponse{Lightvms: make([]*rookeryv1.LightVM, 0, len(vms))}
	for _, vm := range vms {
		response.Lightvms = append(response.Lightvms, s.toProto(ctx, vm))
	}
	return response, nil
}

// GetLightVM returns one LightVM definition.
func (s *lightVMService) GetLightVM(ctx context.Context, request *rookeryv1.GetLightVMRequest) (*rookeryv1.LightVM, error) {
	vm, err := s.store.GetLightVM(ctx, request.GetId())
	if err != nil {
		return nil, lightVMError(err, "read")
	}
	return s.toProto(ctx, vm), nil
}

// CreateLightVM inserts a LightVM definition. This is inventory-only: no
// runtime process is started and no container image is pulled.
func (s *lightVMService) CreateLightVM(ctx context.Context, request *rookeryv1.CreateLightVMRequest) (*rookeryv1.LightVM, error) {
	vm, err := lightVMFromCreateRequest(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := validateLightVMReferences(ctx, s.store, vm); err != nil {
		return nil, err
	}
	if err := validateCgroupClassFor(ctx, s.store, vm.CgroupClass, "LightVM"); err != nil {
		return nil, err
	}
	if s.resourceMu != nil {
		s.resourceMu.Lock()
		defer s.resourceMu.Unlock()
	}
	created, err := s.store.CreateLightVM(ctx, vm)
	if err != nil {
		return nil, lightVMError(err, "create")
	}
	return s.toProto(ctx, created), nil
}

// UpdateLightVM replaces the editable fields and the full interface set.
func (s *lightVMService) UpdateLightVM(ctx context.Context, request *rookeryv1.UpdateLightVMRequest) (*rookeryv1.LightVM, error) {
	if s.startCoordinator != nil {
		release := s.startCoordinator.Reserve(request.GetId())
		defer release()
		s.startCoordinator.Cancel(request.GetId())
	}
	if s.resourceMu != nil {
		s.resourceMu.Lock()
		defer s.resourceMu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.store.GetLightVM(ctx, request.GetId())
	if err != nil {
		return nil, lightVMError(err, "read")
	}
	if s.runtime == nil {
		if existing.Status != store.StatusStopped || existing.DesiredState != store.DesiredStopped {
			return nil, status.Error(codes.Unavailable, "LightVM runtime is unavailable; cannot verify the LightVM is stopped")
		}
	} else if s.runtime.RunningLightVM(existing.ID) {
		return nil, status.Error(codes.Aborted, "stop the LightVM before editing it")
	}
	vm, err := lightVMFromUpdateRequest(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := validateLightVMReferences(ctx, s.store, vm); err != nil {
		return nil, err
	}
	if err := validateCgroupClassFor(ctx, s.store, vm.CgroupClass, "LightVM"); err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateLightVM(ctx, vm)
	if err != nil {
		return nil, lightVMError(err, "update")
	}
	return s.toProto(ctx, updated), nil
}

// DeleteLightVM stops a running LightVM and removes its definition.
func (s *lightVMService) DeleteLightVM(ctx context.Context, request *rookeryv1.DeleteLightVMRequest) (*emptypb.Empty, error) {
	if s.startCoordinator != nil {
		release := s.startCoordinator.Reserve(request.GetId())
		defer release()
		s.startCoordinator.Cancel(request.GetId())
	}
	lifecycleCtx := context.WithoutCancel(ctx)
	s.mu.Lock()
	existing, err := s.store.GetLightVM(ctx, request.GetId())
	if err != nil {
		s.mu.Unlock()
		return nil, lightVMError(err, "read")
	}
	if s.runtime == nil {
		if existing.Status != store.StatusStopped || existing.DesiredState != store.DesiredStopped {
			s.mu.Unlock()
			return nil, status.Error(codes.Unavailable, "LightVM runtime is unavailable; cannot verify the LightVM is stopped")
		}
	} else {
		if _, err := s.store.SetLightVMDesiredState(lifecycleCtx, existing.ID, store.DesiredStopped); err != nil {
			s.mu.Unlock()
			return nil, status.Error(codes.Internal, "could not update LightVM desired state")
		}
		if err := s.runtime.StopQEMULightVM(lifecycleCtx, existing.ID); err != nil || s.runtime.RunningLightVM(existing.ID) {
			s.mu.Unlock()
			if err != nil {
				return nil, status.Error(codes.Internal, "could not stop LightVM: "+err.Error())
			}
			return nil, status.Error(codes.Internal, "LightVM is still running")
		}
		if _, err := s.store.SetLightVMStatus(lifecycleCtx, existing.ID, store.StatusStopped); err != nil {
			s.mu.Unlock()
			return nil, status.Error(codes.Internal, "could not update LightVM status")
		}
	}
	s.mu.Unlock()

	if s.runtime == nil {
		log.Warn().Str("lightvm_id", existing.ID).Msg("LightVM runtime unavailable; skipping container cleanup")
	} else {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err = s.runtime.CleanupStoppedLightVM(cleanupCtx, existing.ID)
		cancel()
		if err != nil {
			return nil, status.Error(codes.Internal, "could not clean up LightVM: "+err.Error())
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.DeleteLightVM(ctx, existing.ID); err != nil {
		return nil, lightVMError(err, "delete")
	}
	return &emptypb.Empty{}, nil
}

// StartLightVM launches the definition's QEMU process.
func (s *lightVMService) StartLightVM(ctx context.Context, request *rookeryv1.StartLightVMRequest) (*rookeryv1.LightVM, error) {
	if err := s.runtimeUnavailable(); err != nil {
		return nil, err
	}
	vm, err := s.store.GetLightVM(ctx, request.GetId())
	if err != nil {
		return nil, lightVMError(err, "read")
	}
	if len(vm.Disks)+len(vm.Interfaces)+len(vm.VirtioFSShares) > store.MaxLightVMDevices {
		return nil, status.Errorf(codes.FailedPrecondition,
			"LightVM has %d disks, %d interfaces, and %d virtiofs shares (limit %d); reduce the device count",
			len(vm.Disks), len(vm.Interfaces), len(vm.VirtioFSShares), store.MaxLightVMDevices)
	}
	requestGeneration := vm.LifecycleGeneration
	loadedDesired := vm.DesiredState
	var startCtx context.Context
	done := func() {}
	if s.startCoordinator != nil {
		startCtx, done, err = s.startCoordinator.Begin(ctx, vm.ID)
		if err != nil {
			return nil, status.Error(codes.Aborted, "LightVM is already starting")
		}
	} else {
		startCtx = ctx
	}
	defer done()

	restorePrevious := func(current store.LightVM) {
		if current.DesiredState != store.DesiredRunning {
			return
		}
		if loadedDesired == store.DesiredStopped {
			_, _ = s.store.SetLightVMDesiredState(context.WithoutCancel(ctx), current.ID, store.DesiredStopped)
		}
	}

	s.mu.Lock()
	current, err := s.store.GetLightVM(ctx, vm.ID)
	if err != nil {
		s.mu.Unlock()
		return nil, lightVMError(err, "read")
	}
	if s.runtime.RunningLightVM(current.ID) {
		if current.DesiredState == store.DesiredStopped {
			s.mu.Unlock()
			return nil, status.Error(codes.Aborted, "a stop won the race with this start")
		}
		runState, stateErr := s.runtime.RunState(context.WithoutCancel(ctx), current.ID)
		if stateErr != nil {
			s.mu.Unlock()
			return nil, status.Errorf(codes.FailedPrecondition, "cannot read LightVM run state; stop and start to recover: %v", stateErr)
		}
		if strings.HasPrefix(runState, "paused") {
			s.mu.Unlock()
			return nil, status.Error(codes.FailedPrecondition, "LightVM is paused by an I/O error; stop and start to recover")
		}
		_, _ = s.store.SetLightVMStatus(context.WithoutCancel(ctx), current.ID, store.StatusRunning)
		_, _ = s.store.SetLightVMDesiredState(context.WithoutCancel(ctx), current.ID, store.DesiredRunning)
		s.mu.Unlock()
		return nil, status.Error(codes.AlreadyExists, "LightVM is already running")
	}
	if current.DesiredState != loadedDesired || current.LifecycleGeneration != requestGeneration {
		s.mu.Unlock()
		return nil, status.Error(codes.Aborted, "LightVM changed while the start was queued")
	}
	intent, err := s.store.SetLightVMDesiredState(context.WithoutCancel(ctx), current.ID, store.DesiredRunning)
	if err != nil {
		s.mu.Unlock()
		return nil, status.Error(codes.Internal, "could not update LightVM desired state")
	}
	startGeneration := intent.LifecycleGeneration
	current.LifecycleGeneration = startGeneration
	s.mu.Unlock()

	prepared, err := s.runtime.PrepareLightVM(startCtx, current)
	if err == nil {
		var plan qemu.StartPlan
		if s.planner != nil {
			plan, err = s.planner.PlanDisks(startCtx, current.Disks)
		}
		if err == nil {
			s.mu.Lock()
			current, readErr := s.store.GetLightVM(ctx, current.ID)
			if readErr != nil {
				s.mu.Unlock()
				err = readErr
			} else if current.LifecycleGeneration != startGeneration || current.DesiredState != store.DesiredRunning {
				restorePrevious(current)
				s.mu.Unlock()
				_ = s.runtime.CleanupPreparedLightVM(context.Background(), prepared)
				return nil, status.Error(codes.Aborted, "LightVM changed while starting")
			} else if startCtx.Err() != nil {
				restorePrevious(current)
				s.mu.Unlock()
				_ = s.runtime.CleanupPreparedLightVM(context.Background(), prepared)
				return nil, status.Error(codes.Aborted, "start was canceled")
			} else {
				err = s.runtime.StartPreparedLightVM(startCtx, &prepared, current, plan)
				if err == nil {
					_, _ = s.store.SetLightVMStatus(context.WithoutCancel(ctx), current.ID, store.StatusRunning)
					updated, updateErr := s.store.SetLightVMDesiredState(context.WithoutCancel(ctx), current.ID, store.DesiredRunning)
					s.mu.Unlock()
					if updateErr != nil {
						return nil, status.Error(codes.Internal, "could not update LightVM state")
					}
					return s.toProto(ctx, updated), nil
				}
				if errors.Is(err, qemu.ErrAlreadyRunning) && s.runtime.RunningLightVM(current.ID) {
					_, _ = s.store.SetLightVMStatus(context.WithoutCancel(ctx), current.ID, store.StatusRunning)
					_, _ = s.store.SetLightVMDesiredState(context.WithoutCancel(ctx), current.ID, store.DesiredRunning)
					s.mu.Unlock()
					_ = s.runtime.CleanupPreparedLightVM(context.Background(), prepared)
					return nil, status.Error(codes.AlreadyExists, "LightVM is already running")
				}
				if prepared.QEMUStarted {
					stopErr := s.runtime.StopQEMULightVM(context.WithoutCancel(ctx), current.ID)
					if stopErr != nil || s.runtime.RunningLightVM(current.ID) {
						s.mu.Unlock()
						return nil, lightVMStartStatusError(err)
					}
				}
				s.mu.Unlock()
				_ = s.runtime.CleanupPreparedLightVM(context.Background(), prepared)
				restorePrevious(current)
				_, _ = s.store.SetLightVMStatus(context.WithoutCancel(ctx), current.ID, store.StatusError)
			}
		}
	}
	if err != nil {
		_ = s.runtime.CleanupPreparedLightVM(context.Background(), prepared)
		if errors.Is(err, qemu.ErrAlreadyRunning) && s.runtime.RunningLightVM(current.ID) {
			_, _ = s.store.SetLightVMStatus(context.WithoutCancel(ctx), current.ID, store.StatusRunning)
			_, _ = s.store.SetLightVMDesiredState(context.WithoutCancel(ctx), current.ID, store.DesiredRunning)
			return nil, status.Error(codes.AlreadyExists, "LightVM is already running")
		}
		restorePrevious(current)
		if !errors.Is(err, context.Canceled) && startCtx.Err() == nil {
			_, _ = s.store.SetLightVMStatus(context.WithoutCancel(ctx), current.ID, store.StatusError)
		}
		return nil, lightVMStartStatusError(err)
	}
	return nil, status.Error(codes.Internal, "could not start LightVM")
}

// StopLightVM terminates the running QEMU process.
func (s *lightVMService) StopLightVM(ctx context.Context, request *rookeryv1.StopLightVMRequest) (*rookeryv1.LightVM, error) {
	if err := s.runtimeUnavailable(); err != nil {
		return nil, err
	}
	if s.startCoordinator != nil {
		release := s.startCoordinator.Reserve(request.GetId())
		defer release()
		s.startCoordinator.Cancel(request.GetId())
	}
	lifecycleCtx := context.WithoutCancel(ctx)
	s.mu.Lock()
	current, err := s.store.GetLightVM(ctx, request.GetId())
	if err != nil {
		s.mu.Unlock()
		return nil, lightVMError(err, "read")
	}
	if _, err := s.store.SetLightVMDesiredState(lifecycleCtx, current.ID, store.DesiredStopped); err != nil {
		s.mu.Unlock()
		return nil, status.Error(codes.Internal, "could not update LightVM desired state")
	}
	if err := s.runtime.StopQEMULightVM(lifecycleCtx, current.ID); err != nil || s.runtime.RunningLightVM(current.ID) {
		s.mu.Unlock()
		if err != nil {
			return nil, status.Error(codes.Internal, "could not stop LightVM: "+err.Error())
		}
		return nil, status.Error(codes.Internal, "LightVM is still running")
	}
	updated, err := s.store.SetLightVMStatus(lifecycleCtx, current.ID, store.StatusStopped)
	if err != nil {
		s.mu.Unlock()
		return nil, status.Error(codes.Internal, "could not update LightVM status")
	}
	s.mu.Unlock()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := s.runtime.CleanupStoppedLightVM(cleanupCtx, current.ID); err != nil {
		return nil, status.Error(codes.Internal, "could not clean up LightVM: "+err.Error())
	}
	return s.toProto(ctx, updated), nil
}

func lightVMFromCreateRequest(request *rookeryv1.CreateLightVMRequest) (store.LightVM, error) {
	vcpus := int(request.GetVcpus())
	if vcpus == 0 {
		vcpus = lightVMDefaultVCPUs
	}
	memoryMB := int(request.GetMemoryMb())
	if memoryMB == 0 {
		memoryMB = lightVMDefaultMemoryMB
	}
	appendValue := store.DefaultLightVMAppend
	if request.Append != nil {
		appendValue = request.GetAppend()
	}
	return lightVMFromRequest(
		"", request.GetName(), request.GetDescription(), vcpus, memoryMB,
		request.GetConsoleEnabled(), request.GetCgroupClass(),
		request.GetKernelId(), request.GetInitramfsId(), request.GetImage(),
		request.GetEnv(), request.GetExecutablePath(), request.GetExecutableArgs(),
		request.GetInterfaceIds(), request.GetDiskIds(), appendValue, request.GetConsoles(),
		request.GetVirtiofsShares(),
	)
}

func lightVMFromUpdateRequest(request *rookeryv1.UpdateLightVMRequest) (store.LightVM, error) {
	vm, err := lightVMFromRequest(
		request.GetId(), request.GetName(), request.GetDescription(),
		int(request.GetVcpus()), int(request.GetMemoryMb()), request.GetConsoleEnabled(),
		request.GetCgroupClass(), request.GetKernelId(), request.GetInitramfsId(),
		request.GetImage(), request.GetEnv(), request.GetExecutablePath(),
		request.GetExecutableArgs(), request.GetInterfaceIds(), request.GetDiskIds(),
		request.GetAppend(), request.GetConsoles(), request.GetVirtiofsShares(),
	)
	if err != nil {
		return store.LightVM{}, err
	}
	vm.ReplaceDiskIDs = request.GetReplaceDiskIds()
	return vm, nil
}

func lightVMFromRequest(id, name, description string, vcpus, memoryMB int, console bool,
	cgroupClass string, kernelID, initramfsID int64, image string,
	env []*rookeryv1.LightVMEnvVar, executablePath string, executableArgs []string,
	interfaceIDs, diskIDs []int64, appendValue string, consoles []*rookeryv1.LightVMConsole,
	shares []*rookeryv1.LightVMVirtioFSShare,
) (store.LightVM, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return store.LightVM{}, errors.New("name is required")
	}
	if len(name) > 64 {
		return store.LightVM{}, errors.New("name must be at most 64 characters")
	}
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > 2000 {
		return store.LightVM{}, errors.New("description must be at most 2000 characters")
	}
	if vcpus < 1 || vcpus > 128 {
		return store.LightVM{}, errors.New("vcpus must be between 1 and 128")
	}
	if memoryMB < 128 || memoryMB > 1_048_576 {
		return store.LightVM{}, errors.New("memory_mb must be between 128 and 1048576")
	}
	cgroupClass = strings.TrimSpace(cgroupClass)
	if utf8.RuneCountInString(cgroupClass) > 200 {
		return store.LightVM{}, errors.New("cgroup_class must be at most 200 characters")
	}
	image = strings.TrimSpace(image)
	if image == "" {
		return store.LightVM{}, errors.New("image is required")
	}
	if utf8.RuneCountInString(image) > 512 {
		return store.LightVM{}, errors.New("image must be at most 512 characters")
	}
	executablePath = strings.TrimSpace(executablePath)
	if executablePath == "" {
		return store.LightVM{}, errors.New("executable_path is required")
	}
	executablePath = path.Clean(executablePath)
	if !path.IsAbs(executablePath) {
		return store.LightVM{}, errors.New("executable_path must be an absolute container path")
	}
	if utf8.RuneCountInString(executablePath) > 4096 {
		return store.LightVM{}, errors.New("executable_path must be at most 4096 characters")
	}

	normalizedEnv, err := normalizeLightVMEnv(env)
	if err != nil {
		return store.LightVM{}, err
	}
	normalizedArgs, err := normalizeLightVMArgs(executableArgs)
	if err != nil {
		return store.LightVM{}, err
	}
	appendValue = strings.TrimSpace(appendValue)
	if utf8.RuneCountInString(appendValue) > 4096 {
		return store.LightVM{}, errors.New("append must be at most 4096 characters")
	}
	if strings.ContainsRune(appendValue, 0) {
		return store.LightVM{}, errors.New("append cannot contain NUL characters")
	}
	normalizedConsoles, err := normalizeLightVMConsoles(consoles)
	if err != nil {
		return store.LightVM{}, err
	}
	normalizedDiskIDs, err := normalizeGatewayIDs(diskIDs, store.MaxWorkloadDisks, "disk_ids")
	if err != nil {
		return store.LightVM{}, err
	}
	virtioFSShares := make([]store.LightVMVirtioFSShare, 0, len(shares))
	for _, share := range shares {
		readOnly := true
		if share.ReadOnly != nil {
			readOnly = share.GetReadOnly()
		}
		virtioFSShares = append(virtioFSShares, store.LightVMVirtioFSShare{
			HostPath: share.GetHostPath(),
			Tag:      share.GetTag(),
			ReadOnly: readOnly,
		})
	}

	return store.LightVM{
		ID:             id,
		Name:           name,
		Description:    description,
		VCPUs:          vcpus,
		MemoryMB:       memoryMB,
		ConsoleEnabled: console,
		CgroupClass:    cgroupClass,
		KernelID:       kernelID,
		InitramfsID:    initramfsID,
		Image:          image,
		Env:            normalizedEnv,
		ExecutablePath: executablePath,
		ExecutableArgs: normalizedArgs,
		Append:         appendValue,
		Consoles:       normalizedConsoles,
		InterfaceIDs:   append([]int64(nil), interfaceIDs...),
		DiskIDs:        normalizedDiskIDs,
		VirtioFSShares: virtioFSShares,
	}, nil
}

func normalizeLightVMConsoles(values []*rookeryv1.LightVMConsole) ([]store.LightVMConsole, error) {
	if len(values) == 0 {
		return []store.LightVMConsole{{Name: "console"}}, nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]store.LightVMConsole, 0, len(values))
	for _, item := range values {
		name := strings.TrimSpace(item.GetName())
		if name == "" {
			return nil, errors.New("console name is required")
		}
		if utf8.RuneCountInString(name) > 64 {
			return nil, fmt.Errorf("console name %q is too long", name)
		}
		if strings.ContainsAny(name, "\r\n\x00") {
			return nil, fmt.Errorf("console name %q must be a single line without NUL characters", name)
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("console %q is duplicated", name)
		}
		seen[key] = struct{}{}
		result = append(result, store.LightVMConsole{Name: name})
	}
	return result, nil
}

func normalizeLightVMEnv(values []*rookeryv1.LightVMEnvVar) ([]store.LightVMEnvVar, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]store.LightVMEnvVar, 0, len(values))
	for _, item := range values {
		name := strings.TrimSpace(item.GetName())
		if name == "" {
			return nil, errors.New("environment variable name is required")
		}
		if utf8.RuneCountInString(name) > 128 {
			return nil, fmt.Errorf("environment variable %q is too long", name)
		}
		if !validLightVMEnvName(name) {
			return nil, fmt.Errorf("environment variable %q must match [A-Za-z_][A-Za-z0-9_]*", name)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("environment variable %q is duplicated", name)
		}
		seen[name] = struct{}{}
		value := item.GetValue()
		if utf8.RuneCountInString(value) > 4096 {
			return nil, fmt.Errorf("environment variable %q value is too long", name)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return nil, fmt.Errorf("environment variable %q must be a single line without NUL characters", name)
		}
		result = append(result, store.LightVMEnvVar{Name: name, Value: value})
	}
	return result, nil
}

func validLightVMEnvName(name string) bool {
	for index, char := range name {
		switch {
		case char == '_':
		case char >= 'A' && char <= 'Z':
		case char >= 'a' && char <= 'z':
		case index > 0 && char >= '0' && char <= '9':
		default:
			return false
		}
	}
	return name != ""
}

func normalizeLightVMArgs(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if utf8.RuneCountInString(value) > 4096 {
			return nil, errors.New("each executable argument must be at most 4096 characters")
		}
		if strings.ContainsRune(value, 0) {
			return nil, errors.New("executable arguments cannot contain NUL characters")
		}
		result = append(result, value)
	}
	return result, nil
}

func validateLightVMReferences(ctx context.Context, st *store.Store, vm store.LightVM) error {
	if _, err := st.GetLightVMKernel(ctx, vm.KernelID); err != nil {
		if errors.Is(err, store.ErrLightVMKernelNotFound) {
			return status.Errorf(codes.InvalidArgument, "kernel %d not found", vm.KernelID)
		}
		return status.Error(codes.Internal, "could not read kernel")
	}
	if _, err := st.GetLightVMInitramfs(ctx, vm.InitramfsID); err != nil {
		if errors.Is(err, store.ErrLightVMInitramfsNotFound) {
			return status.Errorf(codes.InvalidArgument, "initramfs %d not found", vm.InitramfsID)
		}
		return status.Error(codes.Internal, "could not read initramfs")
	}
	return nil
}

func (s *lightVMService) toProto(ctx context.Context, vm store.LightVM) *rookeryv1.LightVM {
	result := lightVMToProto(vm)
	if s.runtime != nil && s.runtime.RunningLightVM(vm.ID) {
		for _, console := range result.Consoles {
			console.Socket = s.runtime.LightVMConsoleSocket(vm.ID, console.Id)
		}
		if len(result.Consoles) > 0 {
			result.ConsoleSocket = result.Consoles[0].Socket
		}
		runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		state, err := s.runtime.RunState(runCtx, vm.ID)
		cancel()
		if err != nil {
			log.Warn().Err(err).Str("lightvm_id", vm.ID).Msg("could not read LightVM run state")
		} else if state != "" {
			result.RunState = &state
		}
	}
	return result
}

func lightVMToProto(vm store.LightVM) *rookeryv1.LightVM {
	result := &rookeryv1.LightVM{
		Id:             vm.ID,
		Name:           vm.Name,
		Description:    vm.Description,
		Vcpus:          int32(vm.VCPUs),
		MemoryMb:       int32(vm.MemoryMB),
		ConsoleEnabled: vm.ConsoleEnabled,
		CgroupClass:    vm.CgroupClass,
		KernelId:       vm.KernelID,
		KernelName:     vm.KernelName,
		InitramfsId:    vm.InitramfsID,
		InitramfsName:  vm.InitramfsName,
		Image:          vm.Image,
		ExecutablePath: vm.ExecutablePath,
		ExecutableArgs: append([]string(nil), vm.ExecutableArgs...),
		Append:         vm.Append,
		Status:         vm.Status,
		DesiredState:   vm.DesiredState,
		InterfaceIds:   append([]int64(nil), vm.InterfaceIDs...),
		DiskIds:        append([]int64(nil), vm.DiskIDs...),
		CreatedAt:      timestamp(vm.CreatedAt),
		UpdatedAt:      timestamp(vm.UpdatedAt),
	}
	for _, item := range vm.Env {
		result.Env = append(result.Env, &rookeryv1.LightVMEnvVar{Name: item.Name, Value: item.Value})
	}
	for _, console := range vm.Consoles {
		result.Consoles = append(result.Consoles, &rookeryv1.LightVMConsole{
			Id:       console.ID,
			Name:     console.Name,
			PortName: console.PortName,
		})
	}
	for _, disk := range vm.Disks {
		result.Disks = append(result.Disks, vmDiskToProto(disk))
	}
	for _, share := range vm.VirtioFSShares {
		readOnly := share.ReadOnly
		result.VirtiofsShares = append(result.VirtiofsShares, &rookeryv1.LightVMVirtioFSShare{
			Id: share.ID, HostPath: share.HostPath, Tag: share.Tag,
			ReadOnly: &readOnly, Position: int32(share.Position),
		})
	}
	for _, iface := range vm.Interfaces {
		result.Interfaces = append(result.Interfaces, &rookeryv1.VMInterface{
			Id: iface.ID, Name: iface.Name, NetworkId: iface.NetworkID,
			NetworkName: iface.NetworkName, SocketPath: iface.SocketPath,
			Mac: iface.MAC, SwIfIndex: iface.SwIfIndex,
			IngressAclName: iface.IngressACLName, EgressAclName: iface.EgressACLName,
		})
	}
	return result
}

func lightVMStartStatusError(err error) error {
	switch {
	case errors.Is(err, kouja.ErrSessionActive):
		return status.Error(codes.Unavailable, "a previous iSCSI session is still active; retry shortly")
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Aborted, "start was canceled; start again")
	case errors.Is(err, qemu.ErrAlreadyRunning):
		return status.Error(codes.AlreadyExists, "LightVM is already running")
	default:
		return status.Error(codes.Internal, "could not start LightVM: "+err.Error())
	}
}

func lightVMError(err error, action string) error {
	switch {
	case errors.Is(err, store.ErrLightVMNotFound):
		return status.Error(codes.NotFound, "lightvm not found")
	case errors.Is(err, store.ErrLightVMNameTaken):
		return status.Error(codes.AlreadyExists, "a lightvm with that name already exists")
	case errors.Is(err, store.ErrLightVMKernelNotFound):
		return status.Error(codes.InvalidArgument, "kernel not found")
	case errors.Is(err, store.ErrLightVMInitramfsNotFound):
		return status.Error(codes.InvalidArgument, "initramfs not found")
	case errors.Is(err, store.ErrInterfaceNotFound):
		return status.Error(codes.InvalidArgument, "interface not found")
	case errors.Is(err, store.ErrInterfaceInUse):
		return status.Error(codes.Aborted, "interface is already attached to a VM or LightVM")
	case errors.Is(err, store.ErrDiskNotFound):
		return status.Error(codes.InvalidArgument, "disk not found")
	case errors.Is(err, store.ErrInvalidDiskIDs):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, store.ErrLightVMDeviceLimit):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, store.ErrInvalidVirtioFSShare):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, store.ErrDiskIsTemplate):
		return status.Error(codes.Aborted, "template disks cannot be attached to a LightVM")
	case errors.Is(err, store.ErrDiskDeleting):
		return status.Error(codes.Aborted, "disk is provisioning or being deleted")
	case errors.Is(err, store.ErrDiskInUse):
		return diskInUseError(err)
	default:
		return status.Errorf(codes.Internal, "could not %s lightvm", action)
	}
}
