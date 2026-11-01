package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func createLightVM(t *testing.T, st *store.Store, name string, kernel store.LightVMKernel, initramfs store.LightVMInitramfs, interfaceIDs ...int64) store.LightVM {
	t.Helper()
	vm, err := st.CreateLightVM(context.Background(), store.LightVM{
		Name:        name,
		Description: "test lightvm",
		VCPUs:       2,
		MemoryMB:    1024,
		KernelID:    kernel.ID,
		InitramfsID: initramfs.ID,
		Image:       "alpine:3.20",
		Env: []store.LightVMEnvVar{
			{Name: "APP_ENV", Value: "test"},
		},
		ExecutablePath: "/usr/local/bin/app",
		ExecutableArgs: []string{"--listen", ":8080"},
		InterfaceIDs:   interfaceIDs,
	})
	if err != nil {
		t.Fatalf("create lightvm %s: %v", name, err)
	}
	return vm
}

func TestLightVMCRUD(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-6.12")
	initramfs := createInitramfs(t, st, "initramfs-6.12")

	created := createLightVM(t, st, "app-01", kernel, initramfs)
	if created.ID == "" || created.Name != "app-01" || created.KernelID != kernel.ID ||
		created.InitramfsID != initramfs.ID || created.Image != "alpine:3.20" {
		t.Fatalf("created lightvm = %+v", created)
	}
	if created.KernelName != kernel.Name || created.InitramfsName != initramfs.Name {
		t.Fatalf("created lightvm references = %+v", created)
	}
	if len(created.Env) != 1 || created.Env[0].Name != "APP_ENV" ||
		len(created.ExecutableArgs) != 2 || created.ExecutableArgs[1] != ":8080" {
		t.Fatalf("created lightvm env/args = %+v %+v", created.Env, created.ExecutableArgs)
	}
	if created.Append != store.DefaultLightVMAppend {
		t.Fatalf("created lightvm append = %q, want default %q", created.Append, store.DefaultLightVMAppend)
	}
	if created.Status != store.StatusStopped || created.DesiredState != store.DesiredStopped {
		t.Fatalf("created lightvm lifecycle = %+v", created)
	}
	if len(created.Consoles) != 1 || created.Consoles[0].Name != "console" ||
		created.Consoles[0].PortName != "org.example.console" {
		t.Fatalf("created lightvm consoles = %+v", created.Consoles)
	}

	got, err := st.GetLightVM(ctx, created.ID)
	if err != nil {
		t.Fatalf("get lightvm: %v", err)
	}
	if got.Name != created.Name || got.ExecutablePath != created.ExecutablePath {
		t.Fatalf("get lightvm = %+v, want %+v", got, created)
	}

	vms, err := st.ListLightVMs(ctx)
	if err != nil {
		t.Fatalf("list lightvms: %v", err)
	}
	if len(vms) != 1 || vms[0].ID != created.ID {
		t.Fatalf("list lightvms = %+v", vms)
	}

	updated, err := st.UpdateLightVM(ctx, store.LightVM{
		ID:             created.ID,
		Name:           "app-02",
		Description:    "updated",
		VCPUs:          4,
		MemoryMB:       2048,
		KernelID:       kernel.ID,
		InitramfsID:    initramfs.ID,
		Image:          "alpine:3.21",
		Env:            []store.LightVMEnvVar{{Name: "APP_ENV", Value: "prod"}},
		ExecutablePath: "/bin/app",
		ExecutableArgs: []string{"serve"},
		Append:         "console=ttyS0 rdinit=/sbin/init",
		Consoles:       []store.LightVMConsole{{Name: "serial"}, {Name: "logs"}},
	})
	if err != nil {
		t.Fatalf("update lightvm: %v", err)
	}
	if updated.Name != "app-02" || updated.VCPUs != 4 || updated.Env[0].Value != "prod" ||
		updated.ExecutableArgs[0] != "serve" || updated.Append != "console=ttyS0 rdinit=/sbin/init" {
		t.Fatalf("updated lightvm = %+v", updated)
	}
	if len(updated.Consoles) != 2 || updated.Consoles[0].Name != "serial" ||
		updated.Consoles[0].PortName != "org.example.console" ||
		updated.Consoles[1].Name != "logs" ||
		updated.Consoles[1].PortName != "org.example.console.2" {
		t.Fatalf("updated lightvm consoles = %+v", updated.Consoles)
	}

	started, err := st.SetLightVMStatus(ctx, created.ID, store.StatusRunning)
	if err != nil {
		t.Fatalf("set lightvm status: %v", err)
	}
	if started.Status != store.StatusRunning {
		t.Fatalf("lightvm status = %q", started.Status)
	}
	started, err = st.SetLightVMDesiredState(ctx, created.ID, store.DesiredRunning)
	if err != nil {
		t.Fatalf("set lightvm desired state: %v", err)
	}
	if started.DesiredState != store.DesiredRunning {
		t.Fatalf("lightvm desired state = %q", started.DesiredState)
	}

	if err := st.DeleteLightVM(ctx, created.ID); err != nil {
		t.Fatalf("delete lightvm: %v", err)
	}
	if _, err := st.GetLightVM(ctx, created.ID); !errors.Is(err, store.ErrLightVMNotFound) {
		t.Fatalf("get deleted lightvm error = %v, want ErrLightVMNotFound", err)
	}
	if err := st.DeleteLightVM(ctx, created.ID); !errors.Is(err, store.ErrLightVMNotFound) {
		t.Fatalf("delete missing lightvm error = %v, want ErrLightVMNotFound", err)
	}
}

