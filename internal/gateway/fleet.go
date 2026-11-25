package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/fleet"
	"github.com/voidstarit/rookery/internal/store"
)

const (
	fleetMaxActionsPerPass = 10
	fleetMaxMemberAttempts = 3
	fleetPassTimeout       = 30 * time.Minute
	fleetStartBackoff      = 5 * time.Second
	fleetMaxBackoff        = 5 * time.Minute
)

type fleetController struct {
	store      *store.Store
	lightvms   *lightVMService
	disks      *diskService
	interfaces *interfaceService
	guard      fleet.Guard
	wake       chan struct{}
	mu         sync.Mutex
}

func newFleetController(st *store.Store, guard fleet.Guard, lightvms *lightVMService, disks *diskService, interfaces *interfaceService) *fleetController {
	return &fleetController{
		store: st, guard: guard, lightvms: lightvms, disks: disks, interfaces: interfaces,
		wake: make(chan struct{}, 1),
	}
}

func (c *fleetController) WakeFleet() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *fleetController) FleetWake() <-chan struct{} {
	return c.wake
}

func (c *fleetController) ReconcileFleet(ctx context.Context, guard fleet.Guard) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lightvms == nil {
		return nil
	}
	passCtx, cancel := context.WithTimeout(ctx, fleetPassTimeout)
	defer cancel()
	groups, err := c.store.ListLightVMGroups(passCtx)
	if err != nil {
		return fmt.Errorf("list groups: %w", err)
	}
	budget := fleetMaxActionsPerPass
	epoch := uint64(0)
	if guard != nil {
		epoch = guard.Epoch()
	}
	for _, group := range groups {
		if budget <= 0 || passCtx.Err() != nil {
			break
		}
		if err := c.reconcileGroup(passCtx, guard, epoch, group.ID, &budget); err != nil {
			log.Warn().Err(err).Int64("group_id", group.ID).Msg("fleet: group reconcile failed")
		}
	}
	return nil
}

func (c *fleetController) canProvision(guard fleet.Guard, epoch uint64) bool {
	return guard != nil && epoch != 0 && guard.CanProvision(epoch)
}

func (c *fleetController) reconcileGroup(ctx context.Context, guard fleet.Guard, epoch uint64, groupID int64, budget *int) error {
	group, err := c.store.GetLightVMGroup(ctx, groupID)
	if err != nil {
		return err
	}
	allowStart := c.canProvision(guard, epoch) && !group.Paused
	members := group.Members
	lastError := ""

	if group.State == "deleting" || group.ForceDelete {
		for i := range members {
			if *budget <= 0 {
				break
			}
			member := members[i]
			if member.State != "removing" {
				if err := c.store.SetGroupMemberState(ctx, member.ID, "removing"); err != nil {
					lastError = err.Error()
					break
				}
			}
			if err := c.cleanupMember(ctx, member, group.ForceDelete); err != nil {
				lastError = err.Error()
				continue
			}
			*budget--
		}
		remaining, _ := c.store.ListGroupMembers(ctx, groupID)
		if len(remaining) == 0 {
			if err := c.store.DeleteLightVMGroup(ctx, groupID); err != nil && !errors.Is(err, store.ErrLightVMGroupNotFound) {
				return err
			}
			return nil
		}
		return c.store.UpdateGroupReconcile(ctx, groupID, time.Now().UTC(), lastError, 0, time.Time{})
	}

	// Scale in first, preferring failed members that never produced a workload.
	if len(members) > int(group.DesiredCount) {
		excess := len(members) - int(group.DesiredCount)
		for _, member := range members {
			if excess <= 0 || *budget <= 0 {
				break
			}
			if member.State == "removing" {
				continue
			}
			if member.State != "provisioning" {
				continue
			}
			if _, err := c.store.GetLightVMByGroupMemberID(ctx, member.ID); !errors.Is(err, store.ErrLightVMNotFound) {
				continue
			}
			if err := c.store.SetGroupMemberState(ctx, member.ID, "removing"); err != nil {
				lastError = err.Error()
				break
			}
			if err := c.cleanupMember(ctx, member, false); err != nil {
				lastError = err.Error()
				continue
			}
			excess--
			*budget--
		}
		for _, member := range members {
			if excess <= 0 || *budget <= 0 {
				break
			}
			if member.State != "active" {
				continue
			}
			if err := c.store.SetGroupMemberState(ctx, member.ID, "removing"); err != nil {
				lastError = err.Error()
				break
			}
			if err := c.cleanupMember(ctx, member, false); err != nil {
				lastError = err.Error()
				continue
			}
			excess--
			*budget--
		}
		members, _ = c.store.ListGroupMembers(ctx, groupID)
	}

	for i := range members {
		if *budget <= 0 {
			break
		}
		member := members[i]
		switch member.State {
		case "removing":
			if err := c.cleanupMember(ctx, member, false); err != nil {
				lastError = err.Error()
				continue
			}
			*budget--
		case "provisioning":
			if err := c.reconcileProvisioning(ctx, member, guard, epoch, allowStart); err != nil {
				lastError = err.Error()
			} else {
				*budget--
			}
		case "active":
			if err := c.reconcileActive(ctx, member, guard, epoch, allowStart); err != nil {
				lastError = err.Error()
			} else {
				*budget--
			}
		}
	}

	members, _ = c.store.ListGroupMembers(ctx, groupID)
	tracked := len(members)
	for tracked < int(group.DesiredCount) && *budget > 0 && allowStart {
		member, created, err := c.createMemberIntent(ctx, group)
		if err != nil {
			lastError = err.Error()
			break
		}
		if !created {
			break
		}
		if err := c.provisionMember(ctx, member, guard, epoch, allowStart); err != nil {
			lastError = err.Error()
		}
		*budget--
		tracked++
	}
	return c.store.UpdateGroupReconcile(ctx, groupID, time.Now().UTC(), lastError, 0, time.Time{})
}

