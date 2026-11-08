package kouja

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/store"
)

var (
	// ErrNotConfigured is returned when a backend has no kouja configuration.
	ErrNotConfigured = errors.New("kouja backend configuration is missing")
	// ErrUnsupportedProtocol is returned for a protocol this build cannot use.
	ErrUnsupportedProtocol = errors.New("unsupported kouja protocol")
)

// Config is the validated kouja portion of a storage backend.
type Config struct {
	Endpoint       string
	Protocol       blockdev.Protocol
	StorageClass   string
	Initiator      string
	HostNQN        string
	CAFile         string
	ClientCertFile string
	ClientKeyFile  string
	CAPEM          string
	ClientCertPEM  string
	ClientKeyPEM   string
	ServerName     string
	Portal         string
	Owner          string
}

// ParseConfig validates a stored kouja backend config.
func ParseConfig(cfg store.StorageBackendConfig) (Config, error) {
	return parseConfig(cfg, true)
}

// ParseConnectionConfig validates the connection and certificate fields without
// requiring a storage class. It is used by the UI connection probe before the
// operator has selected a class.
func ParseConnectionConfig(cfg store.StorageBackendConfig) (Config, error) {
	return parseConfig(cfg, false)
}

func parseConfig(cfg store.StorageBackendConfig, requireStorageClass bool) (Config, error) {
	if cfg.Kouja == nil {
		return Config{}, ErrNotConfigured
	}
	raw := cfg.Kouja
	protocol := blockdev.Protocol(strings.ToLower(strings.TrimSpace(raw.Protocol)))
	if protocol == "" {
		protocol = blockdev.ProtocolISCSI
	}
	if protocol != blockdev.ProtocolISCSI && protocol != blockdev.ProtocolNVMeTCP {
		return Config{}, fmt.Errorf("%w: %q", ErrUnsupportedProtocol, raw.Protocol)
	}
	parsed := Config{
		Endpoint:       strings.TrimSpace(raw.Endpoint),
		Protocol:       protocol,
		StorageClass:   strings.TrimSpace(raw.StorageClass),
		Initiator:      strings.TrimSpace(raw.Initiator),
		HostNQN:        strings.TrimSpace(raw.HostNQN),
		CAFile:         strings.TrimSpace(raw.CAFile),
		ClientCertFile: strings.TrimSpace(raw.ClientCertFile),
		ClientKeyFile:  strings.TrimSpace(raw.ClientKeyFile),
		CAPEM:          strings.TrimSpace(raw.CAPEM),
		ClientCertPEM:  strings.TrimSpace(raw.ClientCertPEM),
		ClientKeyPEM:   strings.TrimSpace(raw.ClientKeyPEM),
		ServerName:     strings.TrimSpace(raw.ServerName),
		Portal:         strings.TrimSpace(raw.Portal),
	}
	if err := validateEndpoint(parsed.Endpoint); err != nil {
		return Config{}, err
	}
	if requireStorageClass && parsed.StorageClass == "" {
		return Config{}, errors.New("kouja storage_class is required")
	}
	if len(parsed.StorageClass) > 127 {
		return Config{}, errors.New("kouja storage_class is too long")
	}
	hasPEM := parsed.CAPEM != "" || parsed.ClientCertPEM != "" || parsed.ClientKeyPEM != ""
	if hasPEM {
		if parsed.CAPEM == "" || parsed.ClientCertPEM == "" || parsed.ClientKeyPEM == "" {
			return Config{}, errors.New("ca_pem, client_cert_pem, and client_key_pem must be provided together")
		}
		tlsConfig, err := tlsConfigFromPEM(parsed.CAPEM, parsed.ClientCertPEM, parsed.ClientKeyPEM)
		if err != nil {
			return Config{}, err
		}
		owner, err := ownerFromPair(tlsConfig.Certificates[0])
		if err != nil {
			return Config{}, err
		}
		parsed.Owner = owner
	} else {
		for name, path := range map[string]string{
			"ca_file": parsed.CAFile, "client_cert_file": parsed.ClientCertFile, "client_key_file": parsed.ClientKeyFile,
		} {
			if path == "" || !filepath.IsAbs(path) {
				return Config{}, fmt.Errorf("%s must be an absolute path", name)
			}
		}
		if _, err := os.Stat(parsed.CAFile); err != nil {
			return Config{}, fmt.Errorf("stat CA file: %w", err)
		}
		keyInfo, err := os.Stat(parsed.ClientKeyFile)
		if err != nil {
			return Config{}, fmt.Errorf("stat client key: %w", err)
		}
		if keyInfo.Mode().Perm()&0o004 != 0 {
			return Config{}, errors.New("client_key_file must not be world-readable")
		}
		owner, err := ownerFromCertificate(parsed.ClientCertFile, parsed.ClientKeyFile)
		if err != nil {
			return Config{}, err
		}
		parsed.Owner = owner
	}
	switch protocol {
	case blockdev.ProtocolISCSI:
		if !isIQN(parsed.Initiator) {
			return Config{}, errors.New("kouja initiator must be an iSCSI IQN")
		}
	case blockdev.ProtocolNVMeTCP:
		if !isNQN(parsed.HostNQN) {
			return Config{}, errors.New("kouja host_nqn must be an NVMe NQN")
		}
	}
	if parsed.Portal != "" {
		if err := validatePortal(parsed.Portal); err != nil {
			return Config{}, err
		}
	}
	return parsed, nil
}

