package gateway_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/pki"
	"github.com/voidstarit/rookery/internal/store"
)

func TestExtraMTLSGRPCListener(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "gateway-mtls.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	manager, err := pki.New(filepath.Join(t.TempDir(), "pki"), st, nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	issued, err := manager.IssueClientCertificate(ctx, "gateway-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := tls.NewListener(raw, manager.TLSConfig())
	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithExtraGRPCListener(listener))
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(manager.CAPEM()) {
		t.Fatal("could not add CA to pool")
	}
	clientCertificate, err := tls.X509KeyPair(issued.CertificatePEM, issued.PrivateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      pool,
		Certificates: []tls.Certificate{clientCertificate},
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	health, err := rookeryv1.NewSystemServiceClient(conn).GetHealth(callCtx, &rookeryv1.GetHealthRequest{})
	if err != nil {
		t.Fatalf("mTLS gRPC GetHealth: %v", err)
	}
	if health.GetStatus() != "ok" {
		t.Fatalf("health status = %q", health.GetStatus())
	}

	noCertConn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = noCertConn.Close() })
	if _, err := rookeryv1.NewSystemServiceClient(noCertConn).GetHealth(callCtx, &rookeryv1.GetHealthRequest{}); err == nil {
		t.Fatal("gRPC call without a client certificate succeeded")
	}
}
