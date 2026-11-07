package qemu

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/store"
)

func testISCSIPlan() StartPlan {
	return StartPlan{Attachments: map[int64]blockdev.Attachment{
		7: {
			Protocol: blockdev.ProtocolISCSI,
			ISCSI: &blockdev.ISCSIAttachment{
				ISCSIReference: blockdev.ISCSIReference{
					Portal: "10.0.0.1:3260", Target: "iqn.2026-11.io.example:disk", LUN: 0,
					Initiator: "iqn.2026-11.io.example:rookery",
				},
				Username: "kouja-resource", Credential: "secret",
			},
		},
	}}
}

func testRemoteDisk() store.VMDisk {
	return store.VMDisk{
		ID: 7, Name: "data", Source: store.DiskSourceKouja, Protocol: string(blockdev.ProtocolISCSI),
	}
}

func TestBuildDiskArgsISCSI(t *testing.T) {
	args, err := buildDiskArgs(testRemoteDisk(), testISCSIPlan(), map[int64]string{7: "/run/rookery/secret"})
	if err != nil {
		t.Fatalf("buildDiskArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-object secret,id=rookery-iscsi-7,file=/run/rookery/secret,format=raw",
		"file.driver=iscsi",
		"file.portal=10.0.0.1:3260",
		"file.target=iqn.2026-11.io.example:disk",
		"file.lun=0",
		"file.user=kouja-resource",
		"file.password-secret=rookery-iscsi-7",
		"file.initiator-name=iqn.2026-11.io.example:rookery",
		"werror=stop,rerror=stop",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q do not contain %q", joined, want)
		}
	}
	if strings.Contains(joined, "aio=native") {
		t.Fatalf("iSCSI args contain aio=native: %q", joined)
	}
}

func TestBuildDiskArgsLocalAndErrors(t *testing.T) {
	local, err := buildDiskArgs(store.VMDisk{Device: "/dev/vg0/data"}, StartPlan{}, nil)
	if err != nil || !strings.Contains(strings.Join(local, " "), "file=/dev/vg0/data") {
		t.Fatalf("local args = %v, %v", local, err)
	}
	if _, err := buildDiskArgs(testRemoteDisk(), StartPlan{}, nil); err == nil {
		t.Fatal("buildDiskArgs accepted a missing attachment")
	}
	unknown := StartPlan{Attachments: map[int64]blockdev.Attachment{
		7: {Protocol: "fc"},
	}}
	if _, err := buildDiskArgs(testRemoteDisk(), unknown, nil); err == nil {
		t.Fatal("buildDiskArgs accepted an unknown protocol")
	}
	if _, err := buildDiskArgs(testRemoteDisk(), testISCSIPlan(), nil); err == nil {
		t.Fatal("buildDiskArgs accepted a missing iSCSI secret")
	}
}

func TestBuildArgsWithPlan(t *testing.T) {
	vm := testVM()
	disk := testRemoteDisk()
	vm.Disk = &disk
	args, err := buildArgsWithPlan(vm, "/tmp/vm.vnc", "/tmp/vm.pid", "/tmp/vm.monitor",
		testISCSIPlan(), map[int64]string{7: "/run/rookery/secret"})
	if err != nil {
		t.Fatalf("buildArgsWithPlan: %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), "file.driver=iscsi") {
		t.Fatalf("args = %v", args)
	}
}

func TestPrepareAndCleanupSecrets(t *testing.T) {
	runDir := t.TempDir()
	q, err := New(DefaultBinary, runDir)
	if err != nil {
		t.Fatal(err)
	}
	vm := store.VM{ID: uuid.NewString(), Disks: []store.VMDisk{testRemoteDisk()}}
	paths, err := q.prepareSecrets(vm, testISCSIPlan())
	if err != nil {
		t.Fatalf("prepareSecrets: %v", err)
	}
	path := paths[7]
	if path == "" {
		t.Fatal("no secret path")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "secret" {
		t.Fatalf("secret = %q, %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode = %o, want 600", info.Mode().Perm())
	}
	q.removeSecretFiles(paths)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("secret still exists: %v", err)
	}

	paths, err = q.prepareSecrets(vm, testISCSIPlan())
	if err != nil {
		t.Fatal(err)
	}
	q.cleanupSecrets(vm.ID)
	if _, err := os.Stat(paths[7]); !os.IsNotExist(err) {
		t.Fatalf("cleanupSecrets left a file: %v", err)
	}
}

func TestCleanupStaleSecrets(t *testing.T) {
	runDir := t.TempDir()
	q, err := New(DefaultBinary, runDir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runDir, "old.secret")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	q.cleanupStaleSecrets()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale secret still exists: %v", err)
	}
	info, err := os.Stat(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("run dir mode = %o, want 700", info.Mode().Perm())
	}
}

