package kouja

import (
	"context"
	"errors"
	"testing"
	"time"

	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/voidstarit/rookery/internal/blockdev"
	"github.com/voidstarit/rookery/internal/store"
)

func testDisk(protocol blockdev.Protocol) store.Disk {
	reference := blockdev.Reference{Protocol: protocol}
	if protocol == blockdev.ProtocolISCSI {
		reference.ISCSI = &blockdev.ISCSIReference{
			Portal: "10.0.0.1:3260", ExportPortal: "10.0.0.1:3260",
			Target: "iqn.2026-11.io.example:disk", LUN: 0,
			Initiator: "iqn.2026-11.io.example:rookery",
		}
	} else {
		reference.NVMeTCP = &blockdev.NVMeTCPReference{
			Address: "10.0.0.1:4420", ExportAddress: "10.0.0.1:4420",
			NQN: "nqn.2026-11.io.example:disk", HostNQN: "nqn.2026-11.io.example:rookery", NSID: 1,
		}
	}
	return store.Disk{
		Protocol: string(protocol),
		BackendMetadata: store.DiskBackendMetadata{
			Volume: store.VolumeMetadata{
				VolumeID: "volume-1", ResourceID: "resource-1", Owner: "spiffe://kouja/caller/rookery",
			},
			Attachment: reference,
		},
	}
}

func testGetVolumeResponse(protocol blockdev.Protocol, observed *koujav1.Observation) *koujav1.GetVolumeResponse {
	export := &koujav1.Export{
		Protocol: toKoujaProtocol(protocol), TargetName: "iqn.2026-11.io.example:disk",
		Portal: "10.0.0.1:3260", Lun: 0, Nsid: 1,
		State: koujav1.ExportState_EXPORT_STATE_EXPORTED,
	}
	if protocol == blockdev.ProtocolNVMeTCP {
		export.TargetName = "nqn.2026-11.io.example:disk"
		export.Portal = "10.0.0.1:4420"
	}
	return &koujav1.GetVolumeResponse{
		Desired: &koujav1.Volume{
			Owner: "spiffe://kouja/caller/rookery", VolumeId: "volume-1",
			ResourceUuid: "resource-1", State: koujav1.VolumeState_VOLUME_STATE_EXPORTED,
		},
		Observed: observed,
		Export:   export,
	}
}

func testObserved(now time.Time) *koujav1.Observation {
	return &koujav1.Observation{
		TargetPresent: true, AclPresent: true, ActiveSessions: 0,
		SessionState: koujav1.SessionState_SESSION_STATE_NONE,
		ObservedAt:   timestamppb.New(now),
	}
}

func TestPreparerSuccess(t *testing.T) {
	now := time.Now().UTC()
	for _, protocol := range []blockdev.Protocol{blockdev.ProtocolISCSI, blockdev.ProtocolNVMeTCP} {
		t.Run(string(protocol), func(t *testing.T) {
			api := &fakeAPI{
				getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
					return testGetVolumeResponse(protocol, testObserved(now)), nil
				},
				getCredential: func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
					return &koujav1.GetCredentialResponse{Protocol: toKoujaProtocol(protocol), Credential: "secret"}, nil
				},
			}
			preparer := &Preparer{API: api, Now: func() time.Time { return now }}
			attachment, err := preparer.Prepare(context.Background(), provisionerConfig(protocol), testDisk(protocol))
			if err != nil {
				t.Fatalf("Prepare() = %v", err)
			}
			if protocol == blockdev.ProtocolISCSI {
				if attachment.ISCSI == nil || attachment.ISCSI.Username != "kouja-resource-1" ||
					attachment.ISCSI.Credential != "secret" {
					t.Fatalf("iSCSI attachment = %+v", attachment)
				}
			} else if attachment.NVMeTCP == nil || attachment.NVMeTCP.PSK != "secret" {
				t.Fatalf("NVMe attachment = %+v", attachment)
			}
		})
	}
}

