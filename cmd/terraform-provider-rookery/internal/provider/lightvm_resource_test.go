package provider

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

type providerTestReadiness struct{}

func (providerTestReadiness) Status() readiness.Status { return readiness.Status{} }

type providerTestAPI struct {
	client  *Client
	store   *store.Store
	storage string
}

func newProviderTestAPI(t *testing.T) *providerTestAPI {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "provider.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	storageDir := t.TempDir()
	gw, err := gateway.New(st, providerTestReadiness{}, gateway.WithGRPCListener(listener), gateway.WithLightVMStorageDir(storageDir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &providerTestAPI{
		client: &Client{
			Conn:             conn,
			LightVMKernels:   rookeryv1.NewLightVMKernelServiceClient(conn),
			LightVMInitramfs: rookeryv1.NewLightVMInitramfsServiceClient(conn),
			LightVMArtifacts: rookeryv1.NewLightVMArtifactServiceClient(conn),
			LightVMs:         rookeryv1.NewLightVMServiceClient(conn),
			LightVMTemplates: rookeryv1.NewLightVMTemplateServiceClient(conn),
			LightVMGroups:    rookeryv1.NewLightVMGroupServiceClient(conn),
			Labels:           rookeryv1.NewLabelServiceClient(conn),
		},
		store:   st,
		storage: storageDir,
	}
}

func artifactSchema(t *testing.T, value resource.Resource) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	value.Schema(context.Background(), resource.SchemaRequest{}, &response)
	return response.Schema
}

func testStringSet(t *testing.T, ctx context.Context, values []string) types.Set {
	t.Helper()
	set, diags := types.SetValueFrom(ctx, types.StringType, values)
	if diags.HasError() {
		t.Fatalf("set value: %v", diags)
	}
	return set
}

func planForModel(t *testing.T, value schema.Schema, model any) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: value}
	if diags := plan.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	return plan
}

func stateForModel(t *testing.T, value schema.Schema, model any) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: value}
	if diags := state.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return state
}

func TestLightVMArtifactResourceCRUD(t *testing.T) {
	ctx := context.Background()
	api := newProviderTestAPI(t)
	hostFile := filepath.Join(t.TempDir(), "vmlinuz")
	if err := os.WriteFile(hostFile, []byte("kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	value := &lightVMArtifactResource{kind: "kernel", typeName: "lightvm_kernel", entityType: "lightvm_kernel", noun: "kernel"}
	value.client = api.client
	artifactSchema := artifactSchema(t, value)

	model := lightVMArtifactModel{Name: types.StringValue("kernel-one"), Path: types.StringValue(hostFile), Labels: testStringSet(t, ctx, []string{"kernel"})}
	plan := planForModel(t, artifactSchema, &model)
	response := resource.CreateResponse{State: tfsdk.State{Schema: artifactSchema}}
	value.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("create: %v", response.Diagnostics)
	}
	var created lightVMArtifactModel
	if diags := response.State.Get(ctx, &created); diags.HasError() {
		t.Fatalf("read created state: %v", diags)
	}
	if created.ID.ValueInt64() == 0 || created.Managed.ValueBool() || created.Path.ValueString() != hostFile || len(created.Labels.Elements()) != 1 {
		t.Fatalf("created = %+v", created)
	}

	readResponse := resource.ReadResponse{State: stateForModel(t, artifactSchema, &created)}
	value.Read(ctx, resource.ReadRequest{State: readResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read: %v", readResponse.Diagnostics)
	}
	var read lightVMArtifactModel
	if diags := readResponse.State.Get(ctx, &read); diags.HasError() || read.Name.ValueString() != "kernel-one" {
		t.Fatalf("read = %+v, %v", read, diags)
	}

	update := read
	update.Description = types.StringValue("updated")
	updatePlan := planForModel(t, artifactSchema, &update)
	updateState := stateForModel(t, artifactSchema, &read)
	updateResponse := resource.UpdateResponse{State: updateState}
	value.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: updateState}, &updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("update: %v", updateResponse.Diagnostics)
	}
	var updated lightVMArtifactModel
	if diags := updateResponse.State.Get(ctx, &updated); diags.HasError() || updated.Description.ValueString() != "updated" {
		t.Fatalf("updated = %+v, %v", updated, diags)
	}

	deleteState := stateForModel(t, artifactSchema, &updated)
	deleteResponse := resource.DeleteResponse{State: deleteState}
	value.Delete(ctx, resource.DeleteRequest{State: deleteState}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("delete: %v", deleteResponse.Diagnostics)
	}
	readResponse = resource.ReadResponse{State: deleteState}
	value.Read(ctx, resource.ReadRequest{State: deleteState}, &readResponse)
	if readResponse.Diagnostics.HasError() || !readResponse.State.Raw.IsNull() {
		t.Fatalf("read after delete = %v, null=%v", readResponse.Diagnostics, readResponse.State.Raw.IsNull())
	}
}

