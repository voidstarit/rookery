package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func TestLightVMDiskAttachments(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-6.12")
	initramfs := createInitramfs(t, st, "initramfs-6.12")
	first := createDisk(t, st, "lv-disk-1")
	second := createDisk(t, st, "lv-disk-2")

	created, err := st.CreateLightVM(ctx, store.LightVM{
		Name:           "disk-app",
		VCPUs:          1,
		MemoryMB:       256,
		KernelID:       kernel.ID,
		InitramfsID:    initramfs.ID,
		Image:          "alpine",
		ExecutablePath: "/bin/sh",
		DiskIDs:        []int64{second.ID, first.ID},
	})
	if err != nil {
		t.Fatalf("create lightvm with disks: %v", err)
	}
	if len(created.DiskIDs) != 2 || created.DiskIDs[0] != second.ID || created.DiskIDs[1] != first.ID {
		t.Fatalf("created disk ids = %v", created.DiskIDs)
	}
	if len(created.Disks) != 2 || created.Disks[0].ID != second.ID {
		t.Fatalf("created disks = %+v", created.Disks)
	}
	attached, err := st.GetDisk(ctx, first.ID)
	if err != nil {
		t.Fatalf("get attached disk: %v", err)
	}
	if attached.AttachedLightVMID == nil || *attached.AttachedLightVMID != created.ID ||
		attached.AttachedLightVMName != created.Name {
		t.Fatalf("disk owner = %v/%q, want %s", attached.AttachedLightVMID, attached.AttachedLightVMName, created.ID)
	}

	updated, err := st.UpdateLightVM(ctx, store.LightVM{
		ID:             created.ID,
		Name:           created.Name,
		VCPUs:          created.VCPUs,
		MemoryMB:       created.MemoryMB,
		KernelID:       kernel.ID,
		InitramfsID:    initramfs.ID,
		Image:          created.Image,
		ExecutablePath: created.ExecutablePath,
		DiskIDs:        []int64{first.ID, second.ID},
		ReplaceDiskIDs: true,
	})
	if err != nil {
		t.Fatalf("replace lightvm disks: %v", err)
	}
	if len(updated.DiskIDs) != 2 || updated.DiskIDs[0] != first.ID || updated.DiskIDs[1] != second.ID {
		t.Fatalf("updated disk ids = %v", updated.DiskIDs)
	}
	preserved, err := st.UpdateLightVM(ctx, store.LightVM{
		ID:             created.ID,
		Name:           "disk-app-renamed",
		VCPUs:          created.VCPUs,
		MemoryMB:       created.MemoryMB,
		KernelID:       kernel.ID,
		InitramfsID:    initramfs.ID,
		Image:          created.Image,
		ExecutablePath: created.ExecutablePath,
	})
	if err != nil {
		t.Fatalf("rename lightvm preserving disks: %v", err)
	}
	if len(preserved.DiskIDs) != 2 || preserved.DiskIDs[0] != first.ID {
		t.Fatalf("preserved disk ids = %v", preserved.DiskIDs)
	}
	if err := st.DeleteLightVM(ctx, created.ID); err != nil {
		t.Fatalf("delete lightvm: %v", err)
	}
	for _, disk := range []store.Disk{first, second} {
		released, err := st.GetDisk(ctx, disk.ID)
		if err != nil {
			t.Fatalf("get released disk %d: %v", disk.ID, err)
		}
		if released.AttachedLightVMID != nil || released.AttachedVMID != nil {
			t.Fatalf("disk %d still attached: %+v", disk.ID, released)
		}
	}
}

func TestLightVMDiskConflictsWithVM(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-6.12")
	initramfs := createInitramfs(t, st, "initramfs-6.12")
	vm := createVM(t, st, "owner-vm")
	_, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "conflict", VCPUs: 1, MemoryMB: 256,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh",
		DiskIDs: []int64{vm.DiskIDs[0]},
	})
	var inUse *store.DiskInUseError
	if !errors.Is(err, store.ErrDiskInUse) || !errors.As(err, &inUse) ||
		inUse.OwnerKind != "vm" || inUse.OwnerID != vm.ID {
		t.Fatalf("lightvm disk conflict error = %v (%#v), want vm owner", err, inUse)
	}
}

