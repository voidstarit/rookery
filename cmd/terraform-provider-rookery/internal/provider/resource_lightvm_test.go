package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func testList[T any](t *testing.T, ctx context.Context, typ attr.Type, values []T) types.List {
	t.Helper()
	list, diags := types.ListValueFrom(ctx, typ, values)
	if diags.HasError() {
		t.Fatalf("list value: %v", diags)
	}
	return list
}

func TestLightVMRequestConversion(t *testing.T) {
	ctx := context.Background()
	envType := types.ObjectType{AttrTypes: lightVMEnvAttrTypes()}
	consoleType := types.ObjectType{AttrTypes: lightVMConsoleAttrTypes()}
	shareType := types.ObjectType{AttrTypes: lightVMShareAttrTypes()}
	model := lightVMModel{
		Name:           types.StringValue("app"),
		Description:    types.StringValue("test"),
		VCPUs:          types.Int64Value(2),
		MemoryMB:       types.Int64Value(512),
		ConsoleEnabled: types.BoolValue(true),
		CgroupClass:    types.StringValue("prod/web"),
		KernelID:       types.Int64Value(1),
		InitramfsID:    types.Int64Value(2),
		Image:          types.StringValue("alpine:3.20"),
		ExecutablePath: types.StringValue("/bin/app"),
		Append:         types.StringValue("console=hvc0"),
		Env:            testList(t, ctx, envType, []lightVMEnvModel{{Name: types.StringValue("A"), Value: types.StringValue("b")}}),
		ExecutableArgs: testList(t, ctx, types.StringType, []string{"serve"}),
		InterfaceIDs:   testList(t, ctx, types.Int64Type, []int64{3, 4}),
		DiskIDs:        testList(t, ctx, types.Int64Type, []int64{5}),
		VirtioFSShares: testList(t, ctx, shareType, []lightVMVirtioFSShareModel{{HostPath: types.StringValue("/tmp"), Tag: types.StringValue("data"), ReadOnly: types.BoolValue(true)}}),
		Consoles:       testList(t, ctx, consoleType, []lightVMConsoleModel{{Name: types.StringValue("serial")}}),
	}
	create, diags := lightVMCreateRequest(ctx, model)
	if diags.HasError() {
		t.Fatalf("create request: %v", diags)
	}
	if create.GetName() != "app" || create.GetVcpus() != 2 || create.GetConsoleEnabled() != true ||
		len(create.GetEnv()) != 1 || len(create.GetConsoles()) != 1 || len(create.GetVirtiofsShares()) != 1 ||
		create.GetVirtiofsShares()[0].GetReadOnly() != true || len(create.GetInterfaceIds()) != 2 ||
		len(create.GetDiskIds()) != 1 || create.GetAppend() != "console=hvc0" {
		t.Fatalf("create request = %+v", create)
	}

	model.ID = types.StringValue("123e4567-e89b-12d3-a456-426614174000")
	update, diags := lightVMUpdateRequest(ctx, model)
	if diags.HasError() {
		t.Fatalf("update request: %v", diags)
	}
	if update.GetId() == "" || !update.GetReplaceDiskIds() || update.GetAppend() != "console=hvc0" {
		t.Fatalf("update request = %+v", update)
	}
}

func TestSetLightVMState(t *testing.T) {
	ctx := context.Background()
	vm := &rookeryv1.LightVM{
		Id: "123e4567-e89b-12d3-a456-426614174000", Name: "app", Description: "test",
		Vcpus: 2, MemoryMb: 512, ConsoleEnabled: true, CgroupClass: "prod/web",
		KernelId: 1, KernelName: "vmlinuz", InitramfsId: 2, InitramfsName: "initramfs",
		Image: "alpine", ExecutablePath: "/bin/app", Append: "console=hvc0",
		Env:            []*rookeryv1.LightVMEnvVar{{Name: "A", Value: "b"}},
		ExecutableArgs: []string{"serve"},
		Interfaces:     []*rookeryv1.VMInterface{{Id: 3}},
		DiskIds:        []int64{4},
		VirtiofsShares: []*rookeryv1.LightVMVirtioFSShare{{Id: 5, HostPath: "/tmp", Tag: "data", ReadOnly: boolPtr(true), Position: 0}},
		Consoles:       []*rookeryv1.LightVMConsole{{Id: 6, Name: "serial", PortName: "org.example.console"}},
		Status:         "stopped", DesiredState: "stopped",
	}
	var state lightVMModel
	if diags := setLightVMState(ctx, &state, vm); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if state.Name.ValueString() != "app" || state.KernelID.ValueInt64() != 1 || state.KernelName.ValueString() != "vmlinuz" {
		t.Fatalf("state = %+v", state)
	}
	if len(state.Env.Elements()) != 1 || len(state.Consoles.Elements()) != 1 || len(state.VirtioFSShares.Elements()) != 1 ||
		len(state.InterfaceIDs.Elements()) != 1 || len(state.DiskIDs.Elements()) != 1 || len(state.ExecutableArgs.Elements()) != 1 {
		t.Fatalf("nested state = %+v", state)
	}
	if !state.RunState.IsNull() || !state.ConsoleSocket.IsNull() {
		t.Fatalf("optional state = %+v", state)
	}
}

func TestArtifactStateAndRecords(t *testing.T) {
	kernel := &rookeryv1.LightVMKernel{Id: 1, Name: "kernel", Description: "desc", Path: "/tmp/kernel", FileName: "kernel", SizeBytes: 10, Managed: true}
	kernelRecord := artifactRecordFromKernel(kernel)
	image := &rookeryv1.LightVMInitramfs{Id: 2, Name: "initramfs", Path: "/tmp/initramfs", FileName: "initramfs", SizeBytes: 20}
	imageRecord := artifactRecordFromInitramfs(image)
	var state lightVMArtifactModel
	setLightVMArtifactState(&state, kernelRecord)
	if state.ID.ValueInt64() != 1 || !state.Managed.ValueBool() || state.SizeBytes.ValueInt64() != 10 {
		t.Fatalf("kernel state = %+v", state)
	}
	setLightVMArtifactState(&state, imageRecord)
	if state.ID.ValueInt64() != 2 || state.Description.ValueString() != "" || state.Managed.ValueBool() {
		t.Fatalf("initramfs state = %+v", state)
	}
}

func boolPtr(value bool) *bool { return &value }
