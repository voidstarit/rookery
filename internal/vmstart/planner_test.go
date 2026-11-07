package vmstart

import (
	"context"
	"testing"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/storage"
	"github.com/voidstarit/rookery/internal/store"
)

type plannerDriver struct {
	kind string
}

func (d *plannerDriver) Kind() string { return d.kind }
func (d *plannerDriver) CreateDisk(context.Context, store.StorageBackend, storage.CreateDiskRequest) (storage.CreatedDisk, error) {
	return storage.CreatedDisk{}, nil
}
func (d *plannerDriver) DeleteDisk(context.Context, store.StorageBackend, store.Disk) error {
	return nil
}
func (d *plannerDriver) Health(context.Context, store.StorageBackend, *store.Disk) (storage.Health, error) {
	return storage.Health{Ready: true}, nil
}
func (d *plannerDriver) PrepareStart(_ context.Context, _ store.StorageBackend, disk store.Disk) (blockdev.Attachment, error) {
	return blockdev.Attachment{
		Protocol: blockdev.ProtocolISCSI,
		ISCSI: &blockdev.ISCSIAttachment{
			ISCSIReference: blockdev.ISCSIReference{Portal: "10.0.0.1:3260", Target: "iqn.x", LUN: 0},
			Username:       "kouja-" + disk.BackendMetadata.Volume.ResourceID,
			Credential:     "secret",
		},
	}, nil
}

func TestPlannerBuildsRemotePlan(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/planner.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	backend, err := st.CreateStorageBackend(ctx, store.StorageBackend{Name: "remote", Driver: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "remote", SizeGB: 5, StorageBackendID: &backend.ID,
		Source: store.DiskSourceKouja, Protocol: string(blockdev.ProtocolISCSI),
		BackendMetadata: store.DiskBackendMetadata{
			Driver: store.DiskSourceKouja,
			Volume: store.VolumeMetadata{VolumeID: "volume-1", ResourceID: "resource-1"},
			Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI, ISCSI: &blockdev.ISCSIReference{
				Portal: "10.0.0.1:3260", Target: "iqn.x", LUN: 0,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateVM(ctx, store.VM{Name: "vm", VCPUs: 1, MemoryMB: 512, DiskIDs: []int64{disk.ID}})
	if err != nil {
		t.Fatal(err)
	}
	planner := NewPlanner(st, storage.NewRegistry(&plannerDriver{kind: "fake"}))
	plan, err := planner.Plan(ctx, vm)
	if err != nil {
		t.Fatalf("Plan() = %v", err)
	}
	attachment, ok := plan.Attachments[disk.ID]
	if !ok || attachment.ISCSI == nil || attachment.ISCSI.Credential != "secret" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlannerLocalPlanIsEmpty(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/planner-local.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	disk, err := st.CreateDisk(ctx, store.Disk{Name: "local", SizeGB: 5, VG: "vg0", ThinPool: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateVM(ctx, store.VM{Name: "vm", VCPUs: 1, MemoryMB: 512, DiskIDs: []int64{disk.ID}})
	if err != nil {
		t.Fatal(err)
	}
	planner := NewPlanner(st, storage.NewRegistry())
	plan, err := planner.Plan(ctx, vm)
	if err != nil || len(plan.Attachments) != 0 {
		t.Fatalf("Plan() = %+v, %v", plan, err)
	}
}
