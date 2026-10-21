package pki

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/store"
)

func TestMTLSClientCertificateLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "pki.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	manager, err := New(filepath.Join(t.TempDir(), "pki"), st, nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if len(manager.CAPEM()) == 0 {
		t.Fatal("CA certificate is empty")
	}
	issued, err := manager.IssueClientCertificate(ctx, "alice", 30)
	if err != nil {
		t.Fatal(err)
	}
	if issued.ID == 0 || len(issued.CertificatePEM) == 0 || len(issued.PrivateKeyPEM) == 0 {
		t.Fatalf("issued certificate = %+v", issued)
	}

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	server.TLS = manager.TLSConfig()
	server.StartTLS()
	defer server.Close()

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(manager.CAPEM()) {
		t.Fatal("could not add CA to pool")
	}
	clientCert, err := tls.X509KeyPair(issued.CertificatePEM, issued.PrivateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{clientCert},
		MinVersion:   tls.VersionTLS13,
	}}}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("mTLS request: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("response = %d %q", response.StatusCode, body)
	}

	// A client without a certificate is rejected.
	noCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS13,
	}}}
	if _, err := noCert.Get(server.URL); err == nil {
		t.Fatal("request without a client certificate succeeded")
	}

	// A revoked certificate is rejected on the next handshake.
	if err := manager.RevokeClientCertificate(ctx, issued.ID); err != nil {
		t.Fatal(err)
	}
	client.CloseIdleConnections()
	if _, err := client.Get(server.URL); err == nil {
		t.Fatal("revoked client certificate was accepted")
	}
}
