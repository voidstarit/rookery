package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/lightvm"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeLightVMRuntime struct {
	running           map[string]bool
	started           store.LightVM
	startErr          error
	prepareErr        error
	startPreparedErr  error
	runState          string
	runStateErr       error
	stopErr           error
	stopLeavesRunning bool
	cleanupStoppedErr error
	onPrepare         func(store.LightVM)
}

func (f *fakeLightVMRuntime) PrepareLightVM(_ context.Context, vm store.LightVM) (lightvm.PreparedLightVM, error) {
	if f.prepareErr != nil {
		return lightvm.PreparedLightVM{}, f.prepareErr
	}
	if f.startErr != nil {
		return lightvm.PreparedLightVM{}, f.startErr
	}
	if f.onPrepare != nil {
		f.onPrepare(vm)
	}
	if vm.KernelPath == "" || vm.InitramfsPath == "" {
		return lightvm.PreparedLightVM{}, errors.New("missing kernel or initramfs path")
	}
	f.started = vm
	return lightvm.PreparedLightVM{
		LightVMID: vm.ID, Container: "rookery-lightvm-" + vm.ID + "-test",
		Rootfs: "/tmp/rootfs", VirtiofsdSocket: "/tmp/lightvm-" + vm.ID + ".virtiofs.sock",
	}, nil
}

func (f *fakeLightVMRuntime) StartPreparedLightVM(_ context.Context, prepared *lightvm.PreparedLightVM, vm store.LightVM, _ qemu.StartPlan) error {
	prepared.VirtiofsdStarted = true
	prepared.QEMUStarted = true
	if f.startPreparedErr != nil {
		if errors.Is(f.startPreparedErr, qemu.ErrAlreadyRunning) {
			f.running[vm.ID] = true
		}
		return f.startPreparedErr
	}
	if f.startErr != nil {
		return f.startErr
	}
	f.started = vm
	f.running[vm.ID] = true
	return nil
}

func (f *fakeLightVMRuntime) CleanupPreparedLightVM(context.Context, lightvm.PreparedLightVM) error {
	return nil
}

func (f *fakeLightVMRuntime) StopQEMULightVM(_ context.Context, id string) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	if !f.stopLeavesRunning {
		f.running[id] = false
	}
	return nil
}

func (f *fakeLightVMRuntime) CleanupStoppedLightVM(context.Context, string) error {
	return f.cleanupStoppedErr
}

func (f *fakeLightVMRuntime) RunState(_ context.Context, id string) (string, error) {
	if f.runStateErr != nil {
		return "", f.runStateErr
	}
	if f.runState != "" {
		return f.runState, nil
	}
	if f.running[id] {
		return "running", nil
	}
	return "", nil
}

func (f *fakeLightVMRuntime) RunningLightVM(id string) bool {
	return f.running[id]
}

