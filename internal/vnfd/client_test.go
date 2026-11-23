package vnfd

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

func TestClientServerRoundTrip(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	manager.leases[1] = []store.VNFLease{{ClientID: "aa:bb", Address: "10.0.0.11", Active: true}}
	manager.bgp[1] = host.BGPStatus{Running: true, SessionState: "SESSION_STATE_ACTIVE"}

	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	listener, err := Listen("tcp://127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	rookeryv1.RegisterVNFDaemonServer(grpcServer, server)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	client, err := NewClient("tcp://"+listener.Addr().String(), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)

	vnf := store.VNF{
		ID: 1, Name: "dhcp", NetworkID: 7, Type: store.VNFTypeDHCPv4,
		Config: store.VNFConfig{Gateway: "10.0.0.1/24", RangeStart: "10.0.0.10", RangeEnd: "10.0.0.20"},
	}
	if err := client.Start(ctx, vnf, "vnf1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !client.Running(ctx, 1) {
		t.Fatal("running = false after start")
	}
	leases, err := client.Leases(ctx, 1)
	if err != nil || len(leases) != 1 || leases[0].Address != "10.0.0.11" {
		t.Fatalf("leases = %+v, err = %v", leases, err)
	}
	removed, err := client.RemoveLeases(ctx, 1, []string{"aa:bb"})
	if err != nil || removed != 1 {
		t.Fatalf("remove leases = %d, err = %v", removed, err)
	}
	if got := manager.removed[1]; len(got) != 1 || got[0] != "aa:bb" {
		t.Fatalf("removed leases = %+v", got)
	}
	status, err := client.BGPStatus(ctx, 1)
	if err != nil || status.SessionState != "SESSION_STATE_ACTIVE" {
		t.Fatalf("bgp status = %+v, err = %v", status, err)
	}
	if err := client.Stop(ctx, 1, true); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if client.Running(context.Background(), 1) {
		t.Fatal("running = true after stop")
	}
}

func TestClientUnavailableDaemonReportsStopped(t *testing.T) {
	client, err := NewClient("tcp://127.0.0.1:1", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if client.Running(context.Background(), 1) {
		t.Fatal("running = true without a daemon")
	}
	if _, err := client.RemoveLeases(context.Background(), 1, []string{"aa"}); err == nil {
		t.Fatal("remove leases succeeded without a daemon")
	}
}
