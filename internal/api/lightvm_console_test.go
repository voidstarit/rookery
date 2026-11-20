package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/net/websocket"

	"github.com/voidstarit/rookery/internal/api"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/lightvm"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeLightVMController struct {
	running  atomic.Bool
	sockets  map[int64]string
	selected atomic.Int64
}

func (f *fakeLightVMController) PrepareLightVM(context.Context, store.LightVM) (lightvm.PreparedLightVM, error) {
	return lightvm.PreparedLightVM{}, nil
}
func (f *fakeLightVMController) StartPreparedLightVM(context.Context, *lightvm.PreparedLightVM, store.LightVM, qemu.StartPlan) error {
	return nil
}
func (f *fakeLightVMController) CleanupPreparedLightVM(context.Context, lightvm.PreparedLightVM) error {
	return nil
}
func (f *fakeLightVMController) StopQEMULightVM(context.Context, string) error { return nil }
func (f *fakeLightVMController) CleanupStoppedLightVM(context.Context, string) error {
	return nil
}
func (f *fakeLightVMController) RunState(context.Context, string) (string, error) {
	return "running", nil
}
func (f *fakeLightVMController) RunningLightVM(string) bool { return f.running.Load() }
func (f *fakeLightVMController) LightVMConsoleSocket(_ string, consoleID int64) string {
	f.selected.Store(consoleID)
	return f.sockets[consoleID]
}

func startEchoSocket(t *testing.T, path string) {
	t.Helper()
	listener, err := net.Listen("unix", path)
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
				_, _ = io.Copy(conn, conn)
			}(conn)
		}
	}()
}

