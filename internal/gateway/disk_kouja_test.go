package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/storage"
	"github.com/voidstarit/rookery/internal/store"
)

type gatewayKoujaDriver struct {
	prepared int
}

func (d *gatewayKoujaDriver) Kind() string { return store.DiskSourceKouja }
func (d *gatewayKoujaDriver) CreateDisk(_ context.Context, _ store.StorageBackend, request storage.CreateDiskRequest) (storage.CreatedDisk, error) {
	return storage.CreatedDisk{
		Metadata: store.DiskBackendMetadata{
			Version: 1, Driver: store.DiskSourceKouja,
			Volume: store.VolumeMetadata{VolumeID: request.Disk.BackendMetadata.Volume.VolumeID, RequestID: request.RequestID,
				ResourceID: "resource-1", Owner: "spiffe://kouja/caller/rookery", StorageClass: "nvme"},
			Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI, ISCSI: &blockdev.ISCSIReference{
				Portal: "10.0.0.1:3260", ExportPortal: "10.0.0.1:3260",
				Target: "iqn.2026-11.io.example:disk", LUN: 0, Initiator: "iqn.2026-11.io.example:rookery",
			}},
		},
		SizeBytes: 5 << 30,
	}, nil
}
func (d *gatewayKoujaDriver) DeleteDisk(context.Context, store.StorageBackend, store.Disk) error {
	return nil
}
func (d *gatewayKoujaDriver) Health(context.Context, store.StorageBackend, *store.Disk) (storage.Health, error) {
	return storage.Health{Reachable: true, Ready: true}, nil
}
func (d *gatewayKoujaDriver) PrepareStart(context.Context, store.StorageBackend, store.Disk) (blockdev.Attachment, error) {
	d.prepared++
	return blockdev.Attachment{
		Protocol: blockdev.ProtocolISCSI,
		ISCSI: &blockdev.ISCSIAttachment{
			ISCSIReference: blockdev.ISCSIReference{Portal: "10.0.0.1:3260", Target: "iqn.2026-11.io.example:disk", LUN: 0},
			Username:       "kouja-resource-1", Credential: "secret",
		},
	}, nil
}

func TestKoujaDiskAndVMStartRoutes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "kouja-gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	backend, err := st.CreateStorageBackend(ctx, store.StorageBackend{
		Name: "kouja", Driver: store.DiskSourceKouja,
		Config: store.StorageBackendConfig{Kouja: &store.KoujaConfig{StorageClass: "nvme"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	driver := &gatewayKoujaDriver{}
	registry := storage.NewRegistry(driver)
	runtime := &fakeVMController{}
	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()},
		gateway.WithStorageRegistry(registry), gateway.WithVMController(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	body := strings.NewReader(`{"name":"remote-disk","size_gb":5,"storage_backend_id":` + jsonNumber(backend.ID) + `}`)
	response, err := http.Post(server.URL+"/api/disks", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var disk map[string]any
	decodeBody(t, response, &disk)
	id := jsonID(disk["id"])
	if disk["source"] != "kouja" || disk["protocol"] != "iscsi" || !strings.Contains(disk["locator"].(string), "iqn.2026-11.io.example:disk") {
		t.Fatalf("disk = %+v", disk)
	}

	vmBody := strings.NewReader(`{"name":"remote-vm","disk_ids":[` + id + `]}`)
	vmResponse, err := http.Post(server.URL+"/api/vms", "application/json", vmBody)
	if err != nil {
		t.Fatal(err)
	}
	if vmResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create vm status = %d, body = %s", vmResponse.StatusCode, readBody(t, vmResponse))
	}
	if driver.prepared != 1 || len(runtime.plan.Attachments) != 1 {
		t.Fatalf("prepared=%d plan=%+v", driver.prepared, runtime.plan)
	}
	var vm map[string]any
	decodeBody(t, vmResponse, &vm)
	vmID := vm["id"].(string)

	stop, err := http.Post(server.URL+"/api/vms/"+vmID+"/stop", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = stop.Body.Close()
	deleteVM, err := http.NewRequest(http.MethodDelete, server.URL+"/api/vms/"+vmID, nil)
	if err != nil {
		t.Fatal(err)
	}
	deletedVM, err := http.DefaultClient.Do(deleteVM)
	if err != nil {
		t.Fatal(err)
	}
	_ = deletedVM.Body.Close()

	deleteDisk, err := http.NewRequest(http.MethodDelete, server.URL+"/api/disks/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	deletedDisk, err := http.DefaultClient.Do(deleteDisk)
	if err != nil {
		t.Fatal(err)
	}
	if deletedDisk.StatusCode != http.StatusAccepted {
		t.Fatalf("delete disk status = %d, body = %s", deletedDisk.StatusCode, readBody(t, deletedDisk))
	}
	_ = deletedDisk.Body.Close()
	stored, err := st.GetDisk(ctx, jsonIDInt(id))
	if err != nil || stored.DeletionState != store.DiskDeletionDeleting {
		t.Fatalf("stored disk = %+v, %v", stored, err)
	}
}

func jsonNumber(value int64) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func jsonIDInt(value string) int64 {
	var id int64
	_ = json.Unmarshal([]byte(value), &id)
	return id
}
