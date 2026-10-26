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

	if err := s.reconcileVMStatuses(ctx); err != nil {
		s.logger.Warn().Err(err).Msg("VM status reconciliation failed")
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

	// Rookery and VPP startup scripts run once the FD.io VPP API is reachable.
	// They run independently of the infrastructure reconciliation worker, which
	// can take longer or retry. The VPP script also runs on every reconnect.
	go s.runRookeryStartupScriptOnce()
	go s.runVPPStartupScript()

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
	if err := s.ReconcileNetworkPipes(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileNetworkHostTaps(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileVhostUsers(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileRouteTables(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileACLs(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileHostInterfaces(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileNAT44s(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileVNFs(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.reconcileVNFBindings(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileVPPInterfaceAddresses(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.ReconcileVPPInterfaceRouteTables(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// reconcileVMStatuses aligns stored VM status with live QEMU processes. It
// runs before the VPP wait so an API restart cannot leave a running VM marked
// stopped (or a stale "running" VM that is no longer alive).
func (s *Server) reconcileVMStatuses(ctx context.Context) error {
	if s.vms == nil {
		return nil
	}
	vms, err := s.store.ListVMs(ctx)
	if err != nil {
		return fmt.Errorf("list VMs: %w", err)
	}
	for _, vm := range vms {
		running := s.vms.Running(vm.ID)
		target := vm.Status
		switch {
		case running && vm.Status != store.StatusRunning:
			target = store.StatusRunning
		case !running && vm.Status == store.StatusRunning:
			target = store.StatusStopped
		}
		if target == vm.Status {
			continue
		}
		if _, err := s.store.SetVMStatus(ctx, vm.ID, target); err != nil {
			return fmt.Errorf("update VM %s status: %w", vm.ID, err)
		}
		s.logger.Info().
			Str("vm_id", vm.ID).
			Str("vm", vm.Name).
			Str("from", vm.Status).
			Str("to", target).
			Msg("VM status reconciled")
	}
	return nil
}

func (s *Server) reconcileInfrastructureWithRetries(ctx context.Context) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		lastErr = s.reconcileInfrastructure(ctx)
		if lastErr == nil {
			return nil
		}
		s.logger.Warn().Int("attempt", attempt).Err(lastErr).Msg("infrastructure reconciliation failed")
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
					s.logger.Error().Err(reconcileErr).Msg("VPP resource rebuild failed; will retry")
					pendingReconcile = true
				} else if reconcileReset {
					s.logger.Info().Msg("VPP infrastructure rebuilt after restart")
				}
				reconcileDone = nil
				reconcileCancel = nil
			default:
			}
		}

		reconnected := false
		pingCtx, cancel := context.WithTimeout(ctx, vppWatchInterval)
		pingErr := s.networks.Ping(pingCtx)
		identity, identityErr := s.networks.Identity()
		cancel()

		if pingErr != nil {
			if healthy {
				s.logger.Warn().Err(pingErr).Msg("VPP API unavailable; waiting for it to return")
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
			s.logger.Info().Msg("VPP API is reachable again")
			healthy = true
			pendingReconcile = true
			reconnected = true
		}
		if identityErr == nil && identity != "" {
			if lastIdentity != "" && identity != lastIdentity {
				s.logger.Warn().
					Str("old_identity", lastIdentity).
					Str("new_identity", identity).
					Msg("VPP restart detected; rebuilding VPP resources")
				pendingReconcile = true
				pendingReset = true
				reconnected = true
				// Do not cancel the in-flight worker here. Starting a second
				// worker while the first can still receive VPP API responses
				// causes duplicate BVI creation (VPP aborts on duplicate
				// 'bviN-tx' interface nodes).
			}
			lastIdentity = identity
		}
		if reconnected {
			go s.runVPPStartupScript()
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
			s.logger.Warn().Err(err).Msg("reset VNF runtime after VPP restart failed")
		}
	}
}

// ReconcileNetworks ensures every recorded Rookery Network has its VPP bridge
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
		if _, err := s.ensureNetworkVNFResources(ctx, network, network.IPv4Gateway); err != nil {
			errs = append(errs, fmt.Errorf("network %s VNF resources: %w", network.Name, err))
			continue
		}
		s.logger.Info().
			Str("network", network.Name).
			Uint32("bd_id", network.BridgeDomainID).
			Msg("network bridge domain ready")
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
		if err := s.bindNetworkInterface(ctx, network, swIfIndex); err != nil {
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
			s.logger.Info().
				Str("interface", updated.Name).
				Str("network", updated.NetworkName).
				Uint32("sw_if_index", updated.SwIfIndex).
				Msg("vhost-user interface restored")
			if updated.AttachedVMID != nil && s.vms != nil && s.vms.Running(*updated.AttachedVMID) {
				s.logger.Warn().
					Str("interface", updated.Name).
					Str("vm", updated.AttachedVMName).
					Msg("running VM must be restarted to reconnect its restored vhost-user interface")
			}
		}
	}
	return errors.Join(errs...)
}

// ReconcileHostInterfaces recreates persisted host NIC takeovers after a VPP
// restart and refreshes their sw_if_index.
func (s *Server) ReconcileHostInterfaces(ctx context.Context) error {
	if s.nat == nil || s.networks == nil {
		return nil
	}
	records, err := s.store.ListHostInterfaceRecords(ctx)
	if err != nil {
		return fmt.Errorf("list host interfaces for reconciliation: %w", err)
	}
	var errs []error
	for _, record := range records {
		name, err := s.nat.EnsureHostInterface(ctx, record.NIC)
		if err != nil {
			s.logger.Warn().Str("nic", record.NIC).Err(err).Msg("host interface reconciliation failed")
			errs = append(errs, fmt.Errorf("host interface %s: %w", record.NIC, err))
			continue
		}
		if interfaces, err := s.networks.VPPInterfaces(ctx); err == nil {
			for _, iface := range interfaces {
				if iface.Name == name {
					if err := s.store.SetHostInterfaceRuntime(ctx, record.NIC, iface.SwIfIndex); err != nil {
						errs = append(errs, fmt.Errorf("host interface %s metadata: %w", record.NIC, err))
					}
					break
				}
			}
		}
	}
	return errors.Join(errs...)
}

// ReconcileNAT44s restores enabled NAT44 VNFs and their BVI/outside state.// ReconcileNAT44s restores enabled NAT44 VNFs and their BVI/outside state.
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
		if vnf.Type != store.VNFTypeNAT44 || !vnf.Enabled || s.vnfRunning(ctx, vnf) {
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
	if !current.Enabled || s.vnfRunning(ctx, current) {
		return nil
	}
	activated, err := s.activateNAT44(ctx, current)
	if err != nil {
		return err
	}
	s.logger.Info().
		Str("vnf", activated.Name).
		Str("network", activated.NetworkName).
		Msg("NAT44 activated during startup reconciliation")
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
		if !vnf.Enabled || vnf.Type == store.VNFTypeNAT44 || s.vnfs.Running(ctx, vnf.ID) {
			continue
		}
		if err := s.reconcileVNF(ctx, vnf); err != nil {
			errs = append(errs, fmt.Errorf("VNF %s: %w", vnf.Name, err))
		}
	}
	return errors.Join(errs...)
}

// reconcileVNFBindings moves already-running VNF interfaces into their
// network's VRF. New/recreated interfaces are bound during activation; this
// covers VNFs that survived an API upgrade without a VPP restart.
func (s *Server) reconcileVNFBindings(ctx context.Context) error {
	if s.networks == nil || s.vnfs == nil {
		return nil
	}
	vnfs, err := s.store.ListVNFs(ctx, nil)
	if err != nil {
		return fmt.Errorf("list VNFs for VRF binding: %w", err)
	}
	var errs []error
	for _, vnf := range vnfs {
		if !vnf.Enabled || vnf.Type == store.VNFTypeNAT44 || !s.vnfs.Running(ctx, vnf.ID) {
			continue
		}
		network, err := s.store.GetNetwork(ctx, vnf.NetworkID)
		if err != nil {
			errs = append(errs, fmt.Errorf("VNF %s network: %w", vnf.Name, err))
			continue
		}
		network, err = s.ensureNetworkRouteTable(ctx, network)
		if err != nil {
			errs = append(errs, fmt.Errorf("VNF %s: %w", vnf.Name, err))
			continue
		}
		for _, swIfIndex := range []uint32{vnf.TapSwIfIndex, vnf.MemifSwIfIndex} {
			if swIfIndex == 0 {
				continue
			}
			if err := s.bindNetworkInterface(ctx, network, swIfIndex); err != nil {
				errs = append(errs, fmt.Errorf("VNF %s: %w", vnf.Name, err))
			}
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
	if !vnf.Enabled || s.vnfs.Running(ctx, vnf.ID) {
		return nil
	}
	if s.memifDHCP && vnf.Type == store.VNFTypeDHCPv4 {
		network, err := s.store.GetNetwork(ctx, vnf.NetworkID)
		if err == nil && s.effectiveVNFTransport(network, vnf) == vnfTransportShared {
			updated, err := s.activateSharedVNF(ctx, vnf, network)
			if err != nil {
				return err
			}
			s.logger.Info().
				Str("vnf", updated.Name).
				Str("type", updated.Type).
				Str("memif", updated.MemifSocket).
				Str("address", updated.Config.BindIP).
				Msg("VNF restored on shared memif during reconciliation")
			return nil
		}
	}

	// The memif transport is now the default. Migrate an enabled DHCPv4 VNF
	// that still has tap-era runtime fields or an old memif socket path.
	if s.memifDHCP && vnf.Type == store.VNFTypeDHCPv4 &&
		(vnf.MemifSocket == "" || vnf.MemifSocket != s.memifSocketPath(vnf.ID) ||
			vnf.MemifHostName != vnfInterfaceName(vnf) ||
			vnf.TapHostName != "" || vnf.TapSwIfIndex != 0) {
		if err := s.stopVNF(ctx, vnf, false); err != nil {
			s.logger.Warn().Str("vnf", vnf.Name).Err(err).Msg("cleanup of old VNF runtime failed")
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
		s.logger.Info().
			Str("vnf", activated.Name).
			Str("type", activated.Type).
			Str("memif", activated.MemifSocket).
			Msg("VNF migrated to memif")
		return nil
	}

	if (vnf.MemifSwIfIndex > 0 || vnf.MemifSocket != "") && vnf.TapHostName == "" {
		if err := s.vnfs.StartMemif(ctx, vnf, vnf.MemifSocket, uint32(vnf.ID)); err == nil {
			s.logger.Info().
				Str("vnf", vnf.Name).
				Str("type", vnf.Type).
				Str("memif", vnf.MemifSocket).
				Msg("VNF runtime restored on memif")
			return nil
		} else {
			s.logger.Warn().
				Str("vnf", vnf.Name).
				Str("memif", vnf.MemifSocket).
				Err(err).
				Msg("VNF memif runtime could not be restored; recreating")
		}
		if err := s.stopVNF(ctx, vnf, false); err != nil {
			s.logger.Warn().Str("vnf", vnf.Name).Err(err).Msg("cleanup of stale VNF memif failed")
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
			s.logger.Info().
				Str("vnf", vnf.Name).
				Str("type", vnf.Type).
				Str("interface", vnf.TapHostName).
				Msg("VNF runtime restored")
			return nil
		} else {
			s.logger.Warn().
				Str("vnf", vnf.Name).
				Str("interface", vnf.TapHostName).
				Err(err).
				Msg("VNF runtime could not use its existing tap; recreating")
		}

		// Remove the old runtime and tap before provisioning a replacement.
		if err := s.stopVNF(ctx, vnf, false); err != nil {
			s.logger.Warn().Str("vnf", vnf.Name).Err(err).Msg("cleanup of stale VNF runtime failed")
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
	s.logger.Info().
		Str("vnf", activated.Name).
		Str("type", activated.Type).
		Str("interface", activated.TapHostName).
		Msg("VNF activated during startup reconciliation")
	return nil
}
