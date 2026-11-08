package kouja

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/store"
)

func writeTestClientCert(t *testing.T, uris []*url.URL) (string, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "rookery"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		URIs:                  uris,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile := filepath.Join(dir, "client.crt")
	keyFile := filepath.Join(dir, "client.key")
	caFile := filepath.Join(dir, "ca.crt")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, caFile
}

func testKoujaConfig(t *testing.T) store.StorageBackendConfig {
	t.Helper()
	uri, err := url.Parse("spiffe://kouja/caller/rookery")
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile, caFile := writeTestClientCert(t, []*url.URL{uri})
	return store.StorageBackendConfig{Kouja: &store.KoujaConfig{
		Endpoint:       "10.0.0.1:9443",
		StorageClass:   "nvme",
		Initiator:      "iqn.2026-11.io.example:rookery",
		HostNQN:        "nqn.2026-11.io.example:rookery",
		CAFile:         caFile,
		ClientCertFile: certFile,
		ClientKeyFile:  keyFile,
	}}
}

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig(testKoujaConfig(t))
	if err != nil {
		t.Fatalf("ParseConfig() = %v", err)
	}
	if cfg.Protocol != "iscsi" || cfg.StorageClass != "nvme" || cfg.Owner != "spiffe://kouja/caller/rookery" {
		t.Fatalf("parsed config = %+v", cfg)
	}
	if _, err := ParseBackend(store.StorageBackend{Config: testKoujaConfig(t)}); err != nil {
		t.Fatalf("ParseBackend() = %v", err)
	}
}

func TestParseConfigNVMe(t *testing.T) {
	cfg := testKoujaConfig(t)
	cfg.Kouja.Protocol = "nvme-tcp"
	parsed, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("ParseConfig(nvme) = %v", err)
	}
	if parsed.Protocol != "nvme-tcp" || parsed.HostNQN == "" {
		t.Fatalf("parsed nvme = %+v", parsed)
	}
}