func TestLightVMConsoleProxy(t *testing.T) {
	dir := t.TempDir()
	firstSocket := filepath.Join(dir, "console-one.sock")
	secondSocket := filepath.Join(dir, "console-two.sock")
	startEchoSocket(t, firstSocket)
	startEchoSocket(t, secondSocket)

	st, err := store.Open(filepath.Join(dir, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
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
	vm, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "console-test", VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh", ConsoleEnabled: true,
		Consoles: []store.LightVMConsole{{Name: "serial"}, {Name: "logs"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(vm.Consoles) != 2 {
		t.Fatalf("consoles = %+v", vm.Consoles)
	}

	controller := &fakeLightVMController{
		sockets: map[int64]string{
			vm.Consoles[0].ID: firstSocket,
			vm.Consoles[1].ID: secondSocket,
		},
	}
	controller.running.Store(true)
	server := api.New(st, nil, nil, nil, nil, nil, nil, zerolog.Nop())
	server.ConfigureLightVMRuntime(controller)
	httpServer := httptest.NewServer(server.NativeHandler())
	t.Cleanup(httpServer.Close)

	dial := func(url string) *websocket.Conn {
		t.Helper()
		conn, err := websocket.Dial(url, "", httpServer.URL)
		if err != nil {
			t.Fatalf("dial console websocket: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		return conn
	}

	baseURL := "ws" + httpServer.URL[len("http"):] + "/ws/lightvms/" + vm.ID + "/console"
	conn := dial(baseURL)
	if got := controller.selected.Load(); got != vm.Consoles[0].ID {
		t.Fatalf("default console id = %d, want %d", got, vm.Consoles[0].ID)
	}
	if _, err := conn.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 6)
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("read console echo: %v", err)
	}
	if string(buffer) != "first\n" {
		t.Fatalf("console echo = %q", buffer)
	}
	_ = conn.Close()

	secondURL := fmt.Sprintf("%s?console=%d", baseURL, vm.Consoles[1].ID)
	conn = dial(secondURL)
	if got := controller.selected.Load(); got != vm.Consoles[1].ID {
		t.Fatalf("selected console id = %d, want %d", got, vm.Consoles[1].ID)
	}
	if _, err := conn.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	buffer = make([]byte, 6)
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("read second console echo: %v", err)
	}
	if string(buffer) != "second" {
		t.Fatalf("second console echo = %q", buffer)
	}

	if _, err := websocket.Dial(baseURL, "", "http://evil.example"); err == nil {
		t.Fatal("console websocket should reject a foreign origin")
	}

	conn = dial(baseURL)
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	controller.running.Store(false)
	if _, err := conn.Read(buffer); err == nil {
		t.Fatal("console websocket should close when the LightVM stops")
	}
	controller.running.Store(true)
	_ = conn.Close()

	if _, err := st.UpdateLightVM(ctx, store.LightVM{
		ID: vm.ID, Name: vm.Name, VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh", ConsoleEnabled: false,
		InterfaceIDs: vm.InterfaceIDs,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := websocket.Dial(secondURL, "", httpServer.URL); err == nil {
		t.Fatal("console websocket should be rejected when console access is disabled")
	}
}

func TestLightVMConsoleRejectsMissingRuntime(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "console.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{Name: "vmlinuz", Path: "/tmp/vmlinuz", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{Name: "initramfs", Path: "/tmp/initramfs", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "console-test", VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh", ConsoleEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := api.New(st, nil, nil, nil, nil, nil, nil, zerolog.Nop())
	httpServer := httptest.NewServer(server.NativeHandler())
	t.Cleanup(httpServer.Close)
	url := "ws" + httpServer.URL[len("http"):] + "/ws/lightvms/" + vm.ID + "/console"
	if _, err := websocket.Dial(url, "", httpServer.URL); err == nil {
		t.Fatal("console websocket should be rejected without a runtime")
	}
}

func TestLightVMConsoleAccessProxy(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "console-access.sock")
	startEchoSocket(t, socket)

	st, err := store.Open(filepath.Join(dir, "console-access.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
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
		})
		if err != nil {
			t.Fatalf("create lightvm %s: %v", name, err)
		}
		return vm
	}
	vm := create("console-access", true)
	disabled := create("console-disabled", false)

	controller := &fakeLightVMController{
		sockets: map[int64]string{vm.Consoles[0].ID: socket},
	}
	controller.running.Store(true)
	server := api.New(st, nil, nil, nil, nil, nil, nil, zerolog.Nop())
	server.ConfigureLightVMRuntime(controller)
	httpServer := httptest.NewServer(server.NativeHandler())
	t.Cleanup(httpServer.Close)

	issue := func(lightVM store.LightVM, ttl time.Duration) string {
		t.Helper()
		token, err := lightvm.NewConsoleAccessToken()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateLightVMConsoleAccess(ctx, lightVM.ID, lightVM.Consoles[0].ID, lightvm.HashConsoleAccessToken(token), time.Now().Add(ttl)); err != nil {
			t.Fatalf("issue console access: %v", err)
		}
		return token
	}
	dial := func(token string) (*websocket.Conn, error) {
		t.Helper()
		return websocket.Dial("ws"+httpServer.URL[len("http"):]+lightvm.ConsoleAccessPath(token), "", httpServer.URL)
	}

	token := issue(vm, time.Minute)
	conn, err := dial(token)
	if err != nil {
		t.Fatalf("dial console access websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ticket\n")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 7)
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("read console echo: %v", err)
	}
	if string(buffer) != "ticket\n" {
		t.Fatalf("console echo = %q", buffer)
	}

	unknown, err := lightvm.NewConsoleAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dial(unknown); err == nil {
		t.Fatal("unknown console access token should be rejected")
	}
	if _, err := dial("not-a-token"); err == nil {
		t.Fatal("malformed console access token should be rejected")
	}
	if _, err := dial(issue(disabled, time.Minute)); err == nil {
		t.Fatal("console access should be rejected when console access is disabled")
	}

	expired := issue(vm, 20*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if _, err := dial(expired); err == nil {
		t.Fatal("expired console access token should be rejected")
	}

	// An open session is closed when the LightVM stops.
	conn, err = dial(issue(vm, time.Minute))
	if err != nil {
		t.Fatalf("dial second console access websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	controller.running.Store(false)
	if _, err := conn.Read(buffer); err == nil {
		t.Fatal("console websocket should close when the LightVM stops")
	}
	controller.running.Store(true)
}

func TestLightVMConsoleAccessProxyErrors(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "console-errors.sock")
	startEchoSocket(t, socket)

	st, err := store.Open(filepath.Join(dir, "console-errors.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{Name: "vmlinuz", Path: "/tmp/vmlinuz", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{Name: "initramfs", Path: "/tmp/initramfs", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string, enabled bool) store.LightVM {
		t.Helper()
		vm, err := st.CreateLightVM(ctx, store.LightVM{
			Name: name, VCPUs: 1, MemoryMB: 128,
			KernelID: kernel.ID, InitramfsID: initramfs.ID,
			Image: "alpine", ExecutablePath: "/bin/sh", ConsoleEnabled: enabled,
		})
		if err != nil {
			t.Fatalf("create lightvm %s: %v", name, err)
		}
		return vm
	}
	vm := create("console-errors", true)
	disabled := create("console-errors-disabled", false)

	controller := &fakeLightVMController{sockets: map[int64]string{vm.Consoles[0].ID: socket}}
	controller.running.Store(true)
	server := api.New(st, nil, nil, nil, nil, nil, nil, zerolog.Nop())
	server.ConfigureLightVMRuntime(controller)
	httpServer := httptest.NewServer(server.NativeHandler())
	t.Cleanup(httpServer.Close)

	issue := func(lightVM store.LightVM) string {
		t.Helper()
		token, err := lightvm.NewConsoleAccessToken()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateLightVMConsoleAccess(ctx, lightVM.ID, lightVM.Consoles[0].ID, lightvm.HashConsoleAccessToken(token), time.Now().Add(time.Minute)); err != nil {
			t.Fatalf("issue console access: %v", err)
		}
		return token
	}
	get := func(token string) int {
		t.Helper()
		response, err := http.Get(httpServer.URL + lightvm.ConsoleAccessPath(token))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}

	token := issue(vm)
	controller.running.Store(false)
	if got := get(token); got != http.StatusConflict {
		t.Fatalf("stopped LightVM status = %d, want 409", got)
	}
	controller.running.Store(true)

	controller.sockets[vm.Consoles[0].ID] = ""
	if got := get(token); got != http.StatusInternalServerError {
		t.Fatalf("missing console socket status = %d, want 500", got)
	}
	controller.sockets[vm.Consoles[0].ID] = filepath.Join(dir, "missing.sock")
	if got := get(token); got != http.StatusBadGateway {
		t.Fatalf("unreachable console socket status = %d, want 502", got)
	}
	controller.sockets[vm.Consoles[0].ID] = socket

	if got := get(issue(disabled)); got != http.StatusForbidden {
		t.Fatalf("disabled console status = %d, want 403", got)
	}
	unknown, err := lightvm.NewConsoleAccessToken()
	if err != nil {
		t.Fatal(err)
	}
	if got := get(unknown); got != http.StatusNotFound {
		t.Fatalf("unknown token status = %d, want 404", got)
	}
	if got := get("malformed"); got != http.StatusNotFound {
		t.Fatalf("malformed token status = %d, want 404", got)
	}

	noRuntime := api.New(st, nil, nil, nil, nil, nil, nil, zerolog.Nop())
	noRuntimeServer := httptest.NewServer(noRuntime.NativeHandler())
	t.Cleanup(noRuntimeServer.Close)
	response, err := http.Get(noRuntimeServer.URL + lightvm.ConsoleAccessPath(token))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing runtime status = %d, want 503", response.StatusCode)
	}
}

func TestLightVMConsoleAccessEndToEnd(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "console-e2e.sock")
	startEchoSocket(t, socket)

	st, err := store.Open(filepath.Join(dir, "console-e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{Name: "vmlinuz", Path: "/tmp/vmlinuz", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{Name: "initramfs", Path: "/tmp/initramfs", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "console-e2e", VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh", ConsoleEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	controller := &fakeLightVMController{sockets: map[int64]string{vm.Consoles[0].ID: socket}}
	controller.running.Store(true)
	apiServer := api.New(st, nil, nil, nil, nil, nil, nil, zerolog.Nop())
	apiServer.ConfigureLightVMRuntime(controller)
	gw, err := gateway.New(st, readyReadiness())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	httpServer := httptest.NewServer(api.CutoverHandler(gw.Handler, apiServer.NativeHandler()))
	t.Cleanup(httpServer.Close)

	response, err := http.Post(httpServer.URL+"/api/lightvm/instances/"+vm.ID+"/console-access", "application/json", strings.NewReader(`{"ttl_seconds":60}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create console access status = %d", response.StatusCode)
	}
	var grant struct {
		WebsocketPath string `json:"websocket_path"`
		WebsocketURL  string `json:"websocket_url"`
		Token         string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&grant); err != nil {
		t.Fatal(err)
	}
	if grant.WebsocketPath == "" || grant.WebsocketURL == "" || grant.Token == "" {
		t.Fatalf("grant = %+v", grant)
	}
	if want := "ws" + strings.TrimPrefix(httpServer.URL, "http") + grant.WebsocketPath; grant.WebsocketURL != want {
		t.Fatalf("websocket_url = %q, want %q", grant.WebsocketURL, want)
	}

	conn, err := websocket.Dial(grant.WebsocketURL, "", httpServer.URL)
	if err != nil {
		t.Fatalf("dial end-to-end console websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("e2e\n")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatal(err)
	}
	if string(buffer) != "e2e\n" {
		t.Fatalf("echo = %q", buffer)
	}
}
