package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/lightvm"
	"github.com/voidstarit/rookery/internal/store"
)

func TestCreateLightVMConsoleAccess(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "console-access.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{
		Name: "vmlinuz", Path: "/tmp/vmlinuz", SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{
		Name: "initramfs", Path: "/tmp/initramfs", SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string, enabled bool) store.LightVM {
		t.Helper()
		vm, err := st.CreateLightVM(ctx, store.LightVM{
			Name: name, VCPUs: 1, MemoryMB: 128,
			KernelID: kernel.ID, InitramfsID: initramfs.ID,
			Image: "alpine", ExecutablePath: "/bin/sh", ConsoleEnabled: enabled,
			Consoles: []store.LightVMConsole{{Name: "serial"}, {Name: "logs"}},
		})
		if err != nil {
			t.Fatalf("create lightvm %s: %v", name, err)
		}
		return vm
	}
	vm := create("console-access", true)
	disabled := create("console-disabled", false)

	gw, err := gateway.New(st, fakeReadiness{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	post := func(id string, body any) (*http.Response, map[string]any) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Post(server.URL+"/api/lightvm/instances/"+id+"/console-access", "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var decoded map[string]any
		if response.StatusCode == http.StatusOK {
			if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
				t.Fatalf("decode response: %v", err)
			}
		}
		return response, decoded
	}

	response, created := post(vm.ID, map[string]any{"ttl_seconds": 60})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d, want 200", response.StatusCode)
	}
	token, _ := created["token"].(string)
	if !lightvm.ValidConsoleAccessToken(token) {
		t.Fatalf("token = %q", token)
	}
	if created["lightvm_id"] != vm.ID {
		t.Fatalf("lightvm_id = %+v", created["lightvm_id"])
	}
	if got := jsonInteger(created["console_id"]); got != vm.Consoles[0].ID {
		t.Fatalf("console_id = %d, want %d", got, vm.Consoles[0].ID)
	}
	if created["console_name"] != vm.Consoles[0].Name {
		t.Fatalf("console_name = %+v", created["console_name"])
	}
	if created["websocket_path"] != lightvm.ConsoleAccessPath(token) {
		t.Fatalf("websocket_path = %+v", created["websocket_path"])
	}
	wantURL := "ws" + server.URL[len("http"):] + lightvm.ConsoleAccessPath(token)
	if created["websocket_url"] != wantURL {
		t.Fatalf("websocket_url = %+v, want %q", created["websocket_url"], wantURL)
	}
	expiresAt, ok := created["expires_at"].(string)
	if !ok {
		t.Fatalf("expires_at = %+v", created["expires_at"])
	}
	expiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		t.Fatalf("parse expires_at: %v", err)
	}
	if remaining := time.Until(expiry); remaining < 30*time.Second || remaining > 2*time.Minute {
		t.Fatalf("expires_at remaining = %v, want about 60s", remaining)
	}

	access, err := st.GetLightVMConsoleAccessByTokenHash(ctx, lightvm.HashConsoleAccessToken(token), time.Now())
	if err != nil {
		t.Fatalf("stored console access: %v", err)
	}
	if access.LightVMID != vm.ID || access.ConsoleID != vm.Consoles[0].ID {
		t.Fatalf("stored console access = %+v", access)
	}

	response, selected := post(vm.ID, map[string]any{"ttl_seconds": 60, "console_id": vm.Consoles[1].ID})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create selected console status = %d", response.StatusCode)
	}
	if got := jsonInteger(selected["console_id"]); got != vm.Consoles[1].ID {
		t.Fatalf("selected console_id = %d, want %d", got, vm.Consoles[1].ID)
	}

	if response, _ := post(vm.ID, map[string]any{"ttl_seconds": 86401}); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized ttl status = %d, want 400", response.StatusCode)
	}
	if response, _ := post(vm.ID, map[string]any{"ttl_seconds": 0}); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("zero ttl status = %d, want 400", response.StatusCode)
	}
	if response, _ := post(vm.ID, map[string]any{"ttl_seconds": 60, "console_id": 999999}); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown console status = %d, want 400", response.StatusCode)
	}
	if response, _ := post(disabled.ID, map[string]any{"ttl_seconds": 60}); response.StatusCode != http.StatusForbidden {
		t.Fatalf("disabled console status = %d, want 403", response.StatusCode)
	}
	if response, _ := post("11111111-1111-4111-8111-111111111111", map[string]any{"ttl_seconds": 60}); response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown lightvm status = %d, want 404", response.StatusCode)
	}

	if access.ExpiresAt.Sub(access.CreatedAt) <= 0 {
		t.Fatalf("access lifetime = %v", access.ExpiresAt.Sub(access.CreatedAt))
	}
	if !strings.HasPrefix(lightvm.ConsoleAccessPath(token), "/ws/lightvm-console/") {
		t.Fatalf("path = %q", lightvm.ConsoleAccessPath(token))
	}
}
