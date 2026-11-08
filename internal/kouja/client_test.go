package kouja

import (
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestNewClientAndFactory(t *testing.T) {
	cfg, err := ParseConfig(testKoujaConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() = %v", err)
	}
	if client.API == nil {
		t.Fatal("NewClient() returned a nil API")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	backend := store.StorageBackend{Config: testKoujaConfig(t)}
	factoryClient, err := Factory{}.New(backend)
	if err != nil {
		t.Fatalf("Factory.New() = %v", err)
	}
	if err := factoryClient.Close(); err != nil {
		t.Fatal(err)
	}
	convenience, err := NewClientForBackend(backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := convenience.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewClientFromServiceAndNilClose(t *testing.T) {
	client := NewClientFromService(&fakeAPI{})
	if client == nil || client.API == nil {
		t.Fatal("NewClientFromService() returned nil")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	var nilClient *Client
	if err := nilClient.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewClientInvalidTLS(t *testing.T) {
	cfg := Config{
		Endpoint: "10.0.0.1:9443",
		CAFile:   "/does/not/exist/ca.crt", ClientCertFile: "/does/not/exist/client.crt",
		ClientKeyFile: "/does/not/exist/client.key",
	}
	if _, err := NewClient(cfg); err == nil {
		t.Fatal("NewClient() accepted missing TLS files")
	}
}
