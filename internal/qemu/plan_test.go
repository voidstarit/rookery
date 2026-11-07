package qemu

import (
	"context"
	"os"
	"path/filepath"
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
