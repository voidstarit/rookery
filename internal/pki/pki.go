// Package pki manages the server CA, the mTLS server certificate, and client
// certificates issued to API consumers. CA and server private keys live in a
// local directory with restrictive permissions; issued client private keys are
// returned once and never stored.
package pki

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
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

// Mode selects how Rookery obtains its CA and server certificate.
type Mode string

const (
	// ModeManaged creates and renews the CA and server certificate under the
	// PKI directory. This is the historical behavior.
	ModeManaged Mode = "managed"
	// ModeExisting loads the CA and server certificate from provisioned files
	// and never writes, rotates, or renews them. The CA certificate must be
	// valid; the CA private key is optional for serving but is required to
	// issue client certificates. The server certificate must be signed by the
	// CA and valid for server authentication.
	ModeExisting Mode = "existing"
)

// Config describes where the mTLS material lives and whether Rookery may
// create it. Empty file fields default to the standard names inside Dir.
type Config struct {
	Dir            string
	Mode           Mode
	CACertFile     string
	CAKeyFile      string
	ServerCertFile string
	ServerKeyFile  string
	ClientDir      string
}

func (c Config) normalized() (Config, error) {
	c.Dir = strings.TrimSpace(c.Dir)
	if c.Dir == "" {
		return c, errors.New("PKI directory is required")
	}
	mode := Mode(strings.ToLower(strings.TrimSpace(string(c.Mode))))
	if mode == "" {
		mode = ModeManaged
	}
	if mode != ModeManaged && mode != ModeExisting {
		return c, fmt.Errorf("PKI mode %q is invalid; use %q or %q", c.Mode, ModeManaged, ModeExisting)
	}
	c.Mode = mode
	c.CACertFile = pkiFilePath(c.Dir, c.CACertFile, caCertFile)
	c.CAKeyFile = pkiFilePath(c.Dir, c.CAKeyFile, caKeyFile)
	c.ServerCertFile = pkiFilePath(c.Dir, c.ServerCertFile, serverCertFile)
	c.ServerKeyFile = pkiFilePath(c.Dir, c.ServerKeyFile, serverKeyFile)
	return c, nil
}

func pkiFilePath(dir, value, name string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return filepath.Join(dir, name)
}

// Manager owns the mTLS CA and certificates.
type Manager struct {
	dir            string
	mode           Mode
	caCertFile     string
	caKeyFile      string
	serverCertFile string
	serverKeyFile  string
	clientDir      string
	store          *store.Store
	logger         zerolog.Logger

	clientMu         sync.RWMutex
	clientReloadMu   sync.Mutex
	clientAllowed    map[[sha256.Size]byte]struct{}
	clientDirModTime time.Time

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
// It uses ModeManaged and is equivalent to Open with the default file names.
func New(dir string, st *store.Store, extraSANs []string, logger zerolog.Logger) (*Manager, error) {
	return Open(Config{Dir: dir, Mode: ModeManaged}, st, extraSANs, logger)
}

// Open loads the configured PKI material. In ModeManaged it creates and renews
// the CA and server certificate as needed. In ModeExisting it loads provisioned
// files, requires the server certificate to be signed by the CA, and never
// writes to disk.
func Open(cfg Config, st *store.Store, extraSANs []string, logger zerolog.Logger) (*Manager, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, errors.New("PKI requires a store")
	}
	m := &Manager{
		dir:            cfg.Dir,
		mode:           cfg.Mode,
		caCertFile:     cfg.CACertFile,
		caKeyFile:      cfg.CAKeyFile,
		serverCertFile: cfg.ServerCertFile,
		serverKeyFile:  cfg.ServerKeyFile,
		clientDir:      cfg.ClientDir,
		store:          st,
		logger:         logger,
	}
	m.logger.Info().Str("pki_mode", string(m.mode)).Msg("PKI configured")
	if cfg.Mode == ModeExisting {
		if err := m.loadExistingCA(); err != nil {
			return nil, err
		}
		if err := m.loadExistingServer(); err != nil {
			return nil, err
		}
		if err := m.loadClientAllowlist(); err != nil {
			return nil, err
		}
		return m, nil
	}

	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("create PKI directory: %w", err)
	}
	for _, path := range []string{cfg.CACertFile, cfg.CAKeyFile, cfg.ServerCertFile, cfg.ServerKeyFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create PKI file directory for %s: %w", path, err)
		}
	}
	if err := m.loadOrCreateCA(); err != nil {
		return nil, err
	}
	if err := m.loadOrCreateServer(extraSANs); err != nil {
		return nil, err
	}
	if err := m.loadClientAllowlist(); err != nil {
		return nil, err
	}
	return m, nil
}

