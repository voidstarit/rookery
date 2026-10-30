package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/cgroup"
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

type cgroupClassModel struct {
	Name           types.String `tfsdk:"name"`
	CPUs           types.String `tfsdk:"cpus"`
	MemoryMaxBytes types.Int64  `tfsdk:"memory_max_bytes"`
	CPUWeight      types.Int64  `tfsdk:"cpu_weight"`
	IOLimits       types.List   `tfsdk:"io_limits"`
}

var cgroupClassAttrTypes = map[string]attr.Type{
	"name":             types.StringType,
	"cpus":             types.StringType,
	"memory_max_bytes": types.Int64Type,
	"cpu_weight":       types.Int64Type,
	"io_limits":        types.ListType{ElemType: cgroupIOLimitObjectType},
}

var cgroupClassObjectType = types.ObjectType{AttrTypes: cgroupClassAttrTypes}

func cgroupClassSchemaAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"name":             schema.StringAttribute{Required: true, Description: "Slash-separated class path, for example prod or prod/web."},
		"cpus":             schema.StringAttribute{Optional: true, Computed: true, Description: "Class cpuset.cpus; empty inherits the host or parent class."},
		"memory_max_bytes": schema.Int64Attribute{Optional: true, Computed: true, Description: "Class memory.max in bytes; 0 inherits the host or parent class."},
		"cpu_weight":       schema.Int64Attribute{Optional: true, Computed: true, Description: "Class cpu.weight (1-10000); 0 inherits."},
		"io_limits": schema.ListNestedAttribute{
			Optional: true, Computed: true,
			Description:  "Class per-block-device I/O limits; omitted values inherit.",
			NestedObject: schema.NestedAttributeObject{Attributes: cgroupIOLimitSchemaAttributes()},
		},
	}
}

func cgroupClassDataSourceAttributes() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"name":             dschema.StringAttribute{Computed: true},
		"cpus":             dschema.StringAttribute{Computed: true},
		"memory_max_bytes": dschema.Int64Attribute{Computed: true},
		"cpu_weight":       dschema.Int64Attribute{Computed: true},
		"io_limits": dschema.ListNestedAttribute{
			Computed:     true,
			NestedObject: dschema.NestedAttributeObject{Attributes: cgroupIOLimitDataSourceAttributes()},
		},
	}
}

func cgroupClassesFromList(ctx context.Context, list types.List) ([]cgroupClassModel, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var classes []cgroupClassModel
	diags := list.ElementsAs(ctx, &classes, false)
	return classes, diags
}

func cgroupClassesToProto(ctx context.Context, classes []cgroupClassModel) ([]*rookeryv1.CgroupClass, diag.Diagnostics) {
	if len(classes) == 0 {
		return nil, nil
	}
	result := make([]*rookeryv1.CgroupClass, 0, len(classes))
	var diags diag.Diagnostics
	for _, class := range classes {
		limits, limitDiags := cgroupIOLimitsFromList(ctx, class.IOLimits)
		diags.Append(limitDiags...)
		if diags.HasError() {
			return nil, diags
		}
		result = append(result, &rookeryv1.CgroupClass{
			Name:           class.Name.ValueString(),
			Cpus:           class.CPUs.ValueString(),
			MemoryMaxBytes: class.MemoryMaxBytes.ValueInt64(),
			CpuWeight:      int32(class.CPUWeight.ValueInt64()),
			IoLimits:       cgroupIOLimitsToProto(limits),
		})
	}
	return result, diags
}

