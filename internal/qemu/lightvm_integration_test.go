package qemu

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/store"
)

func findLightVMBootFiles() (kernel, initramfs string) {
	entries, err := os.ReadDir("/boot")
	if err != nil {
		return "", ""
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "vmlinuz-") {
			continue
		}
		version := strings.TrimPrefix(name, "vmlinuz-")
		candidateKernel := filepath.Join("/boot", name)
		candidateInitramfs := filepath.Join("/boot", "initrd.img-"+version)
		if _, err := os.Stat(candidateInitramfs); err == nil {
			return candidateKernel, candidateInitramfs
		}
	}
	return "", ""
}

func TestStartStopLightVMWithRealQEMU(t *testing.T) {
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
	kernel, initramfs := findLightVMBootFiles()
	if kernel == "" || initramfs == "" {
		t.Skip("no kernel/initramfs pair under /boot")
	}

	dir := t.TempDir()
	q, err := New(binary, filepath.Join(dir, "run"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	vm := store.LightVM{
		ID:            uuid.NewString(),
		Name:          "lightvm-integration-test",
		VCPUs:         1,
		MemoryMB:      256,
		KernelPath:    kernel,
		InitramfsPath: initramfs,
		Append:        "console=hvc0 rdinit=/bin/sh",
	}

	if err := q.StartLightVM(context.Background(), vm); err != nil {
		t.Fatalf("StartLightVM: %v", err)
	}
	t.Cleanup(func() {
		if err := q.StopLightVM(context.Background(), vm.ID); err != nil {
			t.Errorf("StopLightVM: %v", err)
		}
	})
	if !q.RunningLightVM(vm.ID) {
		t.Fatal("LightVM QEMU is not running after StartLightVM")
	}

	console := q.LightVMConsoleSocket(vm.ID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(console); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("console socket %s was not created", console)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
