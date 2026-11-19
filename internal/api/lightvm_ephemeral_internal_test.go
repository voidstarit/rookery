package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/lightvm"
	"github.com/voidstarit/rookery/internal/store"
)

func newEphemeralTestServer(t *testing.T, controller *reconcileLightVMController, ephemeral bool) (*Server, *store.Store, store.LightVM) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "ephemeral.db"))
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
		Name: "once", VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/app",
		Ephemeral: ephemeral,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: st, lightvms: controller, logger: zerolog.Nop()}
	return server, st, vm
}

func setEphemeralLifecycle(t *testing.T, st *store.Store, vm store.LightVM, status, desired string) store.LightVM {
	t.Helper()
	ctx := context.Background()
	if _, err := st.SetLightVMStatus(ctx, vm.ID, status); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetLightVMDesiredState(ctx, vm.ID, desired); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetLightVM(ctx, vm.ID)
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func TestReapEphemeralLightVM(t *testing.T) {
	ctx := context.Background()

	t.Run("terminates and deletes an exited guest", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newEphemeralTestServer(t, controller, true)
		vm = setEphemeralLifecycle(t, st, vm, store.StatusRunning, store.DesiredRunning)

		server.reapEphemeralLightVMs(ctx)

		if _, err := st.GetLightVM(ctx, vm.ID); !errors.Is(err, store.ErrLightVMNotFound) {
			t.Fatalf("terminated LightVM get error = %v, want ErrLightVMNotFound", err)
		}
		if controller.stopped != 1 || controller.cleanupStopped != 1 {
			t.Fatalf("stopped = %d, cleanupStopped = %d", controller.stopped, controller.cleanupStopped)
		}

		server.reapEphemeralLightVMs(ctx)
		if controller.stopped != 1 || controller.cleanupStopped != 1 {
			t.Fatalf("second reap repeated cleanup: stopped = %d, cleanupStopped = %d", controller.stopped, controller.cleanupStopped)
		}
	})

	t.Run("leaves a running guest alone", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newEphemeralTestServer(t, controller, true)
		vm = setEphemeralLifecycle(t, st, vm, store.StatusRunning, store.DesiredRunning)
		controller.running[vm.ID] = true

		server.reapEphemeralLightVMs(ctx)

		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != store.StatusRunning || current.DesiredState != store.DesiredRunning {
			t.Fatalf("running record changed = %+v", current)
		}
		if controller.stopped != 0 || controller.cleanupStopped != 0 {
			t.Fatalf("running guest was reaped: stopped = %d", controller.stopped)
		}
	})

	t.Run("ignores non-ephemeral LightVMs", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newEphemeralTestServer(t, controller, false)
		vm = setEphemeralLifecycle(t, st, vm, store.StatusRunning, store.DesiredRunning)

		server.reapEphemeralLightVMs(ctx)

		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != store.StatusRunning || controller.stopped != 0 {
			t.Fatalf("non-ephemeral record changed = %+v", current)
		}
	})

	t.Run("deletes a stale starting run", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newEphemeralTestServer(t, controller, true)
		vm = setEphemeralLifecycle(t, st, vm, store.StatusStarting, store.DesiredRunning)

		server.reapEphemeralLightVMs(ctx)

		if _, err := st.GetLightVM(ctx, vm.ID); !errors.Is(err, store.ErrLightVMNotFound) {
			t.Fatalf("stale starting LightVM get error = %v, want ErrLightVMNotFound", err)
		}
	})

	t.Run("ignores a start in progress", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newEphemeralTestServer(t, controller, true)
		vm = setEphemeralLifecycle(t, st, vm, store.StatusStarting, store.DesiredRunning)
		coordinator := lightvm.NewStartCoordinator()
		server.lightvmCoordinator = coordinator
		_, done, err := coordinator.Begin(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer done()

		server.reapEphemeralLightVMs(ctx)

		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != store.StatusStarting || controller.cleanupStopped != 0 {
			t.Fatalf("active start record changed = %+v", current)
		}
	})
}

