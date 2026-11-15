package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/store"
)

func TestLightVMConsoleAccess(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	kernel := createKernel(t, st, "vmlinuz-console")
	initramfs := createInitramfs(t, st, "initramfs-console")

	vm, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "console-access", VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh", ConsoleEnabled: true,
		Consoles: []store.LightVMConsole{{Name: "serial"}, {Name: "logs"}},
	})
	if err != nil {
		t.Fatalf("create lightvm: %v", err)
	}
	if len(vm.Consoles) != 2 {
		t.Fatalf("consoles = %+v", vm.Consoles)
	}

	tokenHash := strings.Repeat("a", 64)
	expiresAt := time.Now().Add(time.Hour)
	created, err := st.CreateLightVMConsoleAccess(ctx, vm.ID, vm.Consoles[1].ID, tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("create console access: %v", err)
	}
	if created.ID == 0 || created.LightVMID != vm.ID || created.ConsoleID != vm.Consoles[1].ID {
		t.Fatalf("created console access = %+v", created)
	}
	if created.CreatedAt.IsZero() || created.ExpiresAt.IsZero() {
		t.Fatalf("created timestamps = %+v", created)
	}

	got, err := st.GetLightVMConsoleAccessByTokenHash(ctx, tokenHash, time.Now())
	if err != nil {
		t.Fatalf("get console access: %v", err)
	}
	if got.ID != created.ID || got.LightVMID != vm.ID || got.ConsoleID != vm.Consoles[1].ID {
		t.Fatalf("got console access = %+v, want %+v", got, created)
	}

	if _, err := st.GetLightVMConsoleAccessByTokenHash(ctx, tokenHash, expiresAt.Add(time.Second)); !errors.Is(err, store.ErrLightVMConsoleAccessNotFound) {
		t.Fatalf("expired lookup error = %v, want not found", err)
	}
	if _, err := st.GetLightVMConsoleAccessByTokenHash(ctx, "unknown", time.Now()); !errors.Is(err, store.ErrLightVMConsoleAccessNotFound) {
		t.Fatalf("unknown lookup error = %v, want not found", err)
	}
	if _, err := st.CreateLightVMConsoleAccess(ctx, vm.ID, vm.Consoles[1].ID, "", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("empty token hash should be rejected")
	}
	if _, err := st.CreateLightVMConsoleAccess(ctx, vm.ID, vm.Consoles[1].ID, "hash", time.Now().Add(-time.Second)); err == nil {
		t.Fatal("past expiration should be rejected")
	}
	if _, err := st.CreateLightVMConsoleAccess(ctx, vm.ID, 999999, "other-hash", time.Now().Add(time.Hour)); !errors.Is(err, store.ErrLightVMConsoleAccessNotFound) {
		t.Fatalf("unknown console error = %v, want not found", err)
	}

	shortHash := strings.Repeat("b", 64)
	if _, err := st.CreateLightVMConsoleAccess(ctx, vm.ID, vm.Consoles[0].ID, shortHash, time.Now().Add(20*time.Millisecond)); err != nil {
		t.Fatalf("create short-lived console access: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := st.GetLightVMConsoleAccessByTokenHash(ctx, shortHash, time.Now()); !errors.Is(err, store.ErrLightVMConsoleAccessNotFound) {
		t.Fatalf("short-lived lookup error = %v, want not found", err)
	}
	// Issuing another token prunes expired rows; the call must still succeed.
	if _, err := st.CreateLightVMConsoleAccess(ctx, vm.ID, vm.Consoles[0].ID, strings.Repeat("c", 64), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create after expiry cleanup: %v", err)
	}
}
