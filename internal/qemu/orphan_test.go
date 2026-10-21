package qemu

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestRunningAndStopFindOrphanProcess covers a QEMU process whose pid file was
// lost (for example when the run directory was cleaned while QEMU kept
// running). The process is still found by its "-uuid <vmID>" argument.
func TestRunningAndStopFindOrphanProcess(t *testing.T) {
	vmID := uuid.NewString()
	q, err := New("qemu-system-x86_64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestOrphanHelperProcess", "--", "-uuid", vmID)
	cmd.Env = append(os.Environ(), "ROOKERY_ORPHAN_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	deadline := time.Now().Add(2 * time.Second)
	for !q.Running(vmID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !q.Running(vmID) {
		t.Fatal("orphan QEMU process was not detected")
	}

	if err := q.Stop(context.Background(), vmID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if q.Running(vmID) {
		t.Fatal("orphan QEMU process is still running after stop")
	}
	_ = cmd.Wait()
}

func TestOrphanHelperProcess(t *testing.T) {
	if os.Getenv("ROOKERY_ORPHAN_HELPER") != "1" {
		t.Skip("helper process")
	}
	time.Sleep(30 * time.Second)
}
