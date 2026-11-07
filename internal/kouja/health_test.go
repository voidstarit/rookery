package kouja

import (
	"context"
	"errors"
	"testing"
	"time"

	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/store"
)

func healthInfo() *koujav1.GetInfoResponse {
	return &koujav1.GetInfoResponse{
		Ready:     true,
		Protocols: []koujav1.Protocol{koujav1.Protocol_PROTOCOL_ISCSI},
	}
}

func healthClasses() *koujav1.ListStorageClassesResponse {
	return &koujav1.ListStorageClassesResponse{Classes: []*koujav1.StorageClass{{
		Name: "nvme", VgName: "vg0", VgState: "ready",
		TotalBytes: 100, FreeBytes: 60, AllocatableBytes: 50, MaxVolumeBytes: 20,
	}}}
}

func healthSample() *store.Disk {
	disk := testDisk(blockdev.ProtocolISCSI)
	disk.BackendMetadata.Volume.Owner = "spiffe://kouja/caller/rookery"
	return &disk
}

func TestCheckerSuccess(t *testing.T) {
	now := time.Now().UTC()
	api := &fakeAPI{
		getInfo: func(context.Context, *koujav1.GetInfoRequest) (*koujav1.GetInfoResponse, error) {
			return healthInfo(), nil
		},
		listClasses: func(context.Context, *koujav1.ListStorageClassesRequest) (*koujav1.ListStorageClassesResponse, error) {
			return healthClasses(), nil
		},
		getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			return &koujav1.GetVolumeResponse{Desired: &koujav1.Volume{
				Owner: "spiffe://kouja/caller/rookery",
			}}, nil
		},
		getCredential: func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
			return &koujav1.GetCredentialResponse{Protocol: koujav1.Protocol_PROTOCOL_ISCSI, Credential: "secret"}, nil
		},
	}
	checker := &Checker{API: api, Now: func() time.Time { return now }}
	health, err := checker.Check(context.Background(), provisionerConfig(blockdev.ProtocolISCSI), healthSample())
	if err != nil {
		t.Fatalf("Check() = %v", err)
	}
	if !health.Reachable || !health.Ready || health.Owner == "" ||
		len(health.Classes) != 1 || health.Classes[0].AllocatableBytes != 50 {
		t.Fatalf("health = %+v", health)
	}
	if len(health.Protocols) != 1 || health.Protocols[0] != "iscsi" {
		t.Fatalf("protocols = %v", health.Protocols)
	}
}

func TestCheckerErrors(t *testing.T) {
	apiBase := func() *fakeAPI {
		return &fakeAPI{
			getInfo: func(context.Context, *koujav1.GetInfoRequest) (*koujav1.GetInfoResponse, error) {
				return healthInfo(), nil
			},
			listClasses: func(context.Context, *koujav1.ListStorageClassesRequest) (*koujav1.ListStorageClassesResponse, error) {
				return healthClasses(), nil
			},
		}
	}
	tests := []struct {
		name string
		api  API
		cfg  Config
		samp *store.Disk
	}{
		{name: "nil api", api: nil, cfg: provisionerConfig(blockdev.ProtocolISCSI)},
		{name: "get info", api: &fakeAPI{getInfo: func(context.Context, *koujav1.GetInfoRequest) (*koujav1.GetInfoResponse, error) {
			return nil, errors.New("boom")
		}}, cfg: provisionerConfig(blockdev.ProtocolISCSI)},
		{name: "not ready", api: &fakeAPI{getInfo: func(context.Context, *koujav1.GetInfoRequest) (*koujav1.GetInfoResponse, error) {
			return &koujav1.GetInfoResponse{Ready: false}, nil
		}}, cfg: provisionerConfig(blockdev.ProtocolISCSI)},
		{name: "protocol missing", api: &fakeAPI{getInfo: func(context.Context, *koujav1.GetInfoRequest) (*koujav1.GetInfoResponse, error) {
			return &koujav1.GetInfoResponse{Ready: true, Protocols: []koujav1.Protocol{koujav1.Protocol_PROTOCOL_NVME_TCP}}, nil
		}}, cfg: provisionerConfig(blockdev.ProtocolISCSI)},
		{name: "list classes", api: &fakeAPI{
			getInfo: func(context.Context, *koujav1.GetInfoRequest) (*koujav1.GetInfoResponse, error) {
				return healthInfo(), nil
			},
			listClasses: func(context.Context, *koujav1.ListStorageClassesRequest) (*koujav1.ListStorageClassesResponse, error) {
				return nil, errors.New("boom")
			},
		}, cfg: provisionerConfig(blockdev.ProtocolISCSI)},
		{name: "class missing", api: apiBase(), cfg: Config{Protocol: blockdev.ProtocolISCSI, StorageClass: "missing"}},
		{name: "sample missing volume", api: apiBase(), cfg: provisionerConfig(blockdev.ProtocolISCSI), samp: &store.Disk{}},
		{name: "sample get volume", api: func() *fakeAPI {
			value := apiBase()
			value.getVolume = func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
				return nil, errors.New("boom")
			}
			return value
		}(), cfg: provisionerConfig(blockdev.ProtocolISCSI), samp: healthSample()},
		{name: "sample owner", api: func() *fakeAPI {
			value := apiBase()
			value.getVolume = func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
				return &koujav1.GetVolumeResponse{Desired: &koujav1.Volume{Owner: "spiffe://kouja/caller/other"}}, nil
			}
			return value
		}(), cfg: provisionerConfig(blockdev.ProtocolISCSI), samp: healthSample()},
		{name: "sample credential", api: func() *fakeAPI {
			value := apiBase()
			value.getVolume = func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
				return &koujav1.GetVolumeResponse{Desired: &koujav1.Volume{Owner: "spiffe://kouja/caller/rookery"}}, nil
			}
			value.getCredential = func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
				return nil, errors.New("boom")
			}
			return value
		}(), cfg: provisionerConfig(blockdev.ProtocolISCSI), samp: healthSample()},
		{name: "sample empty credential", api: func() *fakeAPI {
			value := apiBase()
			value.getVolume = func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
				return &koujav1.GetVolumeResponse{Desired: &koujav1.Volume{Owner: "spiffe://kouja/caller/rookery"}}, nil
			}
			value.getCredential = func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
				return &koujav1.GetCredentialResponse{Protocol: koujav1.Protocol_PROTOCOL_ISCSI}, nil
			}
			return value
		}(), cfg: provisionerConfig(blockdev.ProtocolISCSI), samp: healthSample()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker := &Checker{API: tt.api, Now: func() time.Time { return time.Now().UTC() }}
			health, err := checker.Check(context.Background(), tt.cfg, tt.samp)
			if tt.name == "nil api" {
				if err == nil {
					t.Fatal("Check() accepted nil API")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if health.Ready {
				t.Fatalf("health unexpectedly ready: %+v", health)
			}
		})
	}
}

func TestProtocolNames(t *testing.T) {
	names := protocolNames([]koujav1.Protocol{
		koujav1.Protocol_PROTOCOL_ISCSI,
		koujav1.Protocol_PROTOCOL_NVME_TCP,
		koujav1.Protocol_PROTOCOL_UNSPECIFIED,
	})
	if len(names) != 3 || names[0] != "iscsi" || names[1] != "nvme-tcp" {
		t.Fatalf("protocolNames() = %v", names)
	}
	if !containsProtocol([]koujav1.Protocol{koujav1.Protocol_PROTOCOL_ISCSI}, koujav1.Protocol_PROTOCOL_ISCSI) {
		t.Fatal("containsProtocol() = false")
	}
}