func (c *fleetController) createMemberIntent(ctx context.Context, group store.LightVMGroup) (store.LightVMGroupMember, bool, error) {
	for attempt := 0; attempt < fleetMaxMemberAttempts; attempt++ {
		member, created, err := c.store.CreateGroupMemberIntent(
			ctx, group.ID, memberName(group.Name), group.TemplateVersionID,
			group.TemplateVersion, "running",
		)
		if errors.Is(err, store.ErrLightVMGroupMemberNameTaken) {
			continue
		}
		return member, created, err
	}
	return store.LightVMGroupMember{}, false, errors.New("could not allocate a unique member name")
}

func (c *fleetController) reconcileProvisioning(ctx context.Context, member store.LightVMGroupMember, guard fleet.Guard, epoch uint64, allowStart bool) error {
	if !member.NextRetryAt.IsZero() && time.Now().Before(member.NextRetryAt) {
		return nil
	}
	if _, err := c.store.GetLightVMByGroupMemberID(ctx, member.ID); err == nil {
		if err := c.store.SetGroupMemberState(ctx, member.ID, "active"); err != nil {
			return err
		}
		member.State = "active"
		return c.reconcileActive(ctx, member, guard, epoch, allowStart)
	} else if !errors.Is(err, store.ErrLightVMNotFound) {
		return err
	}
	if err := c.cleanupOwnedResources(ctx, member.ID, member.ID, false); err != nil {
		return c.memberBackoff(ctx, member, err)
	}
	return c.provisionMember(ctx, member, guard, epoch, allowStart)
}