// OpenClientIssuer loads an existing CA so client certificates can be issued by
// an administrative command. It never creates a CA or server certificate.
func OpenClientIssuer(cfg Config, st *store.Store, logger zerolog.Logger) (*Manager, error) {
	cfg, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, errors.New("PKI requires a store")
	}
	m := &Manager{
		dir:        cfg.Dir,
		mode:       cfg.Mode,
		caCertFile: cfg.CACertFile,
		caKeyFile:  cfg.CAKeyFile,
		clientDir:  cfg.ClientDir,
		store:      st,
		logger:     logger,
	}
	if err := m.loadCA(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf(
				"Rookery CA not found or incomplete (certificate %s, key %s); start rookery serve with -pki-dir %s first",
				cfg.CACertFile, cfg.CAKeyFile, cfg.Dir,
			)
		}
		return nil, fmt.Errorf("load PKI CA: %w", err)
	}
	return m, nil
}

// NewClientIssuer loads an existing CA from the standard files in dir.
func NewClientIssuer(dir string, st *store.Store, logger zerolog.Logger) (*Manager, error) {
	return OpenClientIssuer(Config{Dir: dir, Mode: ModeManaged}, st, logger)
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
			if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
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
			if m.clientDir != "" {
				if err := m.refreshClientAllowlist(); err != nil {
					return err
				}
				if !m.clientCertificateAllowed(verifiedChains[0][0]) {
					return fmt.Errorf("client certificate is not allowlisted in %s", m.clientDir)
				}
			}
			return nil
		},
	}
}

// loadClientAllowlist loads the public client certificates under
// clientDir. It is a no-op when no client directory is configured.
func (m *Manager) loadClientAllowlist() error {
	if m.clientDir == "" {
		return nil
	}
	m.clientReloadMu.Lock()
	defer m.clientReloadMu.Unlock()
	return m.loadClientAllowlistLocked()
}