func TestPreparerErrors(t *testing.T) {
	now := time.Now().UTC()
	disk := testDisk(blockdev.ProtocolISCSI)
	cfg := provisionerConfig(blockdev.ProtocolISCSI)
	good := testGetVolumeResponse(blockdev.ProtocolISCSI, testObserved(now))
	tests := []struct {
		name string
		api  API
		disk store.Disk
		cfg  Config
	}{
		{name: "nil api", disk: disk, cfg: cfg},
		{name: "protocol", api: &fakeAPI{}, disk: store.Disk{Protocol: string(blockdev.ProtocolLocal)}, cfg: cfg},
		{name: "missing volume", api: &fakeAPI{}, disk: store.Disk{Protocol: string(blockdev.ProtocolISCSI)}, cfg: cfg},
		{name: "get volume", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			return nil, errors.New("boom")
		}}, disk: disk, cfg: cfg},
		{name: "nil response", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			return &koujav1.GetVolumeResponse{}, nil
		}}, disk: disk, cfg: cfg},
		{name: "volume state", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			value := testGetVolumeResponse(blockdev.ProtocolISCSI, testObserved(now))
			value.Desired.State = koujav1.VolumeState_VOLUME_STATE_FAILED
			return value, nil
		}}, disk: disk, cfg: cfg},
		{name: "export state", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			value := testGetVolumeResponse(blockdev.ProtocolISCSI, testObserved(now))
			value.Export.State = koujav1.ExportState_EXPORT_STATE_FAILED
			return value, nil
		}}, disk: disk, cfg: cfg},
		{name: "export protocol", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			value := testGetVolumeResponse(blockdev.ProtocolISCSI, testObserved(now))
			value.Export.Protocol = koujav1.Protocol_PROTOCOL_NVME_TCP
			return value, nil
		}}, disk: disk, cfg: cfg},
		{name: "fresh missing target", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			observed := testObserved(now)
			observed.TargetPresent = false
			return testGetVolumeResponse(blockdev.ProtocolISCSI, observed), nil
		}}, disk: disk, cfg: cfg},
		{name: "active session", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			value := testGetVolumeResponse(blockdev.ProtocolISCSI, testObserved(now))
			value.Export.SessionState = koujav1.SessionState_SESSION_STATE_ACTIVE
			value.Observed.ActiveSessions = 1
			return value, nil
		}}, disk: disk, cfg: cfg},
		{name: "unknown active session", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			value := testGetVolumeResponse(blockdev.ProtocolISCSI, testObserved(now))
			value.Export.SessionState = koujav1.SessionState_SESSION_STATE_UNKNOWN
			value.Observed.ActiveSessions = 1
			return value, nil
		}}, disk: disk, cfg: cfg},
		{name: "owner", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			value := testGetVolumeResponse(blockdev.ProtocolISCSI, testObserved(now))
			value.Desired.Owner = "spiffe://kouja/caller/other"
			return value, nil
		}}, disk: disk, cfg: cfg},
		{name: "missing reference", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			return good, nil
		}}, disk: store.Disk{Protocol: string(blockdev.ProtocolISCSI), BackendMetadata: store.DiskBackendMetadata{
			Volume:     store.VolumeMetadata{VolumeID: "volume-1", ResourceID: "resource-1"},
			Attachment: blockdev.Reference{Protocol: blockdev.ProtocolISCSI},
		}}, cfg: cfg},
		{name: "export portal changed", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			value := testGetVolumeResponse(blockdev.ProtocolISCSI, testObserved(now))
			value.Export.Portal = "10.0.0.2:3260"
			return value, nil
		}}, disk: disk, cfg: cfg},
		{name: "target changed", api: &fakeAPI{getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			value := testGetVolumeResponse(blockdev.ProtocolISCSI, testObserved(now))
			value.Export.TargetName = "iqn.other"
			return value, nil
		}}, disk: disk, cfg: cfg},
		{name: "credential error", api: &fakeAPI{
			getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
				return good, nil
			},
			getCredential: func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
				return nil, errors.New("boom")
			},
		}, disk: disk, cfg: cfg},
		{name: "credential protocol", api: &fakeAPI{
			getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
				return good, nil
			},
			getCredential: func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
				return &koujav1.GetCredentialResponse{Protocol: koujav1.Protocol_PROTOCOL_NVME_TCP, Credential: "secret"}, nil
			},
		}, disk: disk, cfg: cfg},
		{name: "empty credential", api: &fakeAPI{
			getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
				return good, nil
			},
			getCredential: func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
				return &koujav1.GetCredentialResponse{Protocol: koujav1.Protocol_PROTOCOL_ISCSI}, nil
			},
		}, disk: disk, cfg: cfg},
		{name: "missing resource", api: &fakeAPI{
			getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
				return good, nil
			},
			getCredential: func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
				return &koujav1.GetCredentialResponse{Protocol: koujav1.Protocol_PROTOCOL_ISCSI, Credential: "secret"}, nil
			},
		}, disk: store.Disk{Protocol: string(blockdev.ProtocolISCSI), BackendMetadata: store.DiskBackendMetadata{
			Volume:     store.VolumeMetadata{VolumeID: "volume-1"},
			Attachment: disk.BackendMetadata.Attachment,
		}}, cfg: cfg},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preparer := &Preparer{API: tt.api, Now: func() time.Time { return now }}
			if _, err := preparer.Prepare(context.Background(), tt.cfg, tt.disk); err == nil {
				t.Fatal("Prepare() accepted invalid input")
			}
		})
	}
}

