package api_test

import (
	"context"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/net/websocket"

	"github.com/voidstarit/rookery/internal/api"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeLightVMController struct {
	running bool
	socket  string
}

func (f *fakeLightVMController) StartLightVM(context.Context, store.LightVM) error { return nil }
func (f *fakeLightVMController) StopLightVM(context.Context, string) error         { return nil }
func (f *fakeLightVMController) RunningLightVM(string) bool                        { return f.running }
func (f *fakeLightVMController) LightVMConsoleSocket(string) string                { return f.socket }

func TestLightVMConsoleProxy(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "console.sock")
	listener, err := net.Listen("unix", socketPath)
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
	})
	if err != nil {
		t.Fatal(err)
	}

	controller := &fakeLightVMController{running: true, socket: socketPath}
	server := api.New(st, nil, nil, nil, nil, nil, nil, zerolog.Nop())
	server.ConfigureLightVMRuntime(controller)
	httpServer := httptest.NewServer(server.NativeHandler())
	t.Cleanup(httpServer.Close)

	url := "ws" + httpServer.URL[len("http"):] + "/ws/lightvms/" + vm.ID + "/console"
	conn, err := websocket.Dial(url, "", httpServer.URL)
	if err != nil {
		t.Fatalf("dial console websocket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 6)
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("read console echo: %v", err)
	}
	if string(buffer) != "hello\n" {
		t.Fatalf("console echo = %q", buffer)
	}

	if _, err := st.UpdateLightVM(ctx, store.LightVM{
		ID: vm.ID, Name: vm.Name, VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh", ConsoleEnabled: false,
		InterfaceIDs: vm.InterfaceIDs,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := websocket.Dial(url, "", httpServer.URL); err == nil {
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
