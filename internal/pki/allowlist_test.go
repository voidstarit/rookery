package pki

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/store"
)

func TestClientCertificateAllowlistRequiresPublicFile(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "allowlist.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	root := t.TempDir()
	pkiDir := filepath.Join(root, "pki")
	clientDir := filepath.Join(root, "clients")
	if err := os.MkdirAll(clientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(Config{Dir: pkiDir, Mode: ModeManaged, ClientDir: clientDir}, st, nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := manager.IssueClientCertificate(context.Background(), "allowed", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(clientDir, "allowed-"+allowed.Serial+".crt")); err != nil {
		t.Fatalf("issued certificate was not written to the allowlist: %v", err)
	}

	// A second manager using the same CA but no allowlist directory can issue
	// a CA-signed certificate that is not present in the allowlist.
	issuer, err := Open(Config{Dir: pkiDir, Mode: ModeManaged}, st, nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	denied, err := issuer.IssueClientCertificate(context.Background(), "denied", 30)
	if err != nil {
		t.Fatal(err)
	}

	if status, err := mtlsStatus(t, manager, allowed.CertificatePEM, allowed.PrivateKeyPEM); err != nil || status != http.StatusOK {
		t.Fatalf("allowlisted certificate: status=%d err=%v", status, err)
	}
	if _, err := mtlsStatus(t, manager, denied.CertificatePEM, denied.PrivateKeyPEM); err == nil {
		t.Fatal("CA-signed certificate without an allowlist file was accepted")
	}
}

func TestClientCertificateAllowlistPicksUpNewFile(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "reload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	root := t.TempDir()
	pkiDir := filepath.Join(root, "pki")
	clientDir := filepath.Join(root, "clients")
	if err := os.MkdirAll(clientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(Config{Dir: pkiDir, Mode: ModeManaged, ClientDir: clientDir}, st, nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := Open(Config{Dir: pkiDir, Mode: ModeManaged}, st, nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	external, err := issuer.IssueClientCertificate(context.Background(), "external", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mtlsStatus(t, manager, external.CertificatePEM, external.PrivateKeyPEM); err == nil {
		t.Fatal("certificate without an allowlist file was accepted")
	}

	path := filepath.Join(clientDir, "external.crt")
	if err := os.WriteFile(path, external.CertificatePEM, 0o644); err != nil {
		t.Fatal(err)
	}
	// Force a distinct directory mtime even on a low-resolution filesystem.
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(clientDir, future, future); err != nil {
		t.Fatal(err)
	}
	if status, err := mtlsStatus(t, manager, external.CertificatePEM, external.PrivateKeyPEM); err != nil || status != http.StatusOK {
		t.Fatalf("certificate after allowlist file: status=%d err=%v", status, err)
	}
}

func TestClientCertificateAllowlistRejectsRevokedCertificate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "revoked.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	root := t.TempDir()
	clientDir := filepath.Join(root, "clients")
	if err := os.MkdirAll(clientDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := Open(Config{Dir: filepath.Join(root, "pki"), Mode: ModeManaged, ClientDir: clientDir}, st, nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	issued, err := manager.IssueClientCertificate(context.Background(), "revoked", 30)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := mtlsStatus(t, manager, issued.CertificatePEM, issued.PrivateKeyPEM); err != nil || status != http.StatusOK {
		t.Fatalf("certificate before revocation: status=%d err=%v", status, err)
	}
	if err := manager.RevokeClientCertificate(context.Background(), issued.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := mtlsStatus(t, manager, issued.CertificatePEM, issued.PrivateKeyPEM); err == nil {
		t.Fatal("revoked certificate with an allowlist file was accepted")
	}
}

func TestClientCertificateAllowlistRequiresDirectory(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "missing-dir.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	_, err = Open(Config{
		Dir:       filepath.Join(t.TempDir(), "pki"),
		Mode:      ModeManaged,
		ClientDir: filepath.Join(t.TempDir(), "missing-clients"),
	}, st, nil, zerolog.Nop())
	if err == nil {
		t.Fatal("Open accepted a missing client certificate allowlist directory")
	}
}

func mtlsStatus(t *testing.T, manager *Manager, certPEM, keyPEM []byte) (int, error) {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	server.TLS = manager.TLSConfig()
	server.StartTLS()
	defer server.Close()

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(manager.CAPEM()) {
		t.Fatal("could not load CA")
	}
	clientCertificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{clientCertificate},
		MinVersion:   tls.VersionTLS13,
	}}}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode, nil
}
