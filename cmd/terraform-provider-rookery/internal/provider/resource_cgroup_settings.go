package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

const cgroupSettingsID = "cgroup"

var (
	_ resource.Resource                = (*cgroupSettingsResource)(nil)
	_ resource.ResourceWithConfigure   = (*cgroupSettingsResource)(nil)
	_ resource.ResourceWithImportState = (*cgroupSettingsResource)(nil)
)

type cgroupSettingsResource struct{ configuredResource }

// NewCgroupSettingsResource creates the rookery_cgroup_settings resource.
func NewCgroupSettingsResource() resource.Resource {
	return &cgroupSettingsResource{}
}

type cgroupSettingsModel struct {
	ID             types.String `tfsdk:"id"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	CPUs           types.String `tfsdk:"cpus"`
	Mems           types.String `tfsdk:"mems"`
	MemoryMaxBytes types.Int64  `tfsdk:"memory_max_bytes"`
	CPUWeight      types.Int64  `tfsdk:"cpu_weight"`
	IOLimits       types.List   `tfsdk:"io_limits"`
	Available      types.Bool   `tfsdk:"available"`
	Configured     types.Bool   `tfsdk:"configured"`
	Reason         types.String `tfsdk:"reason"`
	Mount          types.String `tfsdk:"mount"`
	Root           types.String `tfsdk:"root"`
	OnlineCPUs     types.List   `tfsdk:"online_cpus"`
	CPUModel       types.String `tfsdk:"cpu_model"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

func (r *cgroupSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cgroup_settings"
}

func (r *cgroupSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the singleton host-wide cgroup v2 resource limits applied to QEMU. Only one resource should exist.",
		Attributes: map[string]schema.Attribute{
			"id":      schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"enabled": schema.BoolAttribute{Required: true, Description: "Enable cgroup v2 limits and place QEMU processes under the Rookery hierarchy."},
			"cpus":    schema.StringAttribute{Optional: true, Computed: true, Description: "Host cpuset.cpus value, for example 0-3,7. Empty means all online CPUs."},
			"mems":    schema.StringAttribute{Optional: true, Computed: true, Description: "Host cpuset.mems NUMA node list. Empty means all online nodes."},
			"memory_max_bytes": schema.Int64Attribute{
				Optional: true, Computed: true,
				Description: "Host memory.max value in bytes; 0 means unlimited.",
			},
			"cpu_weight": schema.Int64Attribute{
				Optional: true, Computed: true,
				Description: "Host cpu.weight (1-10000); 0 uses the kernel default.",
			},
			"io_limits": schema.ListNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Host per-block-device io.max/io.weight limits.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: cgroupIOLimitSchemaAttributes(),
				},
			},
			"available":   schema.BoolAttribute{Computed: true, Description: "Whether cgroup v2 is mounted."},
			"configured":  schema.BoolAttribute{Computed: true, Description: "Whether the host cgroup hierarchy is currently applied."},
			"reason":      schema.StringAttribute{Computed: true},
			"mount":       schema.StringAttribute{Computed: true},
			"root":        schema.StringAttribute{Computed: true},
			"online_cpus": schema.ListAttribute{Computed: true, ElementType: types.Int64Type},
			"cpu_model":   schema.StringAttribute{Computed: true},
			"updated_at":  schema.StringAttribute{Computed: true},
		},
	}
}

func (r *cgroupSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan cgroupSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Configure cgroup settings", err.Error())
		return
	}
	limits, diags := cgroupIOLimitsFromList(ctx, plan.IOLimits)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	settings, err := client.System.UpdateCgroupSettings(callCtx, &rookeryv1.UpdateCgroupSettingsRequest{
		Enabled:        plan.Enabled.ValueBool(),
		Cpus:           plan.CPUs.ValueString(),
		Mems:           plan.Mems.ValueString(),
		MemoryMaxBytes: plan.MemoryMaxBytes.ValueInt64(),
		CpuWeight:      int32(plan.CPUWeight.ValueInt64()),
		IoLimits:       cgroupIOLimitsToProto(limits),
	})
	if err != nil {
		resp.Diagnostics.AddError("Configure cgroup settings", err.Error())
		return
	}
	resp.Diagnostics.Append(setCgroupSettingsState(ctx, &plan, settings)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *cgroupSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state cgroupSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read cgroup settings", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	settings, err := client.System.GetCgroupSettings(callCtx, &rookeryv1.GetCgroupSettingsRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Read cgroup settings", err.Error())
		return
	}
	resp.Diagnostics.Append(setCgroupSettingsState(ctx, &state, settings)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *cgroupSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan cgroupSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update cgroup settings", err.Error())
		return
	}
	limits, diags := cgroupIOLimitsFromList(ctx, plan.IOLimits)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	settings, err := client.System.UpdateCgroupSettings(callCtx, &rookeryv1.UpdateCgroupSettingsRequest{
		Enabled:        plan.Enabled.ValueBool(),
		Cpus:           plan.CPUs.ValueString(),
		Mems:           plan.Mems.ValueString(),
		MemoryMaxBytes: plan.MemoryMaxBytes.ValueInt64(),
		CpuWeight:      int32(plan.CPUWeight.ValueInt64()),
		IoLimits:       cgroupIOLimitsToProto(limits),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update cgroup settings", err.Error())
		return
	}
	resp.Diagnostics.Append(setCgroupSettingsState(ctx, &plan, settings)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *cgroupSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state cgroupSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Disable cgroup settings", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.System.UpdateCgroupSettings(callCtx, &rookeryv1.UpdateCgroupSettingsRequest{Enabled: false}); err != nil {
		resp.Diagnostics.AddError("Disable cgroup settings", err.Error())
	}
}

func (r *cgroupSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.StringValue(cgroupSettingsID))...)
}

func setCgroupSettingsState(ctx context.Context, state *cgroupSettingsModel, settings *rookeryv1.CgroupSettings) diag.Diagnostics {
	var diags diag.Diagnostics
	state.ID = types.StringValue(cgroupSettingsID)
	state.Enabled = types.BoolValue(settings.GetEnabled())
	state.CPUs = types.StringValue(settings.GetCpus())
	state.Mems = types.StringValue(settings.GetMems())
	state.MemoryMaxBytes = types.Int64Value(settings.GetMemoryMaxBytes())
	state.CPUWeight = types.Int64Value(int64(settings.GetCpuWeight()))
	limits, limitDiags := cgroupIOLimitsFromProto(ctx, settings.GetIoLimits())
	diags.Append(limitDiags...)
	state.IOLimits = limits
	state.Available = types.BoolValue(settings.GetAvailable())
	state.Configured = types.BoolValue(settings.GetConfigured())
	state.Reason = optionalStringValue(settings.GetReason())
	state.Mount = optionalStringValue(settings.GetMount())
	state.Root = optionalStringValue(settings.GetRoot())
	onlineCPUs := make([]int64, 0, len(settings.GetOnlineCpus()))
	for _, cpu := range settings.GetOnlineCpus() {
		onlineCPUs = append(onlineCPUs, int64(cpu))
	}
	onlineList, onlineDiags := int64List(ctx, onlineCPUs)
	diags.Append(onlineDiags...)
	state.OnlineCPUs = onlineList
	state.CPUModel = optionalStringValue(settings.GetCpuModel())
	state.UpdatedAt = timestampString(settings.GetUpdatedAt())
	return diags
}
