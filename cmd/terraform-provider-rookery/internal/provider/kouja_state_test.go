package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

type fakeDiskClient struct {
	rookeryv1.DiskServiceClient
	deleteResponse *rookeryv1.DeleteDiskResponse
	deleteErr      error
	getResponse    *rookeryv1.Disk
	getErr         error
}

func (f *fakeDiskClient) DeleteDisk(context.Context, *rookeryv1.DeleteDiskRequest, ...grpc.CallOption) (*rookeryv1.DeleteDiskResponse, error) {
	return f.deleteResponse, f.deleteErr
}

func (f *fakeDiskClient) GetDisk(context.Context, *rookeryv1.GetDiskRequest, ...grpc.CallOption) (*rookeryv1.Disk, error) {
	return f.getResponse, f.getErr
}

func TestDeleteDiskAsyncBranches(t *testing.T) {
	ctx := context.Background()
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{}}, 1, time.Second, time.Millisecond); err != nil {
		t.Fatalf("completed delete = %v", err)
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteErr: status.Error(codes.NotFound, "gone")}, 1, time.Second, time.Millisecond); err != nil {
		t.Fatalf("not-found delete = %v", err)
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteErr: errors.New("boom")}, 1, time.Second, time.Millisecond); err == nil {
		t.Fatal("delete error was ignored")
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getErr: status.Error(codes.NotFound, "gone")}, 1, time.Second, time.Millisecond); err != nil {
		t.Fatalf("accepted not-found = %v", err)
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getErr: errors.New("read failed")}, 1, time.Second, time.Millisecond); err == nil {
		t.Fatal("accepted read error was ignored")
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getResponse: &rookeryv1.Disk{DeletionState: "delete_failed", DeletionError: "failed"}}, 1, time.Second, time.Millisecond); err == nil {
		t.Fatal("delete_failed was ignored")
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getResponse: &rookeryv1.Disk{}}, 1, time.Second, time.Millisecond); err != nil {
		t.Fatalf("completed polling = %v", err)
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getResponse: &rookeryv1.Disk{DeletionState: "deleting"}}, 1, time.Millisecond, time.Millisecond); err == nil {
		t.Fatal("poll timeout was ignored")
	}
}

func TestSetDiskStateRemoteFields(t *testing.T) {
	var state diskModel
	setDiskState(&state, &rookeryv1.Disk{
		Id: 7, Name: "remote", SizeGb: 5, Source: "kouja", Protocol: "iscsi",
		Locator: "10.0.0.1:3260 iqn.2026-11.io.example:disk", DeletionState: "deleting",
		DeletionError: "slow", Device: "/dev/ignored",
		ThrottleClassId: 9, ThrottleClassName: "fast",
	})
	if state.Protocol.ValueString() != "iscsi" || state.Locator.ValueString() == "" ||
		state.DeletionState.ValueString() != "deleting" || state.DeletionError.ValueString() != "slow" {
		t.Fatalf("state = %+v", state)
	}
	if state.ThrottleClassID.ValueInt64() != 9 || state.ThrottleClassName.ValueString() != "fast" {
		t.Fatalf("throttle state = %+v", state)
	}
	if !state.HostDevice.IsNull() {
		t.Fatal("remote disk was treated as a host device")
	}
	setDiskState(&state, &rookeryv1.Disk{Id: 8, Source: "host-device", Device: "/dev/sdb"})
	if state.HostDevice.ValueString() != "/dev/sdb" {
		t.Fatalf("host device = %q", state.HostDevice.ValueString())
	}
}

func TestStorageBackendKoujaRoundTrip(t *testing.T) {
	ctx := context.Background()
	var state storageBackendModel
	backend := &rookeryv1.StorageBackend{
		Id: 3, Name: "kouja", Driver: "kouja", DiskCount: 1,
		Config: &rookeryv1.StorageBackendConfig{DriverConfig: &rookeryv1.StorageBackendConfig_Kouja{
			Kouja: &rookeryv1.KoujaConfig{
				Endpoint: "10.0.0.1:9443", Protocol: "iscsi", StorageClass: "nvme",
				Initiator: "iqn.2026-11.io.example:rookery", HostNqn: "nqn.2026-11.io.example:rookery",
				CaFile: "/etc/ca.crt", ClientCertFile: "/etc/client.crt", ClientKeyFile: "/etc/client.key",
				ServerName: "kouja", Portal: "10.0.0.1:3260",
			},
		}},
	}
	if diags := setStorageBackendState(ctx, &state, backend); diags.HasError() {
		t.Fatalf("setStorageBackendState: %v", diags)
	}
	config, diags := storageBackendConfigFromObject(ctx, state.Config)
	if diags.HasError() {
		t.Fatalf("storageBackendConfigFromObject: %v", diags)
	}
	if config.GetKouja().GetEndpoint() != "10.0.0.1:9443" || config.GetKouja().GetStorageClass() != "nvme" ||
		config.GetKouja().GetClientKeyFile() != "/etc/client.key" {
		t.Fatalf("config = %+v", config)
	}
	var lvmState storageBackendModel
	lvm := &rookeryv1.StorageBackend{Id: 4, Name: "lvm", Driver: "lvm-thin",
		Config: &rookeryv1.StorageBackendConfig{Vg: "vg0", ThinPool: "pool", SupportsTemplates: true}}
	if diags := setStorageBackendState(ctx, &lvmState, lvm); diags.HasError() {
		t.Fatalf("setStorageBackendState(lvm): %v", diags)
	}
	lvmConfig, diags := storageBackendConfigFromObject(ctx, lvmState.Config)
	if diags.HasError() || lvmConfig.GetVg() != "vg0" || lvmConfig.GetKouja() != nil {
		t.Fatalf("lvm config = %+v, %v", lvmConfig, diags)
	}
}