func cgroupClassesFromProto(ctx context.Context, classes []*rookeryv1.CgroupClass) (types.List, diag.Diagnostics) {
	models := make([]cgroupClassModel, 0, len(classes))
	for _, class := range classes {
		if class == nil {
			continue
		}
		limits, diags := cgroupIOLimitsFromProto(ctx, class.GetIoLimits())
		if diags.HasError() {
			return types.ListNull(cgroupClassObjectType), diags
		}
		models = append(models, cgroupClassModel{
			Name:           types.StringValue(class.GetName()),
			CPUs:           types.StringValue(class.GetCpus()),
			MemoryMaxBytes: types.Int64Value(class.GetMemoryMaxBytes()),
			CPUWeight:      types.Int64Value(int64(class.GetCpuWeight())),
			IOLimits:       limits,
		})
	}
	return types.ListValueFrom(ctx, cgroupClassObjectType, models)
}

// validateCgroupClassModels mirrors the server's class tree validation so
// Terraform reports configuration errors before an API call.
func validateCgroupClassModels(ctx context.Context, classes []cgroupClassModel) error {
	if len(classes) > 64 {
		return fmt.Errorf("at most 64 cgroup classes are allowed")
	}
	seen := make(map[string]struct{}, len(classes))
	for _, class := range classes {
		name := strings.TrimSpace(class.Name.ValueString())
		if err := cgroup.ValidateClassName(name); err != nil {
			return err
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate cgroup class %q", name)
		}
		seen[name] = struct{}{}
		if class.MemoryMaxBytes.ValueInt64() < 0 {
			return fmt.Errorf("cgroup class %s memory_max_bytes must not be negative", name)
		}
		if class.CPUWeight.ValueInt64() < 0 || class.CPUWeight.ValueInt64() > 10000 {
			return fmt.Errorf("cgroup class %s cpu_weight must be between 1 and 10000 (0 inherits)", name)
		}
		if cpus := strings.TrimSpace(class.CPUs.ValueString()); cpus != "" {
			if _, err := cgroup.CanonicalCPUSet(cpus); err != nil {
				return fmt.Errorf("cgroup class %s: %w", name, err)
			}
		}
		limits, diags := cgroupIOLimitsFromList(ctx, class.IOLimits)
		if diags.HasError() {
			return fmt.Errorf("cgroup class %s has invalid I/O limits", name)
		}
		devices := make(map[string]struct{}, len(limits))
		for _, limit := range limits {
			device := strings.TrimSpace(limit.Device.ValueString())
			if device == "" {
				return fmt.Errorf("cgroup class %s has an I/O limit without a device", name)
			}
			if _, ok := devices[device]; ok {
				return fmt.Errorf("cgroup class %s has duplicate I/O device %q", name, device)
			}
			devices[device] = struct{}{}
			if limit.ReadBPS.ValueInt64() < 0 || limit.WriteBPS.ValueInt64() < 0 ||
				limit.ReadIOPS.ValueInt64() < 0 || limit.WriteIOPS.ValueInt64() < 0 ||
				limit.Weight.ValueInt64() < 0 || limit.Weight.ValueInt64() > 10000 {
				return fmt.Errorf("cgroup class %s has invalid I/O limits for %s", name, device)
			}
		}
	}
	for name := range seen {
		index := strings.LastIndex(name, "/")
		if index <= 0 {
			continue
		}
		parent := name[:index]
		if _, ok := seen[parent]; !ok {
			return fmt.Errorf("cgroup class %s requires parent class %q to be defined", name, parent)
		}
	}
	return nil
}

func validateVMCgroupClass(ctx context.Context, client *Client, className string) error {
	className = strings.TrimSpace(className)
	if className == "" {
		return nil
	}
	settings, err := client.System.GetCgroupSettings(ctx, &rookeryv1.GetCgroupSettingsRequest{})
	if err != nil {
		return fmt.Errorf("read cgroup settings: %w", err)
	}
	found := false
	for _, class := range settings.GetClasses() {
		if strings.TrimSpace(class.GetName()) == className {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("cgroup class %q not found", className)
	}
	prefix := className + "/"
	for _, class := range settings.GetClasses() {
		if strings.HasPrefix(strings.TrimSpace(class.GetName()), prefix) {
			return fmt.Errorf("cgroup class %q has child classes; assign the VM to a leaf class", className)
		}
	}
	return nil
}
