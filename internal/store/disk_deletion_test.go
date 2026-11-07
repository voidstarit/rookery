package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/store"
)

func TestDiskBackendMetadataEncoding(t *testing.T) {
	if _, err := store.DecodeDiskBackendMetadata(""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecodeDiskBackendMetadata("{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecodeDiskBackendMetadata("null"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecodeDiskBackendMetadata("{bad"); err == nil {
		t.Fatal("Decode accepted invalid JSON")
	}
	invalid := `{"attachment":{"protocol":"iscsi","rbd":{"pool":"p","image":"i"}}}`
	if _, err := store.DecodeDiskBackendMetadata(invalid); err == nil {
		t.Fatal("Decode accepted mismatched attachment")
	}
	zero, err := store.EncodeDiskBackendMetadata(store.DiskBackendMetadata{})
	if err != nil || zero != "{}" {
		t.Fatalf("Encode zero = %q, %v", zero, err)
	}
	encoded, err := store.EncodeDiskBackendMetadata(store.DiskBackendMetadata{Driver: "kouja"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := store.DecodeDiskBackendMetadata(encoded)
	if err != nil || decoded.Version != store.DiskBackendMetadataVersion {
		t.Fatalf("decoded = %+v, err=%v", decoded, err)
	}
}

func TestDiskDeletionNotFoundBranches(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	const missing = int64(99999)
	if err := st.MarkDiskDeleting(ctx, missing); !errors.Is(err, store.ErrDiskNotFound) {
		t.Fatalf("MarkDiskDeleting = %v", err)
	}
	if err := st.SetDiskDeleteFailed(ctx, missing, "x"); !errors.Is(err, store.ErrDiskNotFound) {
		t.Fatalf("SetDiskDeleteFailed = %v", err)
	}
	if err := st.RecordDiskDeletionFailure(ctx, missing, 1, time.Now(), "x"); !errors.Is(err, store.ErrDiskNotFound) {
		t.Fatalf("RecordDiskDeletionFailure = %v", err)
	}
	if err := st.ReleaseDiskDeletionClaim(ctx, missing); !errors.Is(err, store.ErrDiskNotFound) {
		t.Fatalf("ReleaseDiskDeletionClaim = %v", err)
	}
	if err := st.ResetDiskDeletion(ctx, missing); !errors.Is(err, store.ErrDiskNotFound) {
		t.Fatalf("ResetDiskDeletion = %v", err)
	}
	if claimed, err := st.ClaimDueDiskDeletions(ctx, time.Now(), 0); err != nil || claimed != nil {
		t.Fatalf("ClaimDue limit 0 = %+v, %v", claimed, err)
	}
	if count, age, err := st.DueDiskDeletionCount(ctx, time.Now()); err != nil || count != 0 || age != 0 {
		t.Fatalf("DueDiskDeletionCount empty = %d, %v, %v", count, age, err)
	}
}

func TestFinalizeProvisioningValidationAndMissing(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	incomplete := store.DiskBackendMetadata{
		Driver:     store.DiskSourceKouja,
		Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI},
	}
	if _, _, err := st.FinalizeProvisioning(ctx, 1, incomplete, 0, "", false); err == nil {
		t.Fatal("FinalizeProvisioning accepted incomplete metadata")
	}
	complete := store.DiskBackendMetadata{
		Driver: store.DiskSourceKouja,
		Attachment: blockdev.Reference{
			Protocol: blockdev.ProtocolISCSI,
			ISCSI: &blockdev.ISCSIReference{
				Portal: "p:3260", Target: "iqn.x", LUN: 0,
			},
		},
	}
	if _, ok, err := st.FinalizeProvisioning(ctx, 99999, complete, 1, "", false); err != nil || ok {
		t.Fatalf("FinalizeProvisioning missing = ok %t, err %v", ok, err)
	}
}

func TestCreateDiskValidationBranches(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	cases := []store.Disk{
		{Name: "kouja-device", SizeGB: 1, Source: store.DiskSourceKouja, Device: "/dev/x"},
		{Name: "kouja-template", SizeGB: 1, Source: store.DiskSourceKouja, IsTemplate: true},
		{Name: "kouja-local", SizeGB: 1, Source: store.DiskSourceKouja, Protocol: string(blockdev.ProtocolLocal)},
		{Name: "ceph-iscsi", SizeGB: 1, Source: store.DiskSourceCeph, Protocol: string(blockdev.ProtocolISCSI)},
		{Name: "unknown", SizeGB: 1, Source: "unknown"},
		{Name: "provisioning", SizeGB: 1, Source: store.DiskSourceLVMThin, ProvisioningScript: "/bin/true"},
	}
	for _, disk := range cases {
		if _, err := st.CreateDisk(ctx, disk); err == nil {
			t.Fatalf("CreateDisk accepted %+v", disk)
		}
	}
}

func TestScanDiskInvalidMetadata(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "metadata.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	disk, err := st.CreateDisk(ctx, store.Disk{Name: "bad-meta", SizeGB: 5, VG: "vg0", ThinPool: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE disks SET backend_metadata = '{bad' WHERE id = ?`, disk.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetDisk(ctx, disk.ID); err == nil {
		t.Fatal("GetDisk accepted invalid backend metadata")
	}
}

func TestScanDiskProtocolFromMetadataAndClaim(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "protocol.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "protocol", SizeGB: 5, VG: "vg0", ThinPool: "pool",
		DeletionState: store.DiskDeletionDeleting,
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE disks SET attachment_protocol = '', backend_metadata = ? WHERE id = ?`,
		`{"attachment":{"protocol":"iscsi","iscsi":{"portal":"p:3260","target":"iqn.x","lun":0}}}`,
		disk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE disks SET deletion_claimed_at = 123 WHERE id = ?`, disk.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetDisk(ctx, disk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Protocol != string(blockdev.ProtocolISCSI) || got.DeletionClaimedAt.UnixMilli() != 123 {
		t.Fatalf("scanDisk = %+v", got)
	}
}