func TestEphemeralTerminationReleasesAttachments(t *testing.T) {
	ctx := context.Background()
	controller := &reconcileLightVMController{running: map[string]bool{}}
	server, st, vm := newEphemeralTestServer(t, controller, true)

	network, err := st.CreateNetwork(ctx, store.Network{
		Name: "lab", IPv4CIDR: "10.0.0.0/16", IPv6CIDR: "fd00:20::/64",
	})
	if err != nil {
		t.Fatal(err)
	}
	iface, err := st.CreateInterface(ctx, store.VirtualInterface{
		Name: "once-if", NetworkID: network.ID, MAC: "02:00:00:00:00:02",
	})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "once-root", SizeGB: 1, VG: "vg0", ThinPool: "pool",
	})
	if err != nil {
		t.Fatal(err)
	}
	vm, err = st.UpdateLightVM(ctx, store.LightVM{
		ID:             vm.ID,
		Name:           vm.Name,
		VCPUs:          vm.VCPUs,
		MemoryMB:       vm.MemoryMB,
		KernelID:       vm.KernelID,
		InitramfsID:    vm.InitramfsID,
		Image:          vm.Image,
		ExecutablePath: vm.ExecutablePath,
		Ephemeral:      true,
		DiskIDs:        []int64{disk.ID},
		InterfaceIDs:   []int64{iface.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	vm = setEphemeralLifecycle(t, st, vm, store.StatusRunning, store.DesiredRunning)

	server.reapEphemeralLightVMs(ctx)

	if _, err := st.GetLightVM(ctx, vm.ID); !errors.Is(err, store.ErrLightVMNotFound) {
		t.Fatalf("terminated LightVM get error = %v, want ErrLightVMNotFound", err)
	}
	releasedDisk, err := st.GetDisk(ctx, disk.ID)
	if err != nil {
		t.Fatalf("released disk is gone: %v", err)
	}
	if releasedDisk.AttachedLightVMID != nil || releasedDisk.AttachedVMID != nil {
		t.Fatalf("disk still attached after termination: %+v", releasedDisk)
	}
	releasedIface, err := st.GetInterface(ctx, iface.ID)
	if err != nil {
		t.Fatalf("released interface is gone: %v", err)
	}
	if releasedIface.AttachedLightVMID != nil || releasedIface.AttachedVMID != nil {
		t.Fatalf("interface still attached after termination: %+v", releasedIface)
	}
}

func TestReconcileLightVMStatusesRetiresEphemeral(t *testing.T) {
	ctx := context.Background()

	t.Run("stale running run is deleted", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newEphemeralTestServer(t, controller, true)
		vm = setEphemeralLifecycle(t, st, vm, store.StatusRunning, store.DesiredRunning)

		if err := server.reconcileLightVMStatuses(ctx); err != nil {
			t.Fatal(err)
		}

		if _, err := st.GetLightVM(ctx, vm.ID); !errors.Is(err, store.ErrLightVMNotFound) {
			t.Fatalf("reconciled LightVM get error = %v, want ErrLightVMNotFound", err)
		}
		if controller.cleanupStopped != 1 {
			t.Fatalf("cleanupStopped = %d, want 1", controller.cleanupStopped)
		}
	})

	t.Run("failed start is kept but not restarted", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newEphemeralTestServer(t, controller, true)
		vm = setEphemeralLifecycle(t, st, vm, store.StatusError, store.DesiredRunning)

		if err := server.reconcileLightVMStatuses(ctx); err != nil {
			t.Fatal(err)
		}

		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != store.StatusError || current.DesiredState != store.DesiredStopped {
			t.Fatalf("failed start record = %+v", current)
		}
		if controller.cleanupStopped != 0 {
			t.Fatalf("failed start was cleaned up")
		}
	})

	t.Run("surviving QEMU stays running", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newEphemeralTestServer(t, controller, true)
		vm = setEphemeralLifecycle(t, st, vm, store.StatusRunning, store.DesiredRunning)
		controller.running[vm.ID] = true

		if err := server.reconcileLightVMStatuses(ctx); err != nil {
			t.Fatal(err)
		}

		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != store.StatusRunning || current.DesiredState != store.DesiredRunning {
			t.Fatalf("surviving record = %+v", current)
		}
		if controller.cleanupStopped != 0 {
			t.Fatalf("surviving guest was cleaned up")
		}
	})

	t.Run("regular LightVM is left for autostart", func(t *testing.T) {
		controller := &reconcileLightVMController{running: map[string]bool{}}
		server, st, vm := newEphemeralTestServer(t, controller, false)
		vm = setEphemeralLifecycle(t, st, vm, store.StatusRunning, store.DesiredRunning)

		if err := server.reconcileLightVMStatuses(ctx); err != nil {
			t.Fatal(err)
		}

		current, err := st.GetLightVM(ctx, vm.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status != store.StatusStopped || current.DesiredState != store.DesiredRunning {
			t.Fatalf("regular record = %+v", current)
		}
	})
}

func TestAutostartSkipsEphemeralLightVM(t *testing.T) {
	ctx := context.Background()
	controller := &reconcileLightVMController{running: map[string]bool{}}
	server, st, vm := newEphemeralTestServer(t, controller, true)
	vm = setEphemeralLifecycle(t, st, vm, store.StatusStopped, store.DesiredRunning)

	server.autostartLightVMs(ctx)

	if _, err := st.GetLightVM(ctx, vm.ID); !errors.Is(err, store.ErrLightVMNotFound) {
		t.Fatalf("autostarted LightVM get error = %v, want ErrLightVMNotFound", err)
	}
	if controller.prepared != 0 || controller.started != 0 {
		t.Fatalf("ephemeral LightVM was restarted: prepared = %d, started = %d", controller.prepared, controller.started)
	}
	if controller.cleanupStopped != 1 {
		t.Fatalf("cleanupStopped = %d, want 1", controller.cleanupStopped)
	}
}
