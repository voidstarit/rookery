package gateway

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/kouja"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/storage"
	"github.com/voidstarit/rookery/internal/store"
	"github.com/voidstarit/rookery/internal/vmstart"
)

type branchDriver struct {
	createResult storage.CreatedDisk
	createErr    error
	health       storage.Health
	healthErr    error
	prepared     blockdev.Attachment
	prepareErr   error
}

func (d *branchDriver) Kind() string { return store.DiskSourceKouja }
func (d *branchDriver) CreateDisk(context.Context, store.StorageBackend, storage.CreateDiskRequest) (storage.CreatedDisk, error) {
	return d.createResult, d.createErr
}
func (d *branchDriver) DeleteDisk(context.Context, store.StorageBackend, store.Disk) error {
	return nil
}
func (d *branchDriver) Health(context.Context, store.StorageBackend, *store.Disk) (storage.Health, error) {
	return d.health, d.healthErr
}
func (d *branchDriver) PrepareStart(context.Context, store.StorageBackend, store.Disk) (blockdev.Attachment, error) {
	return d.prepared, d.prepareErr
}

func validCreatedDisk() storage.CreatedDisk {
	return storage.CreatedDisk{
		Metadata: store.DiskBackendMetadata{
			Version: 1, Driver: store.DiskSourceKouja,
			Volume: store.VolumeMetadata{VolumeID: "volume-1", ResourceID: "resource-1", Owner: "owner", StorageClass: "nvme"},
			Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI, ISCSI: &blockdev.ISCSIReference{
				Portal: "10.0.0.1:3260", Target: "iqn.x", LUN: 0,
			}},
		},
		SizeBytes: 5 << 30,
	}
}

func branchStore(t *testing.T) (*store.Store, store.StorageBackend) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "gateway-branches.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	backend, err := st.CreateStorageBackend(context.Background(), store.StorageBackend{Name: "kouja", Driver: store.DiskSourceKouja})
	if err != nil {
		t.Fatal(err)
	}
	return st, backend
}

func branchDiskService(st *store.Store, registry *storage.Registry) *diskService {
	return &diskService{store: st, registry: registry, mu: &sync.Mutex{}, logger: zerolog.Nop()}
}

func TestCreateKoujaDiskBranches(t *testing.T) {
	ctx := context.Background()
	st, backend := branchStore(t)
	size := int32(5)
	request := &rookeryv1.CreateDiskRequest{Name: "branch", SizeGb: &size}
	if _, err := branchDiskService(st, nil).createKoujaDisk(ctx, request, "branch", backend, 5, nil); err == nil {
		t.Fatal("createKoujaDisk accepted a nil registry")
	}
	if _, err := branchDiskService(st, storage.NewRegistry()).createKoujaDisk(ctx, request, "branch", backend, 5, nil); err == nil {
		t.Fatal("createKoujaDisk accepted a missing driver")
	}
	// Duplicate name and the out-of-range/default size paths.
	existing, err := st.CreateDisk(ctx, store.Disk{Name: "existing", SizeGB: 5, VG: "vg0", ThinPool: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	driver := &branchDriver{createResult: validCreatedDisk()}
	service := branchDiskService(st, storage.NewRegistry(driver))
	if _, err := service.createKoujaDisk(ctx, request, existing.Name, backend, 5, nil); err == nil {
		t.Fatal("createKoujaDisk accepted a duplicate name")
	}
	badClass := int64(999)
	if _, err := service.createKoujaDisk(ctx, request, "bad-class", backend, 5, &badClass); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("createKoujaDisk(missing class) = %v", err)
	}
	tooBig := int32(maxGatewayDiskGB + 1)
	if _, err := service.CreateDisk(ctx, &rookeryv1.CreateDiskRequest{Name: "too-big", SizeGb: &tooBig, StorageBackendId: &backend.ID}); err == nil {
		t.Fatal("CreateDisk accepted an oversized kouja disk")
	}
	zero := int32(0)
	if _, err := service.CreateDisk(ctx, &rookeryv1.CreateDiskRequest{Name: "default-size", SizeGb: &zero, StorageBackendId: &backend.ID}); err != nil {
		t.Fatalf("CreateDisk(default size) = %v", err)
	}
	// NVMe protocol selection plus a finalize failure (invalid metadata).
	backend.Config.Kouja = &store.KoujaConfig{Protocol: "nvme-tcp", StorageClass: "nvme"}
	badDriver := &branchDriver{createResult: storage.CreatedDisk{SizeBytes: 5 << 30, Metadata: store.DiskBackendMetadata{Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI}}}}
	if _, err := branchDiskService(st, storage.NewRegistry(badDriver)).createKoujaDisk(ctx, &rookeryv1.CreateDiskRequest{Name: "bad-finalize", SizeGb: &size}, "bad-finalize", backend, 5, nil); err == nil {
		t.Fatal("createKoujaDisk accepted invalid finalize metadata")
	}
}

