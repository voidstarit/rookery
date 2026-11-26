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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/kouja"
	"github.com/voidstarit/rookery/internal/lightvm"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

const maxLightVMExecutableScriptBytes = 1 << 20

// maxLightVMConsoleAccessTTL bounds issued console-access tokens.
const maxLightVMConsoleAccessTTL = 24 * time.Hour

const (
	lightVMDefaultVCPUs    = 1
	lightVMDefaultMemoryMB = 512
)

type lightVMService struct {
	rookeryv1.UnimplementedLightVMServiceServer
	store            *store.Store
	runtime          LightVMRuntime
	counters         InterfaceCounterSource
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
	LightVMMetrics(id string) (qemu.Metrics, error)
	LightVMBlockStats(ctx context.Context, id string) ([]qemu.BlockDeviceStats, error)
}

// runtimeUnavailable returns Unavailable when no LightVM runtime is configured, otherwise nil.
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

// GetLightVM returns the LightVM definition with the requested ID, reporting
// NotFound when it is missing.
func (s *lightVMService) GetLightVM(ctx context.Context, request *rookeryv1.GetLightVMRequest) (*rookeryv1.LightVM, error) {
	vm, err := s.store.GetLightVM(ctx, request.GetId())
	if err != nil {
		return nil, lightVMError(err, "read")
	}
	return s.toProto(ctx, vm), nil
}

// CreateLightVM validates and stores a LightVM definition, then boots it;
// stopping is always explicit. A failed start removes the record and any
// partial runtime so callers never see a half-created LightVM. Without a
// LightVM runtime the record is kept stopped.
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
	if s.runtime == nil {
		return s.toProto(ctx, created), nil
	}
	started, err := s.StartLightVM(ctx, &rookeryv1.StartLightVMRequest{Id: created.ID})
	if err != nil {
		// StartLightVM already cleaned up whatever it launched. Remove the
		// definition as well so a failed create leaves nothing behind.
		cleanupCtx := context.WithoutCancel(ctx)
		if _, cleanupErr := s.DeleteLightVM(cleanupCtx, &rookeryv1.DeleteLightVMRequest{Id: created.ID}); cleanupErr != nil {
			log.Warn().Err(cleanupErr).Str("lightvm_id", created.ID).
				Msg("could not remove LightVM after a failed create")
		}
		return nil, err
	}
	return started, nil
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
	vm, err := lightVMFromUpdateRequest(existing, request)
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

// StartLightVM boots the definition's QEMU process after enforcing the device
// budget and runtime availability.
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
	if _, err := s.store.SetLightVMDesiredState(context.WithoutCancel(ctx), current.ID, store.DesiredRunning); err != nil {
		s.mu.Unlock()
		return nil, status.Error(codes.Internal, "could not update LightVM desired state")
	}
	starting, err := s.store.SetLightVMStatus(context.WithoutCancel(ctx), current.ID, store.StatusStarting)
	if err != nil {
		s.mu.Unlock()
		return nil, status.Error(codes.Internal, "could not update LightVM status")
	}
	startGeneration := starting.LifecycleGeneration
	current = starting
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

// StopLightVM terminates the running QEMU process while serializing with
// in-flight starts.
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
	statusTarget := store.StatusStopped
	if current.Ephemeral {
		// An ephemeral LightVM is one-shot. Mark it terminated so the reaper
		// can retry cleanup/deletion if any step below fails.
		statusTarget = store.StatusTerminated
	}
	updated, err := s.store.SetLightVMStatus(lifecycleCtx, current.ID, statusTarget)
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
	if current.Ephemeral {
		if err := s.store.DeleteLightVM(lifecycleCtx, current.ID); err != nil && !errors.Is(err, store.ErrLightVMNotFound) {
			return nil, status.Error(codes.Internal, "could not delete ephemeral LightVM: "+err.Error())
		}
	}
	return s.toProto(ctx, updated), nil
}