func (f *fakeLightVMRuntime) LightVMConsoleSocket(id string, consoleID int64) string {
	return filepath.Join("/tmp", fmt.Sprintf("%s-%d.console", id, consoleID))
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
		"consoles": []map[string]any{{"name": "serial"}, {"name": "logs"}},
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %+v", response.StatusCode, created)
	}
	id, _ := created["id"].(string)
	if id == "" || created["status"] != "stopped" || created["desired_state"] != "stopped" {
		t.Fatalf("created lifecycle = %+v", created)
	}
	createdConsoles, _ := created["consoles"].([]any)
	if len(createdConsoles) != 2 {
		t.Fatalf("created consoles = %+v", created["consoles"])
	}
	firstConsole, _ := createdConsoles[0].(map[string]any)
	secondConsole, _ := createdConsoles[1].(map[string]any)
	if firstConsole["port_name"] != "org.example.console" || secondConsole["port_name"] != "org.example.console.2" {
		t.Fatalf("created console ports = %+v", createdConsoles)
	}

	response, started := request(http.MethodPost, "/api/lightvm/instances/"+id+"/start", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, body = %+v", response.StatusCode, started)
	}
	if started["status"] != "running" || started["desired_state"] != "running" {
		t.Fatalf("started lifecycle = %+v", started)
	}
	if started["console_socket"] != "/tmp/"+id+"-1.console" {
		t.Fatalf("started console socket = %+v", started["console_socket"])
	}
	startedConsoles, _ := started["consoles"].([]any)
	if len(startedConsoles) != 2 {
		t.Fatalf("started consoles = %+v", started["consoles"])
	}
	for index, value := range startedConsoles {
		console, _ := value.(map[string]any)
		want := fmt.Sprintf("/tmp/%s-%d.console", id, index+1)
		if console["socket"] != want {
			t.Fatalf("started console %d socket = %+v, want %q", index, console["socket"], want)
		}
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

type fakeLightVMPlanner struct {
	err error
}

func (p *fakeLightVMPlanner) Plan(context.Context, store.VM) (qemu.StartPlan, error) {
	return qemu.StartPlan{}, p.err
}
func (p *fakeLightVMPlanner) PlanDisks(context.Context, []store.VMDisk) (qemu.StartPlan, error) {
	return qemu.StartPlan{}, p.err
}

func TestLightVMLifecycleRuntimeErrors(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "lightvm-errors.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{
		Name: "vmlinuz-errors", Path: "/tmp/vmlinuz", FileName: "vmlinuz", SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{
		Name: "initramfs-errors", Path: "/tmp/initramfs", FileName: "initramfs", SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeLightVMRuntime{running: make(map[string]bool)}
	planner := &fakeLightVMPlanner{}
	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithLightVMRuntime(runtime), gateway.WithStartPlanner(planner))
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
			_ = json.NewDecoder(response.Body).Decode(&decoded)
		}
		_ = response.Body.Close()
		return response, decoded
	}

	vm, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "errors", VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/app",
	})
	if err != nil {
		t.Fatal(err)
	}
	basePath := "/api/lightvm/instances/" + vm.ID
	updateBody := map[string]any{
		"name": "errors", "vcpus": 1, "memory_mb": 128,
		"kernel_id": kernel.ID, "initramfs_id": initramfs.ID,
		"image": "alpine", "executable_path": "/bin/app",
		"executable_args": []string{}, "interface_ids": []int64{},
	}

	missingPath := "/api/lightvm/instances/" + uuid.NewString()
	response, _ := request(http.MethodPost, missingPath+"/start", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing start = %d", response.StatusCode)
	}
	response, _ = request(http.MethodPost, missingPath+"/stop", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing stop = %d", response.StatusCode)
	}
	response, _ = request(http.MethodDelete, missingPath, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing delete = %d", response.StatusCode)
	}
	response, _ = request(http.MethodGet, missingPath, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing get = %d", response.StatusCode)
	}

	runtime.running[vm.ID] = true
	if _, err := st.SetLightVMDesiredState(ctx, vm.ID, store.DesiredRunning); err != nil {
		t.Fatal(err)
	}
	runtime.runStateErr = errors.New("monitor unavailable")
	response, runStateBody := request(http.MethodPost, basePath+"/start", nil)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("run state error start = %d, body = %+v", response.StatusCode, runStateBody)
	}
	runtime.runStateErr = nil
	runtime.runState = "paused (io-error)"
	response, _ = request(http.MethodPost, basePath+"/start", nil)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("paused start = %d", response.StatusCode)
	}
	response, _ = request(http.MethodGet, basePath, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("failed run state get = %d", response.StatusCode)
	}
	runtime.runState = ""
	runtime.running[vm.ID] = false
	if _, err := st.SetLightVMDesiredState(ctx, vm.ID, store.DesiredStopped); err != nil {
		t.Fatal(err)
	}

	runtime.prepareErr = errors.New("prepare failed")
	response, _ = request(http.MethodPost, basePath+"/start", nil)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("prepare error start = %d", response.StatusCode)
	}
	runtime.prepareErr = nil
	planner.err = errors.New("plan failed")
	response, _ = request(http.MethodPost, basePath+"/start", nil)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("planner error start = %d", response.StatusCode)
	}
	planner.err = nil

	runtime.startPreparedErr = errors.New("qemu failed")
	response, _ = request(http.MethodPost, basePath+"/start", nil)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("start prepared error = %d", response.StatusCode)
	}
	runtime.startPreparedErr = qemu.ErrAlreadyRunning
	response, _ = request(http.MethodPost, basePath+"/start", nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("already running start = %d", response.StatusCode)
	}
	runtime.startPreparedErr = nil
	runtime.running[vm.ID] = false

	runtime.startPreparedErr = errors.New("qemu failed")
	runtime.stopErr = errors.New("stop failed")
	response, _ = request(http.MethodPost, basePath+"/start", nil)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failed start with failed stop = %d", response.StatusCode)
	}
	runtime.startPreparedErr = nil
	runtime.stopErr = nil
	runtime.running[vm.ID] = false

	runtime.onPrepare = func(changed store.LightVM) {
		if _, err := st.SetLightVMDesiredState(ctx, changed.ID, store.DesiredStopped); err != nil {
			t.Fatal(err)
		}
	}
	response, _ = request(http.MethodPost, basePath+"/start", nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("changed during start = %d", response.StatusCode)
	}
	runtime.onPrepare = nil

	response, started := request(http.MethodPost, basePath+"/start", nil)
	if response.StatusCode != http.StatusOK || started["status"] != "running" {
		t.Fatalf("successful start = %d, %+v", response.StatusCode, started)
	}
	runtime.runStateErr = errors.New("monitor unavailable")
	response, _ = request(http.MethodGet, basePath, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("run state warning get = %d", response.StatusCode)
	}
	runtime.runStateErr = nil

	runtime.stopErr = errors.New("stop failed")
	response, _ = request(http.MethodPost, basePath+"/stop", nil)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("stop error = %d", response.StatusCode)
	}
	runtime.stopErr = nil
	runtime.stopLeavesRunning = true
	response, _ = request(http.MethodPost, basePath+"/stop", nil)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("stop still running = %d", response.StatusCode)
	}
	runtime.stopLeavesRunning = false
	runtime.running[vm.ID] = false
	runtime.cleanupStoppedErr = errors.New("cleanup failed")
	response, _ = request(http.MethodPost, basePath+"/stop", nil)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("stop cleanup error = %d", response.StatusCode)
	}
	response, _ = request(http.MethodDelete, basePath, nil)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("delete cleanup error = %d", response.StatusCode)
	}
	runtime.cleanupStoppedErr = nil

	response, _ = request(http.MethodPost, basePath+"/start", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restart = %d", response.StatusCode)
	}
	response, _ = request(http.MethodPost, basePath+"/stop", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stop before no-runtime tests = %d", response.StatusCode)
	}

	noRuntime, err := gateway.New(st, fakeReadiness{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(noRuntime.Close)
	noRuntimeServer := httptest.NewServer(noRuntime.Handler)
	t.Cleanup(noRuntimeServer.Close)
	if _, err := st.SetLightVMDesiredState(ctx, vm.ID, store.DesiredRunning); err != nil {
		t.Fatal(err)
	}
	noRuntimeRequest := func(method, path string, body any) int {
		data, _ := json.Marshal(body)
		req, err := http.NewRequest(method, noRuntimeServer.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := noRuntimeRequest(http.MethodPut, basePath, updateBody); code != http.StatusServiceUnavailable {
		t.Fatalf("update without runtime = %d", code)
	}
	if code := noRuntimeRequest(http.MethodDelete, basePath, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("delete without runtime = %d", code)
	}
	if _, err := st.SetLightVMDesiredState(ctx, vm.ID, store.DesiredStopped); err != nil {
		t.Fatal(err)
	}
	if code := noRuntimeRequest(http.MethodDelete, basePath, nil); code != http.StatusNoContent {
		t.Fatalf("delete after stop = %d", code)
	}
}
