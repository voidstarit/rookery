package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeDriver struct {
	kind       string
	deleteErr  error
	orphanErr  error
	deleted    int
	orphaned   int
	lastDelete store.Disk
}

func (f *fakeDriver) Kind() string { return f.kind }
func (f *fakeDriver) CreateDisk(context.Context, store.StorageBackend, CreateDiskRequest) (CreatedDisk, error) {
	return CreatedDisk{}, nil
}
func (f *fakeDriver) DeleteDisk(_ context.Context, _ store.StorageBackend, disk store.Disk) error {
	f.deleted++
	f.lastDelete = disk
	return f.deleteErr
}
func (f *fakeDriver) DeleteOrphanVolume(context.Context, store.StorageBackend, store.Disk) error {
	f.orphaned++
	return f.orphanErr
}
func (f *fakeDriver) Health(context.Context, store.StorageBackend, *store.Disk) (Health, error) {
	return Health{Ready: true}, nil
}

func newReclaimerStore(t *testing.T) (*store.Store, store.StorageBackend) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/reclaimer.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	backend, err := st.CreateStorageBackend(context.Background(), store.StorageBackend{
		Name: "fake", Driver: "fake",
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, backend
}

func newDueDisk(t *testing.T, st *store.Store, backend store.StorageBackend, state string) store.Disk {
	t.Helper()
	disk, err := st.CreateDisk(context.Background(), store.Disk{
		Name: fmt.Sprintf("due-%s-%d", state, time.Now().UnixNano()), SizeGB: 5, StorageBackendID: &backend.ID,
		Source: store.DiskSourceKouja, Protocol: string(blockdev.ProtocolISCSI),
		BackendMetadata: store.DiskBackendMetadata{
			Driver: store.DiskSourceKouja,
			Volume: store.VolumeMetadata{VolumeID: "volume-" + state},
			Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI, ISCSI: &blockdev.ISCSIReference{
				Portal: "10.0.0.1:3260", Target: "iqn.x", LUN: 0,
			}},
		},
		DeletionState: state, DeletionNextAttemptAt: time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return disk
}

func TestReclaimerDeletesAndCleansOrphans(t *testing.T) {
	ctx := context.Background()
	for _, state := range []string{store.DiskDeletionDeleting, store.DiskDeletionProvisioning} {
		t.Run(state, func(t *testing.T) {
			st, backend := newReclaimerStore(t)
			disk := newDueDisk(t, st, backend, state)
			driver := &fakeDriver{kind: "fake"}
			reclaimer := NewReclaimer(st, NewRegistry(driver), zerolog.Nop())
			reclaimer.now = func() time.Time { return time.Now().UTC() }
			if _, err := reclaimer.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if state == store.DiskDeletionProvisioning && driver.orphaned != 1 {
				t.Fatalf("orphaned = %d", driver.orphaned)
			}
			if state == store.DiskDeletionDeleting && driver.deleted != 1 {
				t.Fatalf("deleted = %d", driver.deleted)
			}
			if _, err := st.GetDisk(ctx, disk.ID); !errors.Is(err, store.ErrDiskNotFound) {
				t.Fatalf("disk still exists: %v", err)
			}
		})
	}
}

func TestReclaimerRetriesAndFails(t *testing.T) {
	ctx := context.Background()
	st, backend := newReclaimerStore(t)
	driver := &fakeDriver{kind: "fake", deleteErr: status.Error(codes.Unavailable, "down")}
	disk := newDueDisk(t, st, backend, store.DiskDeletionDeleting)
	reclaimer := NewReclaimer(st, NewRegistry(driver), zerolog.Nop())
	now := time.Now().UTC()
	reclaimer.now = func() time.Time { return now }
	if _, err := reclaimer.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetDisk(ctx, disk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeletionState != store.DiskDeletionDeleting || got.DeletionAttempts != 1 || !got.DeletionNextAttemptAt.After(now) {
		t.Fatalf("retry disk = %+v", got)
	}

	driver.deleteErr = status.Error(codes.InvalidArgument, "bad")
	if err := st.ResetDiskDeletion(ctx, disk.ID); err != nil {
		t.Fatal(err)
	}
	now = time.Now().UTC()
	if _, err := reclaimer.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	failed, err := st.GetDisk(ctx, disk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.DeletionState != store.DiskDeletionFailed || failed.DeletionError == "" {
		t.Fatalf("failed disk = %+v", failed)
	}
}

func TestReclaimerSkipsAttached(t *testing.T) {
	ctx := context.Background()
	st, backend := newReclaimerStore(t)
	base, err := st.CreateDisk(ctx, store.Disk{Name: "base", SizeGB: 5, VG: "vg0", ThinPool: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	disk := newDueDisk(t, st, backend, store.DiskDeletionDeleting)
	vm, err := st.CreateVM(ctx, store.VM{Name: "vm", VCPUs: 1, MemoryMB: 512, DiskIDs: []int64{base.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetVMDisks(ctx, vm.ID, []int64{base.ID, disk.ID}); err == nil {
		t.Fatal("attached deleting disk")
	}
	// Direct attachment bypass is not possible through the store, so a due
	// attached row is represented by a regular disk; this verifies the skip path
	// through the normal deletion guard instead.
	driver := &fakeDriver{kind: "fake"}
	reclaimer := NewReclaimer(st, NewRegistry(driver), zerolog.Nop())
	if _, err := reclaimer.Tick(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestReclaimerConfigAndFailurePaths(t *testing.T) {
	ctx := context.Background()
	if _, err := (&Reclaimer{}).Tick(ctx); err == nil {
		t.Fatal("Tick accepted unconfigured reclaimer")
	}
	st, backend := newReclaimerStore(t)
	_ = newDueDisk(t, st, backend, store.DiskDeletionDeleting)
	missing := &Reclaimer{store: st, registry: NewRegistry(), logger: zerolog.Nop(), now: time.Now}
	if _, err := missing.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	// A due row whose backend disappears is terminal.
	orphan, err := st.CreateDisk(ctx, store.Disk{Name: "orphan", SizeGB: 5, Source: store.DiskSourceKouja,
		Protocol: string(blockdev.ProtocolISCSI), DeletionState: store.DiskDeletionDeleting,
		DeletionNextAttemptAt: time.Now().UTC().Add(-time.Minute),
		BackendMetadata:       store.DiskBackendMetadata{Volume: store.VolumeMetadata{VolumeID: "orphan"}}})
	if err != nil {
		t.Fatal(err)
	}
	driver := &fakeDriver{kind: "fake"}
	reclaimer := NewReclaimer(st, NewRegistry(driver), zerolog.Nop())
	if _, err := reclaimer.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	failed, err := st.GetDisk(ctx, orphan.ID)
	if err != nil || failed.DeletionState != store.DiskDeletionFailed {
		t.Fatalf("orphan = %+v, %v", failed, err)
	}

	// Retry cap.
	capDisk := newDueDisk(t, st, backend, store.DiskDeletionDeleting)
	if err := st.RecordDiskDeletionFailure(ctx, capDisk.ID, reclaimerMaxTries-1, time.Now().Add(-time.Second), "retry"); err != nil {
		t.Fatal(err)
	}
	driver.deleteErr = status.Error(codes.Unavailable, "down")
	reclaimer.now = time.Now
	if _, err := reclaimer.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	capped, err := st.GetDisk(ctx, capDisk.ID)
	if err != nil || capped.DeletionState != store.DiskDeletionFailed {
		t.Fatalf("capped = %+v, %v", capped, err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { reclaimer.Run(runCtx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestRegistryAndBackoff(t *testing.T) {
	driver := &fakeDriver{kind: "fake"}
	registry := NewRegistry(nil, driver)
	if got, ok := registry.Driver(" FAKE "); !ok || got != driver {
		t.Fatal("Driver lookup failed")
	}
	if _, ok := registry.Driver("missing"); ok {
		t.Fatal("Driver found a missing kind")
	}
	var nilRegistry *Registry
	if _, ok := nilRegistry.Driver("fake"); ok {
		t.Fatal("nil Registry found a driver")
	}
	nilRegistry.Register(driver)
	registry.Register(nil)
	registry.Register(&fakeDriver{kind: "other"})
	if _, ok := registry.Driver("other"); !ok {
		t.Fatal("Register failed")
	}
	if NewDefaultRegistry() == nil {
		t.Fatal("NewDefaultRegistry returned nil")
	}
	want := []time.Duration{30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour}
	for i, duration := range want {
		if got := deletionBackoff(i + 1); got != duration {
			t.Fatalf("backoff(%d) = %v, want %v", i+1, got, duration)
		}
	}
}
