package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func cgroupIOLimitSchemaAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"device":     schema.StringAttribute{Required: true, Description: "Block device path, for example /dev/mapper/vg-lv."},
		"read_bps":   schema.Int64Attribute{Optional: true, Computed: true, Description: "Read bandwidth limit in bytes per second; 0 means unlimited."},
		"write_bps":  schema.Int64Attribute{Optional: true, Computed: true, Description: "Write bandwidth limit in bytes per second; 0 means unlimited."},
		"read_iops":  schema.Int64Attribute{Optional: true, Computed: true, Description: "Read IOPS limit; 0 means unlimited."},
		"write_iops": schema.Int64Attribute{Optional: true, Computed: true, Description: "Write IOPS limit; 0 means unlimited."},
		"weight":     schema.Int64Attribute{Optional: true, Computed: true, Description: "io.weight for this device (1-10000); 0 inherits."},
	}
}

func cgroupIOLimitDataSourceAttributes() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"device":     dschema.StringAttribute{Computed: true},
		"read_bps":   dschema.Int64Attribute{Computed: true},
		"write_bps":  dschema.Int64Attribute{Computed: true},
		"read_iops":  dschema.Int64Attribute{Computed: true},
		"write_iops": dschema.Int64Attribute{Computed: true},
		"weight":     dschema.Int64Attribute{Computed: true},
	}
}

type cgroupIOLimitModel struct {
	Device    types.String `tfsdk:"device"`
	ReadBPS   types.Int64  `tfsdk:"read_bps"`
	WriteBPS  types.Int64  `tfsdk:"write_bps"`
	ReadIOPS  types.Int64  `tfsdk:"read_iops"`
	WriteIOPS types.Int64  `tfsdk:"write_iops"`
	Weight    types.Int64  `tfsdk:"weight"`
}

var cgroupIOLimitAttrTypes = map[string]attr.Type{
	"device":     types.StringType,
	"read_bps":   types.Int64Type,
	"write_bps":  types.Int64Type,
	"read_iops":  types.Int64Type,
	"write_iops": types.Int64Type,
	"weight":     types.Int64Type,
}

var cgroupIOLimitObjectType = types.ObjectType{AttrTypes: cgroupIOLimitAttrTypes}

func cgroupIOLimitsFromList(ctx context.Context, list types.List) ([]cgroupIOLimitModel, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var limits []cgroupIOLimitModel
	diags := list.ElementsAs(ctx, &limits, false)
	return limits, diags
}

func cgroupIOLimitsToProto(limits []cgroupIOLimitModel) []*rookeryv1.CgroupIOLimit {
	if len(limits) == 0 {
		return nil
	}
	result := make([]*rookeryv1.CgroupIOLimit, 0, len(limits))
	for _, limit := range limits {
		result = append(result, &rookeryv1.CgroupIOLimit{
			Device:    limit.Device.ValueString(),
			ReadBps:   limit.ReadBPS.ValueInt64(),
			WriteBps:  limit.WriteBPS.ValueInt64(),
			ReadIops:  limit.ReadIOPS.ValueInt64(),
			WriteIops: limit.WriteIOPS.ValueInt64(),
			Weight:    int32(limit.Weight.ValueInt64()),
		})
	}
	return result
}

func cgroupIOLimitsFromProto(ctx context.Context, limits []*rookeryv1.CgroupIOLimit) (types.List, diag.Diagnostics) {
	models := make([]cgroupIOLimitModel, 0, len(limits))
	for _, limit := range limits {
		if limit == nil {
			continue
		}
		models = append(models, cgroupIOLimitModel{
			Device:    types.StringValue(limit.GetDevice()),
			ReadBPS:   types.Int64Value(limit.GetReadBps()),
			WriteBPS:  types.Int64Value(limit.GetWriteBps()),
			ReadIOPS:  types.Int64Value(limit.GetReadIops()),
			WriteIOPS: types.Int64Value(limit.GetWriteIops()),
			Weight:    types.Int64Value(int64(limit.GetWeight())),
		})
	}
	return types.ListValueFrom(ctx, cgroupIOLimitObjectType, models)
}

type vmCgroupModel struct {
	Enabled        types.Bool   `tfsdk:"enabled"`
	CPUs           types.String `tfsdk:"cpus"`
	MemoryMaxBytes types.Int64  `tfsdk:"memory_max_bytes"`
	CPUWeight      types.Int64  `tfsdk:"cpu_weight"`
	IOLimits       types.List   `tfsdk:"io_limits"`
}

var vmCgroupAttrTypes = map[string]attr.Type{
	"enabled":          types.BoolType,
	"cpus":             types.StringType,
	"memory_max_bytes": types.Int64Type,
	"cpu_weight":       types.Int64Type,
	"io_limits":        types.ListType{ElemType: cgroupIOLimitObjectType},
}

func vmCgroupFromObject(ctx context.Context, object types.Object) (*rookeryv1.VMCgroup, diag.Diagnostics) {
	if object.IsNull() || object.IsUnknown() {
		return nil, nil
	}
	var model vmCgroupModel
	diags := object.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, diags
	}
	limits, limitDiags := cgroupIOLimitsFromList(ctx, model.IOLimits)
	diags.Append(limitDiags...)
	if diags.HasError() {
		return nil, diags
	}
	return &rookeryv1.VMCgroup{
		Enabled:        model.Enabled.ValueBool(),
		Cpus:           model.CPUs.ValueString(),
		MemoryMaxBytes: model.MemoryMaxBytes.ValueInt64(),
		CpuWeight:      int32(model.CPUWeight.ValueInt64()),
		IoLimits:       cgroupIOLimitsToProto(limits),
	}, diags
}

func vmCgroupToObject(ctx context.Context, cgroup *rookeryv1.VMCgroup) (types.Object, diag.Diagnostics) {
	if cgroup == nil {
		return types.ObjectNull(vmCgroupAttrTypes), nil
	}
	limits, diags := cgroupIOLimitsFromProto(ctx, cgroup.GetIoLimits())
	if diags.HasError() {
		return types.ObjectNull(vmCgroupAttrTypes), diags
	}
	return types.ObjectValueFrom(ctx, vmCgroupAttrTypes, vmCgroupModel{
		Enabled:        types.BoolValue(cgroup.GetEnabled()),
		CPUs:           types.StringValue(cgroup.GetCpus()),
		MemoryMaxBytes: types.Int64Value(cgroup.GetMemoryMaxBytes()),
		CPUWeight:      types.Int64Value(int64(cgroup.GetCpuWeight())),
		IOLimits:       limits,
	})
}
