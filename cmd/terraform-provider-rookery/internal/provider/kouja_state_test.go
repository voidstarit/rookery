package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

type fakeDiskClient struct {
	rookeryv1.DiskServiceClient
	deleteResponse *rookeryv1.DeleteDiskResponse
	deleteErr      error
	forceResponse  *rookeryv1.DeleteDiskResponse
	forceErr       error
	getResponse    *rookeryv1.Disk
	getErr         error
	deleteRequests []*rookeryv1.DeleteDiskRequest
}

func (f *fakeDiskClient) DeleteDisk(_ context.Context, request *rookeryv1.DeleteDiskRequest, _ ...grpc.CallOption) (*rookeryv1.DeleteDiskResponse, error) {
	f.deleteRequests = append(f.deleteRequests, request)
	if request.GetForce() && (f.forceResponse != nil || f.forceErr != nil) {
		return f.forceResponse, f.forceErr
	}
	return f.deleteResponse, f.deleteErr
}

func (f *fakeDiskClient) GetDisk(context.Context, *rookeryv1.GetDiskRequest, ...grpc.CallOption) (*rookeryv1.Disk, error) {
	return f.getResponse, f.getErr
}

func TestDeleteDiskAsyncBranches(t *testing.T) {
	ctx := context.Background()
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{}}, 1, time.Second, time.Millisecond, false); err != nil {
		t.Fatalf("completed delete = %v", err)
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteErr: status.Error(codes.NotFound, "gone")}, 1, time.Second, time.Millisecond, false); err != nil {
		t.Fatalf("not-found delete = %v", err)
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteErr: errors.New("boom")}, 1, time.Second, time.Millisecond, false); err == nil {
		t.Fatal("delete error was ignored")
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getErr: status.Error(codes.NotFound, "gone")}, 1, time.Second, time.Millisecond, false); err != nil {
		t.Fatalf("accepted not-found = %v", err)
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getErr: errors.New("read failed")}, 1, time.Second, time.Millisecond, false); err == nil {
		t.Fatal("accepted read error was ignored")
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getResponse: &rookeryv1.Disk{DeletionState: "delete_failed", DeletionError: "failed"}}, 1, time.Second, time.Millisecond, false); err == nil {
		t.Fatal("delete_failed was ignored")
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getResponse: &rookeryv1.Disk{}}, 1, time.Second, time.Millisecond, false); err != nil {
		t.Fatalf("completed polling = %v", err)
	}
	if err := deleteDiskAsync(ctx, &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getResponse: &rookeryv1.Disk{DeletionState: "deleting"}}, 1, time.Millisecond, time.Millisecond, false); err == nil {
		t.Fatal("poll timeout was ignored")
	}

	timeoutClient := &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getResponse: &rookeryv1.Disk{DeletionState: "deleting"}, forceResponse: &rookeryv1.DeleteDiskResponse{}}
	if err := deleteDiskAsync(ctx, timeoutClient, 8, time.Millisecond, time.Millisecond, true); err != nil {
		t.Fatalf("forced timeout = %v", err)
	}
	if len(timeoutClient.deleteRequests) != 2 || !timeoutClient.deleteRequests[1].GetForce() {
		t.Fatalf("force-on-timeout requests = %+v", timeoutClient.deleteRequests)
	}

	forceClient := &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		forceResponse: &rookeryv1.DeleteDiskResponse{},
		getResponse:   &rookeryv1.Disk{DeletionState: "delete_failed", DeletionError: "failed"}}
	if err := deleteDiskAsync(ctx, forceClient, 9, time.Second, time.Millisecond, true); err != nil {
		t.Fatalf("forced delete_failed = %v", err)
	}
	if len(forceClient.deleteRequests) != 2 || !forceClient.deleteRequests[1].GetForce() {
		t.Fatalf("force requests = %+v", forceClient.deleteRequests)
	}
	errorClient := &fakeDiskClient{deleteErr: errors.New("boom"), forceResponse: &rookeryv1.DeleteDiskResponse{}}
	if err := deleteDiskAsync(ctx, errorClient, 10, time.Second, time.Millisecond, true); err != nil {
		t.Fatalf("forced delete error = %v", err)
	}
	if len(errorClient.deleteRequests) != 2 || !errorClient.deleteRequests[1].GetForce() {
		t.Fatalf("force-on-error requests = %+v", errorClient.deleteRequests)
	}
	ignoredClient := &fakeDiskClient{deleteResponse: &rookeryv1.DeleteDiskResponse{Accepted: true},
		getResponse: &rookeryv1.Disk{DeletionState: "delete_failed", DeletionError: "failed"}}
	if err := deleteDiskAsync(ctx, ignoredClient, 11, time.Second, time.Millisecond, true); err == nil {
		t.Fatal("ignored force option was accepted")
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
				CaPem: "CA-PEM", ClientCertPem: "CERT-PEM", ClientKeyPem: "KEY-PEM",
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
		config.GetKouja().GetClientKeyFile() != "/etc/client.key" ||
		config.GetKouja().GetCaPem() != "CA-PEM" || config.GetKouja().GetClientCertPem() != "CERT-PEM" ||
		config.GetKouja().GetClientKeyPem() != "KEY-PEM" {
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

type fakeStorageBackendClient struct {
	rookeryv1.StorageBackendServiceClient
	getResponse *rookeryv1.StorageBackend
	list        []*rookeryv1.StorageBackend
	err         error
}

func (f *fakeStorageBackendClient) GetStorageBackend(context.Context, *rookeryv1.GetStorageBackendRequest, ...grpc.CallOption) (*rookeryv1.StorageBackend, error) {
	return f.getResponse, f.err
}

func (f *fakeStorageBackendClient) ListStorageBackends(context.Context, *rookeryv1.ListStorageBackendsRequest, ...grpc.CallOption) (*rookeryv1.ListStorageBackendsResponse, error) {
	return &rookeryv1.ListStorageBackendsResponse{Backends: f.list}, f.err
}

type fakeLabelClient struct {
	rookeryv1.LabelServiceClient
	labels []string
}

func (f *fakeLabelClient) GetEntityLabels(context.Context, *rookeryv1.GetEntityLabelsRequest, ...grpc.CallOption) (*rookeryv1.EntityLabels, error) {
	return &rookeryv1.EntityLabels{Labels: f.labels}, nil
}

// TestStorageBackendDataSourceRead reproduces the provider bug where the data
// source schema omitted the kouja nested config attribute while the shared
// state setter always emitted it, failing every read with a Value Conversion
// Error.
func TestStorageBackendDataSourceRead(t *testing.T) {
	ctx := context.Background()
	koujaBackend := &rookeryv1.StorageBackend{
		Id: 3, Name: "kouja", Driver: "kouja", DiskCount: 1,
		Config: &rookeryv1.StorageBackendConfig{DriverConfig: &rookeryv1.StorageBackendConfig_Kouja{
			Kouja: &rookeryv1.KoujaConfig{Endpoint: "10.0.0.1:9443", StorageClass: "nvme", Portal: "10.0.0.1:3260"},
		}},
	}
	lvmBackend := &rookeryv1.StorageBackend{
		Id: 4, Name: "pool", Driver: "lvm-thin", DiskCount: 2,
		Config: &rookeryv1.StorageBackendConfig{Vg: "vg0", ThinPool: "pool", SupportsTemplates: true},
	}
	fakeBackends := &fakeStorageBackendClient{
		getResponse: koujaBackend,
		list:        []*rookeryv1.StorageBackend{koujaBackend, lvmBackend},
	}
	value := &storageBackendDataSource{configuredDataSource{client: &Client{
		StorageBackends: fakeBackends,
		Labels:          &fakeLabelClient{labels: []string{"prod"}},
	}}}
	var schemaResponse datasource.SchemaResponse
	value.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResponse.Diagnostics)
	}
	schema := schemaResponse.Schema

	read := func(overrides map[string]tftypes.Value) storageBackendModel {
		t.Helper()
		raw := make(map[string]tftypes.Value, len(schema.Attributes))
		for name, attribute := range schema.Attributes {
			raw[name] = tftypes.NewValue(attribute.GetType().TerraformType(ctx), nil)
		}
		for name, rawValue := range overrides {
			raw[name] = rawValue
		}
		config := tfsdk.Config{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), raw)}
		response := datasource.ReadResponse{State: tfsdk.State{Schema: schema}}
		value.Read(ctx, datasource.ReadRequest{Config: config}, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("read: %v", response.Diagnostics)
		}
		var model storageBackendModel
		if diags := response.State.Get(ctx, &model); diags.HasError() {
			t.Fatalf("state: %v", diags)
		}
		return model
	}

	byID := read(map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.Number, 3)})
	config, diags := storageBackendConfigFromObject(ctx, byID.Config)
	if diags.HasError() {
		t.Fatalf("by-id config: %v", diags)
	}
	if byID.Name.ValueString() != "kouja" || config.GetKouja().GetEndpoint() != "10.0.0.1:9443" ||
		config.GetKouja().GetStorageClass() != "nvme" {
		t.Fatalf("by-id model = %+v, config = %+v", byID, config)
	}

	byName := read(map[string]tftypes.Value{"name": tftypes.NewValue(tftypes.String, "POOL")})
	config, diags = storageBackendConfigFromObject(ctx, byName.Config)
	if diags.HasError() {
		t.Fatalf("by-name config: %v", diags)
	}
	if byName.ID.ValueInt64() != 4 || config.GetVg() != "vg0" || config.GetKouja() != nil {
		t.Fatalf("by-name model = %+v, config = %+v", byName, config)
	}
}
