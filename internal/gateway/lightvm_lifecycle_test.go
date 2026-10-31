package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeLightVMRuntime struct {
	running  map[string]bool
	started  store.LightVM
	startErr error
	stopErr  error
}

func (f *fakeLightVMRuntime) StartLightVM(_ context.Context, vm store.LightVM) error {
	if f.startErr != nil {
		return f.startErr
	}
	if vm.KernelPath == "" || vm.InitramfsPath == "" {
		return errors.New("missing kernel or initramfs path")
	}
	f.started = vm
	f.running[vm.ID] = true
	return nil
}

func (f *fakeLightVMRuntime) StopLightVM(_ context.Context, id string) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	f.running[id] = false
	return nil
}

func (f *fakeLightVMRuntime) RunningLightVM(id string) bool {
	return f.running[id]
}

func (f *fakeLightVMRuntime) LightVMConsoleSocket(id string) string {
	return filepath.Join("/tmp", id+".console")
}

func (f *fakeLightVMRuntime) LightVMStatus() (bool, string, string) {
	return false, "missing", "buildah is required for LightVM image management"
}

func TestLightVMLifecycleRoutes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "lightvm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{
		Name: "vmlinuz", Path: "/tmp/vmlinuz", FileName: "vmlinuz", SizeBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{
		Name: "initramfs", Path: "/tmp/initramfs.img", FileName: "initramfs.img", SizeBytes: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeLightVMRuntime{running: make(map[string]bool)}
	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithLightVMRuntime(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	request := func(method, path string, body any) (*http.Response, map[string]any) {
		t.Helper()
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else {
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(data)
		}
		req, err := http.NewRequest(method, server.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if response.StatusCode != http.StatusNoContent {
			if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil && response.StatusCode < 400 {
				t.Fatalf("%s %s decode: %v", method, path, err)
			}
		}
		_ = response.Body.Close()
		return response, decoded
	}

	response, status := request(http.MethodGet, "/api/status", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status code = %d", response.StatusCode)
	}
	lightvmStatus, _ := status["lightvm"].(map[string]any)
	if lightvmStatus["state"] != "missing" || !strings.Contains(lightvmStatus["detail"].(string), "buildah") {
		t.Fatalf("lightvm status = %+v", lightvmStatus)
	}

	response, created := request(http.MethodPost, "/api/lightvm/instances", map[string]any{"name": "app-01", "vcpus": 2, "memory_mb": 1024,
		"kernel_id": kernel.ID, "initramfs_id": initramfs.ID,
		"image": "alpine", "executable_path": "/bin/app",
		"executable_args": []string{"serve"}, "interface_ids": []int64{},
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %+v", response.StatusCode, created)
	}
	id, _ := created["id"].(string)
	if id == "" || created["status"] != "stopped" || created["desired_state"] != "stopped" {
		t.Fatalf("created lifecycle = %+v", created)
	}

	response, started := request(http.MethodPost, "/api/lightvm/instances/"+id+"/start", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, body = %+v", response.StatusCode, started)
	}
	if started["status"] != "running" || started["desired_state"] != "running" {
		t.Fatalf("started lifecycle = %+v", started)
	}
	if started["console_socket"] != "/tmp/"+id+".console" {
		t.Fatalf("started console socket = %+v", started["console_socket"])
	}
	if runtime.started.Append != store.DefaultLightVMAppend {
		t.Fatalf("runtime append = %q, want default", runtime.started.Append)
	}
	if runtime.started.KernelPath != kernel.Path || runtime.started.InitramfsPath != initramfs.Path {
		t.Fatalf("runtime paths = %+v", runtime.started)
	}

	response, _ = request(http.MethodPost, "/api/lightvm/instances/"+id+"/start", nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("second start status = %d", response.StatusCode)
	}

	response, _ = request(http.MethodPut, "/api/lightvm/instances/"+id, map[string]any{
		"name": "app-01", "vcpus": 2, "memory_mb": 1024,
		"kernel_id": kernel.ID, "initramfs_id": initramfs.ID,
		"image": "alpine", "executable_path": "/bin/app",
		"executable_args": []string{"serve"}, "interface_ids": []int64{},
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("update while running status = %d", response.StatusCode)
	}

	response, stopped := request(http.MethodPost, "/api/lightvm/instances/"+id+"/stop", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d, body = %+v", response.StatusCode, stopped)
	}
	if stopped["status"] != "stopped" || stopped["desired_state"] != "stopped" {
		t.Fatalf("stopped lifecycle = %+v", stopped)
	}

	runtime.startErr = errors.New("kernel boot failed")
	response, _ = request(http.MethodPost, "/api/lightvm/instances/"+id+"/start", nil)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failed start status = %d", response.StatusCode)
	}
	response, failed := request(http.MethodGet, "/api/lightvm/instances/"+id, nil)
	if response.StatusCode != http.StatusOK || failed["status"] != "error" {
		t.Fatalf("failed start record = %d, body = %+v", response.StatusCode, failed)
	}

	runtime.startErr = nil
	response, _ = request(http.MethodDelete, "/api/lightvm/instances/"+id, nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.StatusCode)
	}
}
