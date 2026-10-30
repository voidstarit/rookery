package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

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

func TestVMCgroupObjectRoundTrip(t *testing.T) {
	ctx := context.Background()
	limits, diags := types.ListValueFrom(ctx, cgroupIOLimitObjectType, []cgroupIOLimitModel{{
		Device:  types.StringValue("/dev/mapper/vg0-vm-root"),
		ReadBPS: types.Int64Value(1048576), WriteIOPS: types.Int64Value(200), Weight: types.Int64Value(500),
	}})
	if diags.HasError() {
		t.Fatalf("build limits: %v", diags)
	}
	object, diags := types.ObjectValueFrom(ctx, vmCgroupAttrTypes, vmCgroupModel{
		Enabled: types.BoolValue(true), CPUs: types.StringValue("0-3"),
		MemoryMaxBytes: types.Int64Value(2 << 30), CPUWeight: types.Int64Value(500), IOLimits: limits,
	})
	if diags.HasError() {
		t.Fatalf("build cgroup: %v", diags)
	}
	cgroup, diags := vmCgroupFromObject(ctx, object)
	if diags.HasError() {
		t.Fatalf("convert to proto: %v", diags)
	}
	if !cgroup.GetEnabled() || cgroup.GetCpus() != "0-3" || cgroup.GetMemoryMaxBytes() != 2<<30 ||
		cgroup.GetCpuWeight() != 500 || len(cgroup.GetIoLimits()) != 1 || cgroup.GetIoLimits()[0].GetDevice() != "/dev/mapper/vg0-vm-root" {
		t.Fatalf("cgroup = %+v", cgroup)
	}

	roundTrip, diags := vmCgroupToObject(ctx, cgroup)
	if diags.HasError() {
		t.Fatalf("convert from proto: %v", diags)
	}
	var model vmCgroupModel
	if diags := roundTrip.As(ctx, &model, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("read round trip: %v", diags)
	}
	if !model.Enabled.ValueBool() || model.CPUs.ValueString() != "0-3" || model.MemoryMaxBytes.ValueInt64() != 2<<30 {
		t.Fatalf("round trip model = %+v", model)
	}
}

func TestSetCgroupSettingsState(t *testing.T) {
	var state cgroupSettingsModel
	settings := &rookeryv1.CgroupSettings{
		Enabled: true, Cpus: "0-3", MemoryMaxBytes: 1 << 30, CpuWeight: 500,
		Available: true, Configured: true, Root: "/sys/fs/cgroup/rookery",
		OnlineCpus: []int32{0, 1, 2, 3},
		IoLimits:   []*rookeryv1.CgroupIOLimit{{Device: "/dev/sda", WriteBps: 1024, Weight: 200}},
	}
	if diags := setCgroupSettingsState(context.Background(), &state, settings); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	if state.ID.ValueString() != cgroupSettingsID || !state.Enabled.ValueBool() || state.CPUs.ValueString() != "0-3" ||
		state.MemoryMaxBytes.ValueInt64() != 1<<30 || state.IOLimits.IsNull() || len(state.IOLimits.Elements()) != 1 {
		t.Fatalf("state = %+v", state)
	}
}
