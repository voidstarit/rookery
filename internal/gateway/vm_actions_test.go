package gateway_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeVMController struct {
	running  bool
	metrics  qemu.Metrics
	startErr error
}

func (f *fakeVMController) Start(_ context.Context, _ store.VM) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.running = true
	return nil
}
func (f *fakeVMController) Stop(_ context.Context, _ string) error {
	f.running = false
	return nil
}
func (f *fakeVMController) Reset(_ context.Context, _ string) error          { return nil }
func (f *fakeVMController) SendCtrlAltDel(_ context.Context, _ string) error { return nil }
func (f *fakeVMController) Running(_ string) bool                            { return f.running }
func (f *fakeVMController) Metrics(_ string) (qemu.Metrics, error)           { return f.metrics, nil }

func TestVMActionsAndMetrics(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "vm-actions.db"))
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
		Name: "vm-disk", SizeGB: 10, VG: "vg0", ThinPool: "pool", StorageBackendID: &backend.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateVM(context.Background(), store.VM{
		Name: "vm-actions", VCPUs: 2, MemoryMB: 1024, DiskIDs: []int64{disk.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	runtime := &fakeVMController{metrics: qemu.Metrics{
		Running: true, PID: 1234, UptimeSeconds: 12, CPUSecondsTotal: 2.5,
		RSSBytes: 1024, VirtualBytes: 2048, ReadBytesTotal: 10, WriteBytesTotal: 20,
	}}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{Ready: true}}, gateway.WithVMController(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	post := func(path string) *http.Response {
		t.Helper()
		response, err := http.Post(server.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	start := post("/api/vms/" + vm.ID + "/start")
	if start.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, body = %s", start.StatusCode, readBody(t, start))
	}
	var started map[string]any
	decodeBody(t, start, &started)
	if started["status"] != "running" {
		t.Fatalf("started = %+v", started)
	}

	metricsResponse, err := http.Get(server.URL + "/api/vms/" + vm.ID + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	if metricsResponse.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, body = %s", metricsResponse.StatusCode, readBody(t, metricsResponse))
	}
	metricsBody, err := io.ReadAll(metricsResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = metricsResponse.Body.Close()
	var metrics rookeryv1.VMMetrics
	if err := protojson.Unmarshal(metricsBody, &metrics); err != nil {
		t.Fatal(err)
	}
	if !metrics.GetRunning() || metrics.GetVmId() != vm.ID || metrics.GetPid() != 1234 || metrics.GetCpuSecondsTotal() != 2.5 {
		t.Fatalf("metrics = running=%v vm_id=%q pid=%d cpu=%v", metrics.GetRunning(), metrics.GetVmId(), metrics.GetPid(), metrics.GetCpuSecondsTotal())
	}

	ctrlAltDel := post("/api/vms/" + vm.ID + "/ctrl-alt-del")
	if ctrlAltDel.StatusCode != http.StatusNoContent {
		t.Fatalf("ctrl-alt-del status = %d, body = %s", ctrlAltDel.StatusCode, readBody(t, ctrlAltDel))
	}
	_ = ctrlAltDel.Body.Close()

	reset := post("/api/vms/" + vm.ID + "/reset")
	if reset.StatusCode != http.StatusNoContent {
		t.Fatalf("reset status = %d, body = %s", reset.StatusCode, readBody(t, reset))
	}
	_ = reset.Body.Close()

	stop := post("/api/vms/" + vm.ID + "/stop")
	if stop.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d, body = %s", stop.StatusCode, readBody(t, stop))
	}
	var stopped map[string]any
	decodeBody(t, stop, &stopped)
	if stopped["status"] != "stopped" {
		t.Fatalf("stopped = %+v", stopped)
	}
	if runtime.running {
		t.Fatal("fake runtime still running after stop")
	}

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/vms/"+vm.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete VM status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	_ = deleted.Body.Close()

	// protojson encodes integers as strings; decoding into the generated
	// message above verifies the gateway shape, while this checks the error
	// path for a missing VM.
	missing := post("/api/vms/00000000-0000-0000-0000-000000000000/start")
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing start status = %d, body = %s", missing.StatusCode, readBody(t, missing))
	}
	_ = missing.Body.Close()

}

func TestStartVMRepairsStatusWhenQEMUIsAlreadyRunning(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "vm-actions-idempotent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	backend, err := st.CreateStorageBackend(ctx, store.StorageBackend{
		Name: "default", Driver: "lvm-thin", Config: store.StorageBackendConfig{VG: "vg0", ThinPool: "pool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "vm-disk", SizeGB: 10, VG: "vg0", ThinPool: "pool", StorageBackendID: &backend.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateVM(ctx, store.VM{
		Name: "vm-idempotent", VCPUs: 2, MemoryMB: 1024, DiskIDs: []int64{disk.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	runtime := &fakeVMController{running: true}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{Ready: true}}, gateway.WithVMController(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	response, err := http.Post(server.URL+"/api/vms/"+vm.ID+"/start", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("start status = %d, body = %s, want 409", response.StatusCode, readBody(t, response))
	}
	loaded, err := st.GetVM(ctx, vm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.StatusRunning {
		t.Fatalf("stored status = %q, want running", loaded.Status)
	}
}