func (c *fleetController) reconcileActive(ctx context.Context, member store.LightVMGroupMember, guard fleet.Guard, epoch uint64, allowStart bool) error {
	vm, err := c.store.GetLightVMByGroupMemberID(ctx, member.ID)
	if errors.Is(err, store.ErrLightVMNotFound) {
		return c.store.SetGroupMemberState(ctx, member.ID, "provisioning")
	}
	if err != nil {
		return err
	}
	running := c.lightvms.runtime != nil && c.lightvms.runtime.RunningLightVM(vm.ID)
	switch member.DesiredState {
	case store.DesiredStopped:
		if running {
			if _, err := c.lightvms.StopLightVM(ctx, &rookeryv1.StopLightVMRequest{Id: vm.ID}); err != nil {
				return err
			}
		}
	case store.DesiredRunning:
		if !running {
			if !allowStart {
				return nil
			}
			if _, err := c.lightvms.StartLightVM(ctx, &rookeryv1.StartLightVMRequest{Id: vm.ID}); err != nil {
				switch status.Code(err) {
				case codes.Aborted:
					return nil
				case codes.AlreadyExists:
					// QEMU won the race; treat it as a successful start.
				default:
					return c.startBackoff(ctx, member, err)
				}
			}
		}
		if running && (member.RestartAttempts != 0 || member.LastError != "") {
			_ = c.store.SetGroupMemberRestart(ctx, member.ID, 0, time.Time{}, "")
		}
	}
	if member.State != "active" {
		return c.store.SetGroupMemberState(ctx, member.ID, "active")
	}
	return nil
}

func (c *fleetController) provisionMember(ctx context.Context, member store.LightVMGroupMember, guard fleet.Guard, epoch uint64, allowStart bool) error {
	version, err := c.store.GetLightVMTemplateVersionByID(ctx, member.TemplateVersionID)
	if err != nil {
		return c.memberBackoff(ctx, member, err)
	}
	suffix := randomSuffix()
	diskIDs := make([]int64, 0, len(version.Spec.Disks))
	diskSerials := make(map[int64]string)
	for i, entry := range version.Spec.Disks {
		name := resourceName(member.Name, "d", i) + "-r" + suffix
		disk, err := c.createOwnedDisk(ctx, member.ID, name, entry)
		if err != nil {
			_ = c.cleanupOwnedResources(ctx, member.ID, member.ID, false)
			return c.memberBackoff(ctx, member, err)
		}
		diskIDs = append(diskIDs, disk.ID)
		if entry.Serial != "" {
			diskSerials[disk.ID] = entry.Serial
		}
	}
	interfaceIDs := make([]int64, 0, len(version.Spec.Interfaces))
	for i, entry := range version.Spec.Interfaces {
		name := resourceName(member.Name, "n", i) + "-r" + suffix
		ifaceID, err := c.createOwnedInterface(ctx, member.ID, name, entry)
		if err != nil {
			_ = c.cleanupOwnedResources(ctx, member.ID, member.ID, false)
			return c.memberBackoff(ctx, member, err)
		}
		interfaceIDs = append(interfaceIDs, ifaceID)
	}
	vm := store.LightVM{
		Name: member.Name, Description: "managed group member",
		VCPUs: version.Spec.VCPUs, MemoryMB: version.Spec.MemoryMB,
		CgroupClass: version.Spec.CgroupClass, KernelID: version.Spec.KernelID,
		InitramfsID: version.Spec.InitramfsID, Image: version.Spec.Image,
		Env: version.Spec.Env, ExecutablePath: version.Spec.ExecutablePath,
		ExecutableArgs: version.Spec.ExecutableArgs, ExecutableScript: version.Spec.ExecutableScript,
		Append: version.Spec.Append, ConsoleEnabled: version.Spec.ConsoleEnabled,
		Consoles: version.Spec.Consoles, VirtioFSShares: version.Spec.VirtioFSShares,
		DiskIDs: diskIDs, DiskSerials: diskSerials, InterfaceIDs: interfaceIDs,
		GroupMemberID: &member.ID, Status: store.StatusStopped, DesiredState: member.DesiredState,
	}
	created, err := c.lightvms.createLightVMDefinition(ctx, vm)
	if err != nil {
		_ = c.cleanupOwnedResources(ctx, member.ID, member.ID, false)
		return c.memberBackoff(ctx, member, err)
	}
	if member.DesiredState == store.DesiredRunning && allowStart {
		if _, err := c.lightvms.StartLightVM(ctx, &rookeryv1.StartLightVMRequest{Id: created.ID}); err != nil {
			if status.Code(err) != codes.AlreadyExists {
				_ = c.store.SetGroupMemberState(ctx, member.ID, "active")
				return c.startBackoff(ctx, member, err)
			}
		}
	}
	if err := c.store.SetGroupMemberState(ctx, member.ID, "active"); err != nil {
		return err
	}
	_ = c.store.SetGroupMemberRetry(ctx, member.ID, 0, time.Time{}, "")
	_ = c.store.SetGroupMemberRestart(ctx, member.ID, 0, time.Time{}, "")
	return nil
}