func TestPreparerAllowsStaleActiveSessionObservation(t *testing.T) {
	now := time.Now().UTC()
	api := &fakeAPI{
		getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			observed := testObserved(now.Add(-time.Minute))
			observed.ActiveSessions = 1
			observed.SessionState = koujav1.SessionState_SESSION_STATE_ACTIVE
			value := testGetVolumeResponse(blockdev.ProtocolISCSI, observed)
			value.Export.SessionState = koujav1.SessionState_SESSION_STATE_ACTIVE
			return value, nil
		},
		getCredential: func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
			return &koujav1.GetCredentialResponse{Protocol: koujav1.Protocol_PROTOCOL_ISCSI, Credential: "secret"}, nil
		},
	}
	preparer := &Preparer{API: api, Now: func() time.Time { return now }}
	if _, err := preparer.Prepare(context.Background(), provisionerConfig(blockdev.ProtocolISCSI), testDisk(blockdev.ProtocolISCSI)); err != nil {
		t.Fatalf("Prepare(stale active) = %v", err)
	}
}

func TestPreparerAllowsActiveStateWithZeroSessions(t *testing.T) {
	now := time.Now().UTC()
	api := &fakeAPI{
		getVolume: func(context.Context, *koujav1.GetVolumeRequest) (*koujav1.GetVolumeResponse, error) {
			observed := testObserved(now)
			observed.ActiveSessions = 0
			observed.SessionState = koujav1.SessionState_SESSION_STATE_NONE
			value := testGetVolumeResponse(blockdev.ProtocolISCSI, observed)
			value.Export.SessionState = koujav1.SessionState_SESSION_STATE_ACTIVE
			return value, nil
		},
		getCredential: func(context.Context, *koujav1.GetCredentialRequest) (*koujav1.GetCredentialResponse, error) {
			return &koujav1.GetCredentialResponse{Protocol: koujav1.Protocol_PROTOCOL_ISCSI, Credential: "secret"}, nil
		},
	}
	preparer := &Preparer{API: api, Now: func() time.Time { return now }}
	if _, err := preparer.Prepare(context.Background(), provisionerConfig(blockdev.ProtocolISCSI), testDisk(blockdev.ProtocolISCSI)); err != nil {
		t.Fatalf("Prepare(active state, zero sessions) = %v", err)
	}
}
