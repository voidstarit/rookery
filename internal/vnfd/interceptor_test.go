package vnfd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

func TestTraceInterceptorAndServerLogRPCs(t *testing.T) {
	var output bytes.Buffer
	logger := zerolog.New(&output).Level(zerolog.TraceLevel)

	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), logger)
	listener, err := Listen("tcp://127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(TraceInterceptor(logger)))
	rookeryv1.RegisterVNFDaemonServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	client, err := NewClient("tcp://"+listener.Addr().String(), logger)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	vnf := store.VNF{
		ID: 1, Name: "dhcp", NetworkID: 7, Type: store.VNFTypeDHCPv4,
		Config: store.VNFConfig{Gateway: "10.0.0.1/24", RangeStart: "10.0.0.10", RangeEnd: "10.0.0.20"},
	}
	if err := client.Start(context.Background(), vnf, "vnf1"); err != nil {
		t.Fatal(err)
	}

	logs := output.String()
	for _, want := range []string{
		"VNF daemon client created",
		"ensuring VNF runtime",
		"ensure VNF requested",
		"starting VNF runtime",
		"VNF runtime ensured",
		"VNF daemon RPC completed",
		"/rookery.v1.VNFDaemon/EnsureVNF",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("trace logs do not contain %q:\n%s", want, logs)
		}
	}
}
