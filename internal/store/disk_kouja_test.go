package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/store"
)

func testISCSIReference() blockdev.Reference {
	return blockdev.Reference{
		Protocol: blockdev.ProtocolISCSI,
		ISCSI: &blockdev.ISCSIReference{
			Portal:       "10.0.0.2:3260",
			ExportPortal: "10.0.0.1:3260",
			Target:       "iqn.2026-11.io.example:disk",
			LUN:          0,
			Initiator:    "iqn.2026-11.io.example:rookery",
		},
	}
}

func TestKoujaDiskMetadataRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	next := time.Now().UTC().Add(20 * time.Minute).Truncate(time.Millisecond)
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name:                  "kouja-data",
		SizeGB:                10,
		SizeBytes:             10 << 30,
		Source:                store.DiskSourceKouja,
		Protocol:              string(blockdev.ProtocolISCSI),
		DeletionState:         store.DiskDeletionProvisioning,
		DeletionNextAttemptAt: next,
		BackendMetadata: store.DiskBackendMetadata{
			Version: 1,
			Driver:  store.DiskSourceKouja,
			Volume: store.VolumeMetadata{
				VolumeID:     "volume-1",
				RequestID:    "request-1",
				ResourceID:   "resource-1",
				Owner:        "spiffe://kouja/caller/rookery",
				StorageClass: "nvme",
			},
			Attachment: testISCSIReference(),
		},
	})
	if err != nil {
		t.Fatalf("create kouja disk: %v", err)
	}
	if disk.Device != "" || disk.VG != "" || disk.ThinPool != "" {
		t.Fatalf("kouja disk has local fields: %+v", disk)
	}
	if disk.Protocol != string(blockdev.ProtocolISCSI) || disk.SizeBytes != 10<<30 {
		t.Fatalf("kouja disk protocol/size = %+v", disk)
	}
	if disk.DeletionState != store.DiskDeletionProvisioning || disk.DeletionNextAttemptAt.IsZero() {
		t.Fatalf("kouja disk deletion state = %+v", disk)
	}
	if disk.BackendMetadata.Volume.ResourceID != "resource-1" || disk.BackendMetadata.Attachment.ISCSI == nil {
		t.Fatalf("kouja disk metadata = %+v", disk.BackendMetadata)
	}

	got, err := st.GetDisk(ctx, disk.ID)
	if err != nil {
		t.Fatalf("get kouja disk: %v", err)
	}
	if got.BackendMetadata.Attachment.ISCSI.Target != "iqn.2026-11.io.example:disk" {
		t.Fatalf("metadata target = %+v", got.BackendMetadata)
	}
	if got.DeletionNextAttemptAt.UnixMilli() != next.UnixMilli() {
		t.Fatalf("next attempt = %v, want %v", got.DeletionNextAttemptAt, next)
	}
}

