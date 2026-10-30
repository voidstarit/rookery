package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/cgroup"
	"github.com/voidstarit/rookery/internal/store"
)

func TestApplyVMCgroup(t *testing.T) {
	vm := store.VM{MemoryMB: 512}
	err := applyVMCgroup(&vm, &rookeryv1.VMCgroup{
		Enabled: true, Cpus: "0", MemoryMaxBytes: 1 << 30, CpuWeight: 500,
		IoLimits: []*rookeryv1.CgroupIOLimit{{Device: "/dev/sda", ReadBps: 1024}},
	})
	if err != nil {
		t.Fatalf("applyVMCgroup: %v", err)
	}
	if !vm.Cgroup.Enabled || vm.Cgroup.MemoryMaxBytes != 1<<30 || len(vm.Cgroup.IOLimits) != 1 {
		t.Fatalf("vm cgroup = %+v", vm.Cgroup)
	}

	if err := applyVMCgroup(&store.VM{MemoryMB: 1024}, &rookeryv1.VMCgroup{
		Enabled: true, MemoryMaxBytes: 512 << 20,
	}); err == nil {
		t.Fatal("applyVMCgroup accepted a memory limit below the VM memory")
	}

	if err := applyVMCgroup(&store.VM{MemoryMB: 512}, &rookeryv1.VMCgroup{
		Enabled: true,
		IoLimits: []*rookeryv1.CgroupIOLimit{
			{Device: "/dev/sda"},
			{Device: "/dev/sda"},
		},
	}); err == nil {
		t.Fatal("applyVMCgroup accepted duplicate I/O devices")
	}
}

type recordingCgroupRuntime struct {
	settings     cgroup.Settings
	configureErr error
}

func (r *recordingCgroupRuntime) Configure(settings cgroup.Settings) error {
	if r.configureErr != nil {
		return r.configureErr
	}
	r.settings = settings
	return nil
}

func (r *recordingCgroupRuntime) Validate(cgroup.Settings) error { return nil }

func (r *recordingCgroupRuntime) Status() cgroup.Status {
	return cgroup.Status{
		Available: true, Configured: true, Enabled: r.settings.Enabled,
		Mount: "/sys/fs/cgroup", Root: "/sys/fs/cgroup/rookery",
		OnlineCPUs: []int{0, 1}, CheckedAt: time.Now().UTC(),
	}
}

func TestUpdateCgroupSettingsHTTP(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "gateway-cgroup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	runtime := &recordingCgroupRuntime{}
	gw, err := New(st, nil, WithCgroupRuntime(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	body := []byte(`{
		"enabled": true,
		"cpus": "0-1",
		"memory_max_bytes": 1073741824,
		"cpu_weight": 500,
		"io_limits": [{"device": "/dev/sda", "read_bps": 1048576, "weight": 200}]
	}`)
	response, err := http.NewRequest(http.MethodPut, server.URL+"/api/cgroup-settings", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response.Header.Set("Content-Type", "application/json")
	result, err := http.DefaultClient.Do(response)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d", result.StatusCode)
	}
	if !runtime.settings.Enabled || runtime.settings.MemoryMaxBytes != 1<<30 || len(runtime.settings.IOLimits) != 1 {
		t.Fatalf("applied settings = %+v", runtime.settings)
	}
	var payload map[string]any
	if err := json.NewDecoder(result.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["root"] != "/sys/fs/cgroup/rookery" {
		t.Fatalf("response = %+v", payload)
	}

	runtime.configureErr = errors.New("controller unavailable")
	errorBody := []byte(`{"enabled": false}`)
	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/cgroup-settings", bytes.NewReader(errorBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	errorResult, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = errorResult.Body.Close()
	if errorResult.StatusCode < 400 {
		t.Fatalf("failed configure status = %d, want error", errorResult.StatusCode)
	}
	stored, err := st.GetCgroupSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled || stored.MemoryMaxBytes != 1<<30 {
		t.Fatalf("settings were not rolled back: %+v", stored)
	}
}
