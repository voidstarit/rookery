package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/voidstarit/rookery/internal/store"
)

const vppWatchInterval = 5 * time.Second

// RunInfrastructureReconciler waits for VPP, restores all VPP-backed
// resources, then watches the VPP API for restarts and repeats reconciliation.
func (s *Server) RunInfrastructureReconciler(ctx context.Context) {
	if s.readiness == nil || s.vnfs == nil || s.networks == nil {
		return
	}

	wait := time.NewTicker(2 * time.Second)
	defer wait.Stop()
	for !s.readiness.Status().VPP.Ready {
		select {
		case <-ctx.Done():
			return
		case <-wait.C:
		}
	}

	// The initial reconciliation runs in the same background worker as later
	// VPP-restart rebuilds so the watcher can detect another VPP restart while
	// the initial pass is still in progress.
	s.watchVPP(ctx, true)
}

func (s *Server) reconcileInfrastructure(ctx context.Context) error {
	var errs []error
	if err := s.ReconcileNetworks(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileVhostUsers(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileACLs(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileNAT44s(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileVNFs(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Server) reconcileInfrastructureWithRetries(ctx context.Context) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		lastErr = s.reconcileInfrastructure(ctx)
		if lastErr == nil {
			return nil
		}
		s.logger.Warn("infrastructure reconciliation failed", "attempt", attempt, "error", lastErr)
		if attempt == 3 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return lastErr
}

// watchVPP periodically checks the VPP binary API. A new API socket inode means
// VPP restarted; its in-memory state is gone, so rebuild bridge domains,
// vhost-user interfaces, NAT/BVIs, and VNF taps.
func (s *Server) watchVPP(ctx context.Context, initiallyDirty bool) {
	lastIdentity, _ := s.networks.Identity()
	healthy := true
	pendingReconcile := initiallyDirty
	pendingReset := false

	var reconcileDone chan struct{}
	var reconcileCancel context.CancelFunc
	var reconcileErr error
	var reconcileReset bool

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			if reconcileCancel != nil {
				reconcileCancel()
			}
			return
		case <-timer.C:
			timer.Reset(vppWatchInterval)
		}

		// Collect a completed background reconcile.
		if reconcileDone != nil {
			select {
			case <-reconcileDone:
				if reconcileCancel != nil {
					reconcileCancel()
				}
				if reconcileErr != nil {
					s.logger.Error("VPP resource rebuild failed; will retry", "error", reconcileErr)
					pendingReconcile = true
				} else if reconcileReset {
					s.logger.Info("VPP infrastructure rebuilt after restart")
				}
				reconcileDone = nil
				reconcileCancel = nil
			default:
			}
		}

		pingCtx, cancel := context.WithTimeout(ctx, vppWatchInterval)
		pingErr := s.networks.Ping(pingCtx)
		identity, identityErr := s.networks.Identity()
		cancel()

		if pingErr != nil {
			if healthy {
				s.logger.Warn("VPP API unavailable; waiting for it to return", "error", pingErr)
			}
			healthy = false
			pendingReconcile = true
			pendingReset = true
			// Let the current worker finish instead of canceling it. Canceling
			// VPP API calls can leave half-created BVIs behind and race a new
			// worker creating the same BVI in the restarted VPP.
			continue
		}
		if !healthy {
			s.logger.Info("VPP API is reachable again")
			healthy = true
			pendingReconcile = true
		}
		if identityErr == nil && identity != "" {
			if lastIdentity != "" && identity != lastIdentity {
				s.logger.Warn("VPP restart detected; rebuilding VPP resources",
					"old_identity", lastIdentity,
					"new_identity", identity,
				)
				pendingReconcile = true
				pendingReset = true
				// Do not cancel the in-flight worker here. Starting a second
				// worker while the first can still receive VPP API responses
				// causes duplicate BVI creation (VPP aborts on duplicate
				// 'bviN-tx' interface nodes).
			}
			lastIdentity = identity
		}

		if !pendingReconcile || !healthy || reconcileDone != nil {
			continue
		}

		doReset := pendingReset
		pendingReconcile = false
		pendingReset = false
		reconcileDone = make(chan struct{})
		reconcileReset = doReset
		reconcileErr = nil
		workCtx, workCancel := context.WithTimeout(ctx, 2*time.Minute)
		reconcileCancel = workCancel
		go func(done chan struct{}, reset bool, runCtx context.Context) {
			defer close(done)
			if err := s.waitForVPPReady(runCtx, 15*time.Second); err != nil {
				reconcileErr = err
				return
			}
			if reset {
				s.resetRuntimeForVPPRestart(runCtx)
			}
			reconcileErr = s.reconcileInfrastructureWithRetries(runCtx)
		}(reconcileDone, doReset, workCtx)
	}
}

func (s *Server) waitForVPPReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if s.readiness != nil && s.readiness.Status().VPP.Ready {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("VPP readiness did not become ready")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (s *Server) resetRuntimeForVPPRestart(ctx context.Context) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	s.natMu.Lock()
	s.natRunning = make(map[int64]bool)
	s.natMu.Unlock()
	if s.vnfs != nil {
		if err := s.vnfs.ResetRuntime(ctx); err != nil {
			s.logger.Warn("reset VNF runtime after VPP restart failed", "error", err)
		}
	}
}

// ReconcileNetworks ensures every recorded Nerd's Network has its VPP bridge
// domain, creating missing domains with their original DB-assigned IDs.
func (s *Server) ReconcileNetworks(ctx context.Context) error {
	if s.networks == nil {
		return nil
	}
	if blocked, _ := s.vppBlocked(); blocked {
		return nil
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	networks, err := s.store.ListNetworks(ctx)
	if err != nil {
		return fmt.Errorf("list networks for reconciliation: %w", err)
	}
	var errs []error
	for _, network := range networks {
		if network.BridgeDomainID == 0 {
			errs = append(errs, fmt.Errorf("network %s has no bridge domain ID", network.Name))
			continue
		}
		if err := s.networks.EnsureBridgeDomain(ctx, network.BridgeDomainID, network.Name); err != nil {
			errs = append(errs, fmt.Errorf("network %s: %w", network.Name, err))
			continue
		}
		s.logger.Info("network bridge domain ready", "network", network.Name, "bd_id", network.BridgeDomainID)
	}
	return errors.Join(errs...)
}

// ReconcileVhostUsers finds or recreates the VPP side of every recorded
// vhost-user interface. VPP restarts remove these interfaces even though their
// socket files and database records remain.
func (s *Server) ReconcileVhostUsers(ctx context.Context) error {
	if s.networks == nil {
		return nil
	}
	if blocked, _ := s.vppBlocked(); blocked {
		return nil
	}

	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	interfaces, err := s.store.ListInterfaces(ctx)
	if err != nil {
		return fmt.Errorf("list interfaces for reconciliation: %w", err)
	}
	var errs []error
	for _, iface := range interfaces {
		network, err := s.store.GetNetwork(ctx, iface.NetworkID)
		if err != nil {
			errs = append(errs, fmt.Errorf("interface %s: %w", iface.Name, err))
			continue
		}
		socket, swIfIndex, err := s.networks.EnsureVhostUser(ctx, iface.Name, iface.MAC, network.BridgeDomainID)
		if err != nil {
			errs = append(errs, fmt.Errorf("interface %s: %w", iface.Name, err))
			continue
		}
		changed := socket != iface.SocketPath || swIfIndex != iface.SwIfIndex
		if changed {
			updated, err := s.store.SetInterfaceRuntime(ctx, iface.ID, swIfIndex, socket)
			if err != nil {
				errs = append(errs, fmt.Errorf("interface %s: update runtime metadata: %w", iface.Name, err))
				continue
			}
			s.logger.Info("vhost-user interface restored",
				"interface", updated.Name,
				"network", updated.NetworkName,
				"sw_if_index", updated.SwIfIndex,
			)
			if updated.AttachedVMID != nil && s.vms != nil && s.vms.Running(*updated.AttachedVMID) {
				s.logger.Warn("running VM must be restarted to reconnect its restored vhost-user interface",
					"interface", updated.Name,
					"vm", updated.AttachedVMName,
				)
			}
		}
	}
	return errors.Join(errs...)
}

// ReconcileNAT44s restores enabled NAT44 VNFs and their BVI/outside state.
func (s *Server) ReconcileNAT44s(ctx context.Context) error {
	if s.nat == nil {
		return nil
	}
	if blocked, _ := s.vppBlocked(); blocked {
		return nil
	}
	vnfs, err := s.store.ListVNFs(ctx, nil)
	if err != nil {
		return fmt.Errorf("list NAT44 VNFs for reconciliation: %w", err)
	}
	var errs []error
	for _, vnf := range vnfs {
		if vnf.Type != store.VNFTypeNAT44 || !vnf.Enabled || s.vnfRunning(vnf) {
			continue
		}
		if err := s.reconcileNAT44(ctx, vnf); err != nil {
			errs = append(errs, fmt.Errorf("NAT44 VNF %s: %w", vnf.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) reconcileNAT44(ctx context.Context, vnf store.VNF) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()
	current, err := s.store.GetVNF(ctx, vnf.ID)
	if errors.Is(err, store.ErrVNFNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reload NAT44 VNF: %w", err)
	}
	if !current.Enabled || s.vnfRunning(current) {
		return nil
	}
	activated, err := s.activateNAT44(ctx, current)
	if err != nil {
		return err
	}
	s.logger.Info("NAT44 activated during startup reconciliation",
		"vnf", activated.Name,
		"network", activated.NetworkName,
	)
	return nil
}

// ReconcileVNFs starts every enabled VNF that is not currently running.
func (s *Server) ReconcileVNFs(ctx context.Context) error {
	if s.vnfs == nil || s.networks == nil {
		return nil
	}
	if blocked, _ := s.vppBlocked(); blocked {
		return nil
	}
	vnfs, err := s.store.ListVNFs(ctx, nil)
	if err != nil {
		return fmt.Errorf("list VNFs for reconciliation: %w", err)
	}

	var errs []error
	for _, vnf := range vnfs {
		if !vnf.Enabled || vnf.Type == store.VNFTypeNAT44 || s.vnfs.Running(vnf.ID) {
			continue
		}
		if err := s.reconcileVNF(ctx, vnf); err != nil {
			errs = append(errs, fmt.Errorf("VNF %s: %w", vnf.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Server) reconcileVNF(ctx context.Context, vnf store.VNF) error {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	// The VNF may have been disabled/deleted while this reconciliation pass
	// was waiting for the resource lock.
	current, err := s.store.GetVNF(ctx, vnf.ID)
	if errors.Is(err, store.ErrVNFNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reload VNF: %w", err)
	}
	vnf = current
	if !vnf.Enabled || s.vnfs.Running(vnf.ID) {
		return nil
	}

	// The memif transport is now the default. Migrate an enabled DHCPv4 VNF
	// that still has tap-era runtime fields or an old memif socket path.
	if s.memifDHCP && vnf.Type == store.VNFTypeDHCPv4 &&
		(vnf.MemifSocket == "" || vnf.MemifSocket != s.memifSocketPath(vnf.ID) ||
			vnf.MemifHostName != vnfInterfaceName(vnf) ||
			vnf.TapHostName != "" || vnf.TapSwIfIndex != 0) {
		if err := s.stopVNF(ctx, vnf, false); err != nil {
			s.logger.Warn("cleanup of old VNF runtime failed", "vnf", vnf.Name, "error", err)
		}
		if vnf.MemifSocket != "" {
			_ = os.Remove(vnf.MemifSocket)
		}
		vnf.TapSwIfIndex = 0
		vnf.TapHostName = ""
		vnf.MemifSwIfIndex = 0
		vnf.MemifSocket = ""
		if _, err := s.store.UpdateVNF(ctx, vnf.ID, vnf); err != nil {
			return fmt.Errorf("clear tap-era VNF runtime: %w", err)
		}
		activated, err := s.activateVNF(ctx, vnf)
		if err != nil {
			return err
		}
		s.logger.Info("VNF migrated to memif",
			"vnf", activated.Name,
			"type", activated.Type,
			"memif", activated.MemifSocket,
		)
		return nil
	}

	if (vnf.MemifSwIfIndex > 0 || vnf.MemifSocket != "") && vnf.TapHostName == "" {
		if err := s.vnfs.StartMemif(ctx, vnf, vnf.MemifSocket, uint32(vnf.ID)); err == nil {
			s.logger.Info("VNF runtime restored on memif",
				"vnf", vnf.Name,
				"type", vnf.Type,
				"memif", vnf.MemifSocket,
			)
			return nil
		} else {
			s.logger.Warn("VNF memif runtime could not be restored; recreating",
				"vnf", vnf.Name,
				"memif", vnf.MemifSocket,
				"error", err,
			)
		}
		if err := s.stopVNF(ctx, vnf, false); err != nil {
			s.logger.Warn("cleanup of stale VNF memif failed", "vnf", vnf.Name, "error", err)
		}
		vnf.MemifSwIfIndex = 0
		vnf.MemifSocket = ""
		vnf.TapSwIfIndex = 0
		vnf.TapHostName = ""
		if _, err := s.store.UpdateVNF(ctx, vnf.ID, vnf); err != nil {
			return fmt.Errorf("clear stale VNF memif: %w", err)
		}
	}

	if vnf.TapHostName != "" {
		if err := s.vnfs.Start(ctx, vnf, vnf.TapHostName); err == nil {
			s.logger.Info("VNF runtime restored",
				"vnf", vnf.Name,
				"type", vnf.Type,
				"interface", vnf.TapHostName,
			)
			return nil
		} else {
			s.logger.Warn("VNF runtime could not use its existing tap; recreating",
				"vnf", vnf.Name,
				"interface", vnf.TapHostName,
				"error", err,
			)
		}

		// Remove the old runtime and tap before provisioning a replacement.
		if err := s.stopVNF(ctx, vnf, false); err != nil {
			s.logger.Warn("cleanup of stale VNF runtime failed", "vnf", vnf.Name, "error", err)
		}
		vnf.TapSwIfIndex = 0
		vnf.TapHostName = ""
		if _, err := s.store.UpdateVNF(ctx, vnf.ID, vnf); err != nil {
			return fmt.Errorf("clear stale VNF tap: %w", err)
		}
	}

	activated, err := s.activateVNF(ctx, vnf)
	if err != nil {
		return err
	}
	s.logger.Info("VNF activated during startup reconciliation",
		"vnf", activated.Name,
		"type", activated.Type,
		"interface", activated.TapHostName,
	)
	return nil
}
