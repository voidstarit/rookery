package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

func TestUIContractRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "ui-contract.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	backend, err := st.CreateStorageBackend(context.Background(), store.StorageBackend{
		Name: "default", Driver: "lvm-thin", Config: store.StorageBackendConfig{VG: "vg0", ThinPool: "pool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := st.CreateDisk(context.Background(), store.Disk{
		Name: "disk-1", SizeGB: 10, VG: "vg0", ThinPool: "pool", StorageBackendID: &backend.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateVM(context.Background(), store.VM{
		Name: "vm-1", VCPUs: 2, MemoryMB: 1024, DiskIDs: []int64{disk.ID}, ConsoleEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	network, err := st.CreateNetwork(context.Background(), store.Network{Name: "lab", BridgeDomainID: 700})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateVNF(context.Background(), store.VNF{
		Name: "dhcp", NetworkID: network.ID, Type: store.VNFTypeDHCPv4, Enabled: true,
		Config: store.VNFConfig{Gateway: "10.0.0.1/24", RangeStart: "10.0.0.10", RangeEnd: "10.0.0.20"},
	}); err != nil {
		t.Fatal(err)
	}

	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{
		Ready: true,
		ThinPool: readiness.ThinPoolStatus{
			ComponentStatus: readiness.ComponentStatus{Ready: true}, VG: "vg0", Name: "pool",
		},
		VPP: readiness.VPPStatus{ComponentStatus: readiness.ComponentStatus{Ready: true}},
	}}, gateway.WithVNFRuntime(&fakeVNFRuntime{}), gateway.WithVNFStatusProvider(fakeVNFStatusProvider{}), gateway.WithDiagnosticsRuntime(&fakeDiagnostics{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	get := func(path string) *http.Response {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", path, response.StatusCode, readBody(t, response))
		}
		return response
	}

	statusResponse := get("/api/status")
	var status map[string]any
	decodeBody(t, statusResponse, &status)
	if _, ok := status["ready"].(bool); !ok {
		t.Fatalf("status.ready = %+v", status["ready"])
	}
	if _, ok := status["thin_pool"].(map[string]any)["ready"].(bool); !ok {
		t.Fatalf("thin_pool.ready = %+v", status["thin_pool"])
	}

	vmsResponse := get("/api/vms")
	var vms []map[string]any
	decodeBody(t, vmsResponse, &vms)
	if len(vms) != 1 {
		t.Fatalf("vms = %+v", vms)
	}
	if _, ok := vms[0]["id"].(string); !ok {
		t.Fatalf("vm.id = %+v", vms[0]["id"])
	}
	diskIDs, ok := vms[0]["disk_ids"].([]any)
	if !ok || len(diskIDs) != 1 || jsonInteger(diskIDs[0]) != disk.ID {
		t.Fatalf("vm.disk_ids = %+v", vms[0]["disk_ids"])
	}
	if _, ok := vms[0]["console_enabled"].(bool); !ok {
		t.Fatalf("vm.console_enabled = %+v", vms[0]["console_enabled"])
	}

	for _, path := range []string{
		"/api/disks", "/api/networks", "/api/vnfs", "/api/storage-backends",
	} {
		response := get(path)
		var items []map[string]any
		decodeBody(t, response, &items)
		if len(items) == 0 {
			t.Fatalf("%s returned no items", path)
		}
		if _, ok := items[0]["id"].(float64); !ok {
			t.Fatalf("%s id = %+v", path, items[0]["id"])
		}
	}

	for _, path := range []string{
		"/api/media", "/api/interfaces", "/api/acls",
	} {
		response := get(path)
		var items []any
		if err := json.NewDecoder(response.Body).Decode(&items); err != nil {
			t.Fatalf("%s body: %v", path, err)
		}
		_ = response.Body.Close()
		if len(items) != 0 {
			t.Fatalf("%s = %+v", path, items)
		}
	}

	captureResponse := get("/api/troubleshoot/capture/files")
	var captures []map[string]any
	decodeBody(t, captureResponse, &captures)
	if len(captures) != 1 {
		t.Fatalf("capture files = %+v", captures)
	}
}