func TestLightVMDeviceBudgetAndGeneration(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-6.12")
	initramfs := createInitramfs(t, st, "initramfs-6.12")
	network := createNetwork(t, st, "budget-net")
	makeInterfaces := func(prefix string, count int) []int64 {
		ids := make([]int64, 0, count)
		for i := 0; i < count; i++ {
			iface := createInterface(t, st, fmt.Sprintf("%s-%d", prefix, i), network.ID)
			ids = append(ids, iface.ID)
		}
		return ids
	}
	makeDisks := func(prefix string, count int) []int64 {
		ids := make([]int64, 0, count)
		for i := 0; i < count; i++ {
			disk := createDisk(t, st, fmt.Sprintf("%s-%d", prefix, i))
			ids = append(ids, disk.ID)
		}
		return ids
	}
	diskIDs := makeDisks("budget", 17)
	firstInterfaces := makeInterfaces("budget-if", 8)
	secondInterfaces := makeInterfaces("budget-if2", 8)
	thirdInterfaces := makeInterfaces("budget-if3", 9)

	created, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "twenty-four", VCPUs: 1, MemoryMB: 256,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh",
		DiskIDs: diskIDs[:16], InterfaceIDs: firstInterfaces,
	})
	if err != nil {
		t.Fatalf("create 24-device lightvm: %v", err)
	}
	if created.LifecycleGeneration != 0 {
		t.Fatalf("initial generation = %d, want 0", created.LifecycleGeneration)
	}
	secondDiskIDs := makeDisks("budget2", 17)
	_, err = st.CreateLightVM(ctx, store.LightVM{
		Name: "twenty-five-create", VCPUs: 1, MemoryMB: 256,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh",
		DiskIDs: secondDiskIDs, InterfaceIDs: secondInterfaces,
	})
	if !errors.Is(err, store.ErrLightVMDeviceLimit) {
		t.Fatalf("create 25-device error = %v, want ErrLightVMDeviceLimit", err)
	}
	_, err = st.CreateLightVM(ctx, store.LightVM{
		Name: "twenty-five-interfaces", VCPUs: 1, MemoryMB: 256,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh",
		DiskIDs: secondDiskIDs[:16], InterfaceIDs: thirdInterfaces,
	})
	if !errors.Is(err, store.ErrLightVMDeviceLimit) {
		t.Fatalf("create 16+9 error = %v, want ErrLightVMDeviceLimit", err)
	}
	_, err = st.UpdateLightVM(ctx, store.LightVM{
		ID: created.ID, Name: created.Name, VCPUs: created.VCPUs, MemoryMB: created.MemoryMB,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: created.Image, ExecutablePath: created.ExecutablePath,
		InterfaceIDs: thirdInterfaces,
	})
	if !errors.Is(err, store.ErrLightVMDeviceLimit) {
		t.Fatalf("update 16+9 error = %v, want ErrLightVMDeviceLimit", err)
	}

	started, err := st.SetLightVMStatus(ctx, created.ID, store.StatusRunning)
	if err != nil {
		t.Fatalf("set status: %v", err)
	}
	if started.LifecycleGeneration != 1 {
		t.Fatalf("generation after status = %d, want 1", started.LifecycleGeneration)
	}
	desired, err := st.SetLightVMDesiredState(ctx, created.ID, store.DesiredRunning)
	if err != nil {
		t.Fatalf("set desired state: %v", err)
	}
	if desired.LifecycleGeneration != 2 {
		t.Fatalf("generation after desired state = %d, want 2", desired.LifecycleGeneration)
	}
	updated, err := st.UpdateLightVM(ctx, store.LightVM{
		ID: created.ID, Name: "twenty-four-updated", VCPUs: created.VCPUs, MemoryMB: created.MemoryMB,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: created.Image, ExecutablePath: created.ExecutablePath,
	})
	if err != nil {
		t.Fatalf("rename preserved 24-device lightvm: %v", err)
	}
	if updated.LifecycleGeneration != 3 || updated.DesiredState != store.DesiredStopped {
		t.Fatalf("updated lifecycle = generation %d desired %q, want 3/stopped", updated.LifecycleGeneration, updated.DesiredState)
	}
	read, err := st.GetLightVM(ctx, created.ID)
	if err != nil {
		t.Fatalf("get lightvm: %v", err)
	}
	if read.LifecycleGeneration != 3 {
		t.Fatalf("read changed generation to %d", read.LifecycleGeneration)
	}
}

