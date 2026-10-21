// Package pki manages the server CA, the mTLS server certificate, and client
// certificates issued to API consumers. CA and server private keys live in a
// local directory with restrictive permissions; issued client private keys are
// returned once and never stored.
package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/store"
)

const (
	caCertFile     = "ca.pem"
	caKeyFile      = "ca-key.pem"
	serverCertFile = "server.pem"
	serverKeyFile  = "server-key.pem"
	caValidity     = 10 * 365 * 24 * time.Hour
	serverValidity = 365 * 24 * time.Hour
	clientValidity = 365 * 24 * time.Hour
)

// Manager owns the mTLS CA and certificates.
type Manager struct {
	dir    string
	store  *store.Store
	logger zerolog.Logger

	mu         sync.Mutex
	caCert     *x509.Certificate
	caKey      *ecdsa.PrivateKey
	caPEM      []byte
	caDER      []byte
	serverCert tls.Certificate
	serverPEM  []byte
}

// IssuedClientCertificate is returned once when a client certificate is
// created. PrivateKeyPEM must be delivered to the client and is not stored.
type IssuedClientCertificate struct {
	ID             int64
	Name           string
	Serial         string
	NotBefore      time.Time
	NotAfter       time.Time
	CertificatePEM []byte
	PrivateKeyPEM  []byte
	CAPEM          []byte
}

// New loads or creates the CA and server certificate under dir. extraSANs may
// contain additional DNS names or IP addresses for the server certificate.
func New(dir string, st *store.Store, extraSANs []string, logger zerolog.Logger) (*Manager, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("PKI directory is required")
	}
	if st == nil {
		return nil, errors.New("PKI requires a store")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create PKI directory: %w", err)
	}
	m := &Manager{dir: dir, store: st, logger: logger}
	if err := m.loadOrCreateCA(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreateServer(extraSANs); err != nil {
		return nil, err
	}
	return m, nil
}

// CAPEM returns the PEM encoded CA certificate.
func (m *Manager) CAPEM() []byte {
	return append([]byte(nil), m.caPEM...)
}

// TLSConfig returns the server configuration that requires a client
// certificate signed by this CA and rejects revoked certificates.
func (m *Manager) TLSConfig() *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(m.caCert)
	// h2 is required by grpc-go when this config is used for the mTLS gRPC
	// listener. HTTP clients negotiate h2 or fall back to HTTP/1.1.
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		NextProtos:             []string{"h2", "http/1.1"},
		Certificates:           []tls.Certificate{m.serverCert},
		ClientAuth:             tls.RequireAndVerifyClientCert,
		ClientCAs:              pool,
		SessionTicketsDisabled: true,
		VerifyPeerCertificate: func(_ [][]byte, verifiedChains [][]*x509.Certificate) error {
			if len(verifiedChains) == 0 {
				return errors.New("client certificate is required")
			}
			for _, chain := range verifiedChains {
				for _, cert := range chain {
					if cert.IsCA {
						continue
					}
					revoked, err := m.store.IsClientCertificateRevoked(context.Background(), cert.SerialNumber.Text(16))
					if err != nil {
						return fmt.Errorf("check client certificate revocation: %w", err)
					}
					if revoked {
						return errors.New("client certificate has been revoked")
					}
				}
			}
			return nil
		},
	}
}

// ListClientCertificates returns the issued certificate metadata.
func (m *Manager) ListClientCertificates(ctx context.Context) ([]store.ClientCertificate, error) {
	return m.store.ListClientCertificates(ctx)
}

// RevokeClientCertificate revokes an issued certificate.
func (m *Manager) RevokeClientCertificate(ctx context.Context, id int64) error {
	return m.store.RevokeClientCertificate(ctx, id)
}

