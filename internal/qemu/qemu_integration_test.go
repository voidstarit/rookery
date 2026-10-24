package qemu

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/store"
)

func TestStartStopWithRealQEMU(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping QEMU integration test in short mode")
	}
	if _, err := os.Stat(kvmDevice); err != nil {
		t.Skipf("KVM unavailable: %v", err)
	}
	binary, err := exec.LookPath(DefaultBinary)
	if err != nil {
		t.Skipf("QEMU unavailable: %v", err)
	}

	dir := t.TempDir()
	diskPath := filepath.Join(dir, "disk.raw")
	if err := os.WriteFile(diskPath, make([]byte, 64<<20), 0o600); err != nil {
		t.Fatalf("create raw disk: %v", err)
	}

	q, err := New(binary, filepath.Join(dir, "run"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	vm := store.VM{
		ID:       uuid.NewString(),
		Name:     "qemu-integration-test",
		VCPUs:    1,
		MemoryMB: 128,
		Disk:     &store.VMDisk{Device: diskPath},
	}

	if err := q.Start(context.Background(), vm); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := q.Stop(context.Background(), vm.ID); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})

	if !q.Running(vm.ID) {
		t.Fatal("QEMU is not running after Start")
	}

	socket := q.VNCSocket(vm.ID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("VNC socket %s was not created", socket)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := q.SendCtrlAltDel(context.Background(), vm.ID); err != nil {
		t.Fatalf("SendCtrlAltDel: %v", err)
	}
	if err := q.Reset(context.Background(), vm.ID); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if !q.Running(vm.ID) {
		t.Fatal("QEMU is not running after monitor commands")
	}
}
