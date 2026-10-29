package api

import (
	"context"
	"errors"
	"fmt"
)

// ReconcileNetworkPipes recreates bridge-domain pipes after an API or VPP
// restart and refreshes their current VPP indices.
func (s *Server) ReconcileNetworkPipes(ctx context.Context) error {
	if s.networks == nil {
		return nil
	}
	if blocked, _ := s.vppBlocked(); blocked {
		return nil
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	pipes, err := s.store.ListNetworkPipes(ctx)
	if err != nil {
		return fmt.Errorf("list network pipes: %w", err)
	}
	var errs []error
	for _, pipe := range pipes {
		networkA, err := s.store.GetNetwork(ctx, pipe.NetworkAID)
		if err != nil {
			errs = append(errs, fmt.Errorf("pipe %d network %d: %w", pipe.ID, pipe.NetworkAID, err))
			continue
		}
		networkB, err := s.store.GetNetwork(ctx, pipe.NetworkBID)
		if err != nil {
			errs = append(errs, fmt.Errorf("pipe %d network %d: %w", pipe.ID, pipe.NetworkBID, err))
			continue
		}
		runtime, err := s.networks.EnsurePipe(ctx, pipe.Instance, networkA.BridgeDomainID, networkB.BridgeDomainID)
		if err != nil {
			errs = append(errs, fmt.Errorf("pipe %d: %w", pipe.ID, err))
			continue
		}
		if _, err := s.store.SetNetworkPipeRuntime(ctx, pipe.ID, runtime.Instance,
			runtime.ParentSwIfIndex, runtime.EndpointASwIfIndex, runtime.EndpointBSwIfIndex); err != nil {
			errs = append(errs, fmt.Errorf("pipe %d runtime: %w", pipe.ID, err))
		}
	}
	return errors.Join(errs...)
}