func (c *fleetController) createOwnedDisk(ctx context.Context, memberID int64, name string, entry store.LightVMTemplateDisk) (store.Disk, error) {
	if c.disks == nil {
		return store.Disk{}, errors.New("disk service is not configured")
	}
	size := entry.SizeGB
	backendID := entry.StorageBackendID
	request := &rookeryv1.CreateDiskRequest{
		Name: name, SizeGb: &size, StorageBackendId: &backendID,
	}
	if entry.ThrottleClassID != nil {
		request.ThrottleClassId = *entry.ThrottleClassID
	}
	created, err := c.disks.CreateDisk(ctx, request)
	if err != nil {
		return store.Disk{}, err
	}
	if err := c.store.SetDiskGroupMember(ctx, created.GetId(), &memberID); err != nil {
		_, _ = c.disks.DeleteDisk(ctx, &rookeryv1.DeleteDiskRequest{Id: created.GetId(), Force: true})
		return store.Disk{}, err
	}
	disk, err := c.store.GetDisk(ctx, created.GetId())
	if err != nil {
		return store.Disk{}, err
	}
	return disk, nil
}

func (c *fleetController) createOwnedInterface(ctx context.Context, memberID int64, name string, entry store.LightVMTemplateInterface) (int64, error) {
	if c.interfaces == nil {
		return 0, errors.New("interface service is not configured")
	}
	created, err := c.interfaces.CreateInterface(ctx, &rookeryv1.CreateInterfaceRequest{
		Name: name, NetworkId: entry.NetworkID,
	})
	if err != nil {
		return 0, err
	}
	if err := c.store.SetInterfaceGroupMember(ctx, created.GetId(), &memberID); err != nil {
		_, _ = c.interfaces.DeleteInterface(ctx, &rookeryv1.DeleteInterfaceRequest{Id: created.GetId()})
		return 0, err
	}
	if err := c.applyInterfaceACLs(ctx, created.GetId(), entry.IngressACLIDs, entry.EgressACLIDs); err != nil {
		_, _ = c.interfaces.DeleteInterface(ctx, &rookeryv1.DeleteInterfaceRequest{Id: created.GetId()})
		return 0, err
	}
	return created.GetId(), nil
}

func (c *fleetController) applyInterfaceACLs(ctx context.Context, interfaceID int64, ingressIDs, egressIDs []int64) error {
	if len(ingressIDs) == 0 && len(egressIDs) == 0 {
		return nil
	}
	names := func(ids []int64) ([]string, error) {
		result := make([]string, 0, len(ids))
		for _, id := range ids {
			acl, err := c.store.GetACL(ctx, id)
			if err != nil {
				return nil, err
			}
			result = append(result, acl.Name)
		}
		return result, nil
	}
	ingress, err := names(ingressIDs)
	if err != nil {
		return err
	}
	egress, err := names(egressIDs)
	if err != nil {
		return err
	}
	_, err = c.interfaces.SetInterfaceACLs(ctx, &rookeryv1.SetInterfaceACLsRequest{
		Id: interfaceID, IngressAcls: ingress, EgressAcls: egress,
	})
	return err
}

