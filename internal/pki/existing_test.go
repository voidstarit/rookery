package pki

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/store"
)

type testPKI struct {
	caCertPEM     []byte
	caKeyPEM      []byte
	caCert        *x509.Certificate
	caKey         *ecdsa.PrivateKey
	serverCertPEM []byte
	serverKeyPEM  []byte
	serverCert    *x509.Certificate
}

func newTestPKI(t *testing.T, dnsNames []string, notAfter time.Time) testPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Site CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "server.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverCert, err := x509.ParseCertificate(serverDER)
	if err != nil {
		t.Fatal(err)
	}
	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	caKeyDER, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		t.Fatal(err)
	}
	return testPKI{
		caCertPEM:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		caKeyPEM:      pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caKeyDER}),
		caCert:        caCert,
		caKey:         caKey,
		serverCertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		serverKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyDER}),
		serverCert:    serverCert,
	}
}

func writeTestPKI(t *testing.T, dir string, material testPKI) {
	t.Helper()
	files := []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{caCertFile, material.caCertPEM, 0o644},
		{caKeyFile, material.caKeyPEM, 0o600},
		{serverCertFile, material.serverCertPEM, 0o644},
		{serverKeyFile, material.serverKeyPEM, 0o600},
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(dir, file.name), file.data, file.mode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExistingPKILoadsProvisionedCertificates(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "existing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	dir := t.TempDir()
	material := newTestPKI(t, []string{"server.test"}, time.Now().Add(365*24*time.Hour))
	writeTestPKI(t, dir, material)

	manager, err := Open(Config{Dir: dir, Mode: ModeExisting}, st, []string{"extra.test", "192.0.2.10"}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if got := manager.CAPEM(); !bytes.Equal(got, material.caCertPEM) {
		t.Fatal("loaded CA does not match the provisioned CA")
	}
	if len(manager.serverCert.Certificate) == 0 {
		t.Fatal("server certificate was not loaded")
	}
	// Existing mode must not renew the certificate for missing extra SANs.
	onDisk, err := os.ReadFile(filepath.Join(dir, serverCertFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, material.serverCertPEM) {
		t.Fatal("existing mode rewrote the server certificate")
	}

	// Client certificates can still be issued with the provisioned CA key.
	issued, err := manager.IssueClientCertificate(context.Background(), "alice", 30)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(issued.CertificatePEM)
	if block == nil {
		t.Fatal("issued certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(material.caCertPEM) {
		t.Fatal("could not load CA")
	}
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("verify issued certificate: %v", err)
	}

	// The provisioned server certificate and a CA-issued client certificate
	// complete a real mTLS request; the server must not have fallen back to a
	// generated certificate.
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	server.TLS = manager.TLSConfig()
	server.StartTLS()
	defer server.Close()

	clientCertificate, err := tls.X509KeyPair(issued.CertificatePEM, issued.PrivateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      roots,
		Certificates: []tls.Certificate{clientCertificate},
		ServerName:   "server.test",
		MinVersion:   tls.VersionTLS13,
	}}}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("mTLS request with existing certificates: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("response = %d %q", response.StatusCode, body)
	}
}

func TestExistingPKIRequiresProvisionedCertificates(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "missing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	dir := t.TempDir()
	if _, err := Open(Config{Dir: dir, Mode: ModeExisting}, st, nil, zerolog.Nop()); err == nil {
		t.Fatal("existing mode accepted a missing certificate directory")
	}
	for _, name := range []string{caCertFile, caKeyFile, serverCertFile, serverKeyFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("existing mode created %s", name)
		}
	}
}

func TestExistingPKIRejectsUntrustedServerCertificate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "untrusted.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	trusted := newTestPKI(t, []string{"server.test"}, time.Now().Add(365*24*time.Hour))
	untrusted := newTestPKI(t, []string{"server.test"}, time.Now().Add(365*24*time.Hour))
	dir := t.TempDir()
	writeTestPKI(t, dir, testPKI{
		caCertPEM:     trusted.caCertPEM,
		caKeyPEM:      trusted.caKeyPEM,
		caCert:        trusted.caCert,
		caKey:         trusted.caKey,
		serverCertPEM: untrusted.serverCertPEM,
		serverKeyPEM:  untrusted.serverKeyPEM,
		serverCert:    untrusted.serverCert,
	})

	_, err = Open(Config{Dir: dir, Mode: ModeExisting}, st, nil, zerolog.Nop())
	if err == nil || !strings.Contains(err.Error(), "verify mTLS server certificate") {
		t.Fatalf("error = %v, want CA verification failure", err)
	}
}

func TestExistingPKIAllowsCAWithoutKey(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "no-key.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	dir := t.TempDir()
	material := newTestPKI(t, []string{"server.test"}, time.Now().Add(365*24*time.Hour))
	writeTestPKI(t, dir, material)
	if err := os.Remove(filepath.Join(dir, caKeyFile)); err != nil {
		t.Fatal(err)
	}

	manager, err := Open(Config{Dir: dir, Mode: ModeExisting}, st, nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.IssueClientCertificate(context.Background(), "alice", 1); err == nil {
		t.Fatal("issued a client certificate without the CA key")
	}
}

func TestExistingPKIUsesCustomFilePaths(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "custom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	root := t.TempDir()
	material := newTestPKI(t, []string{"server.test"}, time.Now().Add(365*24*time.Hour))
	paths := map[string]string{
		"ca":     filepath.Join(root, "ca", "site-ca.crt"),
		"ca-key": filepath.Join(root, "ca", "site-ca.key"),
		"cert":   filepath.Join(root, "server", "tls.crt"),
		"key":    filepath.Join(root, "server", "tls.key"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string, data []byte, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(paths["ca"], material.caCertPEM, 0o644)
	write(paths["ca-key"], material.caKeyPEM, 0o600)
	write(paths["cert"], material.serverCertPEM, 0o644)
	write(paths["key"], material.serverKeyPEM, 0o600)

	manager, err := Open(Config{
		Dir:            root,
		Mode:           ModeExisting,
		CACertFile:     paths["ca"],
		CAKeyFile:      paths["ca-key"],
		ServerCertFile: paths["cert"],
		ServerKeyFile:  paths["key"],
	}, st, nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(manager.CAPEM(), material.caCertPEM) {
		t.Fatal("loaded CA does not match the custom CA file")
	}
}