// loadClientAllowlistLocked reloads the allowlist while clientReloadMu is held.
func (m *Manager) loadClientAllowlistLocked() error {
	info, err := os.Stat(m.clientDir)
	if err != nil {
		return fmt.Errorf("read client certificate directory %s: %w", m.clientDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("client certificate path %s is not a directory", m.clientDir)
	}
	allowed := make(map[[sha256.Size]byte]struct{})
	skipped := 0
	err = filepath.WalkDir(m.clientDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".crt" && extension != ".pem" && extension != ".cer" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read client certificate %s: %w", path, err)
		}
		certs, err := parseCertificatesPEM(data)
		if err != nil {
			m.logger.Warn().Err(err).Str("file", path).Msg("skipping invalid client certificate")
			skipped++
			return nil
		}
		for _, cert := range certs {
			if cert.IsCA || !certificateAllowsClientAuth(cert) {
				continue
			}
			allowed[sha256.Sum256(cert.Raw)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk client certificate directory %s: %w", m.clientDir, err)
	}
	m.clientMu.Lock()
	m.clientAllowed = allowed
	m.clientMu.Unlock()
	m.clientDirModTime = info.ModTime()
	m.logger.Info().
		Str("client_cert_dir", m.clientDir).
		Int("allowed_clients", len(allowed)).
		Int("skipped_files", skipped).
		Msg("loaded client certificate allowlist")
	return nil
}

// refreshClientAllowlist reloads the allowlist when the directory changed.
// This lets an administrator or create-client-cert add a public certificate
// without restarting the API.
func (m *Manager) refreshClientAllowlist() error {
	if m.clientDir == "" {
		return nil
	}
	m.clientReloadMu.Lock()
	defer m.clientReloadMu.Unlock()
	info, err := os.Stat(m.clientDir)
	if err != nil {
		return fmt.Errorf("read client certificate directory %s: %w", m.clientDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("client certificate path %s is not a directory", m.clientDir)
	}
	if info.ModTime().Equal(m.clientDirModTime) {
		return nil
	}
	return m.loadClientAllowlistLocked()
}

func (m *Manager) clientCertificateAllowed(cert *x509.Certificate) bool {
	if cert == nil {
		return false
	}
	fingerprint := sha256.Sum256(cert.Raw)
	m.clientMu.RLock()
	defer m.clientMu.RUnlock()
	_, ok := m.clientAllowed[fingerprint]
	return ok
}

func (m *Manager) allowClientCertificate(certPEM []byte) error {
	certs, err := parseCertificatesPEM(certPEM)
	if err != nil {
		return err
	}
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if m.clientAllowed == nil {
		m.clientAllowed = make(map[[sha256.Size]byte]struct{})
	}
	for _, cert := range certs {
		if cert.IsCA || !certificateAllowsClientAuth(cert) {
			continue
		}
		m.clientAllowed[sha256.Sum256(cert.Raw)] = struct{}{}
	}
	return nil
}

// writeClientCertificateToAllowlist writes the public certificate for an issued
// client into clientDir. It returns an empty path when no directory is set.
func (m *Manager) writeClientCertificateToAllowlist(certPEM []byte, name string, serial *big.Int) (string, error) {
	if m.clientDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(m.clientDir, 0o700); err != nil {
		return "", fmt.Errorf("create client certificate directory %s: %w", m.clientDir, err)
	}
	filename := clientCertFileStem(name) + "-" + serial.Text(16) + ".crt"
	path := filepath.Join(m.clientDir, filename)
	if err := os.WriteFile(path, certPEM, 0o644); err != nil {
		return "", fmt.Errorf("write client certificate %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		return "", fmt.Errorf("set client certificate permissions %s: %w", path, err)
	}
	return path, nil
}

func parseCertificatesPEM(data []byte) ([]*x509.Certificate, error) {
	certs := make([]*x509.Certificate, 0)
	for {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		data = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, errors.New("no certificates found")
	}
	return certs, nil
}

func certificateAllowsClientAuth(cert *x509.Certificate) bool {
	if cert == nil {
		return false
	}
	if len(cert.ExtKeyUsage) == 0 && len(cert.UnknownExtKeyUsage) == 0 {
		return true
	}
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageAny || usage == x509.ExtKeyUsageClientAuth {
			return true
		}
	}
	return false
}

func clientCertFileStem(name string) string {
	var stem strings.Builder
	lastDash := false
	for _, char := range strings.TrimSpace(name) {
		safe := char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' ||
			char == '.' || char == '_' || char == '-'
		if safe {
			stem.WriteRune(char)
			lastDash = false
			continue
		}
		if !lastDash {
			stem.WriteByte('-')
			lastDash = true
		}
	}
	value := strings.Trim(stem.String(), ".-_")
	if value == "" {
		return "client"
	}
	if len(value) > 64 {
		value = value[:64]
	}
	return value
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
	if m.caKey == nil {
		return IssuedClientCertificate{}, fmt.Errorf(
			"CA private key is not available at %s; client certificate issuance requires the CA key",
			m.caKeyFile,
		)
	}

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

	allowlistPath, err := m.writeClientCertificateToAllowlist(certPEM, name, serial)
	if err != nil {
		return IssuedClientCertificate{}, err
	}
	record, err := m.store.CreateClientCertificate(ctx, store.ClientCertificate{
		Name: name, Serial: serial.Text(16), NotBefore: template.NotBefore, NotAfter: template.NotAfter,
	})
	if err != nil {
		if allowlistPath != "" {
			_ = os.Remove(allowlistPath)
		}
		return IssuedClientCertificate{}, err
	}
	if allowlistPath != "" {
		if err := m.allowClientCertificate(certPEM); err != nil {
			m.logger.Warn().Err(err).Str("file", allowlistPath).Msg("failed to add issued client certificate to the in-memory allowlist")
		}
	}
	m.logger.Info().Str("client", name).Str("serial", record.Serial).Time("not_after", record.NotAfter).Msg("issued mTLS client certificate")
	return IssuedClientCertificate{
		ID: record.ID, Name: record.Name, Serial: record.Serial,
		NotBefore: record.NotBefore, NotAfter: record.NotAfter,
		CertificatePEM: certPEM, PrivateKeyPEM: keyPEM, CAPEM: m.CAPEM(),
	}, nil
}

func (m *Manager) loadCA() error {
	certPEM, certErr := os.ReadFile(m.caCertFile)
	keyPEM, keyErr := os.ReadFile(m.caKeyFile)
	if certErr != nil || keyErr != nil {
		return errors.Join(certErr, keyErr)
	}
	return m.setCA(certPEM, keyPEM)
}

// loadExistingCA loads the provisioned CA. The CA key is optional so a server
// can trust an offline CA, but client certificate issuance then fails.
func (m *Manager) loadExistingCA() error {
	certPEM, err := os.ReadFile(m.caCertFile)
	if err != nil {
		return fmt.Errorf("read CA certificate %s: %w", m.caCertFile, err)
	}
	keyPEM, keyErr := os.ReadFile(m.caKeyFile)
	switch {
	case keyErr == nil:
		if err := m.setCA(certPEM, keyPEM); err != nil {
			return fmt.Errorf("load existing CA: %w", err)
		}
	case errors.Is(keyErr, os.ErrNotExist):
		cert, parsedPEM, der, err := parseCACertificate(certPEM)
		if err != nil {
			return fmt.Errorf("load existing CA: %w", err)
		}
		m.caCert, m.caKey, m.caPEM, m.caDER = cert, nil, parsedPEM, der
		m.logger.Warn().
			Str("ca_key", m.caKeyFile).
			Msg("CA private key not found; client certificate issuance is disabled")
	default:
		return fmt.Errorf("read CA key %s: %w", m.caKeyFile, keyErr)
	}
	return nil
}

func (m *Manager) setCA(certPEM, keyPEM []byte) error {
	cert, key, parsedPEM, der, err := parseCA(certPEM, keyPEM)
	if err != nil {
		return err
	}
	m.caCert, m.caKey, m.caPEM, m.caDER = cert, key, parsedPEM, der
	return nil
}

func (m *Manager) loadOrCreateCA() error {
	certPath := m.caCertFile
	keyPath := m.caKeyFile
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr == nil && keyErr == nil {
		if err := m.setCA(certPEM, keyPEM); err != nil {
			return fmt.Errorf("load PKI CA: %w", err)
		}
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
	certPath := m.serverCertFile
	keyPath := m.serverKeyFile
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

// loadExistingServer loads a provisioned server certificate without renewing
// it. The certificate must chain to the configured CA and be valid for server
// authentication; the SANs are entirely the operator's responsibility.
func (m *Manager) loadExistingServer() error {
	certPEM, err := os.ReadFile(m.serverCertFile)
	if err != nil {
		return fmt.Errorf("read mTLS server certificate %s: %w", m.serverCertFile, err)
	}
	keyPEM, err := os.ReadFile(m.serverKeyFile)
	if err != nil {
		return fmt.Errorf("read mTLS server key %s: %w", m.serverKeyFile, err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("load mTLS server certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse mTLS server certificate: %w", err)
	}
	if err := m.verifyServerCertificate(cert, leaf); err != nil {
		return err
	}
	cert.Leaf = leaf
	m.serverCert = cert
	m.serverPEM = certPEM
	m.logger.Info().
		Str("certificate", m.serverCertFile).
		Time("not_after", leaf.NotAfter).
		Msg("loaded existing mTLS server certificate")
	return nil
}

func (m *Manager) verifyServerCertificate(cert tls.Certificate, leaf *x509.Certificate) error {
	roots := x509.NewCertPool()
	roots.AddCert(m.caCert)
	intermediates := x509.NewCertPool()
	for _, der := range cert.Certificate[1:] {
		parsed, err := x509.ParseCertificate(der)
		if err != nil {
			return fmt.Errorf("parse mTLS server certificate chain: %w", err)
		}
		intermediates.AddCert(parsed)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return fmt.Errorf("verify mTLS server certificate %s against CA %s: %w", m.serverCertFile, m.caCertFile, err)
	}
	return nil
}

func parseCA(certPEM, keyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte, error) {
	cert, parsedPEM, der, err := parseCACertificate(certPEM)
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
	certPublic, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("encode CA public key: %w", err)
	}
	keyPublic, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("encode CA private key public part: %w", err)
	}
	if !bytes.Equal(certPublic, keyPublic) {
		return nil, nil, nil, nil, errors.New("CA private key does not match the CA certificate")
	}
	return cert, key, parsedPEM, der, nil
}

func parseCACertificate(certPEM []byte) (*x509.Certificate, []byte, []byte, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, nil, nil, errors.New("CA certificate PEM is invalid")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, nil, err
	}
	if !cert.IsCA {
		return nil, nil, nil, errors.New("stored CA certificate is not a CA")
	}
	return cert, certPEM, certBlock.Bytes, nil
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