func TestLightVMVirtioFSShares(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-6.12")
	initramfs := createInitramfs(t, st, "initramfs-6.12")
	dir1 := filepath.Join(t.TempDir(), "share-one")
	dir2 := filepath.Join(t.TempDir(), "share-two")
	if err := os.MkdirAll(dir1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatal(err)
	}

	created, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "share-app", VCPUs: 1, MemoryMB: 256,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh",
		VirtioFSShares: []store.LightVMVirtioFSShare{
			{HostPath: dir1, Tag: "tag1", ReadOnly: true},
			{HostPath: dir2, Tag: "tag2", ReadOnly: false},
		},
	})
	if err != nil {
		t.Fatalf("create lightvm with shares: %v", err)
	}
	if len(created.VirtioFSShares) != 2 ||
		created.VirtioFSShares[0].Tag != "tag1" || !created.VirtioFSShares[0].ReadOnly ||
		created.VirtioFSShares[1].Tag != "tag2" || created.VirtioFSShares[1].ReadOnly {
		t.Fatalf("created shares = %+v", created.VirtioFSShares)
	}
	if created.VirtioFSShares[0].HostPath != dir1 || created.VirtioFSShares[1].HostPath != dir2 {
		t.Fatalf("share paths = %+v", created.VirtioFSShares)
	}

	updated, err := st.UpdateLightVM(ctx, store.LightVM{
		ID: created.ID, Name: created.Name, VCPUs: created.VCPUs, MemoryMB: created.MemoryMB,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: created.Image, ExecutablePath: created.ExecutablePath,
		VirtioFSShares: []store.LightVMVirtioFSShare{
			{HostPath: dir2, Tag: "data", ReadOnly: true},
		},
	})
	if err != nil {
		t.Fatalf("update shares: %v", err)
	}
	if len(updated.VirtioFSShares) != 1 || updated.VirtioFSShares[0].Tag != "data" ||
		!updated.VirtioFSShares[0].ReadOnly {
		t.Fatalf("updated shares = %+v", updated.VirtioFSShares)
	}
	if got, err := st.GetLightVM(ctx, created.ID); err != nil || len(got.VirtioFSShares) != 1 {
		t.Fatalf("get shares = %+v, %v", got.VirtioFSShares, err)
	}

	for _, tc := range []struct {
		name  string
		share store.LightVMVirtioFSShare
	}{
		{name: "relative", share: store.LightVMVirtioFSShare{HostPath: "relative", Tag: "x"}},
		{name: "missing", share: store.LightVMVirtioFSShare{HostPath: filepath.Join(dir1, "missing"), Tag: "x"}},
		{name: "rootfs", share: store.LightVMVirtioFSShare{HostPath: dir1, Tag: "rootfs"}},
		{name: "bad tag", share: store.LightVMVirtioFSShare{HostPath: dir1, Tag: "bad tag"}},
	} {
		_, err := st.CreateLightVM(ctx, store.LightVM{
			Name: "bad-" + tc.name, VCPUs: 1, MemoryMB: 256,
			KernelID: kernel.ID, InitramfsID: initramfs.ID,
			Image: "alpine", ExecutablePath: "/bin/sh",
			VirtioFSShares: []store.LightVMVirtioFSShare{tc.share},
		})
		if !errors.Is(err, store.ErrInvalidVirtioFSShare) {
			t.Fatalf("%s share error = %v, want ErrInvalidVirtioFSShare", tc.name, err)
		}
	}
	_, err = st.CreateLightVM(ctx, store.LightVM{
		Name: "duplicate-tag", VCPUs: 1, MemoryMB: 256,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh",
		VirtioFSShares: []store.LightVMVirtioFSShare{
			{HostPath: dir1, Tag: "same", ReadOnly: true},
			{HostPath: dir2, Tag: "same", ReadOnly: true},
		},
	})
	if !errors.Is(err, store.ErrInvalidVirtioFSShare) {
		t.Fatalf("duplicate tag error = %v, want ErrInvalidVirtioFSShare", err)
	}

	if err := st.DeleteLightVM(ctx, created.ID); err != nil {
		t.Fatalf("delete lightvm: %v", err)
	}
}

func TestLightVMShareBudget(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-6.12")
	initramfs := createInitramfs(t, st, "initramfs-6.12")
	root := t.TempDir()
	shares := make([]store.LightVMVirtioFSShare, 0, 25)
	for i := 0; i < 25; i++ {
		dir := filepath.Join(root, fmt.Sprintf("share-%02d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		shares = append(shares, store.LightVMVirtioFSShare{HostPath: dir, Tag: fmt.Sprintf("share%02d", i), ReadOnly: true})
	}
	if _, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "twenty-four-shares", VCPUs: 1, MemoryMB: 256,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh",
		VirtioFSShares: shares[:24],
	}); err != nil {
		t.Fatalf("create 24 shares: %v", err)
	}
	if _, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "twenty-five-shares", VCPUs: 1, MemoryMB: 256,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh",
		VirtioFSShares: shares,
	}); !errors.Is(err, store.ErrLightVMDeviceLimit) {
		t.Fatalf("create 25 shares error = %v, want ErrLightVMDeviceLimit", err)
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
