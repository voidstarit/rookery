package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/store"
)

func TestConfigNormalizedValidation(t *testing.T) {
	if _, err := (Config{}).normalized(); err == nil {
		t.Fatal("empty PKI directory was accepted")
	}
	cfg, err := (Config{Dir: "pki"}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModeManaged {
		t.Fatalf("default mode = %q, want %q", cfg.Mode, ModeManaged)
	}
	if _, err := (Config{Dir: "pki", Mode: Mode("bogus")}).normalized(); err == nil {
		t.Fatal("invalid PKI mode was accepted")
	}
	if _, err := OpenClientIssuer(Config{}, nil, zerolog.Nop()); err == nil {
		t.Fatal("OpenClientIssuer accepted an empty PKI directory")
	}
}

func TestParseCertificatesPEMValidation(t *testing.T) {
	if _, err := parseCertificatesPEM([]byte("not a certificate")); err == nil {
		t.Fatal("accepted a non-PEM value")
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not-a-key")})
	if _, err := parseCertificatesPEM(keyPEM); err == nil {
		t.Fatal("accepted a PEM file without a certificate")
	}
	badCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not-a-certificate")})
	if _, err := parseCertificatesPEM(badCert); err == nil {
		t.Fatal("accepted an invalid certificate block")
	}
}

func TestCertificateAllowsClientAuth(t *testing.T) {
	if certificateAllowsClientAuth(nil) {
		t.Fatal("nil certificate allows client auth")
	}
	if !certificateAllowsClientAuth(&x509.Certificate{}) {
		t.Fatal("certificate without EKU should allow client auth")
	}
	if certificateAllowsClientAuth(&x509.Certificate{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}) {
		t.Fatal("server-auth-only certificate allows client auth")
	}
	if !certificateAllowsClientAuth(&x509.Certificate{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}) {
		t.Fatal("client-auth certificate does not allow client auth")
	}
	if !certificateAllowsClientAuth(&x509.Certificate{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}) {
		t.Fatal("any-usage certificate does not allow client auth")
	}
}

func TestClientCertFileStem(t *testing.T) {
	tests := map[string]string{
		"ops laptop":             "ops-laptop",
		"team/ops.node":          "team-ops.node",
		"---":                    "client",
		"日本語":                    "client",
		strings.Repeat("a", 100): strings.Repeat("a", 64),
	}
	for input, want := range tests {
		if got := clientCertFileStem(input); got != want {
			t.Errorf("clientCertFileStem(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseCAValidation(t *testing.T) {
	material := newTestPKI(t, []string{"server.test"}, time.Now().Add(24*time.Hour))
	otherKey := generateTestKeyPEM(t)
	if _, _, _, _, err := parseCA(material.caCertPEM, otherKey); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched CA key error = %v", err)
	}
	if _, _, _, err := parseCACertificate([]byte("not a certificate")); err == nil {
		t.Fatal("accepted an invalid CA PEM")
	}
	badCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not-a-certificate")})
	if _, _, _, err := parseCACertificate(badCert); err == nil {
		t.Fatal("accepted an invalid CA certificate")
	}
	if _, _, _, err := parseCACertificate(material.serverCertPEM); err == nil || !strings.Contains(err.Error(), "not a CA") {
		t.Fatalf("non-CA certificate error = %v", err)
	}
}

func TestExistingPKIErrorPaths(t *testing.T) {
	root := t.TempDir()
	material := newTestPKI(t, []string{"server.test"}, time.Now().Add(24*time.Hour))
	writeTestPKI(t, root, material)

	// Mismatched CA key.
	mismatchedKey := filepath.Join(root, "mismatched-ca.key")
	if err := os.WriteFile(mismatchedKey, generateTestKeyPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{caCertFile: filepath.Join(root, caCertFile), caKeyFile: mismatchedKey, logger: zerolog.Nop()}
	if err := m.loadExistingCA(); err == nil {
		t.Fatal("loadExistingCA accepted a mismatched CA key")
	}

	// Missing CA key is allowed, but an invalid CA certificate is not.
	noKeyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(noKeyDir, caCertFile), []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = &Manager{caCertFile: filepath.Join(noKeyDir, caCertFile), caKeyFile: filepath.Join(noKeyDir, caKeyFile), logger: zerolog.Nop()}
	if err := m.loadExistingCA(); err == nil {
		t.Fatal("loadExistingCA accepted an invalid CA certificate")
	}

	// A CA key path that is not a regular file is a hard error.
	m = &Manager{caCertFile: filepath.Join(root, caCertFile), caKeyFile: root, logger: zerolog.Nop()}
	if err := m.loadExistingCA(); err == nil {
		t.Fatal("loadExistingCA accepted a directory as the CA key")
	}

	// Missing server certificate and key.
	m = &Manager{
		caCert:         material.caCert,
		caCertFile:     filepath.Join(root, caCertFile),
		serverCertFile: filepath.Join(root, "missing-server.pem"),
		serverKeyFile:  filepath.Join(root, "missing-server-key.pem"),
		logger:         zerolog.Nop(),
	}
	if err := m.loadExistingServer(); err == nil {
		t.Fatal("loadExistingServer accepted a missing server certificate")
	}
	m.serverCertFile = filepath.Join(root, serverCertFile)
	m.serverKeyFile = filepath.Join(root, "missing-server-key.pem")
	if err := m.loadExistingServer(); err == nil {
		t.Fatal("loadExistingServer accepted a missing server key")
	}

	// Mismatched server certificate and key.
	m.serverKeyFile = filepath.Join(root, "mismatched-server.key")
	if err := os.WriteFile(m.serverKeyFile, generateTestKeyPEM(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.loadExistingServer(); err == nil {
		t.Fatal("loadExistingServer accepted a mismatched server key")
	}
}

func TestClientAllowlistErrorPaths(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "allowlist-errors.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// A configured client path that is a regular file fails closed.
	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Config{Dir: filepath.Join(t.TempDir(), "pki"), Mode: ModeManaged, ClientDir: filePath}, st, nil, zerolog.Nop()); err == nil {
		t.Fatal("Open accepted a client path that is not a directory")
	}

	m := &Manager{logger: zerolog.Nop()}
	if err := m.refreshClientAllowlist(); err != nil {
		t.Fatalf("refresh without a client directory: %v", err)
	}
	m.clientDir = filepath.Join(t.TempDir(), "missing")
	if err := m.refreshClientAllowlist(); err == nil {
		t.Fatal("refresh accepted a missing client directory")
	}
	m.clientDir = filePath
	if err := m.refreshClientAllowlist(); err == nil {
		t.Fatal("refresh accepted a client path that is not a directory")
	}
	if m.clientCertificateAllowed(nil) {
		t.Fatal("nil client certificate was allowed")
	}
	if err := m.allowClientCertificate([]byte("invalid")); err == nil {
		t.Fatal("allowClientCertificate accepted invalid PEM")
	}
	material := newTestPKI(t, []string{"server.test"}, time.Now().Add(24*time.Hour))
	if err := m.allowClientCertificate(material.caCertPEM); err != nil {
		t.Fatalf("allowClientCertificate rejected a CA certificate: %v", err)
	}

	// Invalid certificate files are skipped; CA and non-certificate files are
	// not added to the allowlist.
	clientDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(clientDir, "bad.crt"), []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clientDir, "ignore.key"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clientDir, "ca.crt"), material.caCertPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	m = &Manager{clientDir: clientDir, logger: zerolog.Nop()}
	if err := m.loadClientAllowlist(); err != nil {
		t.Fatal(err)
	}
	if len(m.clientAllowed) != 0 {
		t.Fatalf("allowlist = %d, want 0", len(m.clientAllowed))
	}
}

func generateTestKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}
