package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func TestCgroupSchemasAreValid(t *testing.T) {
	ctx := context.Background()

	res := NewCgroupSettingsResource()
	var resourceResp resource.SchemaResponse
	res.Schema(ctx, resource.SchemaRequest{}, &resourceResp)
	if diags := resourceResp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("cgroup settings resource schema: %v", diags)
	}

	var vmResourceResp resource.SchemaResponse
	NewVMResource().Schema(ctx, resource.SchemaRequest{}, &vmResourceResp)
	if diags := vmResourceResp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("VM resource schema: %v", diags)
	}

	ds := NewCgroupSettingsDataSource()
	var dsResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &dsResp)
	if diags := dsResp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("cgroup settings data source schema: %v", diags)
	}

	var vmDSResp datasource.SchemaResponse
	NewVMDataSource().Schema(ctx, datasource.SchemaRequest{}, &vmDSResp)
	if diags := vmDSResp.Schema.ValidateImplementation(ctx); diags.HasError() {
		t.Fatalf("VM data source schema: %v", diags)
	}
}

func TestCgroupClassRoundTrip(t *testing.T) {
	ctx := context.Background()
	limits, diags := types.ListValueFrom(ctx, cgroupIOLimitObjectType, []cgroupIOLimitModel{{
		Device:  types.StringValue("/dev/mapper/vg0-vm-root"),
		ReadBPS: types.Int64Value(1048576), WriteIOPS: types.Int64Value(200), Weight: types.Int64Value(500),
	}})
	if diags.HasError() {
		t.Fatalf("build limits: %v", diags)
	}
	list, diags := types.ListValueFrom(ctx, cgroupClassObjectType, []cgroupClassModel{{
		Name: types.StringValue("prod/web"), CPUs: types.StringValue("0-3"),
		MemoryMaxBytes: types.Int64Value(2 << 30), CPUWeight: types.Int64Value(500), IOLimits: limits,
	}})
	if diags.HasError() {
		t.Fatalf("build classes: %v", diags)
	}
	classes, diags := cgroupClassesFromList(ctx, list)
	if diags.HasError() {
		t.Fatalf("read classes: %v", diags)
	}
	protos, diags := cgroupClassesToProto(ctx, classes)
	if diags.HasError() {
		t.Fatalf("convert to proto: %v", diags)
	}
	if len(protos) != 1 || protos[0].GetName() != "prod/web" || protos[0].GetCpus() != "0-3" ||
		protos[0].GetMemoryMaxBytes() != 2<<30 || len(protos[0].GetIoLimits()) != 1 {
		t.Fatalf("classes = %+v", protos)
	}

	roundTrip, diags := cgroupClassesFromProto(ctx, protos)
	if diags.HasError() {
		t.Fatalf("convert from proto: %v", diags)
	}
	var models []cgroupClassModel
	if diags := roundTrip.ElementsAs(ctx, &models, false); diags.HasError() {
		t.Fatalf("read round trip: %v", diags)
	}
	if len(models) != 1 || models[0].Name.ValueString() != "prod/web" || models[0].MemoryMaxBytes.ValueInt64() != 2<<30 {
		t.Fatalf("round trip models = %+v", models)
	}
}

func TestSetCgroupSettingsState(t *testing.T) {
	var state cgroupSettingsModel
	settings := &rookeryv1.CgroupSettings{
		Enabled: true, Cpus: "0-3", MemoryMaxBytes: 1 << 30, CpuWeight: 500,
		Available: true, Configured: true, Root: "/sys/fs/cgroup/rookery",
		OnlineCpus: []int32{0, 1, 2, 3},
		IoLimits:   []*rookeryv1.CgroupIOLimit{{Device: "/dev/sda", WriteBps: 1024, Weight: 200}},
		Classes:    []*rookeryv1.CgroupClass{{Name: "prod/web", Cpus: "0-1"}},
	}
	if diags := setCgroupSettingsState(context.Background(), &state, settings); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	if state.ID.ValueString() != cgroupSettingsID || !state.Enabled.ValueBool() || state.CPUs.ValueString() != "0-3" ||
		state.MemoryMaxBytes.ValueInt64() != 1<<30 || state.IOLimits.IsNull() || len(state.IOLimits.Elements()) != 1 ||
		state.Classes.IsNull() || len(state.Classes.Elements()) != 1 {
		t.Fatalf("state = %+v", state)
	}
}

func TestValidateCgroupClassTree(t *testing.T) {
	ctx := context.Background()
	empty, diags := types.ListValueFrom(ctx, cgroupIOLimitObjectType, []cgroupIOLimitModel{})
	if diags.HasError() {
		t.Fatal(diags)
	}
	class := func(name string) cgroupClassModel {
		return cgroupClassModel{
			Name: types.StringValue(name), CPUs: types.StringValue(""),
			MemoryMaxBytes: types.Int64Value(0), CPUWeight: types.Int64Value(0), IOLimits: empty,
		}
	}
	valid := []cgroupClassModel{class("prod"), class("prod/web"), class("prod/web/frontend")}
	if err := validateCgroupClassModels(ctx, valid); err != nil {
		t.Fatalf("valid tree: %v", err)
	}
	if err := validateCgroupClassModels(ctx, []cgroupClassModel{class("prod/web")}); err == nil {
		t.Fatal("class without parent was accepted")
	}
	if err := validateCgroupClassModels(ctx, []cgroupClassModel{class("a/b/c/d")}); err == nil {
		t.Fatal("class deeper than three levels was accepted")
	}
	if err := validateCgroupClassModels(ctx, []cgroupClassModel{class("prod"), class("prod")}); err == nil {
		t.Fatal("duplicate class was accepted")
	}
}