func (c *fleetController) cleanupMember(ctx context.Context, member store.LightVMGroupMember, force bool) error {
	vm, err := c.store.GetLightVMByGroupMemberID(ctx, member.ID)
	orphan := false
	if err == nil {
		if c.lightvms.runtime != nil && c.lightvms.runtime.RunningLightVM(vm.ID) {
			if _, stopErr := c.lightvms.StopLightVM(ctx, &rookeryv1.StopLightVMRequest{Id: vm.ID}); stopErr != nil {
				if !force {
					return stopErr
				}
				if c.lightvms.runtime.RunningLightVM(vm.ID) {
					return errors.New("force cleanup: QEMU is still running")
				}
			}
		}
		if _, deleteErr := c.lightvms.DeleteLightVM(ctx, &rookeryv1.DeleteLightVMRequest{Id: vm.ID}); deleteErr != nil {
			if !force {
				return deleteErr
			}
			_, _ = c.lightvms.store.SetLightVMDesiredState(ctx, vm.ID, store.DesiredStopped)
			_ = c.store.ClearLightVMGroupOwner(ctx, vm.ID)
			orphan = true
		}
	} else if !errors.Is(err, store.ErrLightVMNotFound) {
		return err
	}
	if !orphan {
		if err := c.cleanupOwnedResources(ctx, member.ID, member.ID, force); err != nil {
			return err
		}
	}
	return c.store.DeleteGroupMember(ctx, member.ID)
}

func (c *fleetController) cleanupOwnedResources(ctx context.Context, memberID, _ int64, force bool) error {
	interfaces, err := c.store.ListInterfacesByGroupMemberID(ctx, memberID)
	if err != nil {
		return err
	}
	for _, iface := range interfaces {
		if c.interfaces != nil {
			if _, deleteErr := c.interfaces.DeleteInterface(ctx, &rookeryv1.DeleteInterfaceRequest{Id: iface.ID}); deleteErr != nil {
				if !force {
					return deleteErr
				}
				_ = c.store.SetInterfaceGroupMember(ctx, iface.ID, nil)
			}
		} else {
			_ = c.store.SetInterfaceGroupMember(ctx, iface.ID, nil)
		}
	}
	disks, err := c.store.ListDisksByGroupMemberID(ctx, memberID)
	if err != nil {
		return err
	}
	for _, disk := range disks {
		if disk.DeletionState == "deleting" || disk.DeletionState == "delete_failed" {
			continue
		}
		if c.disks != nil {
			if _, deleteErr := c.disks.DeleteDisk(ctx, &rookeryv1.DeleteDiskRequest{Id: disk.ID, Force: force}); deleteErr != nil {
				if !force {
					return deleteErr
				}
				_ = c.store.SetDiskGroupMember(ctx, disk.ID, nil)
			}
		} else {
			_ = c.store.SetDiskGroupMember(ctx, disk.ID, nil)
		}
	}
	return nil
}

func (c *fleetController) memberBackoff(ctx context.Context, member store.LightVMGroupMember, cause error) error {
	attempts := member.RetryAttempts + 1
	delay := backoffDelay(attempts)
	_ = c.store.SetGroupMemberRetry(ctx, member.ID, attempts, time.Now().Add(delay), cause.Error())
	_ = c.store.SetGroupMemberState(ctx, member.ID, "provisioning")
	return nil
}

func (c *fleetController) startBackoff(ctx context.Context, member store.LightVMGroupMember, cause error) error {
	attempts := member.RestartAttempts + 1
	delay := backoffDelay(attempts)
	_ = c.store.SetGroupMemberRestart(ctx, member.ID, attempts, time.Now().Add(delay), cause.Error())
	_ = c.store.SetGroupMemberState(ctx, member.ID, "active")
	return nil
}

func backoffDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := fleetStartBackoff
	for i := 1; i < attempts && delay < fleetMaxBackoff; i++ {
		delay *= 2
	}
	if delay > fleetMaxBackoff {
		delay = fleetMaxBackoff
	}
	return delay
}

func memberName(groupName string) string {
	prefix := strings.TrimSpace(groupName)
	if len(prefix) > 40 {
		prefix = prefix[:40]
	}
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()&0xffffffff)
	}
	return prefix + "-" + hex.EncodeToString(random[:])
}

func resourceName(memberName, kind string, position int) string {
	name := fmt.Sprintf("%s-%s%d", memberName, kind, position)
	if len(name) > 55 {
		name = name[:55]
	}
	return name
}

func randomSuffix() string {
	var random [2]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Sprintf("%04x", time.Now().UnixNano()&0xffff)
	}
	return hex.EncodeToString(random[:])
}

var _ fleet.Reconciler = (*fleetController)(nil)