// CreateLightVMConsoleAccess issues a time-limited bearer token for a
// LightVM's virtio console. The token is returned once and only its hash is
// persisted. Access is still checked against console_enabled and the runtime
// state on every connection.
func (s *lightVMService) CreateLightVMConsoleAccess(ctx context.Context, request *rookeryv1.CreateLightVMConsoleAccessRequest) (*rookeryv1.LightVMConsoleAccess, error) {
	vm, err := s.store.GetLightVM(ctx, request.GetId())
	switch {
	case errors.Is(err, store.ErrLightVMNotFound):
		return nil, status.Error(codes.NotFound, "lightvm not found")
	case err != nil:
		return nil, status.Error(codes.Internal, "could not read LightVM")
	}
	if !vm.ConsoleEnabled {
		return nil, status.Error(codes.PermissionDenied, "console access is disabled for this LightVM")
	}
	console, err := lightVMConsoleForAccess(vm, request.GetConsoleId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	ttl := time.Duration(request.GetTtlSeconds()) * time.Second
	if ttl <= 0 || ttl > maxLightVMConsoleAccessTTL {
		return nil, status.Errorf(codes.InvalidArgument, "ttl_seconds must be between 1 and %d", int64(maxLightVMConsoleAccessTTL/time.Second))
	}
	token, err := lightvm.NewConsoleAccessToken()
	if err != nil {
		return nil, status.Error(codes.Internal, "could not generate console access token")
	}
	access, err := s.store.CreateLightVMConsoleAccess(ctx, vm.ID, console.ID, lightvm.HashConsoleAccessToken(token), time.Now().Add(ttl))
	switch {
	case errors.Is(err, store.ErrLightVMConsoleAccessNotFound):
		return nil, status.Error(codes.NotFound, "console not found")
	case err != nil:
		return nil, status.Error(codes.Internal, "could not issue console access")
	}
	path := lightvm.ConsoleAccessPath(token)
	return &rookeryv1.LightVMConsoleAccess{
		Id:            access.ID,
		LightvmId:     access.LightVMID,
		ConsoleId:     access.ConsoleID,
		ConsoleName:   console.Name,
		Token:         token,
		WebsocketPath: path,
		WebsocketUrl:  lightVMConsoleWebSocketURL(ctx, path),
		ExpiresAt:     timestamp(access.ExpiresAt),
		CreatedAt:     timestamp(access.CreatedAt),
	}, nil
}

// lightVMConsoleWebSocketURL builds a ws or wss URL from the x-forwarded-host and forwarded-proto
// gRPC metadata, returning an empty string when either header is absent or the scheme is not
// http/https.
func lightVMConsoleWebSocketURL(ctx context.Context, path string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	hosts := md.Get("x-forwarded-host")
	schemes := md.Get(forwardedProtoMetadataKey)
	if len(hosts) == 0 || len(schemes) == 0 {
		return ""
	}
	host := strings.TrimSpace(hosts[0])
	scheme := strings.TrimSpace(schemes[0])
	if host == "" {
		return ""
	}
	switch scheme {
	case "https":
		scheme = "wss"
	case "http":
		scheme = "ws"
	default:
		return ""
	}
	return scheme + "://" + host + path
}

// lightVMConsoleForAccess returns the console with consoleID, or the first console when consoleID
// is 0; it fails when the LightVM has no consoles or no console matches.
func lightVMConsoleForAccess(vm store.LightVM, consoleID int64) (store.LightVMConsole, error) {
	if len(vm.Consoles) == 0 {
		return store.LightVMConsole{}, errors.New("LightVM has no consoles")
	}
	if consoleID == 0 {
		return vm.Consoles[0], nil
	}
	for _, console := range vm.Consoles {
		if console.ID == consoleID {
			return console, nil
		}
	}
	return store.LightVMConsole{}, fmt.Errorf("console %d not found", consoleID)
}

// GetLightVMMetrics returns process-level QEMU telemetry for one LightVM.
func (s *lightVMService) GetLightVMMetrics(ctx context.Context, request *rookeryv1.GetLightVMMetricsRequest) (*rookeryv1.LightVMMetrics, error) {
	if err := s.runtimeUnavailable(); err != nil {
		return nil, err
	}
	vm, err := s.store.GetLightVM(ctx, request.GetId())
	switch {
	case errors.Is(err, store.ErrLightVMNotFound):
		return nil, status.Error(codes.NotFound, "lightvm not found")
	case err != nil:
		return nil, status.Error(codes.Internal, "could not read LightVM")
	}
	metrics, err := s.runtime.LightVMMetrics(vm.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read LightVM metrics: "+err.Error())
	}
	result := &rookeryv1.LightVMMetrics{
		Running:             metrics.Running,
		Pid:                 int32(metrics.PID),
		UptimeSeconds:       metrics.UptimeSeconds,
		CpuSecondsTotal:     metrics.CPUSecondsTotal,
		MemoryRssBytes:      metrics.RSSBytes,
		MemoryVirtualBytes:  metrics.VirtualBytes,
		DiskReadBytesTotal:  metrics.ReadBytesTotal,
		DiskWriteBytesTotal: metrics.WriteBytesTotal,
		LightvmId:           vm.ID,
		Name:                vm.Name,
		Vcpus:               int32(vm.VCPUs),
		MemoryMb:            int32(vm.MemoryMB),
		SampledAt:           timestamppb.New(time.Now().UTC()),
	}
	if metrics.Running {
		if blockStats, blockErr := s.runtime.LightVMBlockStats(ctx, vm.ID); blockErr == nil {
			for _, stat := range blockStats {
				result.BlockDevices = append(result.BlockDevices, blockDeviceToProtoForDisks(vm.Disks, stat))
			}
		}
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

// lightVMFromCreateRequest fills in the default VCPUs, memory, and append value, then validates
// the create request through lightVMFromRequest and records the requested disk serials.
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
	vm, err := lightVMFromRequest(
		"", request.GetName(), request.GetDescription(), vcpus, memoryMB,
		request.GetConsoleEnabled(), request.GetEphemeral(), request.GetCgroupClass(),
		request.GetKernelId(), request.GetInitramfsId(), request.GetImage(),
		request.GetEnv(), request.GetConfigFiles(), request.GetExecutablePath(),
		request.GetExecutableScript(), request.GetExecutableArgs(),
		request.GetInterfaceIds(), request.GetDiskIds(), appendValue, request.GetConsoles(),
		request.GetVirtiofsShares(), 0,
	)
	if err != nil {
		return store.LightVM{}, err
	}
	vm.DiskSerials = request.GetDiskSerials()
	return vm, nil
}

// lightVMFromUpdateRequest validates an update request, allowing an omitted image and disk list to
// fall back to the existing disk count unless replace_disk_ids clears them, and carries over the
// replace flag and disk serials.
func lightVMFromUpdateRequest(existing store.LightVM, request *rookeryv1.UpdateLightVMRequest) (store.LightVM, error) {
	existingDisks := len(existing.Disks)
	if request.GetReplaceDiskIds() && len(request.GetDiskIds()) == 0 {
		existingDisks = 0
	}
	vm, err := lightVMFromRequest(
		request.GetId(), request.GetName(), request.GetDescription(),
		int(request.GetVcpus()), int(request.GetMemoryMb()), request.GetConsoleEnabled(),
		request.GetEphemeral(), request.GetCgroupClass(), request.GetKernelId(), request.GetInitramfsId(),
		request.GetImage(), request.GetEnv(), request.GetConfigFiles(),
		request.GetExecutablePath(), request.GetExecutableScript(), request.GetExecutableArgs(),
		request.GetInterfaceIds(), request.GetDiskIds(),
		request.GetAppend(), request.GetConsoles(), request.GetVirtiofsShares(),
		existingDisks,
	)
	if err != nil {
		return store.LightVM{}, err
	}
	vm.ReplaceDiskIDs = request.GetReplaceDiskIds()
	vm.DiskSerials = request.GetDiskSerials()
	return vm, nil
}

// lightVMFromRequest validates every LightVM field and returns the normalized store record: it
// enforces name, size, and resource limits, the image-or-disk requirement, executable path/script
// exclusivity, and normalized env, config files, consoles, args, and virtiofs shares.
func lightVMFromRequest(id, name, description string, vcpus, memoryMB int, console, ephemeral bool,
	cgroupClass string, kernelID, initramfsID int64, image string,
	env []*rookeryv1.LightVMEnvVar, configFiles []*rookeryv1.LightVMConfigFile,
	executablePath, executableScript string, executableArgs []string,
	interfaceIDs, diskIDs []int64, appendValue string, consoles []*rookeryv1.LightVMConsole,
	shares []*rookeryv1.LightVMVirtioFSShare, existingDisks int,
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
	normalizedDiskIDs, err := normalizeGatewayIDs(diskIDs, store.MaxWorkloadDisks, "disk_ids")
	if err != nil {
		return store.LightVM{}, err
	}
	image = strings.TrimSpace(image)
	if utf8.RuneCountInString(image) > 512 {
		return store.LightVM{}, errors.New("image must be at most 512 characters")
	}
	if image == "" && len(normalizedDiskIDs) == 0 && existingDisks == 0 {
		return store.LightVM{}, errors.New("a container image or at least one disk is required")
	}
	normalizedConfigFiles, err := normalizeGatewayConfigFiles(configFiles)
	if err != nil {
		return store.LightVM{}, err
	}
	if image != "" && len(normalizedConfigFiles) > 0 {
		return store.LightVM{}, errors.New("config_files can only be used with a disk-backed LightVM")
	}
	executablePath = strings.TrimSpace(executablePath)
	scriptEmpty := strings.TrimSpace(executableScript) == ""
	switch {
	case executablePath != "" && !scriptEmpty:
		return store.LightVM{}, errors.New("executable_path and executable_script are mutually exclusive")
	case !scriptEmpty && len(executableArgs) > 0:
		return store.LightVM{}, errors.New("executable_args cannot be used with executable_script")
	case len(executableArgs) > 0 && executablePath == "":
		return store.LightVM{}, errors.New("executable_args requires executable_path")
	case image != "" && executablePath == "" && scriptEmpty:
		return store.LightVM{}, errors.New("exactly one of executable_path or executable_script is required")
	}
	if executablePath != "" {
		executablePath = path.Clean(executablePath)
		if !path.IsAbs(executablePath) {
			return store.LightVM{}, errors.New("executable_path must be an absolute guest path")
		}
		if utf8.RuneCountInString(executablePath) > 4096 {
			return store.LightVM{}, errors.New("executable_path must be at most 4096 characters")
		}
	}
	if !scriptEmpty {
		if len(executableScript) > maxLightVMExecutableScriptBytes {
			return store.LightVM{}, fmt.Errorf("executable_script must be at most %d bytes", maxLightVMExecutableScriptBytes)
		}
		if strings.ContainsRune(executableScript, 0) {
			return store.LightVM{}, errors.New("executable_script cannot contain NUL characters")
		}
	}

	normalizedEnv, err := normalizeLightVMEnv(env)
	if err != nil {
		return store.LightVM{}, err
	}
	var normalizedArgs []string
	if executablePath != "" {
		normalizedArgs, err = normalizeLightVMArgs(executableArgs)
		if err != nil {
			return store.LightVM{}, err
		}
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
		ID:               id,
		Name:             name,
		Description:      description,
		VCPUs:            vcpus,
		MemoryMB:         memoryMB,
		ConsoleEnabled:   console,
		Ephemeral:        ephemeral,
		CgroupClass:      cgroupClass,
		KernelID:         kernelID,
		InitramfsID:      initramfsID,
		Image:            image,
		Env:              normalizedEnv,
		ConfigFiles:      normalizedConfigFiles,
		ExecutablePath:   executablePath,
		ExecutableScript: executableScript,
		ExecutableArgs:   normalizedArgs,
		Append:           appendValue,
		Consoles:         normalizedConsoles,
		InterfaceIDs:     append([]int64(nil), interfaceIDs...),
		DiskIDs:          normalizedDiskIDs,
		VirtioFSShares:   virtioFSShares,
	}, nil
}

// normalizeLightVMConsoles defaults an empty list to a single console named "console" and
// otherwise trims each name, rejecting empty, over-long, multi-line, or case-insensitively
// duplicated names.
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

// normalizeGatewayConfigFiles converts the requested config files and delegates validation of
// their names and contents to the store.
func normalizeGatewayConfigFiles(values []*rookeryv1.LightVMConfigFile) ([]store.LightVMConfigFile, error) {
	files := make([]store.LightVMConfigFile, 0, len(values))
	for _, item := range values {
		files = append(files, store.LightVMConfigFile{
			Name:    item.GetName(),
			Content: item.GetContent(),
		})
	}
	return store.NormalizeLightVMConfigFiles(files)
}

// normalizeLightVMEnv trims each environment variable name and rejects empty, over-long,
// malformed, or duplicated names along with values that exceed 4096 runes or span lines.
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

// validLightVMEnvName reports whether name is a non-empty identifier usable as a shell variable:
// it must start with a letter or underscore and contain only letters, digits, and underscores.
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

// normalizeLightVMArgs copies the executable arguments, rejecting any longer than 4096 runes or
// containing NUL bytes.
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

// validateLightVMReferences confirms the referenced kernel and initramfs exist, returning
// InvalidArgument for missing IDs and Internal for other store failures.
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

// toProto converts a LightVM and, when its runtime reports it running, fills in each console's
// socket path and the run state (queried with a two-second timeout); run-state failures are only
// logged.
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

// lightVMToProto converts a stored LightVM into its API representation, including its env, config
// files, consoles, disks, virtiofs shares, and interfaces, with slices copied rather than aliased.
func lightVMToProto(vm store.LightVM) *rookeryv1.LightVM {
	result := &rookeryv1.LightVM{
		Id:               vm.ID,
		Name:             vm.Name,
		Description:      vm.Description,
		Vcpus:            int32(vm.VCPUs),
		MemoryMb:         int32(vm.MemoryMB),
		ConsoleEnabled:   vm.ConsoleEnabled,
		Ephemeral:        vm.Ephemeral,
		CgroupClass:      vm.CgroupClass,
		KernelId:         vm.KernelID,
		KernelName:       vm.KernelName,
		InitramfsId:      vm.InitramfsID,
		InitramfsName:    vm.InitramfsName,
		Image:            vm.Image,
		ExecutablePath:   vm.ExecutablePath,
		ExecutableScript: vm.ExecutableScript,
		ExecutableArgs:   append([]string(nil), vm.ExecutableArgs...),
		Append:           vm.Append,
		Status:           vm.Status,
		DesiredState:     vm.DesiredState,
		InterfaceIds:     append([]int64(nil), vm.InterfaceIDs...),
		DiskIds:          append([]int64(nil), vm.DiskIDs...),
		CreatedAt:        timestamp(vm.CreatedAt),
		UpdatedAt:        timestamp(vm.UpdatedAt),
	}
	for _, item := range vm.Env {
		result.Env = append(result.Env, &rookeryv1.LightVMEnvVar{Name: item.Name, Value: item.Value})
	}
	for _, item := range vm.ConfigFiles {
		result.ConfigFiles = append(result.ConfigFiles, &rookeryv1.LightVMConfigFile{
			Name: item.Name, Content: item.Content,
		})
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

// lightVMStartStatusError converts a LightVM start failure into a gRPC error: an active iSCSI
// session becomes a retryable Unavailable, cancellation Aborted, and an already-running QEMU
// process AlreadyExists.
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

// lightVMError maps LightVM store failures to gRPC statuses for missing records, name conflicts,
// referenced kernels and initramfs, interfaces, disks, and device limits; unknown errors become
// Internal using action in the message.
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
	case errors.Is(err, store.ErrInvalidDiskSerials):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, store.ErrLightVMRootRequired):
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