// ParseBackend validates the kouja config from a storage backend.
func ParseBackend(backend store.StorageBackend) (Config, error) {
	return ParseConfig(backend.Config)
}

func validateEndpoint(value string) error {
	if value == "" {
		return errors.New("kouja endpoint is required")
	}
	target := value
	if strings.HasPrefix(value, "http://") {
		return errors.New("kouja endpoint must use TLS (https://host:port or host:port)")
	}
	if strings.HasPrefix(value, "https://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return fmt.Errorf("parse kouja endpoint: %w", err)
		}
		if parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") {
			return errors.New("kouja endpoint must be host:port or https://host:port")
		}
		target = parsed.Host
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil || strings.TrimSpace(host) == "" {
		return errors.New("kouja endpoint must include host and port")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return errors.New("kouja endpoint port must be between 1 and 65535")
	}
	return nil
}

func validatePortal(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || strings.TrimSpace(host) == "" {
		return errors.New("kouja portal must be host:port")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return errors.New("kouja portal port must be between 1 and 65535")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsUnspecified() || ip.IsLoopback()) {
		return errors.New("kouja portal must not be loopback or unspecified")
	}
	return nil
}

func isIQN(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "iqn.")
}

func isNQN(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "nqn.")
}

func tlsConfigFromPEM(caPEM, certPEM, keyPEM string) (*tls.Config, error) {
	certificate, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("load kouja client certificate: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, errors.New("ca_pem does not contain a certificate")
	}
	return &tls.Config{ //nolint:gosec // kouja requires TLS 1.3 and hostname verification
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		RootCAs:      roots,
	}, nil
}

func ownerFromCertificate(certFile, keyFile string) (string, error) {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return "", fmt.Errorf("load kouja client certificate: %w", err)
	}
	return ownerFromPair(pair)
}

func ownerFromPair(pair tls.Certificate) (string, error) {
	if len(pair.Certificate) == 0 {
		return "", errors.New("kouja client certificate is empty")
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return "", fmt.Errorf("parse kouja client certificate: %w", err)
	}
	if len(cert.URIs) != 1 {
		return "", errors.New("kouja client certificate must have exactly one URI SAN")
	}
	uri := cert.URIs[0]
	if uri.Scheme != "spiffe" || uri.Host != "kouja" {
		return "", errors.New("kouja client certificate URI SAN must use spiffe://kouja/caller/<name>")
	}
	parts := strings.Split(strings.Trim(uri.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "caller" || parts[1] == "" {
		return "", errors.New("kouja client certificate URI SAN must use spiffe://kouja/caller/<name>")
	}
	return uri.String(), nil
}
