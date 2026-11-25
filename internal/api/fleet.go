package api

import (
	"context"
	"time"

	"github.com/voidstarit/rookery/internal/fleet"
)

// SetFleetController installs the gateway reconciler used by the API worker.
func (s *Server) SetFleetController(controller fleet.Reconciler) {
	s.fleetController = controller
}

// Epoch returns the current VPP-network reconciliation epoch.
func (s *Server) Epoch() uint64 {
	return s.fleetEpoch.Load()
}

// CanProvision reports whether the captured epoch is still current and the
// gated VPP network phase is ready.
func (s *Server) CanProvision(epoch uint64) bool {
	return epoch != 0 && s.fleetReady.Load() && s.fleetEpoch.Load() == epoch
}

// RunFleetReconciler drives the gateway fleet controller until ctx ends.
func (s *Server) RunFleetReconciler(ctx context.Context) {
	if s.fleetController == nil {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.fleetController.FleetWake():
		}
		if err := s.fleetController.ReconcileFleet(ctx, s); err != nil {
			s.logger.Warn().Err(err).Msg("fleet reconciliation failed")
		}
	}
}

var _ fleet.Guard = (*Server)(nil)
