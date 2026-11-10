package qemu

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func startArgProcess(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	all := append([]string{"-c", "while :; do sleep 1; done", "sh"}, args...)
	cmd := exec.Command("sh", all...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func TestLightVMProcessMatchingIsolatesSharedUUID(t *testing.T) {
	id := uuid.NewString()
	q, err := New("qemu-system-x86_64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, vmPidfile, _, err := q.paths(id)
	if err != nil {
		t.Fatal(err)
	}
	_, lightPidfile, _, err := q.lightVMPaths(id)
	if err != nil {
		t.Fatal(err)
	}
	startArgProcess(t, "-uuid", id, "-pidfile", vmPidfile)
	light := startArgProcess(t, "-uuid", id, "-pidfile", lightPidfile,
		"-chardev", "socket,id=fs0,path=/run/rookery/lightvm-"+id+"-0001.virtiofs.sock")

	deadline := time.Now().Add(2 * time.Second)
	for (!q.RunningLightVM(id) || !q.Running(id)) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !q.RunningLightVM(id) {
		t.Fatal("RunningLightVM did not find the LightVM process")
	}
	if !q.Running(id) {
		t.Fatal("Running did not find the VM process")
	}

	_ = light.Process.Kill()
	_, _ = light.Process.Wait()
	deadline = time.Now().Add(2 * time.Second)
	for q.RunningLightVM(id) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if q.RunningLightVM(id) {
		t.Fatal("RunningLightVM still matched the VM process with the same UUID")
	}
	if !q.Running(id) {
		t.Fatal("VM process was lost while stopping the LightVM process")
	}
}

func TestSocketOnlyLightVMStop(t *testing.T) {
	id := uuid.NewString()
	q, err := New("qemu-system-x86_64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	socket := "/run/rookery/lightvm-" + id + "-abcd.virtiofs.sock"
	cmd := startArgProcess(t, "-uuid", id, "-chardev", "socket,id=fs0,path="+socket)
	deadline := time.Now().Add(2 * time.Second)
	for !q.RunningLightVM(id) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !q.RunningLightVM(id) {
		t.Fatal("socket-only LightVM process was not detected")
	}
	if err := q.StopLightVM(context.Background(), id); err != nil {
		t.Fatalf("stop socket-only LightVM: %v", err)
	}
	if q.RunningLightVM(id) {
		t.Fatal("socket-only LightVM process still running")
	}
	_ = cmd.Wait()
}

func TestVMOnlyProcessDoesNotMatchLightVM(t *testing.T) {
	id := uuid.NewString()
	q, err := New("qemu-system-x86_64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, pidfile, _, err := q.paths(id)
	if err != nil {
		t.Fatal(err)
	}
	startArgProcess(t, "-uuid", id, "-pidfile", pidfile)
	deadline := time.Now().Add(2 * time.Second)
	for !q.Running(id) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !q.Running(id) {
		t.Fatal("VM process was not detected")
	}
	if q.RunningLightVM(id) {
		t.Fatal("RunningLightVM matched a VM-only process")
	}
	if err := q.StopLightVM(context.Background(), id); err != nil {
		t.Fatalf("StopLightVM on VM-only process: %v", err)
	}
	if !q.Running(id) {
		t.Fatal("StopLightVM stopped a VM-only process")
	}
	if !strings.Contains(id, "-") {
		t.Fatalf("unexpected UUID %q", id)
	}
}