func TestParseConfigValidationBranches(t *testing.T) {
	base := testKoujaConfig(t)
	tests := []struct {
		name string
		cfg  store.StorageBackendConfig
		want string
	}{
		{name: "missing", cfg: store.StorageBackendConfig{}, want: "missing"},
		{name: "protocol", cfg: func() store.StorageBackendConfig {
			value := base
			value.Kouja = cloneKouja(base.Kouja)
			value.Kouja.Protocol = "fc"
			return value
		}(), want: "unsupported"},
		{name: "endpoint", cfg: func() store.StorageBackendConfig {
			value := base
			value.Kouja = cloneKouja(base.Kouja)
			value.Kouja.Endpoint = "http://host:9443"
			return value
		}(), want: "TLS"},
		{name: "endpoint port", cfg: func() store.StorageBackendConfig {
			value := base
			value.Kouja = cloneKouja(base.Kouja)
			value.Kouja.Endpoint = "host"
			return value
		}(), want: "host and port"},
		{name: "class", cfg: func() store.StorageBackendConfig {
			value := base
			value.Kouja = cloneKouja(base.Kouja)
			value.Kouja.StorageClass = ""
			return value
		}(), want: "storage_class"},
		{name: "relative ca", cfg: func() store.StorageBackendConfig {
			value := base
			value.Kouja = cloneKouja(base.Kouja)
			value.Kouja.CAFile = "ca.crt"
			return value
		}(), want: "absolute"},
		{name: "world readable key", cfg: func() store.StorageBackendConfig {
			value := testKoujaConfig(t)
			if err := os.Chmod(value.Kouja.ClientKeyFile, 0o644); err != nil {
				t.Fatal(err)
			}
			return value
		}(), want: "world-readable"},
		{name: "initiator", cfg: func() store.StorageBackendConfig {
			value := base
			value.Kouja = cloneKouja(base.Kouja)
			value.Kouja.Initiator = "host"
			return value
		}(), want: "IQN"},
		{name: "nvme host nqn", cfg: func() store.StorageBackendConfig {
			value := base
			value.Kouja = cloneKouja(base.Kouja)
			value.Kouja.Protocol = "nvme-tcp"
			value.Kouja.HostNQN = "host"
			return value
		}(), want: "NQN"},
		{name: "loopback portal", cfg: func() store.StorageBackendConfig {
			value := base
			value.Kouja = cloneKouja(base.Kouja)
			value.Kouja.Portal = "127.0.0.1:3260"
			return value
		}(), want: "loopback"},
		{name: "unspecified portal", cfg: func() store.StorageBackendConfig {
			value := base
			value.Kouja = cloneKouja(base.Kouja)
			value.Kouja.Portal = "0.0.0.0:3260"
			return value
		}(), want: "loopback or unspecified"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfig(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseConfig() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestOwnerFromCertificateValidation(t *testing.T) {
	uri, _ := url.Parse("spiffe://kouja/caller/rookery")
	certFile, keyFile, _ := writeTestClientCert(t, nil)
	if _, err := ownerFromCertificate(certFile, keyFile); err == nil {
		t.Fatal("ownerFromCertificate accepted no URI SAN")
	}
	certFile, keyFile, _ = writeTestClientCert(t, []*url.URL{uri, uri})
	if _, err := ownerFromCertificate(certFile, keyFile); err == nil {
		t.Fatal("ownerFromCertificate accepted multiple URI SANs")
	}
	bad, _ := url.Parse("spiffe://other/caller/rookery")
	certFile, keyFile, _ = writeTestClientCert(t, []*url.URL{bad})
	if _, err := ownerFromCertificate(certFile, keyFile); err == nil {
		t.Fatal("ownerFromCertificate accepted wrong URI SAN")
	}
}

func cloneKouja(value *store.KoujaConfig) *store.KoujaConfig {
	copy := *value
	return &copy
}

func TestParseConfigPEMContent(t *testing.T) {
	uri, _ := url.Parse("spiffe://kouja/caller/rookery")
	certFile, keyFile, caFile := writeTestClientCert(t, []*url.URL{uri})
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.StorageBackendConfig{Kouja: &store.KoujaConfig{
		Endpoint: "10.0.0.1:9443", StorageClass: "nvme",
		Initiator: "iqn.2026-11.io.example:rookery",
		CAPEM:     string(caPEM), ClientCertPEM: string(certPEM), ClientKeyPEM: string(keyPEM),
	}}
	parsed, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("ParseConfig(PEM) = %v", err)
	}
	if parsed.Owner != "spiffe://kouja/caller/rookery" || parsed.CAPEM == "" {
		t.Fatalf("parsed PEM config = %+v", parsed)
	}
	partial := store.StorageBackendConfig{Kouja: cloneKouja(cfg.Kouja)}
	partial.Kouja.ClientKeyPEM = ""
	if _, err := ParseConfig(partial); err == nil || !strings.Contains(err.Error(), "together") {
		t.Fatalf("partial PEM config = %v", err)
	}
	badCA := store.StorageBackendConfig{Kouja: cloneKouja(cfg.Kouja)}
	badCA.Kouja.CAPEM = "not a certificate"
	if _, err := ParseConfig(badCA); err == nil {
		t.Fatal("ParseConfig accepted a bad CA PEM")
	}
	badKey := store.StorageBackendConfig{Kouja: cloneKouja(cfg.Kouja)}
	badKey.Kouja.ClientKeyPEM = "not a key"
	if _, err := ParseConfig(badKey); err == nil {
		t.Fatal("ParseConfig accepted a bad client key PEM")
	}
}

func TestParseConnectionConfigAllowsEmptyClass(t *testing.T) {
	base := testKoujaConfig(t)
	base.Kouja.StorageClass = ""
	if _, err := ParseConnectionConfig(base); err != nil {
		t.Fatalf("ParseConnectionConfig() = %v", err)
	}
	if _, err := ParseConfig(base); err == nil {
		t.Fatal("ParseConfig accepted an empty storage class")
	}
}