// IssueClientCertificate signs a new client certificate. The private key is
// returned once and never persisted.
func (m *Manager) IssueClientCertificate(ctx context.Context, name string, days int) (IssuedClientCertificate, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return IssuedClientCertificate{}, errors.New("client name is required")
	}
	if len([]rune(name)) > 64 {
		return IssuedClientCertificate{}, errors.New("client name must be at most 64 characters")
	}
	if days <= 0 {
		days = int(clientValidity / (24 * time.Hour))
	}
	if days > 3650 {
		return IssuedClientCertificate{}, errors.New("client certificate validity cannot exceed 3650 days")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return IssuedClientCertificate{}, fmt.Errorf("generate client key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return IssuedClientCertificate{}, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   name,
			Organization: []string{"Rookery clients"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(0, 0, days),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, m.caCert, &key.PublicKey, m.caKey)
	if err != nil {
		return IssuedClientCertificate{}, fmt.Errorf("sign client certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return IssuedClientCertificate{}, fmt.Errorf("encode client key: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	record, err := m.store.CreateClientCertificate(ctx, store.ClientCertificate{
		Name: name, Serial: serial.Text(16), NotBefore: template.NotBefore, NotAfter: template.NotAfter,
	})
	if err != nil {
		return IssuedClientCertificate{}, err
	}
	m.logger.Info().Str("client", name).Str("serial", record.Serial).Time("not_after", record.NotAfter).Msg("issued mTLS client certificate")
	return IssuedClientCertificate{
		ID: record.ID, Name: record.Name, Serial: record.Serial,
		NotBefore: record.NotBefore, NotAfter: record.NotAfter,
		CertificatePEM: certPEM, PrivateKeyPEM: keyPEM, CAPEM: m.CAPEM(),
	}, nil
}

func (m *Manager) loadOrCreateCA() error {
	certPath := filepath.Join(m.dir, caCertFile)
	keyPath := filepath.Join(m.dir, caKeyFile)
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		cert, key, parsedPEM, der, err := parseCA(certPEM, keyPEM)
		if err != nil {
			return fmt.Errorf("load PKI CA: %w", err)
		}
		m.caCert, m.caKey, m.caPEM, m.caDER = cert, key, parsedPEM, der
		return nil
	}
	if !errors.Is(certErr, os.ErrNotExist) || !errors.Is(keyErr, os.ErrNotExist) {
		return fmt.Errorf("load PKI CA: %w", errors.Join(certErr, keyErr))
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate PKI CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Rookery CA",
			Organization: []string{"Rookery"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create PKI CA: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return fmt.Errorf("parse PKI CA: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("encode PKI CA key: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write PKI CA key: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("write PKI CA certificate: %w", err)
	}
	m.caCert, m.caKey, m.caPEM, m.caDER = cert, key, certPEM, der
	m.logger.Info().Str("ca", certPath).Msg("generated new mTLS CA")
	return nil
}

func (m *Manager) loadOrCreateServer(extraSANs []string) error {
	certPath := filepath.Join(m.dir, serverCertFile)
	keyPath := filepath.Join(m.dir, serverKeyFile)
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return fmt.Errorf("load mTLS server certificate: %w", err)
		}
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return fmt.Errorf("parse mTLS server certificate: %w", err)
		}
		if !serverNeedsRenewal(leaf, extraSANs) {
			cert.Leaf = leaf
			m.serverCert = cert
			m.serverPEM = certPEM
			return nil
		}
	}
	if certErr != nil && !errors.Is(certErr, os.ErrNotExist) {
		return fmt.Errorf("read mTLS server certificate: %w", certErr)
	}
	if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
		return fmt.Errorf("read mTLS server key: %w", keyErr)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate mTLS server key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	dnsNames, ips := serverSANs(extraSANs)
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "rookery",
			Organization: []string{"Rookery"},
		},
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(serverValidity),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    dnsNames,
		IPAddresses: ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, m.caCert, &key.PublicKey, m.caKey)
	if err != nil {
		return fmt.Errorf("sign mTLS server certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return fmt.Errorf("parse mTLS server certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("encode mTLS server key: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write mTLS server key: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("write mTLS server certificate: %w", err)
	}
	m.serverCert = tls.Certificate{Certificate: [][]byte{der, m.caDER}, PrivateKey: key, Leaf: leaf}
	m.serverPEM = certPEM
	m.logger.Info().Str("certificate", certPath).Strs("sans", dnsNames).Msg("generated mTLS server certificate")
	return nil
}

func parseCA(certPEM, keyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, nil, nil, nil, errors.New("CA certificate PEM is invalid")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, nil, nil, errors.New("CA key PEM is invalid")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if !cert.IsCA {
		return nil, nil, nil, nil, errors.New("stored CA certificate is not a CA")
	}
	return cert, key, certPEM, certBlock.Bytes, nil
}

func serverNeedsRenewal(cert *x509.Certificate, extraSANs []string) bool {
	if time.Until(cert.NotAfter) < 7*24*time.Hour {
		return true
	}
	dnsNames, ips := serverSANs(extraSANs)
	haveDNS := make(map[string]bool, len(cert.DNSNames))
	for _, name := range cert.DNSNames {
		haveDNS[strings.ToLower(name)] = true
	}
	for _, name := range dnsNames {
		if !haveDNS[strings.ToLower(name)] {
			return true
		}
	}
	haveIP := make(map[string]bool, len(cert.IPAddresses))
	for _, ip := range cert.IPAddresses {
		haveIP[ip.String()] = true
	}
	for _, ip := range ips {
		if !haveIP[ip.String()] {
			return true
		}
	}
	return false
}

func serverSANs(extra []string) ([]string, []net.IP) {
	dnsNames := []string{"localhost"}
	seenDNS := map[string]bool{"localhost": true}
	seenIP := make(map[string]bool)
	var ips []net.IP
	addIP := func(ip net.IP) {
		if ip == nil || ip.IsUnspecified() {
			return
		}
		key := ip.String()
		if seenIP[key] {
			return
		}
		seenIP[key] = true
		ips = append(ips, ip)
	}
	addDNS := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seenDNS[strings.ToLower(name)] {
			return
		}
		seenDNS[strings.ToLower(name)] = true
		dnsNames = append(dnsNames, name)
	}
	if hostname, err := os.Hostname(); err == nil {
		addDNS(hostname)
	}
	addIP(net.ParseIP("127.0.0.1"))
	addIP(net.ParseIP("::1"))
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok {
				addIP(ipNet.IP)
			}
		}
	}
	for _, san := range extra {
		san = strings.TrimSpace(san)
		if san == "" {
			continue
		}
		if ip := net.ParseIP(san); ip != nil {
			addIP(ip)
			continue
		}
		addDNS(san)
	}
	return dnsNames, ips
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	return serial, nil
}
