package provider

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

// ClientConfig is the resolved provider configuration.
type ClientConfig struct {
	Endpoint   string
	CAFile     string
	CertFile   string
	KeyFile    string
	CAPEM      string
	CertPEM    string
	KeyPEM     string
	ServerName string
	TimeoutRaw string
}

// Client bundles the generated gRPC service clients.
type Client struct {
	Conn *grpc.ClientConn

	System          rookeryv1.SystemServiceClient
	VMs             rookeryv1.VMServiceClient
	Disks           rookeryv1.DiskServiceClient
	Media           rookeryv1.MediaServiceClient
	Networks        rookeryv1.NetworkServiceClient
	Interfaces      rookeryv1.InterfaceServiceClient
	ACLs            rookeryv1.ACLServiceClient
	RouteTables     rookeryv1.RouteTableServiceClient
	VNFs            rookeryv1.VNFServiceClient
	StorageBackends rookeryv1.StorageBackendServiceClient
	HostInterfaces  rookeryv1.HostServiceClient
	Troubleshoot    rookeryv1.TroubleshootServiceClient
	Captures        rookeryv1.CaptureServiceClient

	timeout time.Duration
}

func (c *Client) Timeout() time.Duration {
	if c == nil || c.timeout <= 0 {
		return 30 * time.Second
	}
	return c.timeout
}

// NewClient creates an mTLS gRPC client. The server certificate is verified
// against the supplied CA and a client certificate is always presented.
func NewClient(config ClientConfig) (*Client, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		return nil, errors.New("endpoint is required (set it in the provider block or ROOKERY_ENDPOINT)")
	}
	caPEM, err := loadMaterial("CA certificate", config.CAFile, "ROOKERY_CA_FILE", config.CAPEM)
	if err != nil {
		return nil, err
	}
	certPEM, err := loadMaterial("client certificate", config.CertFile, "ROOKERY_CLIENT_CERT", config.CertPEM)
	if err != nil {
		return nil, err
	}
	keyPEM, err := loadMaterial("client private key", config.KeyFile, "ROOKERY_CLIENT_KEY", config.KeyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("CA certificate does not contain a valid PEM certificate")
	}
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load client certificate/key: %w", err)
	}
	timeout := 30 * time.Second
	if strings.TrimSpace(config.TimeoutRaw) != "" {
		timeout, err = time.ParseDuration(strings.TrimSpace(config.TimeoutRaw))
		if err != nil || timeout <= 0 {
			return nil, fmt.Errorf("timeout must be a positive duration, got %q", config.TimeoutRaw)
		}
	}
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      pool,
		Certificates: []tls.Certificate{certificate},
		ServerName:   strings.TrimSpace(config.ServerName),
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	if err != nil {
		return nil, fmt.Errorf("create Rookery gRPC client: %w", err)
	}
	return &Client{
		Conn:            conn,
		System:          rookeryv1.NewSystemServiceClient(conn),
		VMs:             rookeryv1.NewVMServiceClient(conn),
		Disks:           rookeryv1.NewDiskServiceClient(conn),
		Media:           rookeryv1.NewMediaServiceClient(conn),
		Networks:        rookeryv1.NewNetworkServiceClient(conn),
		Interfaces:      rookeryv1.NewInterfaceServiceClient(conn),
		ACLs:            rookeryv1.NewACLServiceClient(conn),
		RouteTables:     rookeryv1.NewRouteTableServiceClient(conn),
		VNFs:            rookeryv1.NewVNFServiceClient(conn),
		StorageBackends: rookeryv1.NewStorageBackendServiceClient(conn),
		HostInterfaces:  rookeryv1.NewHostServiceClient(conn),
		Troubleshoot:    rookeryv1.NewTroubleshootServiceClient(conn),
		Captures:        rookeryv1.NewCaptureServiceClient(conn),
		timeout:         timeout,
	}, nil
}

func loadMaterial(name, file, envFile, inline string) ([]byte, error) {
	if strings.TrimSpace(inline) != "" {
		return []byte(inline), nil
	}
	path := strings.TrimSpace(file)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(envFile))
	}
	if path == "" {
		return nil, fmt.Errorf("%s is required: set the file path, inline PEM, or %s", name, envFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s %q: %w", name, path, err)
	}
	return data, nil
}
