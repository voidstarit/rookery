package gateway

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

func TestLightVMConsoleAccessValidation(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "console-access-internal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{Name: "vmlinuz", Path: "/tmp/vmlinuz", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{Name: "initramfs", Path: "/tmp/initramfs", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	vm, err := st.CreateLightVM(ctx, store.LightVM{
		Name: "console-access", VCPUs: 1, MemoryMB: 128,
		KernelID: kernel.ID, InitramfsID: initramfs.ID,
		Image: "alpine", ExecutablePath: "/bin/sh", ConsoleEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := &lightVMService{store: st}

	for _, ttl := range []uint32{0, 86401} {
		_, err := service.CreateLightVMConsoleAccess(ctx, &rookeryv1.CreateLightVMConsoleAccessRequest{
			Id: vm.ID, TtlSeconds: ttl,
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("ttl %d error = %v, want InvalidArgument", ttl, err)
		}
	}

	if _, err := lightVMConsoleForAccess(store.LightVM{}, 0); err == nil {
		t.Fatal("a LightVM without consoles should be rejected")
	}
	if _, err := lightVMConsoleForAccess(store.LightVM{Consoles: []store.LightVMConsole{{ID: 1}}}, 2); err == nil {
		t.Fatal("an unknown console should be rejected")
	}
}

func TestLightVMConsoleWebSocketURL(t *testing.T) {
	const path = "/ws/lightvm-console/lvc_test"
	cases := []struct {
		name   string
		host   string
		scheme string
		want   string
	}{
		{name: "https", host: "rookery.example:8443", scheme: "https", want: "wss://rookery.example:8443" + path},
		{name: "http", host: "127.0.0.1:8080", scheme: "http", want: "ws://127.0.0.1:8080" + path},
		{name: "invalid scheme", host: "rookery.example", scheme: "ftp", want: ""},
		{name: "missing host", host: "", scheme: "https", want: ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				"x-forwarded-host", test.host,
				forwardedProtoMetadataKey, test.scheme,
			))
			if got := lightVMConsoleWebSocketURL(ctx, path); got != test.want {
				t.Fatalf("url = %q, want %q", got, test.want)
			}
		})
	}
	if got := lightVMConsoleWebSocketURL(context.Background(), path); got != "" {
		t.Fatalf("url without metadata = %q", got)
	}
}

func TestConsoleAccessRequestMetadata(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	metadataFor := func() string {
		md := consoleAccessRequestMetadata(context.Background(), request)
		values := md.Get(forwardedProtoMetadataKey)
		if len(values) != 1 {
			t.Fatalf("metadata values = %v", values)
		}
		return values[0]
	}
	if got := metadataFor(); got != "http" {
		t.Fatalf("plain scheme = %q", got)
	}
	request.TLS = &tls.ConnectionState{}
	if got := metadataFor(); got != "https" {
		t.Fatalf("TLS scheme = %q", got)
	}
	request.Header.Set("X-Forwarded-Proto", "http")
	if got := metadataFor(); got != "http" {
		t.Fatalf("forwarded scheme = %q", got)
	}
	request.Header.Set("X-Forwarded-Proto", "ftp")
	if got := metadataFor(); got != "https" {
		t.Fatalf("invalid forwarded scheme = %q", got)
	}
}
