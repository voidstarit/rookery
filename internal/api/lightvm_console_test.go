package api_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/net/websocket"

	"github.com/voidstarit/rookery/internal/api"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeLightVMController struct {
	running  bool
	sockets  map[int64]string
	selected atomic.Int64
}

func (f *fakeLightVMController) StartLightVM(context.Context, store.LightVM) error { return nil }
func (f *fakeLightVMController) StopLightVM(context.Context, string) error         { return nil }
func (f *fakeLightVMController) RunningLightVM(string) bool                        { return f.running }
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
		running: true,
		sockets: map[int64]string{
			vm.Consoles[0].ID: firstSocket,
			vm.Consoles[1].ID: secondSocket,
		},
	}
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
