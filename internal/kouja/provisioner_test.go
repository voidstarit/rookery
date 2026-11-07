package kouja

import (
	"context"
	"errors"
	"testing"

	koujacli "github.com/voidstarit/kouja/gen/client"
	koujav1 "github.com/voidstarit/kouja/gen/kouja/v1"

	"github.com/voidstarit/rookery/internal/blockdev"
)

func provisionerConfig(protocol blockdev.Protocol) Config {
	return Config{
		Protocol:     protocol,
		StorageClass: "nvme",
		Initiator:    "iqn.2026-11.io.example:rookery",
		HostNQN:      "nqn.2026-11.io.example:rookery",
		Owner:        "spiffe://kouja/caller/rookery",
	}
}

func createdResponse(protocol blockdev.Protocol) *koujav1.CreateVolumeResponse {
	export := &koujav1.Export{
		Protocol:   toKoujaProtocol(protocol),
		TargetName: "iqn.2026-11.io.example:disk",
		Portal:     "10.0.0.1:3260",
		Lun:        0,
		Nsid:       1,
		State:      koujav1.ExportState_EXPORT_STATE_EXPORTED,
	}
	if protocol == blockdev.ProtocolNVMeTCP {
		export.TargetName = "nqn.2026-11.io.example:disk"
		export.Portal = "10.0.0.1:4420"
	}
	return &koujav1.CreateVolumeResponse{
		Volume: &koujav1.Volume{
			Owner:           "spiffe://kouja/caller/rookery",
			VolumeId:        "volume-1",
			ResourceUuid:    "resource-1",
			ActualSizeBytes: 10 << 30,
			State:           koujav1.VolumeState_VOLUME_STATE_EXPORTED,
		},
		Export: export,
	}
}

func TestProvisionerCreate(t *testing.T) {
	for _, protocol := range []blockdev.Protocol{blockdev.ProtocolISCSI, blockdev.ProtocolNVMeTCP} {
		t.Run(string(protocol), func(t *testing.T) {
			var gotRequest *koujav1.CreateVolumeRequest
			api := &fakeAPI{create: func(ctx context.Context, in *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
				gotRequest = in
				if got := koujacli.RequestIDFromContext(ctx); got != "request-1" {
					t.Fatalf("request ID in context = %q", got)
				}
				return createdResponse(protocol), nil
			}}
			provisioner := &Provisioner{API: api}
			created, err := provisioner.Create(context.Background(), provisionerConfig(protocol), CreateRequest{
				VolumeID: "volume-1", RequestID: "request-1", SizeBytes: 10 << 30,
			})
			if err != nil {
				t.Fatalf("Create() = %v", err)
			}
			if gotRequest.GetProtocol() != toKoujaProtocol(protocol) || !gotRequest.GetExport() ||
				gotRequest.GetIncludeCredential() || gotRequest.GetStorageClass() != "nvme" {
				t.Fatalf("request = %+v", gotRequest)
			}
			if created.Reference.Protocol != protocol || created.ResourceID != "resource-1" ||
				created.ActualSizeBytes != 10<<30 {
				t.Fatalf("created = %+v", created)
			}
			if protocol == blockdev.ProtocolISCSI && created.Reference.ISCSI == nil {
				t.Fatal("missing iSCSI reference")
			}
			if protocol == blockdev.ProtocolNVMeTCP && created.Reference.NVMeTCP == nil {
				t.Fatal("missing NVMe reference")
			}
		})
	}
}

func TestProvisionerErrors(t *testing.T) {
	cfg := provisionerConfig(blockdev.ProtocolISCSI)
	base := createdResponse(blockdev.ProtocolISCSI)
	tests := []struct {
		name string
		api  API
		cfg  Config
		req  CreateRequest
	}{
		{name: "nil api", api: nil, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "protocol", api: &fakeAPI{}, cfg: Config{Protocol: blockdev.ProtocolLocal}, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "create error", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return nil, errors.New("boom")
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "nil response", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return &koujav1.CreateVolumeResponse{}, nil
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "volume id mismatch", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return mutateCreated(base, func(r *koujav1.CreateVolumeResponse) { r.Volume.VolumeId = "other" }), nil
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "resource", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return mutateCreated(base, func(r *koujav1.CreateVolumeResponse) { r.Volume.ResourceUuid = "" }), nil
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "state", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return mutateCreated(base, func(r *koujav1.CreateVolumeResponse) {
				r.Volume.State = koujav1.VolumeState_VOLUME_STATE_FAILED
			}), nil
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "export state", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return mutateCreated(base, func(r *koujav1.CreateVolumeResponse) {
				r.Export.State = koujav1.ExportState_EXPORT_STATE_FAILED
			}), nil
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "protocol", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return mutateCreated(base, func(r *koujav1.CreateVolumeResponse) {
				r.Export.Protocol = koujav1.Protocol_PROTOCOL_NVME_TCP
			}), nil
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "target", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return mutateCreated(base, func(r *koujav1.CreateVolumeResponse) { r.Export.TargetName = "" }), nil
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "portal", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return mutateCreated(base, func(r *koujav1.CreateVolumeResponse) { r.Export.Portal = "" }), nil
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "lun", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return mutateCreated(base, func(r *koujav1.CreateVolumeResponse) { r.Export.Lun = -1 }), nil
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "owner", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return mutateCreated(base, func(r *koujav1.CreateVolumeResponse) { r.Volume.Owner = "" }), nil
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
		{name: "bad portal", api: &fakeAPI{create: func(context.Context, *koujav1.CreateVolumeRequest) (*koujav1.CreateVolumeResponse, error) {
			return mutateCreated(base, func(r *koujav1.CreateVolumeResponse) { r.Export.Portal = "127.0.0.1:3260" }), nil
		}}, cfg: cfg, req: CreateRequest{VolumeID: "v", SizeBytes: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provisioner := &Provisioner{API: tt.api}
			if _, err := provisioner.Create(context.Background(), tt.cfg, tt.req); err == nil {
				t.Fatal("Create() accepted invalid input")
			}
		})
	}
	if _, err := (&Provisioner{API: &fakeAPI{}}).Create(context.Background(), cfg, CreateRequest{SizeBytes: 1}); err == nil {
		t.Fatal("Create() accepted an empty volume ID")
	}
	if _, err := (&Provisioner{API: &fakeAPI{}}).Create(context.Background(), cfg, CreateRequest{VolumeID: "v"}); err == nil {
		t.Fatal("Create() accepted an empty size")
	}
}

func mutateCreated(base *koujav1.CreateVolumeResponse, mutate func(*koujav1.CreateVolumeResponse)) *koujav1.CreateVolumeResponse {
	value := &koujav1.CreateVolumeResponse{
		Volume: &koujav1.Volume{
			Owner: base.Volume.Owner, VolumeId: base.Volume.VolumeId,
			ResourceUuid: base.Volume.ResourceUuid, ActualSizeBytes: base.Volume.ActualSizeBytes,
			State: base.Volume.State,
		},
		Export: &koujav1.Export{
			Protocol: base.Export.Protocol, TargetName: base.Export.TargetName,
			Portal: base.Export.Portal, Lun: base.Export.Lun, Nsid: base.Export.Nsid,
			State: base.Export.State,
		},
	}
	mutate(value)
	return value
}