func TestCephDiskProtocolDefault(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "ceph-data", SizeGB: 10, Source: store.DiskSourceCeph,
		BackendMetadata: store.DiskBackendMetadata{
			Driver: store.DiskSourceCeph,
			Attachment: blockdev.Reference{
				Protocol: blockdev.ProtocolRBD,
				RBD: &blockdev.RBDReference{
					Monitors: []string{"mon1:6789"},
					Pool:     "rbd",
					Image:    "disk",
					User:     "rookery",
					Keyring:  "/etc/ceph/rookery.keyring",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create ceph disk: %v", err)
	}
	if disk.Protocol != string(blockdev.ProtocolRBD) || disk.Device != "" {
		t.Fatalf("ceph disk = %+v", disk)
	}
}

func TestDiskProtocolNormalization(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	local, err := st.CreateDisk(ctx, store.Disk{Name: "local", SizeGB: 5, VG: "vg0", ThinPool: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	if local.Protocol != string(blockdev.ProtocolLocal) || local.Device != "/dev/vg0/local" {
		t.Fatalf("local disk = %+v", local)
	}
	host, err := st.CreateDisk(ctx, store.Disk{
		Name: "host", SizeGB: 1, Device: "/dev/mapper/host", Source: store.DiskSourceHostDevice,
	})
	if err != nil {
		t.Fatal(err)
	}
	if host.Protocol != string(blockdev.ProtocolLocal) || host.Device != "/dev/mapper/host" {
		t.Fatalf("host disk = %+v", host)
	}
}

func TestReadyRemoteDiskRequiresCompleteAttachment(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	_, err := st.CreateDisk(ctx, store.Disk{
		Name: "incomplete", SizeGB: 5, Source: store.DiskSourceKouja,
		Protocol: string(blockdev.ProtocolISCSI),
		BackendMetadata: store.DiskBackendMetadata{
			Driver:     store.DiskSourceKouja,
			Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI},
		},
	})
	if err == nil {
		t.Fatal("CreateDisk accepted a ready iSCSI disk without an attachment body")
	}
}

func TestRemoteDiskCannotBeTemplate(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "remote", SizeGB: 5, Source: store.DiskSourceKouja,
		BackendMetadata: store.DiskBackendMetadata{
			Driver: store.DiskSourceKouja, Attachment: testISCSIReference(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetDiskTemplate(ctx, disk.ID, true); err == nil {
		t.Fatal("SetDiskTemplate accepted a kouja disk")
	}
	if _, err := st.SetDiskProvisioning(ctx, disk.ID, "/bin/true", "1m"); err == nil {
		t.Fatal("SetDiskProvisioning accepted a kouja disk")
	}
}

func TestDeletingDiskCannotAttach(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	base, err := st.CreateDisk(ctx, store.Disk{Name: "base", SizeGB: 5, VG: "vg0", ThinPool: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateVM(ctx, store.VM{
		Name: "vm", Status: store.StatusStopped, VCPUs: 1, MemoryMB: 512, DiskIDs: []int64{base.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{
		store.DiskDeletionProvisioning, store.DiskDeletionDeleting, store.DiskDeletionFailed,
	} {
		disk, err := st.CreateDisk(ctx, store.Disk{
			Name: "pending-" + state, SizeGB: 5, VG: "vg0", ThinPool: "pool",
			DeletionState: state,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateVM(ctx, store.VM{
			Name: "vm-" + state, VCPUs: 1, MemoryMB: 512, DiskIDs: []int64{disk.ID},
		}); !errors.Is(err, store.ErrDiskDeleting) {
			t.Fatalf("CreateVM with %s disk error = %v, want ErrDiskDeleting", state, err)
		}
		if _, err := st.SetVMDisks(ctx, vm.ID, []int64{disk.ID}); !errors.Is(err, store.ErrDiskDeleting) {
			t.Fatalf("SetVMDisks with %s disk error = %v, want ErrDiskDeleting", state, err)
		}
	}
}

func TestFinalizeProvisioningCAS(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "provisioning", SizeGB: 5, Source: store.DiskSourceKouja,
		DeletionState: store.DiskDeletionProvisioning,
		BackendMetadata: store.DiskBackendMetadata{
			Driver:     store.DiskSourceKouja,
			Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata := store.DiskBackendMetadata{
		Version:    1,
		Driver:     store.DiskSourceKouja,
		Volume:     store.VolumeMetadata{VolumeID: "volume-1", ResourceID: "resource-1"},
		Attachment: testISCSIReference(),
	}
	finalized, ok, err := st.FinalizeProvisioning(ctx, disk.ID, metadata, 5<<30, "", true)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if !ok || finalized.DeletionState != "" || finalized.Protocol != string(blockdev.ProtocolISCSI) ||
		finalized.SizeBytes != 5<<30 || !finalized.PreparedByScript {
		t.Fatalf("finalized disk = %+v, ok=%t", finalized, ok)
	}
	if finalized.BackendMetadata.Attachment.ISCSI == nil {
		t.Fatalf("finalized metadata = %+v", finalized.BackendMetadata)
	}
	if _, ok, err := st.FinalizeProvisioning(ctx, disk.ID, metadata, 5<<30, "", true); err != nil || ok {
		t.Fatalf("second finalize = ok %t, err %v; want false/nil", ok, err)
	}
}

func TestClaimDueDiskDeletions(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "delete-me", SizeGB: 5, VG: "vg0", ThinPool: "pool",
		DeletionState:         store.DiskDeletionDeleting,
		DeletionNextAttemptAt: now.Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimDueDiskDeletions(ctx, now, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != disk.ID || claimed[0].DeletionClaimedAt.IsZero() {
		t.Fatalf("claimed = %+v", claimed)
	}
	if again, err := st.ClaimDueDiskDeletions(ctx, now.Add(time.Second), 10); err != nil || len(again) != 0 {
		t.Fatalf("claim while leased = %+v, err=%v", again, err)
	}
	if err := st.ReleaseDiskDeletionClaim(ctx, disk.ID); err != nil {
		t.Fatal(err)
	}
	released, err := st.ClaimDueDiskDeletions(ctx, now.Add(2*time.Second), 10)
	if err != nil || len(released) != 1 {
		t.Fatalf("reclaim after release = %+v, err=%v", released, err)
	}

	next := now.Add(time.Hour)
	if err := st.RecordDiskDeletionFailure(ctx, disk.ID, 3, next, "session active"); err != nil {
		t.Fatal(err)
	}
	if due, err := st.ClaimDueDiskDeletions(ctx, now.Add(3*time.Second), 10); err != nil || len(due) != 0 {
		t.Fatalf("claim before backoff = %+v, err=%v", due, err)
	}
	if due, err := st.ClaimDueDiskDeletions(ctx, next.Add(time.Second), 10); err != nil || len(due) != 1 {
		t.Fatalf("claim after backoff = %+v, err=%v", due, err)
	}
	if err := st.SetDiskDeleteFailed(ctx, disk.ID, "permanent"); err != nil {
		t.Fatal(err)
	}
	failed, err := st.GetDisk(ctx, disk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.DeletionState != store.DiskDeletionFailed || failed.DeletionError != "permanent" {
		t.Fatalf("failed disk = %+v", failed)
	}
	if err := st.ResetDiskDeletion(ctx, disk.ID); err != nil {
		t.Fatal(err)
	}
	reset, err := st.GetDisk(ctx, disk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reset.DeletionState != store.DiskDeletionDeleting || reset.DeletionAttempts != 0 {
		t.Fatalf("reset disk = %+v", reset)
	}
	count, _, err := st.DueDiskDeletionCount(ctx, time.Now().UTC().Add(time.Second))
	if err != nil || count != 1 {
		t.Fatalf("backlog count = %d, err=%v", count, err)
	}
}
