// Package vmstart builds runtime disk attachments before a VM starts.
package vmstart

import (
	"context"
	"fmt"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/storage"
	"github.com/voidstarit/rookery/internal/store"
)

// Planner builds a QEMU start plan from a VM's disk set.
type Planner struct {
	store    *store.Store
	registry *storage.Registry
}

// NewPlanner creates a planner.
func NewPlanner(st *store.Store, registry *storage.Registry) *Planner {
	return &Planner{store: st, registry: registry}
}

// Plan prepares attachments for every remote disk in vm.
func (p *Planner) Plan(ctx context.Context, vm store.VM) (qemu.StartPlan, error) {
	return p.PlanDisks(ctx, vmDisks(vm))
}

// PlanDisks prepares attachments for an ordered disk set.
func (p *Planner) PlanDisks(ctx context.Context, disks []store.VMDisk) (qemu.StartPlan, error) {
	plan := qemu.StartPlan{Attachments: make(map[int64]blockdev.Attachment)}
	if p == nil || p.store == nil || p.registry == nil {
		return plan, nil
	}
	for _, disk := range disks {
		protocol := blockdev.Protocol(disk.Protocol).Normalize()
		remote := disk.Source == store.DiskSourceKouja || disk.Source == store.DiskSourceCeph ||
			(disk.Protocol != "" && protocol != blockdev.ProtocolLocal)
		if !remote {
			continue
		}
		if disk.StorageBackendID == nil {
			return qemu.StartPlan{}, fmt.Errorf("disk %q has no storage backend", disk.Name)
		}
		backend, err := p.store.GetStorageBackend(ctx, *disk.StorageBackendID)
		if err != nil {
			return qemu.StartPlan{}, fmt.Errorf("read disk %q backend: %w", disk.Name, err)
		}
		driver, ok := p.registry.Driver(backend.Driver)
		if !ok {
			return qemu.StartPlan{}, fmt.Errorf("storage driver %q is not available", backend.Driver)
		}
		preparer, ok := driver.(storage.StartPreparer)
		if !ok {
			return qemu.StartPlan{}, fmt.Errorf("storage driver %q cannot prepare VM starts", backend.Driver)
		}
		diskRecord, err := p.store.GetDisk(ctx, disk.ID)
		if err != nil {
			return qemu.StartPlan{}, fmt.Errorf("read disk %q: %w", disk.Name, err)
		}
		attachment, err := preparer.PrepareStart(ctx, backend, diskRecord)
		if err != nil {
			return qemu.StartPlan{}, fmt.Errorf("prepare disk %q: %w", disk.Name, err)
		}
		plan.Attachments[disk.ID] = attachment
	}
	return plan, nil
}

func vmDisks(vm store.VM) []store.VMDisk {
	if len(vm.Disks) > 0 {
		return vm.Disks
	}
	if vm.Disk != nil {
		return []store.VMDisk{*vm.Disk}
	}
	return nil
}
