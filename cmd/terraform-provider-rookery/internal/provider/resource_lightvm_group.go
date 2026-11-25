package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*lightVMGroupResource)(nil)
	_ resource.ResourceWithConfigure   = (*lightVMGroupResource)(nil)
	_ resource.ResourceWithImportState = (*lightVMGroupResource)(nil)
)

type lightVMGroupResource struct{ configuredResource }

func NewLightVMGroupResource() resource.Resource {
	return &lightVMGroupResource{}
}

type lightVMGroupModel struct {
	ID                types.Int64  `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	TemplateVersionID types.Int64  `tfsdk:"template_version_id"`
	DesiredCount      types.Int64  `tfsdk:"desired_count"`
	Paused            types.Bool   `tfsdk:"paused"`
	WaitForReady      types.Bool   `tfsdk:"wait_for_ready"`
	Timeout           types.String `tfsdk:"timeout"`
	ForceDestroy      types.Bool   `tfsdk:"force_destroy"`
	DestroyTimeout    types.String `tfsdk:"destroy_timeout"`
	ActualCount       types.Int64  `tfsdk:"actual_count"`
	RunningCount      types.Int64  `tfsdk:"running_count"`
	ProvisioningCount types.Int64  `tfsdk:"provisioning_count"`
	RemovingCount     types.Int64  `tfsdk:"removing_count"`
	Drift             types.Int64  `tfsdk:"drift"`
	Converged         types.Bool   `tfsdk:"converged"`
	State             types.String `tfsdk:"state"`
	LastError         types.String `tfsdk:"last_error"`
	LastReconcileAt   types.String `tfsdk:"last_reconcile_at"`
}

func (r *lightVMGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lightvm_group"
}

func (r *lightVMGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A managed LightVM group pinned to one template version. Member creation is asynchronous.",
		Attributes: map[string]schema.Attribute{
			"id":                  schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name":                schema.StringAttribute{Required: true, Description: "Group name."},
			"description":         schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"template_version_id": schema.Int64Attribute{Required: true, Description: "Pinned template version for new members."},
			"desired_count":       schema.Int64Attribute{Required: true, Description: "Desired member count (0..1024)."},
			"paused":              schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}, Description: "Suppress creates and restarts. Cleanup and scale-in continue."},
			"wait_for_ready":      schema.BoolAttribute{Optional: true, Description: "Wait for converged and running_count == desired_count after create/update."},
			"timeout":             schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Readiness wait timeout, for example 30m. Defaults to 2h."},
			"force_destroy":       schema.BoolAttribute{Optional: true, Description: "Pass force=true to group deletion and wait for the group to disappear."},
			"destroy_timeout":     schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Deletion wait timeout. Defaults to 6h."},
			"actual_count":        schema.Int64Attribute{Computed: true},
			"running_count":       schema.Int64Attribute{Computed: true},
			"provisioning_count":  schema.Int64Attribute{Computed: true},
			"removing_count":      schema.Int64Attribute{Computed: true},
			"drift":               schema.Int64Attribute{Computed: true},
			"converged":           schema.BoolAttribute{Computed: true},
			"state":               schema.StringAttribute{Computed: true},
			"last_error":          schema.StringAttribute{Computed: true},
			"last_reconcile_at":   schema.StringAttribute{Computed: true},
		},
	}
}

func (r *lightVMGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan lightVMGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create LightVM group", err.Error())
		return
	}
	paused := false
	if !plan.Paused.IsNull() && !plan.Paused.IsUnknown() {
		paused = plan.Paused.ValueBool()
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	group, err := client.LightVMGroups.CreateLightVMGroup(callCtx, &rookeryv1.CreateLightVMGroupRequest{
		Name: plan.Name.ValueString(), Description: plan.Description.ValueString(),
		TemplateVersionId: plan.TemplateVersionID.ValueInt64(),
		DesiredCount:      uint32(plan.DesiredCount.ValueInt64()), Paused: &paused,
	})
	if err != nil {
		resp.Diagnostics.AddError("Create LightVM group", err.Error())
		return
	}
	resp.Diagnostics.Append(setLightVMGroupState(&plan, group)...)
	if plan.Timeout.IsNull() || plan.Timeout.IsUnknown() {
		plan.Timeout = types.StringValue("2h")
	}
	if plan.DestroyTimeout.IsNull() || plan.DestroyTimeout.IsUnknown() {
		plan.DestroyTimeout = types.StringValue("6h")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.WaitForReady.ValueBool() {
		if err := waitForLightVMGroup(ctx, client, group.GetId(), uint32(plan.DesiredCount.ValueInt64()), durationOr(plan.Timeout, 2*time.Hour)); err != nil {
			resp.Diagnostics.AddError("Wait for LightVM group readiness", err.Error())
		}
	}
}

func (r *lightVMGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state lightVMGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read LightVM group", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	group, err := client.LightVMGroups.GetLightVMGroup(callCtx, &rookeryv1.GetLightVMGroupRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read LightVM group", err.Error())
		return
	}
	resp.Diagnostics.Append(setLightVMGroupState(&state, group.GetGroup())...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *lightVMGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan lightVMGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update LightVM group", err.Error())
		return
	}
	request := &rookeryv1.UpdateLightVMGroupRequest{Id: plan.ID.ValueInt64()}
	if !plan.Name.IsNull() {
		request.Name = stringPointer(plan.Name)
	}
	if !plan.Description.IsNull() {
		request.Description = stringPointer(plan.Description)
	}
	if !plan.TemplateVersionID.IsNull() {
		request.TemplateVersionId = int64Pointer(plan.TemplateVersionID)
	}
	if !plan.DesiredCount.IsNull() {
		count := uint32(plan.DesiredCount.ValueInt64())
		request.DesiredCount = &count
	}
	if !plan.Paused.IsNull() && !plan.Paused.IsUnknown() {
		paused := plan.Paused.ValueBool()
		request.Paused = &paused
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	group, err := client.LightVMGroups.UpdateLightVMGroup(callCtx, request)
	if err != nil {
		resp.Diagnostics.AddError("Update LightVM group", err.Error())
		return
	}
	resp.Diagnostics.Append(setLightVMGroupState(&plan, group)...)
	if plan.Timeout.IsNull() || plan.Timeout.IsUnknown() {
		plan.Timeout = types.StringValue("2h")
	}
	if plan.DestroyTimeout.IsNull() || plan.DestroyTimeout.IsUnknown() {
		plan.DestroyTimeout = types.StringValue("6h")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.WaitForReady.ValueBool() {
		if err := waitForLightVMGroup(ctx, client, group.GetId(), uint32(plan.DesiredCount.ValueInt64()), durationOr(plan.Timeout, 2*time.Hour)); err != nil {
			resp.Diagnostics.AddError("Wait for LightVM group readiness", err.Error())
		}
	}
}

func (r *lightVMGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state lightVMGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete LightVM group", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	force := state.ForceDestroy.ValueBool()
	_, err = client.LightVMGroups.DeleteLightVMGroup(callCtx, &rookeryv1.DeleteLightVMGroupRequest{
		Id: state.ID.ValueInt64(), Force: force,
	})
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete LightVM group", err.Error())
		return
	}
	timeout := durationOr(state.DestroyTimeout, 6*time.Hour)
	waitCtx, waitCancel := context.WithTimeout(ctx, timeout)
	defer waitCancel()
	if err := waitForLightVMGroupDeleted(waitCtx, client, state.ID.ValueInt64()); err != nil {
		resp.Diagnostics.AddError("Wait for LightVM group deletion", err.Error())
	}
}

func (r *lightVMGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func setLightVMGroupState(model *lightVMGroupModel, group *rookeryv1.LightVMGroup) diag.Diagnostics {
	model.ID = types.Int64Value(group.GetId())
	model.Name = types.StringValue(group.GetName())
	model.Description = types.StringValue(group.GetDescription())
	model.TemplateVersionID = types.Int64Value(group.GetTemplateVersionId())
	model.DesiredCount = types.Int64Value(int64(group.GetDesiredCount()))
	model.Paused = types.BoolValue(group.GetPaused())
	model.ActualCount = types.Int64Value(int64(group.GetActualCount()))
	model.RunningCount = types.Int64Value(int64(group.GetRunningCount()))
	model.ProvisioningCount = types.Int64Value(int64(group.GetProvisioningCount()))
	model.RemovingCount = types.Int64Value(int64(group.GetRemovingCount()))
	model.Drift = types.Int64Value(int64(group.GetDrift()))
	model.Converged = types.BoolValue(group.GetConverged())
	model.State = types.StringValue(group.GetState())
	model.LastError = types.StringValue(group.GetLastError())
	model.LastReconcileAt = timestampString(group.GetLastReconcileAt())
	return nil
}

func waitForLightVMGroup(ctx context.Context, client *Client, id int64, desired uint32, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		callCtx, callCancel := context.WithTimeout(waitCtx, client.Timeout())
		response, err := client.LightVMGroups.GetLightVMGroup(callCtx, &rookeryv1.GetLightVMGroupRequest{Id: id})
		callCancel()
		if err != nil {
			if isNotFound(err) {
				return fmt.Errorf("group disappeared while waiting for readiness")
			}
			return err
		}
		group := response.GetGroup()
		if group.GetConverged() && group.GetRunningCount() == desired {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("group did not become ready within %s", timeout)
		case <-ticker.C:
		}
	}
}

func waitForLightVMGroupDeleted(ctx context.Context, client *Client, id int64) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
		_, err := client.LightVMGroups.GetLightVMGroup(callCtx, &rookeryv1.GetLightVMGroupRequest{Id: id})
		cancel()
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func durationOr(value types.String, fallback time.Duration) time.Duration {
	if value.IsNull() || value.IsUnknown() || value.ValueString() == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value.ValueString())
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