func TestDeleteKoujaDiskNilRegistry(t *testing.T) {
	ctx := context.Background()
	st, backend := branchStore(t)
	disk, err := st.CreateDisk(ctx, store.Disk{Name: "remote", SizeGB: 5, StorageBackendID: &backend.ID,
		Source: store.DiskSourceKouja, Protocol: string(blockdev.ProtocolISCSI),
		BackendMetadata: store.DiskBackendMetadata{Volume: store.VolumeMetadata{VolumeID: "volume-1"},
			Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI, ISCSI: &blockdev.ISCSIReference{
				Portal: "10.0.0.1:3260", Target: "iqn.x", LUN: 0,
			}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := branchDiskService(st, nil).DeleteDisk(ctx, &rookeryv1.DeleteDiskRequest{Id: disk.ID}); err == nil {
		t.Fatal("DeleteDisk accepted a nil registry")
	}
}

func TestNormalizeKoujaBackendBranches(t *testing.T) {
	if _, err := normalizeStorageBackend(&rookeryv1.CreateStorageBackendRequest{Name: "x", Driver: "kouja", Config: &rookeryv1.StorageBackendConfig{}}); err == nil {
		t.Fatal("normalizeStorageBackend accepted missing kouja config")
	}
	if _, err := normalizeStorageBackend(&rookeryv1.CreateStorageBackendRequest{Name: "x", Driver: "kouja",
		Config: &rookeryv1.StorageBackendConfig{DriverConfig: &rookeryv1.StorageBackendConfig_Kouja{Kouja: &rookeryv1.KoujaConfig{Endpoint: "10.0.0.1:9443"}}}}); err == nil {
		t.Fatal("normalizeStorageBackend accepted an invalid kouja config")
	}
}

type staticReadiness struct{ status readiness.Status }

func (r staticReadiness) Status() readiness.Status { return r.status }

func TestStorageBackendHealthBranches(t *testing.T) {
	ctx := context.Background()
	st, kouja := branchStore(t)
	lvm, err := st.CreateStorageBackend(ctx, store.StorageBackend{Name: "lvm", Driver: "lvm-thin", Config: store.StorageBackendConfig{VG: "vg0", ThinPool: "pool"}})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := st.CreateStorageBackend(ctx, store.StorageBackend{Name: "unknown", Driver: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	service := &storageBackendService{store: st, readiness: staticReadiness{status: readiness.Status{ThinPool: readiness.ThinPoolStatus{
		ComponentStatus: readiness.ComponentStatus{Ready: true, Detail: "ready"},
	}}}, registry: storage.NewRegistry(&branchDriver{healthErr: errors.New("health failed")})}
	if _, err := service.GetStorageBackendHealth(ctx, &rookeryv1.GetStorageBackendHealthRequest{Id: 999}); err == nil {
		t.Fatal("health accepted a missing backend")
	}
	lvmHealth, err := service.GetStorageBackendHealth(ctx, &rookeryv1.GetStorageBackendHealthRequest{Id: lvm.ID})
	if err != nil || !lvmHealth.Ready {
		t.Fatalf("lvm health = %+v, %v", lvmHealth, err)
	}
	unknownHealth, err := service.GetStorageBackendHealth(ctx, &rookeryv1.GetStorageBackendHealthRequest{Id: unknown.ID})
	if err != nil || unknownHealth.Detail == "" {
		t.Fatalf("unknown health = %+v, %v", unknownHealth, err)
	}
	if _, err := service.GetStorageBackendHealth(ctx, &rookeryv1.GetStorageBackendHealthRequest{Id: kouja.ID}); err != nil {
		t.Fatal(err)
	}
	// A backend with a disk exercises the sample lookup loop.
	if _, err := st.CreateDisk(ctx, store.Disk{Name: "sample", SizeGB: 5, StorageBackendID: &kouja.ID,
		Source: store.DiskSourceKouja, Protocol: string(blockdev.ProtocolISCSI),
		BackendMetadata: store.DiskBackendMetadata{Volume: store.VolumeMetadata{VolumeID: "sample"},
			Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI, ISCSI: &blockdev.ISCSIReference{
				Portal: "10.0.0.1:3260", Target: "iqn.x", LUN: 0,
			}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetStorageBackendHealth(ctx, &rookeryv1.GetStorageBackendHealthRequest{Id: kouja.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TestStorageBackendDataPath(ctx, &rookeryv1.TestStorageBackendDataPathRequest{Id: kouja.ID}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("TestStorageBackendDataPath = %v", err)
	}
	if _, err := (&storageBackendService{}).withDeletionBacklog(ctx, &rookeryv1.StorageBackendHealth{}); err != nil {
		t.Fatal(err)
	}
}

func TestWithStartPlannerOption(t *testing.T) {
	st, _ := branchStore(t)
	gateway, err := New(st, staticReadiness{}, WithStartPlanner(vmstart.NewPlanner(st, storage.NewRegistry())))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gateway.Close)
}

func TestVMActionsPlannerBranches(t *testing.T) {
	ctx := context.Background()
	st, backend := branchStore(t)
	disk, err := st.CreateDisk(ctx, store.Disk{Name: "remote", SizeGB: 5, StorageBackendID: &backend.ID,
		Source: store.DiskSourceKouja, Protocol: string(blockdev.ProtocolISCSI),
		BackendMetadata: store.DiskBackendMetadata{Volume: store.VolumeMetadata{VolumeID: "volume-1"},
			Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI, ISCSI: &blockdev.ISCSIReference{
				Portal: "10.0.0.1:3260", Target: "iqn.x", LUN: 0,
			}}}})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateVM(ctx, store.VM{Name: "vm", VCPUs: 1, MemoryMB: 512, DiskIDs: []int64{disk.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// Planner error.
	planner := vmstart.NewPlanner(st, storage.NewRegistry())
	service := &vmService{store: st, planner: planner, runtime: &legacyRuntime{}}
	if err := service.startVM(ctx, vm); err == nil {
		t.Fatal("startVM accepted a planner error")
	}
	// Non-empty plan with a runtime that only implements the old Start method.
	driver := &branchDriver{prepared: blockdev.Attachment{Protocol: blockdev.ProtocolISCSI, ISCSI: &blockdev.ISCSIAttachment{
		ISCSIReference: blockdev.ISCSIReference{Portal: "10.0.0.1:3260", Target: "iqn.x", LUN: 0},
		Username:       "user", Credential: "secret",
	}}}
	service.planner = vmstart.NewPlanner(st, storage.NewRegistry(driver))
	if err := service.startVM(ctx, vm); err == nil {
		t.Fatal("startVM accepted a runtime without PreparedStarter")
	}
}

type legacyRuntime struct{}

func (r *legacyRuntime) Start(context.Context, store.VM) error        { return nil }
func (r *legacyRuntime) Stop(context.Context, string) error           { return nil }
func (r *legacyRuntime) Reset(context.Context, string) error          { return nil }
func (r *legacyRuntime) SendCtrlAltDel(context.Context, string) error { return nil }
func (r *legacyRuntime) Running(string) bool                          { return false }
func (r *legacyRuntime) Metrics(string) (qemu.Metrics, error)         { return qemu.Metrics{}, nil }

func TestVMStartStatusErrorSessionActive(t *testing.T) {
	err := vmStartStatusError(fmt.Errorf("prepare disk: %w", kouja.ErrSessionActive))
	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "previous iSCSI session") {
		t.Fatalf("vmStartStatusError = %v", err)
	}
}
