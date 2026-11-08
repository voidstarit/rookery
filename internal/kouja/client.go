package kouja

import (
	"context"
	"crypto/tls"

	koujacli "github.com/voidstarit/kouja/gen/client"
	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"
	"google.golang.org/grpc"

	"github.com/voidstarit/rookery/internal/store"
)

// API is the subset of the kouja VolumeService used by Rookery.
type API interface {
	CreateVolume(context.Context, *koujav1.CreateVolumeRequest, ...grpc.CallOption) (*koujav1.CreateVolumeResponse, error)
	GetVolume(context.Context, *koujav1.GetVolumeRequest, ...grpc.CallOption) (*koujav1.GetVolumeResponse, error)
	GetCredential(context.Context, *koujav1.GetCredentialRequest, ...grpc.CallOption) (*koujav1.GetCredentialResponse, error)
	UnexportVolume(context.Context, *koujav1.UnexportVolumeRequest, ...grpc.CallOption) (*koujav1.UnexportVolumeResponse, error)
	DeleteVolume(context.Context, *koujav1.DeleteVolumeRequest, ...grpc.CallOption) (*koujav1.DeleteVolumeResponse, error)
	GetInfo(context.Context, *koujav1.GetInfoRequest, ...grpc.CallOption) (*koujav1.GetInfoResponse, error)
	ListStorageClasses(context.Context, *koujav1.ListStorageClassesRequest, ...grpc.CallOption) (*koujav1.ListStorageClassesResponse, error)
}

// Client is a kouja VolumeService client with an owned connection.
type Client struct {
	API
	close func() error
}

// NewClient creates a kouja mTLS client for cfg.
func NewClient(cfg Config) (*Client, error) {
	tlsConfig, err := tlsConfigForConfig(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.ServerName != "" {
		tlsConfig.ServerName = cfg.ServerName
	}
	grpcClient, err := koujacli.NewGRPC(cfg.Endpoint, tlsConfig)
	if err != nil {
		return nil, err
	}
	return &Client{API: grpcClient, close: grpcClient.Close}, nil
}

func tlsConfigForConfig(cfg Config) (*tls.Config, error) {
	if cfg.CAPEM != "" || cfg.ClientCertPEM != "" || cfg.ClientKeyPEM != "" {
		return tlsConfigFromPEM(cfg.CAPEM, cfg.ClientCertPEM, cfg.ClientKeyPEM)
	}
	return koujacli.LoadTLSConfig(cfg.ClientCertFile, cfg.ClientKeyFile, cfg.CAFile)
}

// NewClientFromService wraps an existing service client. It is intended for
// tests and for callers that own the gRPC connection.
func NewClientFromService(service API) *Client {
	return &Client{API: service}
}

// Close closes the owned gRPC connection, if any.
func (c *Client) Close() error {
	if c == nil || c.close == nil {
		return nil
	}
	return c.close()
}

// Factory builds a client per backend. It never shares a connection between
// backends with different endpoints or certificate identities.
type Factory struct{}

// New constructs a client for a backend.
func (Factory) New(backend store.StorageBackend) (*Client, error) {
	cfg, err := ParseBackend(backend)
	if err != nil {
		return nil, err
	}
	return NewClient(cfg)
}

// NewClientForBackend is a convenience wrapper around Factory.New.
func NewClientForBackend(backend store.StorageBackend) (*Client, error) {
	return Factory{}.New(backend)
}
