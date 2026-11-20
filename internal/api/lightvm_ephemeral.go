package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/voidstarit/rookery/internal/store"
)

const (
	// lightVMEphemeralPollInterval bounds how long an exited ephemeral guest
	// can leave its runtime resources behind.
	lightVMEphemeralPollInterval = time.Second
	// lightVMEphemeralCleanupTimeout bounds attempt cleanup after termination.
	lightVMEphemeralCleanupTimeout = 2 * time.Minute
)

// RunLightVMEphemeralReaper watches ephemeral LightVMs and deletes the ones
// whose guest run ended. A finished ephemeral LightVM is one-shot: QEMU,
// virtiofsd, the attempt container, console sockets, and the config drive are
// removed, then the definition is deleted. Deleting the definition cascades its
// disk and interface attachment rows, so those resources become available to
// other workloads; the disks and VPP interfaces themselves are not deleted.
func (s *Server) RunLightVMEphemeralReaper(ctx context.Context) {
	if s.lightvms == nil || s.store == nil {
		return
	}
	ticker := time.NewTicker(lightVMEphemeralPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reapEphemeralLightVMs(ctx)
		}
	}
}

func (s *Server) reapEphemeralLightVMs(ctx context.Context) {
	vms, err := s.store.ListReapableEphemeralLightVMs(ctx)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not list LightVMs for ephemeral termination")
		return
	}
	for _, vm := range vms {
		if s.lightvms.RunningLightVM(vm.ID) {
			continue
		}
		if err := s.terminateEphemeralLightVM(ctx, vm); err != nil {
			s.logger.Warn().Err(err).Str("lightvm_id", vm.ID).Str("lightvm", vm.Name).
				Msg("could not terminate ephemeral LightVM")
		}
	}
}

// terminateEphemeralLightVM cleans up and deletes one ephemeral LightVM whose
// guest run ended. A LightVM with an active start is left alone. Failed starts
// are kept for debugging; their desired state is cleared so they are not
// resurrected after a restart.
func (s *Server) terminateEphemeralLightVM(ctx context.Context, vm store.LightVM) error {
	if s.lightvmCoordinator != nil {
		release, busy := s.lightvmCoordinator.ReserveForCleanup(vm.ID)
		defer release()
		if busy {
			return nil
		}
	}

	s.runMu.Lock()
	current, err := s.store.GetLightVM(ctx, vm.ID)
	if errors.Is(err, store.ErrLightVMNotFound) {
		s.runMu.Unlock()
		return nil
	}
	if err != nil {
		s.runMu.Unlock()
		return fmt.Errorf("read ephemeral LightVM: %w", err)
	}
	if !current.Ephemeral || s.lightvms.RunningLightVM(current.ID) {
		s.runMu.Unlock()
		return nil
	}
	switch current.Status {
	case store.StatusRunning, store.StatusStarting, store.StatusTerminated:
		// A run ended or a previous cleanup needs another attempt.
	case store.StatusError:
		// A start that never produced a running guest is not a finished run.
		// Keep the definition for inspection but do not restart it today.
		if current.DesiredState == store.DesiredRunning {
			if _, err := s.store.SetLightVMDesiredState(ctx, current.ID, store.DesiredStopped); err != nil {
				s.runMu.Unlock()
				return fmt.Errorf("clear failed ephemeral LightVM desired state: %w", err)
			}
		}
		s.runMu.Unlock()
		return nil
	default:
		if current.DesiredState != store.DesiredRunning {
			s.runMu.Unlock()
			return nil
		}
	}
	if current.DesiredState == store.DesiredRunning {
		if _, err := s.store.SetLightVMDesiredState(ctx, current.ID, store.DesiredStopped); err != nil {
			s.runMu.Unlock()
			return fmt.Errorf("clear ephemeral LightVM desired state: %w", err)
		}
	}
	if current.Status != store.StatusTerminated {
		if _, err := s.store.SetLightVMStatus(ctx, current.ID, store.StatusTerminated); err != nil {
			s.runMu.Unlock()
			return fmt.Errorf("mark ephemeral LightVM terminated: %w", err)
		}
	}
	s.runMu.Unlock()

	if err := s.cleanupTerminatedLightVM(ctx, current.ID); err != nil {
		return fmt.Errorf("clean up terminated ephemeral LightVM: %w", err)
	}
	if err := s.store.DeleteLightVM(ctx, current.ID); err != nil && !errors.Is(err, store.ErrLightVMNotFound) {
		return fmt.Errorf("delete terminated ephemeral LightVM: %w", err)
	}
	s.logger.Info().Str("lightvm_id", current.ID).Str("lightvm", current.Name).
		Msg("ephemeral LightVM terminated, deleted, and attachments released")
	return nil
}

// cleanupTerminatedLightVM stops any remaining QEMU process and removes the
// attempt-scoped virtiofsd sockets, config drive, and buildah container. It
// never touches disks or VPP interfaces.
func (s *Server) cleanupTerminatedLightVM(ctx context.Context, id string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lightVMEphemeralCleanupTimeout)
	defer cancel()

	var errs []error
	if err := s.lightvms.StopQEMULightVM(cleanupCtx, id); err != nil {
		errs = append(errs, err)
	}
	if s.lightvms.RunningLightVM(id) {
		errs = append(errs, errors.New("QEMU is still running"))
		return errors.Join(errs...)
	}
	if err := s.lightvms.CleanupStoppedLightVM(cleanupCtx, id); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
