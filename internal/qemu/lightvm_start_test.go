package qemu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/store"
)

func TestFakeLightVMQEMUHelper(t *testing.T) {
	if os.Getenv("GO_WANT_FAKE_LIGHTVM_QEMU") != "1" {
		return
	}
	time.Sleep(2 * time.Minute)
}

func writeFakeLightVMQEMU(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "qemu-system-x86_64")
	content := `#!/bin/sh
if [ "$LIGHTVM_FAKE_QEMU_MODE" = "fail" ]; then echo "boot failed" >&2; exit 1; fi
if [ "$LIGHTVM_FAKE_QEMU_MODE" = "exit" ]; then exit 0; fi
pidfile=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "-pidfile" ]; then pidfile="$arg"; fi
  prev="$arg"
done
"$LIGHTVM_QEMU_TEST_BINARY" -test.run=TestFakeLightVMQEMUHelper -- "$@" >/dev/null 2>&1 &
echo $! > "$pidfile"
exit 0
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestLightVMStartAndStopLifecycle(t *testing.T) {
	oldKVM := kvmDevice
	fakeKVM := filepath.Join(t.TempDir(), "kvm")
	kvmDevice = fakeKVM
	if err := os.WriteFile(kvmDevice, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { kvmDevice = oldKVM })

	dir := t.TempDir()
	t.Setenv("LIGHTVM_QEMU_TEST_BINARY", os.Args[0])
	t.Setenv("GO_WANT_FAKE_LIGHTVM_QEMU", "1")
	q, err := New(writeFakeLightVMQEMU(t, dir), filepath.Join(dir, "run"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	vm := testLightVM()
	vm.Interfaces = nil
	vm.KernelPath = filepath.Join(dir, "vmlinuz")
	vm.InitramfsPath = filepath.Join(dir, "initramfs")
	for _, path := range []string{vm.KernelPath, vm.InitramfsPath} {
		if err := os.WriteFile(path, []byte("image"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rootfsSocket := filepath.Join(dir, "rootfs.sock")
	if err := os.WriteFile(rootfsSocket, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, store.LightVM{ID: vm.ID}, LightVMVirtioFS{}, nil, StartPlan{}); err == nil {
		t.Fatal("missing kernel and initramfs paths were accepted")
	}
	kvmDevice = filepath.Join(dir, "missing-kvm")
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, vm, LightVMVirtioFS{}, nil, StartPlan{}); err == nil {
		t.Fatal("missing KVM was accepted")
	}
	kvmDevice = fakeKVM

	missingKernel := vm
	missingKernel.KernelPath = filepath.Join(dir, "missing-kernel")
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, missingKernel, LightVMVirtioFS{}, nil, StartPlan{}); err == nil {
		t.Fatal("missing kernel was accepted")
	}
	missingInitramfs := vm
	missingInitramfs.InitramfsPath = filepath.Join(dir, "missing-initramfs")
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, missingInitramfs, LightVMVirtioFS{}, nil, StartPlan{}); err == nil {
		t.Fatal("missing initramfs was accepted")
	}
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, vm, LightVMVirtioFS{SocketPath: filepath.Join(dir, "missing.sock")}, nil, StartPlan{}); err == nil {
		t.Fatal("missing rootfs socket was accepted")
	}
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, vm, LightVMVirtioFS{SocketPath: rootfsSocket}, []LightVMVirtioFS{{SocketPath: filepath.Join(dir, "missing-share.sock"), Tag: "data"}}, StartPlan{}); err == nil {
		t.Fatal("missing share socket was accepted")
	}
	missingIface := vm
	missingIface.Interfaces = []store.VMInterface{{SocketPath: filepath.Join(dir, "missing-vhost.sock")}}
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, missingIface, LightVMVirtioFS{SocketPath: rootfsSocket}, nil, StartPlan{}); err == nil {
		t.Fatal("missing vhost socket was accepted")
	}
	badDisk := vm
	badDisk.Disks = []store.VMDisk{{
		ID: 1, Name: "bad", Source: store.DiskSourceHostDevice, Device: filepath.Join(dir, "missing-disk"),
	}}
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, badDisk, LightVMVirtioFS{SocketPath: rootfsSocket}, nil, StartPlan{}); err == nil {
		t.Fatal("invalid host disk was accepted")
	}
	badID := vm
	badID.ID = "not-a-uuid"
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, badID, LightVMVirtioFS{}, nil, StartPlan{}); err == nil {
		t.Fatal("invalid LightVM ID was accepted")
	}

	koujaDisk := vm
	koujaDisk.Disks = []store.VMDisk{{ID: 1, Name: "remote", Source: store.DiskSourceKouja, Protocol: "iscsi"}}
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, koujaDisk, LightVMVirtioFS{SocketPath: rootfsSocket}, nil, StartPlan{}); err == nil {
		t.Fatal("remote disk without a prepared attachment was accepted")
	}
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, koujaDisk, LightVMVirtioFS{SocketPath: rootfsSocket}, nil, StartPlan{
		Attachments: map[int64]blockdev.Attachment{1: {Protocol: blockdev.ProtocolISCSI}},
	}); err == nil {
		t.Fatal("remote disk without an iSCSI credential was accepted")
	}

	diskPath := filepath.Join(dir, "data.img")
	if err := os.WriteFile(diskPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	vm.Disks = []store.VMDisk{{ID: 7, Name: "data", Source: store.DiskSourceLVMThin, Device: diskPath}}
	started, err := q.StartLightVMWithVirtioFSAndPlan(ctx, vm, LightVMVirtioFS{SocketPath: rootfsSocket, Tag: "rootfs"}, nil, StartPlan{})
	if err != nil || !started {
		t.Fatalf("start = %v, %v", started, err)
	}
	if !q.RunningLightVM(vm.ID) {
		t.Fatal("LightVM is not running after start")
	}
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, vm, LightVMVirtioFS{SocketPath: rootfsSocket}, nil, StartPlan{}); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second start error = %v", err)
	}
	if state, err := q.LightVMRunState(ctx, vm.ID); err == nil || state != "" {
		t.Fatalf("run state without a monitor = %q, %v", state, err)
	}
	if state, err := q.LightVMRunState(ctx, uuid.NewString()); err != nil || state != "" {
		t.Fatalf("stopped run state = %q, %v", state, err)
	}
	if state, err := q.LightVMRunState(ctx, vm.ID); err == nil || state != "" {
		t.Fatalf("run state without a monitor = %q, %v", state, err)
	}
	if state, err := q.LightVMRunState(ctx, uuid.NewString()); err != nil || state != "" {
		t.Fatalf("stopped run state = %q, %v", state, err)
	}
	if socket := q.LightVMConsoleSocket(vm.ID, 0); socket == "" {
		t.Fatal("default console socket is empty")
	}
	if socket := q.LightVMConsoleSocket(vm.ID, 2); socket == "" {
		t.Fatal("indexed console socket is empty")
	}
	if socket := q.LightVMConsoleSocket("bad", 2); socket != "" {
		t.Fatalf("invalid id console socket = %q", socket)
	}
	if err := q.StopLightVM(ctx, vm.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if q.RunningLightVM(vm.ID) {
		t.Fatal("LightVM is still running after stop")
	}
	if err := q.StopLightVM(ctx, vm.ID); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	if err := q.StopLightVM(ctx, "bad"); err == nil {
		t.Fatal("invalid LightVM stop id was accepted")
	}

	failing, err := New(filepath.Join(dir, "missing-qemu"), filepath.Join(dir, "run-fail"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failing.StartLightVMWithVirtioFSAndPlan(ctx, vm, LightVMVirtioFS{SocketPath: rootfsSocket}, nil, StartPlan{}); err == nil {
		t.Fatal("missing QEMU binary was accepted")
	}
	t.Setenv("LIGHTVM_FAKE_QEMU_MODE", "fail")
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, vm, LightVMVirtioFS{SocketPath: rootfsSocket}, nil, StartPlan{}); err == nil {
		t.Fatal("failing QEMU was accepted")
	}
	t.Setenv("LIGHTVM_FAKE_QEMU_MODE", "exit")
	if _, err := q.StartLightVMWithVirtioFSAndPlan(ctx, vm, LightVMVirtioFS{SocketPath: rootfsSocket}, nil, StartPlan{}); err == nil {
		t.Fatal("QEMU that exited without a pid file was accepted")
	}
	if err := q.StopLightVM(ctx, uuid.NewString()); err != nil {
		t.Fatalf("stop unknown: %v", err)
	}
}

func TestLightVMStartErrorFormatting(t *testing.T) {
	if got := lightVMStartError(nil, errors.New("boom")).Error(); got != "start lightvm qemu: boom" {
		t.Fatalf("empty output error = %q", got)
	}
	if got := lightVMStartError([]byte("boot failed\n"), errors.New("boom")).Error(); got != "start lightvm qemu: boom: boot failed" {
		t.Fatalf("output error = %q", got)
	}
}
