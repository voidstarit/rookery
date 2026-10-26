package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*startupScriptResource)(nil)
	_ resource.ResourceWithConfigure   = (*startupScriptResource)(nil)
	_ resource.ResourceWithImportState = (*startupScriptResource)(nil)
)

type startupScriptResource struct{ configuredResource }

// NewStartupScriptResource creates the rookery_startup_script resource.
func NewStartupScriptResource() resource.Resource {
	return &startupScriptResource{}
}

type startupScriptModel struct {
	Kind      types.String `tfsdk:"kind"`
	Enabled   types.Bool   `tfsdk:"enabled"`
	Script    types.String `tfsdk:"script"`
	Status    types.String `tfsdk:"status"`
	ExitCode  types.Int64  `tfsdk:"exit_code"`
	Output    types.String `tfsdk:"output"`
	RanAt     types.String `tfsdk:"ran_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
}

func (r *startupScriptResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_startup_script"
}

func (r *startupScriptResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a server-level Rookery startup script. Set kind to rookery for the once-per-API-process script or vpp for the VPP reconnect script. Only one resource per kind should exist.",
		Attributes: map[string]schema.Attribute{
			"kind": schema.StringAttribute{
				Required:      true,
				Description:   "Script kind: rookery or vpp.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"enabled":    schema.BoolAttribute{Optional: true, Computed: true},
			"script":     schema.StringAttribute{Optional: true, Computed: true, Description: "Script passed to /bin/sh -c."},
			"status":     schema.StringAttribute{Computed: true},
			"exit_code":  schema.Int64Attribute{Computed: true},
			"output":     schema.StringAttribute{Computed: true},
			"ran_at":     schema.StringAttribute{Computed: true},
			"updated_at": schema.StringAttribute{Computed: true},
		},
	}
}

func (r *startupScriptResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan startupScriptModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Configure startup script", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	script, err := updateStartupScript(callCtx, client, plan.Kind.ValueString(), plan.Enabled.ValueBool(), plan.Script.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Configure startup script", err.Error())
		return
	}
	setStartupScriptState(&plan, script)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *startupScriptResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state startupScriptModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read startup script", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	script, err := getStartupScript(callCtx, client, state.Kind.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Read startup script", err.Error())
		return
	}
	setStartupScriptState(&state, script)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *startupScriptResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan startupScriptModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update startup script", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	script, err := updateStartupScript(callCtx, client, plan.Kind.ValueString(), plan.Enabled.ValueBool(), plan.Script.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Update startup script", err.Error())
		return
	}
	setStartupScriptState(&plan, script)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *startupScriptResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state startupScriptModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Disable startup script", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := updateStartupScript(callCtx, client, state.Kind.ValueString(), false, state.Script.ValueString()); err != nil {
		resp.Diagnostics.AddError("Disable startup script", err.Error())
	}
}

func (r *startupScriptResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("kind"), types.StringValue(req.ID))...)
}

func updateStartupScript(ctx context.Context, client *Client, kind string, enabled bool, script string) (*rookeryv1.StartupScript, error) {
	switch kind {
	case "rookery":
		return client.System.UpdateStartupScript(ctx, &rookeryv1.UpdateStartupScriptRequest{Enabled: enabled, Script: script})
	case "vpp":
		return client.System.UpdateVPPStartupScript(ctx, &rookeryv1.UpdateVPPStartupScriptRequest{Enabled: enabled, Script: script})
	default:
		return nil, fmt.Errorf("kind must be rookery or vpp, got %q", kind)
	}
}

func getStartupScript(ctx context.Context, client *Client, kind string) (*rookeryv1.StartupScript, error) {
	switch kind {
	case "rookery":
		return client.System.GetStartupScript(ctx, &rookeryv1.GetStartupScriptRequest{})
	case "vpp":
		return client.System.GetVPPStartupScript(ctx, &rookeryv1.GetVPPStartupScriptRequest{})
	default:
		return nil, fmt.Errorf("kind must be rookery or vpp, got %q", kind)
	}
}

func setStartupScriptState(state *startupScriptModel, script *rookeryv1.StartupScript) {
	state.Kind = types.StringValue(state.Kind.ValueString())
	state.Enabled = types.BoolValue(script.GetEnabled())
	state.Script = types.StringValue(script.GetScript())
	state.Status = types.StringValue(script.GetStatus())
	state.ExitCode = types.Int64Value(int64(script.GetExitCode()))
	state.Output = types.StringValue(script.GetOutput())
	state.RanAt = timestampString(script.GetRanAt())
	state.UpdatedAt = timestampString(script.GetUpdatedAt())
}
