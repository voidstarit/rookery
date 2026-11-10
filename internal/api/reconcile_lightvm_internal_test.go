package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/lightvm"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

type reconcileLightVMController struct {
	running    map[string]bool
	prepareErr error
	startErr   error
	stopErr    error
	onPrepare  func(store.LightVM)
	prepared   int
	started    int
	cleaned    int
	stopped    int
}

func (f *reconcileLightVMController) PrepareLightVM(_ context.Context, vm store.LightVM) (lightvm.PreparedLightVM, error) {
	f.prepared++
	if f.onPrepare != nil {
		f.onPrepare(vm)
	}
	if f.prepareErr != nil {
		return lightvm.PreparedLightVM{}, f.prepareErr
	}
	return lightvm.PreparedLightVM{
		LightVMID: vm.ID,
		Container: "rookery-lightvm-" + vm.ID + "-test",
		Rootfs:    "/tmp/rootfs",
	}, nil
}

func (f *reconcileLightVMController) StartPreparedLightVM(_ context.Context, prepared *lightvm.PreparedLightVM, vm store.LightVM, _ qemu.StartPlan) error {
	f.started++
	prepared.QEMUStarted = true
	if f.startErr != nil {
		return f.startErr
	}
	f.running[vm.ID] = true
	return nil
}

func (f *reconcileLightVMController) CleanupPreparedLightVM(context.Context, lightvm.PreparedLightVM) error {
	f.cleaned++
	return nil
}

func (f *reconcileLightVMController) StopQEMULightVM(_ context.Context, id string) error {
	f.stopped++
	if f.stopErr != nil {
		return f.stopErr
	}
	f.running[id] = false
	return nil
}

func (f *reconcileLightVMController) RunState(context.Context, string) (string, error) {
	return "", nil
}

func (f *reconcileLightVMController) RunningLightVM(id string) bool {
	return f.running[id]
}

func (f *reconcileLightVMController) LightVMConsoleSocket(string, int64) string {
	return ""
}

type reconcilePlanner struct {
	err error
}

func (p reconcilePlanner) Plan(context.Context, store.VM) (qemu.StartPlan, error) {
	return qemu.StartPlan{}, p.err
}

func (p reconcilePlanner) PlanDisks(context.Context, []store.VMDisk) (qemu.StartPlan, error) {
	return qemu.StartPlan{}, p.err
}

func newReconcileTestServer(t *testing.T, controller *reconcileLightVMController) (*Server, *store.Store, store.LightVM) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "reconcile.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{
		Name: "vmlinuz", Path: "/tmp/vmlinuz", FileName: "vmlinuz", SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{
		Name: "initramfs", Path: "/tmp/initramfs", FileName: "initramfs", SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "autostart", VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/app",
		DesiredState: store.DesiredRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: st, lightvms: controller, logger: zerolog.Nop()}
	return server, st, vm
}

func TestStartAutostartLightVM(t *testing.T) {
	ctx := context.Background()
	t.Run("success", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newReconcileTestServer(t, controller)
		server.startAutostartLightVM(ctx, vm)
		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if controller.started != 1 || current.Status != store.StatusRunning || current.DesiredState != store.DesiredRunning {
			t.Fatalf("started = %d, record = %+v", controller.started, current)
		}
		server.autostartLightVMs(ctx)
	})

	t.Run("already running repairs status", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newReconcileTestServer(t, controller)
		controller.running[vm.ID] = true
		server.startAutostartLightVM(ctx, vm)
		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if controller.prepared != 0 || current.Status != store.StatusRunning {
			t.Fatalf("prepared = %d, status = %q", controller.prepared, current.Status)
		}
	})

	t.Run("device budget", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newReconcileTestServer(t, controller)
		vm.Interfaces = make([]store.VMInterface, store.MaxLightVMDevices+1)
		server.startAutostartLightVM(ctx, vm)
		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if controller.prepared != 0 || current.DesiredState != store.DesiredStopped || current.Status != store.StatusError {
			t.Fatalf("prepared = %d, record = %+v", controller.prepared, current)
		}
	})

	t.Run("prepare error", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}, prepareErr: errors.New("prepare failed")}
		server, st, vm := newReconcileTestServer(t, controller)
		server.startAutostartLightVM(ctx, vm)
		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != store.StatusStopped {
			t.Fatalf("status = %q", current.Status)
		}
	})

	t.Run("planner error", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newReconcileTestServer(t, controller)
		server.startPlanner = reconcilePlanner{err: errors.New("plan failed")}
		server.startAutostartLightVM(ctx, vm)
		if controller.started != 0 || controller.cleaned != 1 {
			t.Fatalf("started = %d, cleaned = %d", controller.started, controller.cleaned)
		}
		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != store.StatusStopped {
			t.Fatalf("status = %q", current.Status)
		}
	})

	t.Run("generation changed during prepare", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newReconcileTestServer(t, controller)
		controller.onPrepare = func(prepared store.LightVM) {
			if _, err := st.SetLightVMDesiredState(ctx, prepared.ID, store.DesiredStopped); err != nil {
				t.Fatal(err)
			}
		}
		server.startAutostartLightVM(ctx, vm)
		if controller.started != 0 || controller.cleaned != 1 {
			t.Fatalf("started = %d, cleaned = %d", controller.started, controller.cleaned)
		}
		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.DesiredState != store.DesiredStopped || current.Status != store.StatusStopped {
			t.Fatalf("record = %+v", current)
		}
	})

	t.Run("running became true during prepare", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newReconcileTestServer(t, controller)
		controller.onPrepare = func(prepared store.LightVM) {
			controller.running[prepared.ID] = true
		}
		server.startAutostartLightVM(ctx, vm)
		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if controller.started != 0 || current.Status != store.StatusRunning {
			t.Fatalf("started = %d, status = %q", controller.started, current.Status)
		}
	})

	t.Run("start error", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}, startErr: errors.New("start failed")}
		server, st, vm := newReconcileTestServer(t, controller)
		server.startAutostartLightVM(ctx, vm)
		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if controller.started != 1 || current.Status != store.StatusError || current.DesiredState != store.DesiredRunning {
			t.Fatalf("started = %d, record = %+v", controller.started, current)
		}
	})

	t.Run("start error with failed stop", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}, startErr: errors.New("start failed"), stopErr: errors.New("stop failed")}
		server, st, vm := newReconcileTestServer(t, controller)
		server.startAutostartLightVM(ctx, vm)
		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if controller.stopped != 1 || controller.cleaned != 0 || current.Status != store.StatusError {
			t.Fatalf("stopped = %d, cleaned = %d, status = %q", controller.stopped, controller.cleaned, current.Status)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, _, vm := newReconcileTestServer(t, controller)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		server.startAutostartLightVM(canceled, vm)
		if controller.prepared != 0 {
			t.Fatalf("prepared = %d", controller.prepared)
		}
	})

	t.Run("store read failure after prepare", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newReconcileTestServer(t, controller)
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		server.startAutostartLightVM(ctx, vm)
		if controller.started != 0 || controller.cleaned != 1 {
			t.Fatalf("started = %d, cleaned = %d", controller.started, controller.cleaned)
		}
	})
}

func TestAutostartLightVMsNilController(t *testing.T) {
	server := &Server{logger: zerolog.Nop()}
	server.autostartLightVMs(context.Background())
	server.ConfigureLightVMStartCoordinator(nil)
	if server.lightvmCoordinator == nil {
		t.Fatal("nil coordinator was not replaced")
	}
}
