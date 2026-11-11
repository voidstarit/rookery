package provider

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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
		ExecutableArgs: testList(t, ctx, types.StringType, []string{"serve"}),
		InterfaceIDs:   types.ListNull(types.Int64Type),
		DiskIDs:        types.ListNull(types.Int64Type),
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

func TestLightVMResourceErrors(t *testing.T) {
	ctx := context.Background()
	value := &lightVMResource{}
	var schemaResponse resource.SchemaResponse
	value.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
	model := lightVMModel{
		Name:           types.StringValue("missing-client"),
		Env:            types.ListNull(types.ObjectType{AttrTypes: lightVMEnvAttrTypes()}),
		ExecutableArgs: types.ListNull(types.StringType),
		InterfaceIDs:   types.ListNull(types.Int64Type), DiskIDs: types.ListNull(types.Int64Type),
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
		ExecutableArgs: types.ListNull(types.StringType),
		InterfaceIDs:   types.ListNull(types.Int64Type), DiskIDs: types.ListNull(types.Int64Type),
		VirtioFSShares: types.ListNull(types.ObjectType{AttrTypes: lightVMShareAttrTypes()}),
		Consoles:       types.ListNull(types.ObjectType{AttrTypes: lightVMConsoleAttrTypes()}),
		Labels:         types.SetNull(types.StringType),
	}
	invalidResponse := resource.CreateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	value.Create(ctx, resource.CreateRequest{Plan: planForModel(t, schemaResponse.Schema, &invalid)}, &invalidResponse)
	if !invalidResponse.Diagnostics.HasError() {
		t.Fatal("invalid kernel references succeeded")
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