func TestValidateVMDisksRemoteSkipsDevice(t *testing.T) {
	if err := validateVMDisks(context.Background(), []store.VMDisk{
		{Source: store.DiskSourceKouja, Protocol: string(blockdev.ProtocolISCSI)},
	}); err != nil {
		t.Fatalf("validateVMDisks(remote) = %v", err)
	}
	if err := validateVMDisks(context.Background(), []store.VMDisk{
		{Device: filepath.Join(t.TempDir(), "missing")},
	}); err == nil {
		t.Fatal("validateVMDisks accepted a missing local device")
	}
}

func TestStartWithoutDiskAndMonitorStubs(t *testing.T) {
	ctx := context.Background()
	q, err := New(DefaultBinary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Start(ctx, store.VM{}); err == nil || !strings.Contains(err.Error(), "no disk") {
		t.Fatalf("Start(no disk) = %v", err)
	}
	vmID := uuid.NewString()
	if state, err := q.RunState(ctx, vmID); err != nil || state != "" {
		t.Fatalf("RunState(stopped) = %q, %v", state, err)
	}
	if paused, err := q.Paused(ctx, vmID); err != nil || paused {
		t.Fatalf("Paused(stopped) = %t, %v", paused, err)
	}
	if _, err := q.Resume(ctx, vmID); !errors.Is(err, ErrNotPaused) {
		t.Fatalf("Resume(stopped) = %v", err)
	}
	if _, err := q.monitorQuery(ctx, vmID, "info status"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("monitorQuery(stopped) = %v", err)
	}
}

func TestPrepareSecretBranches(t *testing.T) {
	q, err := New(DefaultBinary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vm := store.VM{ID: uuid.NewString(), Disks: []store.VMDisk{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}}
	plan := StartPlan{Attachments: map[int64]blockdev.Attachment{
		1: {Protocol: blockdev.ProtocolISCSI},
	}}
	if _, err := q.prepareSecrets(vm, plan); err == nil {
		t.Fatal("prepareSecrets accepted an iSCSI attachment without a credential")
	}
	plan = StartPlan{Attachments: map[int64]blockdev.Attachment{
		2: {Protocol: blockdev.ProtocolNVMeTCP},
	}}
	if _, err := q.prepareSecrets(vm, plan); err == nil {
		t.Fatal("prepareSecrets accepted NVMe/TCP without a PSK")
	}
	plan = StartPlan{Attachments: map[int64]blockdev.Attachment{
		3: {Protocol: blockdev.ProtocolRBD},
		4: {Protocol: "unknown"},
	}}
	if paths, err := q.prepareSecrets(vm, plan); err != nil || len(paths) != 0 {
		t.Fatalf("prepareSecrets(non-secret) = %+v, %v", paths, err)
	}
	// A non-empty directory cannot be removed, so writeSecret reports the
	// cleanup failure before trying to create the secret file.
	dir := filepath.Join(t.TempDir(), "secret-dir")
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := q.writeSecret(dir, "secret"); err == nil {
		t.Fatal("writeSecret accepted a directory path")
	}

	// Fresh and stale secret cleanup plus an unreadable run directory.
	fresh := filepath.Join(q.runDir, "fresh.secret")
	if err := os.WriteFile(fresh, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(q.runDir, "secret-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	q.cleanupStaleSecrets()
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh secret was removed: %v", err)
	}
	q.cleanupSecrets("[")
	if err := os.RemoveAll(q.runDir); err != nil {
		t.Fatal(err)
	}
	q.cleanupStaleSecrets()
}

func TestBuildDiskArgsReservedProtocols(t *testing.T) {
	nvme := blockdev.Attachment{Protocol: blockdev.ProtocolNVMeTCP, NVMeTCP: &blockdev.NVMeTCPAttachment{
		NVMeTCPReference: blockdev.NVMeTCPReference{Address: "10.0.0.1:4420", NQN: "nqn", NSID: 1},
	}}
	if _, err := buildDiskArgs(store.VMDisk{ID: 1, Source: store.DiskSourceKouja, Protocol: "nvme-tcp"},
		StartPlan{Attachments: map[int64]blockdev.Attachment{1: nvme}}, nil); err == nil {
		t.Fatal("buildDiskArgs accepted NVMe/TCP")
	}
	rbd := blockdev.Attachment{Protocol: blockdev.ProtocolRBD, RBD: &blockdev.RBDAttachment{
		RBDReference: blockdev.RBDReference{Pool: "pool", Image: "image"},
	}}
	if _, err := buildDiskArgs(store.VMDisk{ID: 2, Source: store.DiskSourceCeph, Protocol: "rbd"},
		StartPlan{Attachments: map[int64]blockdev.Attachment{2: rbd}}, nil); err == nil {
		t.Fatal("buildDiskArgs accepted RBD")
	}
}

func TestPrepareSecretsWriteAndBuildErrors(t *testing.T) {
	q, err := New(DefaultBinary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vm := store.VM{ID: uuid.NewString(), Disks: []store.VMDisk{{ID: 7}}}
	plan := testISCSIPlan()
	// A run directory that is a regular file makes the secret path unwritable.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	q.runDir = file
	if _, err := q.prepareSecrets(vm, plan); err == nil {
		t.Fatal("prepareSecrets accepted an unwritable run directory")
	}
	q.runDir = t.TempDir()
	if err := q.writeSecret(filepath.Join(q.runDir, "missing", "secret"), "x"); err == nil {
		t.Fatal("writeSecret accepted a missing parent directory")
	}
	remote := testRemoteDisk()
	vm = store.VM{ID: uuid.NewString(), Disks: []store.VMDisk{remote}}
	if _, err := buildArgsWithPlan(vm, "/tmp/vm.vnc", "/tmp/vm.pid", "/tmp/vm.monitor", StartPlan{}, nil); err == nil {
		t.Fatal("buildArgsWithPlan accepted a remote disk without an attachment")
	}
}

func startMonitorProcess(t *testing.T, q *QEMU, vmID string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", "while :; do sleep 1; done", vmID)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	_, pidfile, _, err := q.paths(vmID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidfile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	// Give /proc a moment to expose the child's command line.
	deadline := time.Now().Add(time.Second)
	for !q.Running(vmID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !q.Running(vmID) {
		t.Fatal("test qemu process was not detected")
	}
	return cmd
}

func startFakeMonitor(t *testing.T, q *QEMU, vmID string, responses []string) {
	t.Helper()
	_, _, socket, err := q.paths(vmID)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				_, _ = reader.ReadString('\n')
				if len(responses) == 0 {
					return
				}
				response := responses[0]
				responses = responses[1:]
				if response != "" {
					_, _ = conn.Write([]byte(response))
				}
			}(conn)
		}
	}()
}

func TestRunStateAndResumeWithMonitor(t *testing.T) {
	ctx := context.Background()
	q, err := New(DefaultBinary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vmID := uuid.NewString()
	startMonitorProcess(t, q, vmID)
	startFakeMonitor(t, q, vmID, []string{
		"VM status: running\n",
		"VM status: paused (io-error)\n",
		"VM status: paused\n",
		"VM status: unknown\n",
		"VM status: paused\n",
		"VM status: paused\n",
		"",
		"VM status: running\n",
	})
	for _, want := range []string{"running", "paused (io-error)", "paused", ""} {
		state, err := q.RunState(ctx, vmID)
		if err != nil || state != want {
			t.Fatalf("RunState() = %q, %v; want %q", state, err, want)
		}
	}
	paused, err := q.Paused(ctx, vmID)
	if err != nil || !paused {
		t.Fatalf("Paused() = %t, %v", paused, err)
	}
	state, err := q.Resume(ctx, vmID)
	if err != nil || state != "running" {
		t.Fatalf("Resume() = %q, %v", state, err)
	}
	if err := q.Reset(ctx, vmID); err != nil {
		t.Fatalf("Reset() = %v", err)
	}
	if err := q.SendCtrlAltDel(ctx, vmID); err != nil {
		t.Fatalf("SendCtrlAltDel() = %v", err)
	}
}

func TestRunStateMonitorErrors(t *testing.T) {
	ctx := context.Background()
	q, err := New(DefaultBinary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vmID := uuid.NewString()
	startMonitorProcess(t, q, vmID)
	if _, err := q.RunState(ctx, vmID); !errors.Is(err, ErrMonitorUnavailable) {
		t.Fatalf("RunState(missing monitor) = %v", err)
	}
	if _, err := q.Paused(ctx, vmID); !errors.Is(err, ErrMonitorUnavailable) {
		t.Fatalf("Paused(missing monitor) = %v", err)
	}
	if _, err := q.Resume(ctx, vmID); !errors.Is(err, ErrMonitorUnavailable) {
		t.Fatalf("Resume(missing monitor) = %v", err)
	}
	if err := q.Reset(ctx, vmID); !errors.Is(err, ErrMonitorUnavailable) {
		t.Fatalf("Reset(missing monitor) = %v", err)
	}
	if _, err := q.monitorQuery(ctx, vmID, "info status"); !errors.Is(err, ErrMonitorUnavailable) {
		t.Fatalf("monitorQuery(missing monitor) = %v", err)
	}
}

func TestBuildDiskArgsThrottling(t *testing.T) {
	local, err := buildDiskArgs(store.VMDisk{
		Device: "/dev/vg0/data", ReadBPS: 10 * 1024 * 1024, WriteBPS: 5 * 1024 * 1024,
	}, StartPlan{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	localArgs := strings.Join(local, " ")
	if !strings.Contains(localArgs, "throttling.bps-read=10485760") ||
		!strings.Contains(localArgs, "throttling.bps-write=5242880") {
		t.Fatalf("local args = %q", localArgs)
	}
	disk := testRemoteDisk()
	disk.ReadBPS = 10 * 1024 * 1024
	disk.WriteBPS = 5 * 1024 * 1024
	remote, err := buildDiskArgs(disk, testISCSIPlan(), map[int64]string{7: "/run/rookery/secret"})
	if err != nil {
		t.Fatal(err)
	}
	remoteArgs := strings.Join(remote, " ")
	if !strings.Contains(remoteArgs, "throttling.bps-read=10485760") ||
		!strings.Contains(remoteArgs, "throttling.bps-write=5242880") {
		t.Fatalf("remote args = %q", remoteArgs)
	}
}
