package gateway_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/voidstarit/rookery/internal/cgroup"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

func (f *fakeVMController) ApplyCgroup(_ context.Context, vm store.VM) error {
	if f.cgroupErr != nil {
		return f.cgroupErr
	}
	f.cgroupApplied = append(f.cgroupApplied, vm)
	return nil
}

func TestUpdateVMAppliesCgroupClassLive(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "vm-cgroup-live.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if _, err := st.SetCgroupSettings(ctx, cgroup.Settings{
		Enabled: true,
		Classes: []cgroup.Class{{Name: "prod"}, {Name: "batch"}},
	}); err != nil {
		t.Fatal(err)
	}

	backend, err := st.CreateStorageBackend(ctx, store.StorageBackend{
		Name: "default", Driver: "lvm-thin", Config: store.StorageBackendConfig{VG: "vg0", ThinPool: "pool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "vm-cgroup-disk", SizeGB: 10, VG: "vg0", ThinPool: "pool", StorageBackendID: &backend.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateVM(ctx, store.VM{
		Name: "vm-cgroup-live", Status: store.StatusRunning, VCPUs: 2, MemoryMB: 1024, DiskIDs: []int64{disk.ID},
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

	update := func(body string) *http.Response {
		t.Helper()
		request, err := http.NewRequest(http.MethodPut, server.URL+"/api/vms/"+vm.ID, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	response := update(`{"name": "vm-cgroup-live", "cgroup_class": "prod"}`)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d", response.StatusCode)
	}
	if len(runtime.cgroupApplied) != 1 {
		t.Fatalf("live cgroup applications = %d, want 1", len(runtime.cgroupApplied))
	}
	if runtime.cgroupApplied[0].CgroupClass != "prod" {
		t.Fatalf("applied class = %q", runtime.cgroupApplied[0].CgroupClass)
	}
	stored, err := st.GetVM(ctx, vm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CgroupClass != "prod" {
		t.Fatalf("stored class = %q", stored.CgroupClass)
	}

	runtime.cgroupErr = errTestCgroup
	response = update(`{"name": "vm-cgroup-live", "cgroup_class": "batch"}`)
	_ = response.Body.Close()
	if response.StatusCode < 400 {
		t.Fatalf("failed live update status = %d, want error", response.StatusCode)
	}
	stored, err = st.GetVM(ctx, vm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CgroupClass != "prod" {
		t.Fatalf("class was stored despite live apply failure: %q", stored.CgroupClass)
	}

	runtime.cgroupErr = nil
	response = update(`{"name": "vm-cgroup-live", "cgroup_class": ""}`)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("clear update status = %d", response.StatusCode)
	}
	if len(runtime.cgroupApplied) != 2 || runtime.cgroupApplied[1].CgroupClass != "" {
		t.Fatalf("class clear was not applied live: %+v", runtime.cgroupApplied)
	}
	stored, err = st.GetVM(ctx, vm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CgroupClass != "" {
		t.Fatalf("stored class = %q, want shared", stored.CgroupClass)
	}
}

type testCgroupError struct{}

func (testCgroupError) Error() string { return "cgroup apply failed" }

var errTestCgroup testCgroupError

type fakeHostCgroupRuntime struct {
	settings cgroup.Settings
}

func (f *fakeHostCgroupRuntime) Configure(settings cgroup.Settings) error {
	f.settings = settings
	return nil
}

func (f *fakeHostCgroupRuntime) Validate(cgroup.Settings) error { return nil }

func (f *fakeHostCgroupRuntime) Usage() cgroup.Usage { return cgroup.Usage{} }

func (f *fakeHostCgroupRuntime) Status() cgroup.Status {
	return cgroup.Status{
		Available: true, Configured: true, Enabled: f.settings.Enabled,
		Mount: "/sys/fs/cgroup", Root: "/sys/fs/cgroup/rookery", OnlineCPUs: []int{0},
	}
}

func TestUpdateCgroupSettingsAppliesToRunningVMs(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "host-cgroup-live.db"))
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
	createVM := func(name string) store.VM {
		t.Helper()
		disk, err := st.CreateDisk(context.Background(), store.Disk{
			Name: name + "-disk", SizeGB: 10, VG: "vg0", ThinPool: "pool", StorageBackendID: &backend.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		vm, err := st.CreateVM(context.Background(), store.VM{
			Name: name, Status: store.StatusRunning, VCPUs: 1, MemoryMB: 512, DiskIDs: []int64{disk.ID},
		})
		if err != nil {
			t.Fatal(err)
		}
		return vm
	}
	running := createVM("host-cgroup-running")
	stopped := createVM("host-cgroup-stopped")
	running.CgroupClass = "prod"
	if _, err := st.UpdateVM(context.Background(), running); err != nil {
		t.Fatal(err)
	}

	vmRuntime := &fakeVMController{runningIDs: map[string]bool{running.ID: true}}
	hostRuntime := &fakeHostCgroupRuntime{}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{Ready: true}},
		gateway.WithVMController(vmRuntime), gateway.WithCgroupRuntime(hostRuntime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	update := func(body string) *http.Response {
		t.Helper()
		request, err := http.NewRequest(http.MethodPut, server.URL+"/api/cgroup-settings", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	response := update(`{"enabled": true, "cpus": "0", "classes": [{"name": "prod", "cpus": "0"}]}`)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("enable status = %d", response.StatusCode)
	}
	if !hostRuntime.settings.Enabled || len(hostRuntime.settings.Classes) != 1 {
		t.Fatalf("host settings were not configured: %+v", hostRuntime.settings)
	}
	if len(vmRuntime.cgroupApplied) != 1 || vmRuntime.cgroupApplied[0].ID != running.ID {
		t.Fatalf("running VMs applied = %+v, want only %s", vmRuntime.cgroupApplied, running.ID)
	}
	for _, vm := range vmRuntime.cgroupApplied {
		if vm.ID == stopped.ID {
			t.Fatalf("stopped VM %s received a live cgroup update", stopped.ID)
		}
	}

	response = update(`{"enabled": true, "cpus": "0", "memory_max_bytes": 1073741824, "classes": [{"name": "prod", "cpus": "0"}]}`)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d", response.StatusCode)
	}
	if len(vmRuntime.cgroupApplied) != 2 {
		t.Fatalf("running VM was not re-applied after a host setting change: %d", len(vmRuntime.cgroupApplied))
	}

	response = update(`{"enabled": true, "cpus": "0", "classes": []}`)
	_ = response.Body.Close()
	if response.StatusCode < 400 {
		t.Fatalf("removing an assigned class status = %d, want error", response.StatusCode)
	}
	stored, err := st.GetCgroupSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Classes) != 1 || stored.Classes[0].Name != "prod" {
		t.Fatalf("assigned class was removed: %+v", stored.Classes)
	}
}
