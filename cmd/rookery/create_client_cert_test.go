package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/pki"
	"github.com/voidstarit/rookery/internal/store"
)

func TestCreateClientCertCommand(t *testing.T) {
	t.Setenv("ROOKERY_CONFIG", "")
	t.Setenv("ROOKERY_DB", "")
	t.Setenv("ROOKERY_PKI_DIR", "")
	root := t.TempDir()
	dbPath := filepath.Join(root, "rookery.db")
	pkiDir := filepath.Join(root, "pki")
	outDir := filepath.Join(root, "out")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pki.New(pkiDir, st, nil, zerolog.Nop()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	args := []string{"-db", dbPath, "-pki-dir", pkiDir, "-out-dir", outDir, "ops-laptop"}
	if err := runCreateClientCert(args); err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(outDir, "ops-laptop.crt")
	keyPath := filepath.Join(outDir, "ops-laptop.key")
	caPath := filepath.Join(outDir, "ops-laptop-ca.crt")
	for _, path := range []string{certPath, keyPath, caPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := keyInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("private key mode = %o, want 600", got)
	}

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("certificate and key do not match: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("certificate is not PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Subject.CommonName != "ops-laptop" {
		t.Fatalf("common name = %q", leaf.Subject.CommonName)
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("could not load output CA")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("verify certificate: %v", err)
	}

	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	certs, err := st.ListClientCertificates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 1 || certs[0].Name != "ops-laptop" {
		t.Fatalf("stored certificates = %+v", certs)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	if err := runCreateClientCert(args); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second issue error = %v, want existing-file error", err)
	}
	args = []string{"-db", dbPath, "-pki-dir", pkiDir, "-out-dir", outDir, "-force", "ops-laptop"}
	if err := runCreateClientCert(args); err != nil {
		t.Fatal(err)
	}
}

func TestCreateClientCertRequiresExistingCA(t *testing.T) {
	t.Setenv("ROOKERY_CONFIG", "")
	root := t.TempDir()
	dbPath := filepath.Join(root, "rookery.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	err = runCreateClientCert([]string{
		"-db", dbPath,
		"-pki-dir", filepath.Join(root, "missing-pki"),
		"-out-dir", filepath.Join(root, "out"),
		"client",
	})
	if err == nil || !strings.Contains(err.Error(), "CA not found") {
		t.Fatalf("error = %v, want missing CA error", err)
	}
}

func TestCreateClientCertRequiresExistingDatabase(t *testing.T) {
	t.Setenv("ROOKERY_CONFIG", "")
	root := t.TempDir()
	dbPath := filepath.Join(root, "rookery.db")
	err := runCreateClientCert([]string{
		"-db", dbPath,
		"-pki-dir", filepath.Join(root, "missing-pki"),
		"-out-dir", filepath.Join(root, "out"),
		"client",
	})
	if err == nil || !strings.Contains(err.Error(), "database") || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error = %v, want missing database error", err)
	}
	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatalf("database was created: %v", statErr)
	}
}

func TestClientCertificateStem(t *testing.T) {
	tests := map[string]string{
		"ops-laptop":    "ops-laptop",
		"ops laptop":    "ops-laptop",
		"../../escape":  "escape",
		"":              "client",
		"日本語":           "client",
		"team/ops.node": "team-ops.node",
	}
	for input, want := range tests {
		if got := clientCertificateStem(input); got != want {
			t.Errorf("clientCertificateStem(%q) = %q, want %q", input, got, want)
		}
	}
}