func TestLightVMArtifactUploadAndUpdate(t *testing.T) {
	ctx := context.Background()
	api := newProviderTestAPI(t)
	source := filepath.Join(t.TempDir(), "initramfs.img")
	if err := os.WriteFile(source, []byte("initramfs-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	value := &lightVMArtifactResource{kind: "initramfs", typeName: "lightvm_initramfs", entityType: "lightvm_initramfs", noun: "initramfs"}
	value.client = api.client
	artifactSchema := artifactSchema(t, value)

	model := lightVMArtifactModel{Name: types.StringValue("initramfs-one"), SourcePath: types.StringValue(source), Labels: types.SetNull(types.StringType)}
	response := resource.CreateResponse{State: tfsdk.State{Schema: artifactSchema}}
	value.Create(ctx, resource.CreateRequest{Plan: planForModel(t, artifactSchema, &model)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("upload create: %v", response.Diagnostics)
	}
	var created lightVMArtifactModel
	if diags := response.State.Get(ctx, &created); diags.HasError() {
		t.Fatalf("created state: %v", diags)
	}
	if !created.Managed.ValueBool() || created.SizeBytes.ValueInt64() != int64(len("initramfs-bytes")) {
		t.Fatalf("created = %+v", created)
	}
	if data, err := os.ReadFile(created.Path.ValueString()); err != nil || string(data) != "initramfs-bytes" {
		t.Fatalf("stored file = %q, %v", data, err)
	}

	updatedModel := created
	updatedModel.SourceHash = types.StringValue("new-hash")
	updateState := stateForModel(t, artifactSchema, &created)
	updatePlan := planForModel(t, artifactSchema, &updatedModel)
	updateResponse := resource.UpdateResponse{State: updateState}
	value.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: updateState}, &updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("upload update: %v", updateResponse.Diagnostics)
	}
	var updated lightVMArtifactModel
	if diags := updateResponse.State.Get(ctx, &updated); diags.HasError() || updated.Path.ValueString() == created.Path.ValueString() {
		t.Fatalf("updated = %+v, %v", updated, diags)
	}
	if _, err := os.Stat(created.Path.ValueString()); !os.IsNotExist(err) {
		t.Fatalf("old upload still exists: %v", err)
	}

	badModel := lightVMArtifactModel{Name: types.StringValue("bad"), Labels: types.SetNull(types.StringType)}
	badResponse := resource.CreateResponse{State: tfsdk.State{Schema: artifactSchema}}
	value.Create(ctx, resource.CreateRequest{Plan: planForModel(t, artifactSchema, &badModel)}, &badResponse)
	if !badResponse.Diagnostics.HasError() {
		t.Fatal("missing source/path was accepted")
	}
	missing := lightVMArtifactModel{Name: types.StringValue("missing"), SourcePath: types.StringValue(filepath.Join(t.TempDir(), "missing")), Labels: types.SetNull(types.StringType)}
	missingResponse := resource.CreateResponse{State: tfsdk.State{Schema: artifactSchema}}
	value.Create(ctx, resource.CreateRequest{Plan: planForModel(t, artifactSchema, &missing)}, &missingResponse)
	if !missingResponse.Diagnostics.HasError() {
		t.Fatal("missing source file was accepted")
	}
	cleanupState := stateForModel(t, artifactSchema, &updated)
	cleanupResponse := resource.DeleteResponse{State: cleanupState}
	value.Delete(ctx, resource.DeleteRequest{State: cleanupState}, &cleanupResponse)
	if cleanupResponse.Diagnostics.HasError() {
		t.Fatalf("cleanup delete: %v", cleanupResponse.Diagnostics)
	}
}

func TestLightVMArtifactUploadCleanupOnCreateFailure(t *testing.T) {
	ctx := context.Background()
	api := newProviderTestAPI(t)
	source := filepath.Join(t.TempDir(), "vmlinuz")
	if err := os.WriteFile(source, []byte("kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	value := &lightVMArtifactResource{kind: "kernel", typeName: "lightvm_kernel", entityType: "lightvm_kernel", noun: "kernel"}
	value.client = api.client
	artifactSchema := artifactSchema(t, value)
	model := lightVMArtifactModel{Name: types.StringValue("bad name!"), SourcePath: types.StringValue(source), Labels: types.SetNull(types.StringType)}
	response := resource.CreateResponse{State: tfsdk.State{Schema: artifactSchema}}
	value.Create(ctx, resource.CreateRequest{Plan: planForModel(t, artifactSchema, &model)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("invalid artifact name was accepted")
	}
	entries, err := os.ReadDir(filepath.Join(api.storage, "kernels"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("uploaded file was not cleaned up: %+v", entries)
	}
}

func TestLightVMResourceCRUD(t *testing.T) {
	ctx := context.Background()
	api := newProviderTestAPI(t)
	kernelPath := filepath.Join(t.TempDir(), "vmlinuz")
	initramfsPath := filepath.Join(t.TempDir(), "initramfs")
	if err := os.WriteFile(kernelPath, []byte("kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(initramfsPath, []byte("initramfs"), 0o644); err != nil {
		t.Fatal(err)
	}
	kernel, err := api.store.CreateLightVMKernel(ctx, store.LightVMKernel{
		Name: "kernel", Path: kernelPath, FileName: "vmlinuz", SizeBytes: 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := api.store.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{
		Name: "initramfs", Path: initramfsPath, FileName: "initramfs", SizeBytes: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	shareDir := t.TempDir()
	shareDir2 := t.TempDir()
	value := &lightVMResource{configuredResource: configuredResource{client: api.client}}
	lightVMSchema := artifactSchema(t, value)

	envType := types.ObjectType{AttrTypes: lightVMEnvAttrTypes()}
	consoleType := types.ObjectType{AttrTypes: lightVMConsoleAttrTypes()}
	shareType := types.ObjectType{AttrTypes: lightVMShareAttrTypes()}
	model := lightVMModel{
		Name:           types.StringValue("app-01"),
		Description:    types.StringValue("created by terraform"),
		VCPUs:          types.Int64Value(2),
		MemoryMB:       types.Int64Value(512),
		ConsoleEnabled: types.BoolValue(true),
		KernelID:       types.Int64Value(kernel.ID),
		InitramfsID:    types.Int64Value(initramfs.ID),
		Image:          types.StringValue("alpine:3.20"),
		ExecutablePath: types.StringValue("/bin/app"),
		Append:         types.StringValue("console=hvc0 rdinit=/init"),
		Env: testList(t, ctx, envType, []lightVMEnvModel{
			{Name: types.StringValue("APP_ENV"), Value: types.StringValue("test")},
		}),
		ConfigFiles:    types.ListNull(types.ObjectType{AttrTypes: lightVMConfigFileAttrTypes()}),
		ExecutableArgs: testList(t, ctx, types.StringType, []string{"serve"}),
		InterfaceIDs:   types.ListNull(types.Int64Type),
		DiskIDs:        types.ListNull(types.Int64Type),
		DiskSerials:    types.MapNull(types.StringType),
		Consoles: testList(t, ctx, consoleType, []lightVMConsoleModel{
			{Name: types.StringValue("serial")},
		}),
		VirtioFSShares: testList(t, ctx, shareType, []lightVMVirtioFSShareModel{
			{HostPath: types.StringValue(shareDir), Tag: types.StringValue("data"), ReadOnly: types.BoolValue(true)},
			{HostPath: types.StringValue(shareDir2), Tag: types.StringValue("scratch"), ReadOnly: types.BoolValue(false)},
		}),
		Labels: testStringSet(t, ctx, []string{"app"}),
	}
	plan := planForModel(t, lightVMSchema, &model)
	response := resource.CreateResponse{State: tfsdk.State{Schema: lightVMSchema}}
	value.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("create: %v", response.Diagnostics)
	}
	var created lightVMModel
	if diags := response.State.Get(ctx, &created); diags.HasError() {
		t.Fatalf("created state: %v", diags)
	}
	if created.ID.ValueString() == "" || created.Status.ValueString() != "stopped" || created.KernelName.ValueString() != "kernel" ||
		len(created.Env.Elements()) != 1 || len(created.Consoles.Elements()) != 1 || len(created.VirtioFSShares.Elements()) != 2 ||
		len(created.Labels.Elements()) != 1 {
		t.Fatalf("created = %+v", created)
	}

	readResponse := resource.ReadResponse{State: stateForModel(t, lightVMSchema, &created)}
	value.Read(ctx, resource.ReadRequest{State: readResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read: %v", readResponse.Diagnostics)
	}
	var read lightVMModel
	if diags := readResponse.State.Get(ctx, &read); diags.HasError() || read.Name.ValueString() != "app-01" {
		t.Fatalf("read = %+v, %v", read, diags)
	}

	update := read
	update.Description = types.StringValue("updated")
	update.Append = types.StringValue("console=hvc0")
	update.Env = testList(t, ctx, envType, []lightVMEnvModel{
		{Name: types.StringValue("APP_ENV"), Value: types.StringValue("prod")},
	})
	update.VirtioFSShares = testList(t, ctx, shareType, []lightVMVirtioFSShareModel{
		{HostPath: types.StringValue(shareDir), Tag: types.StringValue("data"), ReadOnly: types.BoolValue(false)},
	})
	updatePlan := planForModel(t, lightVMSchema, &update)
	updateState := stateForModel(t, lightVMSchema, &read)
	updateResponse := resource.UpdateResponse{State: updateState}
	value.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: updateState}, &updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("update: %v", updateResponse.Diagnostics)
	}
	var updated lightVMModel
	if diags := updateResponse.State.Get(ctx, &updated); diags.HasError() {
		t.Fatalf("updated state: %v", diags)
	}
	if updated.Description.ValueString() != "updated" || updated.Append.ValueString() != "console=hvc0" || len(updated.VirtioFSShares.Elements()) != 1 {
		t.Fatalf("updated = %+v", updated)
	}

	scripted := updated
	scripted.ExecutablePath = types.StringNull()
	scripted.ExecutableScript = types.StringValue("#!/bin/sh\necho hello")
	scripted.ExecutableArgs = types.ListNull(types.StringType)
	scriptPlan := planForModel(t, lightVMSchema, &scripted)
	scriptState := stateForModel(t, lightVMSchema, &updated)
	scriptResponse := resource.UpdateResponse{State: scriptState}
	value.Update(ctx, resource.UpdateRequest{Plan: scriptPlan, State: scriptState}, &scriptResponse)
	if scriptResponse.Diagnostics.HasError() {
		t.Fatalf("script update: %v", scriptResponse.Diagnostics)
	}
	if diags := scriptResponse.State.Get(ctx, &scripted); diags.HasError() {
		t.Fatalf("scripted state: %v", diags)
	}
	if scripted.ExecutableScript.ValueString() != "#!/bin/sh\necho hello" || !scripted.ExecutablePath.IsNull() {
		t.Fatalf("scripted = %+v", scripted)
	}
	updated = scripted

	importResponse := resource.ImportStateResponse{State: stateForModel(t, lightVMSchema, &created)}
	value.ImportState(ctx, resource.ImportStateRequest{ID: created.ID.ValueString()}, &importResponse)
	if importResponse.Diagnostics.HasError() {
		t.Fatalf("import: %v", importResponse.Diagnostics)
	}

	deleteState := stateForModel(t, lightVMSchema, &updated)
	deleteResponse := resource.DeleteResponse{State: deleteState}
	value.Delete(ctx, resource.DeleteRequest{State: deleteState}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("delete: %v", deleteResponse.Diagnostics)
	}
	readAfterDelete := resource.ReadResponse{State: deleteState}
	value.Read(ctx, resource.ReadRequest{State: deleteState}, &readAfterDelete)
	if readAfterDelete.Diagnostics.HasError() || !readAfterDelete.State.Raw.IsNull() {
		t.Fatalf("read after delete = %v, null=%v", readAfterDelete.Diagnostics, readAfterDelete.State.Raw.IsNull())
	}
	again := resource.DeleteResponse{State: deleteState}
	value.Delete(ctx, resource.DeleteRequest{State: deleteState}, &again)
	if again.Diagnostics.HasError() {
		t.Fatalf("second delete: %v", again.Diagnostics)
	}
}

// waitLightVMServiceClient fakes the live metrics queried by wait_for_exit.
// GetLightVMMetrics reports running for the first runningPolls calls and then
// not running. Calls after deletedAfter report NotFound to simulate an
// ephemeral definition removed by the server; deleted reports NotFound at once.
type waitLightVMServiceClient struct {
	rookeryv1.LightVMServiceClient
	runningPolls int
	deletedAfter int
	deleted      bool
	calls        int
}

func (c *waitLightVMServiceClient) GetLightVMMetrics(_ context.Context, request *rookeryv1.GetLightVMMetricsRequest, _ ...grpc.CallOption) (*rookeryv1.LightVMMetrics, error) {
	c.calls++
	if c.deleted || (c.deletedAfter > 0 && c.calls > c.deletedAfter) {
		return nil, status.Error(codes.NotFound, "lightvm not found")
	}
	return &rookeryv1.LightVMMetrics{
		LightvmId: request.GetId(),
		Running:   c.calls <= c.runningPolls,
	}, nil
}

// TestLightVMResourceWaitsForExit covers the wait_for_exit option: a normal
// LightVM completes when its process stops, while an ephemeral LightVM also
// waits for the server to delete the definition.
func TestLightVMResourceWaitsForExit(t *testing.T) {
	previous := lightVMExitPollInterval
	lightVMExitPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { lightVMExitPollInterval = previous })

	ctx := context.Background()
	api := newProviderTestAPI(t)
	kernel, err := api.store.CreateLightVMKernel(ctx, store.LightVMKernel{
		Name: "wait-kernel", Path: "/tmp/vmlinuz", FileName: "vmlinuz", SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := api.store.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{
		Name: "wait-initramfs", Path: "/tmp/initramfs", FileName: "initramfs", SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		ephemeral bool
		stub      *waitLightVMServiceClient
		wantCalls int
	}{
		{
			name:      "running LightVM stops",
			stub:      &waitLightVMServiceClient{runningPolls: 2},
			wantCalls: 3,
		},
		{
			name:      "ephemeral waits for deletion",
			ephemeral: true,
			stub:      &waitLightVMServiceClient{runningPolls: 1, deletedAfter: 2},
			wantCalls: 3,
		},
		{
			name:      "already deleted",
			ephemeral: true,
			stub:      &waitLightVMServiceClient{deleted: true},
			wantCalls: 1,
		},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := *api.client
			tc.stub.LightVMServiceClient = api.client.LightVMs
			client.LightVMs = tc.stub
			value := &lightVMResource{configuredResource: configuredResource{client: &client}}
			lightVMSchema := artifactSchema(t, value)

			model := lightVMModel{
				Name:           types.StringValue(fmt.Sprintf("wait-%d", index)),
				KernelID:       types.Int64Value(kernel.ID),
				InitramfsID:    types.Int64Value(initramfs.ID),
				Image:          types.StringValue("alpine"),
				ExecutablePath: types.StringValue("/bin/app"),
				Ephemeral:      types.BoolValue(tc.ephemeral),
				WaitForExit:    types.BoolValue(true),
				Env:            types.ListNull(types.ObjectType{AttrTypes: lightVMEnvAttrTypes()}),
				ConfigFiles:    types.ListNull(types.ObjectType{AttrTypes: lightVMConfigFileAttrTypes()}),
				ExecutableArgs: types.ListNull(types.StringType),
				InterfaceIDs:   types.ListNull(types.Int64Type),
				DiskIDs:        types.ListNull(types.Int64Type),
				DiskSerials:    types.MapNull(types.StringType),
				Consoles:       types.ListNull(types.ObjectType{AttrTypes: lightVMConsoleAttrTypes()}),
				VirtioFSShares: types.ListNull(types.ObjectType{AttrTypes: lightVMShareAttrTypes()}),
				Labels:         types.SetNull(types.StringType),
			}
			plan := planForModel(t, lightVMSchema, &model)
			response := resource.CreateResponse{State: tfsdk.State{Schema: lightVMSchema}}
			value.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("create: %v", response.Diagnostics)
			}
			if tc.stub.calls != tc.wantCalls {
				t.Fatalf("metrics calls = %d, want %d", tc.stub.calls, tc.wantCalls)
			}
			var created lightVMModel
			if diags := response.State.Get(ctx, &created); diags.HasError() {
				t.Fatalf("created state: %v", diags)
			}
			if created.ID.ValueString() == "" || !created.WaitForExit.ValueBool() {
				t.Fatalf("created = %+v", created)
			}
		})
	}
}

// TestLightVMResourceWaitForExitCanceled verifies that an interrupted wait
// reports the error but still records the created resource so it can be
// inspected or destroyed.
func TestLightVMResourceWaitForExitCanceled(t *testing.T) {
	previous := lightVMExitPollInterval
	lightVMExitPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { lightVMExitPollInterval = previous })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	api := newProviderTestAPI(t)
	kernel, err := api.store.CreateLightVMKernel(ctx, store.LightVMKernel{
		Name: "cancel-kernel", Path: "/tmp/vmlinuz", FileName: "vmlinuz", SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := api.store.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{
		Name: "cancel-initramfs", Path: "/tmp/initramfs", FileName: "initramfs", SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	stub := &waitLightVMServiceClient{runningPolls: 1 << 20}
	stub.LightVMServiceClient = api.client.LightVMs
	client := *api.client
	client.LightVMs = stub
	value := &lightVMResource{configuredResource: configuredResource{client: &client}}
	lightVMSchema := artifactSchema(t, value)
	model := lightVMModel{
		Name:           types.StringValue("wait-canceled"),
		KernelID:       types.Int64Value(kernel.ID),
		InitramfsID:    types.Int64Value(initramfs.ID),
		Image:          types.StringValue("alpine"),
		ExecutablePath: types.StringValue("/bin/app"),
		WaitForExit:    types.BoolValue(true),
		Env:            types.ListNull(types.ObjectType{AttrTypes: lightVMEnvAttrTypes()}),
		ConfigFiles:    types.ListNull(types.ObjectType{AttrTypes: lightVMConfigFileAttrTypes()}),
		ExecutableArgs: types.ListNull(types.StringType),
		InterfaceIDs:   types.ListNull(types.Int64Type),
		DiskIDs:        types.ListNull(types.Int64Type),
		DiskSerials:    types.MapNull(types.StringType),
		Consoles:       types.ListNull(types.ObjectType{AttrTypes: lightVMConsoleAttrTypes()}),
		VirtioFSShares: types.ListNull(types.ObjectType{AttrTypes: lightVMShareAttrTypes()}),
		Labels:         types.SetNull(types.StringType),
	}
	response := resource.CreateResponse{State: tfsdk.State{Schema: lightVMSchema}}
	value.Create(ctx, resource.CreateRequest{Plan: planForModel(t, lightVMSchema, &model)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("canceled wait did not report an error")
	}
	var created lightVMModel
	if diags := response.State.Get(context.Background(), &created); diags.HasError() || created.ID.ValueString() == "" {
		t.Fatalf("created state after canceled wait = %+v, %v", created, diags)
	}
}

func TestLightVMResourceErrors(t *testing.T) {
	ctx := context.Background()
	value := &lightVMResource{}
	var schemaResponse resource.SchemaResponse
	value.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
	model := lightVMModel{
		Name:           types.StringValue("missing-client"),
		Env:            types.ListNull(types.ObjectType{AttrTypes: lightVMEnvAttrTypes()}),
		ConfigFiles:    types.ListNull(types.ObjectType{AttrTypes: lightVMConfigFileAttrTypes()}),
		ExecutableArgs: types.ListNull(types.StringType),
		InterfaceIDs:   types.ListNull(types.Int64Type), DiskIDs: types.ListNull(types.Int64Type),
		DiskSerials:    types.MapNull(types.StringType),
		VirtioFSShares: types.ListNull(types.ObjectType{AttrTypes: lightVMShareAttrTypes()}),
		Consoles:       types.ListNull(types.ObjectType{AttrTypes: lightVMConsoleAttrTypes()}),
		Labels:         types.SetNull(types.StringType),
	}
	response := resource.CreateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	value.Create(ctx, resource.CreateRequest{Plan: planForModel(t, schemaResponse.Schema, &model)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("create without a configured client succeeded")
	}

	api := newProviderTestAPI(t)
	value.client = api.client
	invalid := lightVMModel{
		Name: types.StringValue("bad-kernel"), KernelID: types.Int64Value(999), InitramfsID: types.Int64Value(999),
		Image: types.StringValue("alpine"), ExecutablePath: types.StringValue("/bin/app"),
		Env:            types.ListNull(types.ObjectType{AttrTypes: lightVMEnvAttrTypes()}),
		ConfigFiles:    types.ListNull(types.ObjectType{AttrTypes: lightVMConfigFileAttrTypes()}),
		ExecutableArgs: types.ListNull(types.StringType),
		InterfaceIDs:   types.ListNull(types.Int64Type), DiskIDs: types.ListNull(types.Int64Type),
		DiskSerials:    types.MapNull(types.StringType),
		VirtioFSShares: types.ListNull(types.ObjectType{AttrTypes: lightVMShareAttrTypes()}),
		Consoles:       types.ListNull(types.ObjectType{AttrTypes: lightVMConsoleAttrTypes()}),
		Labels:         types.SetNull(types.StringType),
	}
	invalidResponse := resource.CreateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	value.Create(ctx, resource.CreateRequest{Plan: planForModel(t, schemaResponse.Schema, &invalid)}, &invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("invalid kernel references succeeded")
	}

	conflicting := invalid
	conflicting.ExecutablePath = types.StringValue("/bin/app")
	conflicting.ExecutableScript = types.StringValue("echo hello")
	conflictResponse := resource.CreateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	value.Create(ctx, resource.CreateRequest{Plan: planForModel(t, schemaResponse.Schema, &conflicting)}, &conflictResponse)
	if !conflictResponse.Diagnostics.HasError() {
		t.Fatal("executable path and script together succeeded")
	}
}

func TestLightVMResourceMetadata(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		value resource.Resource
		want  string
	}{
		{NewLightVMKernelResource(), "rookery_lightvm_kernel"},
		{NewLightVMInitramfsResource(), "rookery_lightvm_initramfs"},
		{NewLightVMResource(), "rookery_lightvm"},
	}
	for _, tc := range cases {
		var response resource.MetadataResponse
		tc.value.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "rookery"}, &response)
		if response.TypeName != tc.want {
			t.Fatalf("type name = %q, want %q", response.TypeName, tc.want)
		}
	}
}

func TestArtifactPathPlanModifier(t *testing.T) {
	ctx := context.Background()
	value := &lightVMArtifactResource{kind: "kernel", typeName: "lightvm_kernel", entityType: "lightvm_kernel", noun: "kernel"}
	schemaValue := artifactSchema(t, value)
	modifier := artifactPathPlanModifier{}

	hostPlan := lightVMArtifactModel{Name: types.StringValue("host"), Path: types.StringValue("/host/kernel"), Labels: types.SetNull(types.StringType)}
	request := planmodifier.StringRequest{
		Plan: planForModel(t, schemaValue, &hostPlan), PlanValue: types.StringValue("/host/kernel"),
	}
	response := planmodifier.StringResponse{PlanValue: request.PlanValue}
	modifier.PlanModifyString(ctx, request, &response)
	if response.Diagnostics.HasError() || response.PlanValue.ValueString() != "/host/kernel" {
		t.Fatalf("host path plan = %v, %v", response.PlanValue, response.Diagnostics)
	}

	sourcePlan := lightVMArtifactModel{Name: types.StringValue("upload"), SourcePath: types.StringValue("/local/kernel"), SourceHash: types.StringValue("one"), Labels: types.SetNull(types.StringType)}
	request = planmodifier.StringRequest{Plan: planForModel(t, schemaValue, &sourcePlan), PlanValue: types.StringUnknown()}
	response = planmodifier.StringResponse{PlanValue: request.PlanValue}
	modifier.PlanModifyString(ctx, request, &response)
	if !response.PlanValue.IsUnknown() {
		t.Fatalf("new upload path = %v, want unknown", response.PlanValue)
	}

	state := sourcePlan
	state.Path = types.StringValue("/managed/one")
	request = planmodifier.StringRequest{
		Plan: planForModel(t, schemaValue, &sourcePlan), State: stateForModel(t, schemaValue, &state),
		StateValue: types.StringValue("/managed/one"), PlanValue: types.StringValue("/managed/one"),
	}
	response = planmodifier.StringResponse{PlanValue: request.PlanValue}
	modifier.PlanModifyString(ctx, request, &response)
	if response.PlanValue.ValueString() != "/managed/one" {
		t.Fatalf("unchanged upload path = %v", response.PlanValue)
	}

	changedHash := sourcePlan
	changedHash.SourceHash = types.StringValue("two")
	request = planmodifier.StringRequest{
		Plan: planForModel(t, schemaValue, &changedHash), State: stateForModel(t, schemaValue, &state),
		StateValue: types.StringValue("/managed/one"), PlanValue: types.StringValue("/managed/one"),
	}
	response = planmodifier.StringResponse{PlanValue: request.PlanValue}
	modifier.PlanModifyString(ctx, request, &response)
	if !response.PlanValue.IsUnknown() {
		t.Fatalf("changed hash path = %v, want unknown", response.PlanValue)
	}
	if capitalize("") != "" || capitalize("kernel") != "Kernel" {
		t.Fatalf("capitalize = %q, %q", capitalize(""), capitalize("kernel"))
	}
}

func TestArtifactDerivedPlanModifiers(t *testing.T) {
	ctx := context.Background()
	value := &lightVMArtifactResource{kind: "kernel", typeName: "lightvm_kernel", entityType: "lightvm_kernel", noun: "kernel"}
	schemaValue := artifactSchema(t, value)

	configFor := func(model lightVMArtifactModel) tfsdk.Config {
		t.Helper()
		return tfsdk.Config{Schema: schemaValue, Raw: planForModel(t, schemaValue, &model).Raw}
	}

	// State left by an uploaded artifact: Rookery assigned the managed path.
	uploadState := lightVMArtifactModel{
		ID: types.Int64Value(7), Name: types.StringValue("kernel"),
		SourcePath: types.StringValue("/local/kernel"), SourceHash: types.StringValue("one"),
		Path: types.StringValue("/managed/one"), FileName: types.StringValue("kernel"),
		SizeBytes: types.Int64Value(1070494), Managed: types.BoolValue(true),
		Labels: types.SetNull(types.StringType),
	}
	// Matching configuration: path stays unset because the upload assigns it.
	uploadConfig := lightVMArtifactModel{
		Name:       types.StringValue("kernel"),
		SourcePath: types.StringValue("/local/kernel"), SourceHash: types.StringValue("one"),
		Labels: types.SetNull(types.StringType),
	}
	// State left by a host-path registration.
	hostState := lightVMArtifactModel{
		ID: types.Int64Value(8), Name: types.StringValue("host-kernel"),
		Path: types.StringValue("/host/kernel"), FileName: types.StringValue("kernel"),
		SizeBytes: types.Int64Value(4096), Managed: types.BoolValue(false),
		Labels: types.SetNull(types.StringType),
	}
	hostConfig := lightVMArtifactModel{
		Name: types.StringValue("host-kernel"), Path: types.StringValue("/host/kernel"),
		Labels: types.SetNull(types.StringType),
	}

	check := func(t *testing.T, configModel, stateModel lightVMArtifactModel, wantSize types.Int64, wantManaged types.Bool) {
		t.Helper()
		state := stateForModel(t, schemaValue, &stateModel)
		plan := planForModel(t, schemaValue, &stateModel)
		sizeRequest := planmodifier.Int64Request{
			Config: configFor(configModel), Plan: plan, State: state,
			PlanValue: types.Int64Unknown(), StateValue: stateModel.SizeBytes,
		}
		sizeResponse := planmodifier.Int64Response{PlanValue: sizeRequest.PlanValue}
		artifactSizePlanModifier{}.PlanModifyInt64(ctx, sizeRequest, &sizeResponse)
		if sizeResponse.Diagnostics.HasError() {
			t.Fatalf("size diagnostics: %v", sizeResponse.Diagnostics)
		}
		if !sizeResponse.PlanValue.Equal(wantSize) {
			t.Fatalf("size = %v, want %v", sizeResponse.PlanValue, wantSize)
		}
		managedRequest := planmodifier.BoolRequest{
			Config: configFor(configModel), Plan: plan, State: state,
			PlanValue: types.BoolUnknown(), StateValue: stateModel.Managed,
		}
		managedResponse := planmodifier.BoolResponse{PlanValue: managedRequest.PlanValue}
		artifactManagedPlanModifier{}.PlanModifyBool(ctx, managedRequest, &managedResponse)
		if managedResponse.Diagnostics.HasError() {
			t.Fatalf("managed diagnostics: %v", managedResponse.Diagnostics)
		}
		if !managedResponse.PlanValue.Equal(wantManaged) {
			t.Fatalf("managed = %v, want %v", managedResponse.PlanValue, wantManaged)
		}
	}

	// Updates that leave the file alone (labels, description, name) keep the
	// server-derived values in the plan.
	check(t, uploadConfig, uploadState, types.Int64Value(1070494), types.BoolValue(true))
	check(t, hostConfig, hostState, types.Int64Value(4096), types.BoolValue(false))

	// Re-uploading a rebuilt file changes its size, so the plan must defer both
	// values to apply. A stale size in the plan is exactly what produced
	// "provider produced inconsistent result after apply".
	rebuilt := uploadConfig
	rebuilt.SourceHash = types.StringValue("two")
	check(t, rebuilt, uploadState, types.Int64Unknown(), types.BoolUnknown())

	renamed := uploadConfig
	renamed.SourcePath = types.StringValue("/local/kernel-2")
	check(t, renamed, uploadState, types.Int64Unknown(), types.BoolUnknown())

	// Switching from a managed upload to a host path flips managed and re-stats
	// the file.
	toHost := uploadConfig
	toHost.SourcePath = types.StringNull()
	toHost.SourceHash = types.StringNull()
	toHost.Path = types.StringValue("/host/kernel")
	check(t, toHost, uploadState, types.Int64Unknown(), types.BoolUnknown())

	// A different registered host path is a different file.
	movedHost := hostConfig
	movedHost.Path = types.StringValue("/host/kernel-2")
	check(t, movedHost, hostState, types.Int64Unknown(), types.BoolUnknown())

	// An interpolated path cannot be compared at plan time, so defer to apply.
	unknownHost := hostConfig
	unknownHost.Path = types.StringUnknown()
	check(t, unknownHost, hostState, types.Int64Unknown(), types.BoolUnknown())

	// Create has no prior state, so the values stay unknown until apply.
	createPlan := planForModel(t, schemaValue, &uploadConfig)
	createRequest := planmodifier.Int64Request{
		Config: configFor(uploadConfig), Plan: createPlan,
		State: tfsdk.State{Schema: schemaValue}, PlanValue: types.Int64Unknown(),
	}
	createResponse := planmodifier.Int64Response{PlanValue: createRequest.PlanValue}
	artifactSizePlanModifier{}.PlanModifyInt64(ctx, createRequest, &createResponse)
	if createResponse.Diagnostics.HasError() || !createResponse.PlanValue.IsUnknown() {
		t.Fatalf("create size = %v, %v", createResponse.PlanValue, createResponse.Diagnostics)
	}
}

func TestLightVMScriptRequestConversion(t *testing.T) {
	ctx := context.Background()
	model := lightVMModel{
		Name: types.StringValue("scripted"), VCPUs: types.Int64Value(1), MemoryMB: types.Int64Value(128),
		KernelID: types.Int64Value(1), InitramfsID: types.Int64Value(2), Image: types.StringValue("alpine"),
		ExecutableScript: types.StringValue("echo hello"),
		Env:              types.ListNull(types.ObjectType{AttrTypes: lightVMEnvAttrTypes()}),
		ConfigFiles:      types.ListNull(types.ObjectType{AttrTypes: lightVMConfigFileAttrTypes()}),
		ExecutableArgs:   types.ListNull(types.StringType),
		InterfaceIDs:     types.ListNull(types.Int64Type), DiskIDs: types.ListNull(types.Int64Type),
		DiskSerials:    types.MapNull(types.StringType),
		VirtioFSShares: types.ListNull(types.ObjectType{AttrTypes: lightVMShareAttrTypes()}),
		Consoles:       types.ListNull(types.ObjectType{AttrTypes: lightVMConsoleAttrTypes()}),
		Labels:         types.SetNull(types.StringType),
	}
	create, diags := lightVMCreateRequest(ctx, model)
	if diags.HasError() || create.GetExecutableScript() != "echo hello" || create.GetExecutablePath() != "" {
		t.Fatalf("script create = %+v, %v", create, diags)
	}
	model.ID = types.StringValue("123e4567-e89b-12d3-a456-426614174000")
	update, diags := lightVMUpdateRequest(ctx, model)
	if diags.HasError() || update.GetExecutableScript() != "echo hello" {
		t.Fatalf("script update = %+v, %v", update, diags)
	}
}