func TestLightVMNamesAreUnique(t *testing.T) {
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-6.12")
	initramfs := createInitramfs(t, st, "initramfs-6.12")
	createLightVM(t, st, "app-01", kernel, initramfs)

	_, err := st.CreateLightVM(context.Background(), store.LightVM{
		Name: "APP-01", VCPUs: 1, MemoryMB: 512,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/app",
	})
	if !errors.Is(err, store.ErrLightVMNameTaken) {
		t.Fatalf("create duplicate lightvm error = %v, want ErrLightVMNameTaken", err)
	}
}

func TestLightVMReferencesKernelAndInitramfs(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-6.12")
	initramfs := createInitramfs(t, st, "initramfs-6.12")
	createLightVM(t, st, "app-01", kernel, initramfs)

	if _, err := st.DeleteLightVMKernel(ctx, kernel.ID); !errors.Is(err, store.ErrLightVMKernelInUse) {
		t.Fatalf("delete referenced kernel error = %v, want ErrLightVMKernelInUse", err)
	}
	if _, err := st.DeleteLightVMInitramfs(ctx, initramfs.ID); !errors.Is(err, store.ErrLightVMInitramfsInUse) {
		t.Fatalf("delete referenced initramfs error = %v, want ErrLightVMInitramfsInUse", err)
	}
}

func TestLightVMInterfaceConflicts(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	network := createNetwork(t, st, "lab")
	iface := createInterface(t, st, "app-if", network.ID)
	kernel := createKernel(t, st, "vmlinuz-6.12")
	initramfs := createInitramfs(t, st, "initramfs-6.12")

	vm := createLightVM(t, st, "app-01", kernel, initramfs, iface.ID)
	if len(vm.InterfaceIDs) != 1 || vm.InterfaceIDs[0] != iface.ID || len(vm.Interfaces) != 1 {
		t.Fatalf("lightvm interfaces = %+v, ids = %v", vm.Interfaces, vm.InterfaceIDs)
	}
	attachedIface, err := st.GetInterface(ctx, iface.ID)
	if err != nil {
		t.Fatalf("get attached interface: %v", err)
	}
	if attachedIface.AttachedLightVMID == nil || *attachedIface.AttachedLightVMID != vm.ID ||
		attachedIface.AttachedLightVMName != vm.Name {
		t.Fatalf("interface lightvm attachment = %+v", attachedIface)
	}

	if _, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "app-02", VCPUs: 1, MemoryMB: 512,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/app",
		InterfaceIDs: []int64{iface.ID},
	}); !errors.Is(err, store.ErrInterfaceInUse) {
		t.Fatalf("create second lightvm with interface error = %v, want ErrInterfaceInUse", err)
	}

	disk := createDisk(t, st, "app-root")
	regularVM, err := st.CreateVM(ctx, store.VM{
		Name: "regular", Status: store.StatusStopped, VCPUs: 1, MemoryMB: 512, DiskID: &disk.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetInterfaceAttachment(ctx, iface.ID, &regularVM.ID); !errors.Is(err, store.ErrInterfaceInUse) {
		t.Fatalf("attach lightvm interface to vm error = %v, want ErrInterfaceInUse", err)
	}
	if _, err := st.SetVMInterfaces(ctx, regularVM.ID, []int64{iface.ID}); !errors.Is(err, store.ErrInterfaceInUse) {
		t.Fatalf("set vm interfaces error = %v, want ErrInterfaceInUse", err)
	}
	if err := st.DeleteInterface(ctx, iface.ID); !errors.Is(err, store.ErrInterfaceInUse) {
		t.Fatalf("delete lightvm interface error = %v, want ErrInterfaceInUse", err)
	}

	if err := st.DeleteLightVM(ctx, vm.ID); err != nil {
		t.Fatalf("delete lightvm: %v", err)
	}
	if _, err := st.SetInterfaceAttachment(ctx, iface.ID, &regularVM.ID); err != nil {
		t.Fatalf("attach released interface to vm: %v", err)
	}
}
