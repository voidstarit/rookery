package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*nat44DNATRuleResource)(nil)
	_ resource.ResourceWithConfigure   = (*nat44DNATRuleResource)(nil)
	_ resource.ResourceWithImportState = (*nat44DNATRuleResource)(nil)
)

type nat44DNATRuleResource struct{ configuredResource }

// NewNAT44DNATRuleResource creates the rookery_nat44_dnat_rule resource.
func NewNAT44DNATRuleResource() resource.Resource {
	return &nat44DNATRuleResource{}
}

type nat44DNATRuleModel struct {
	ID           types.Int64  `tfsdk:"id"`
	VNFID        types.Int64  `tfsdk:"vnf_id"`
	Protocol     types.String `tfsdk:"protocol"`
	ExternalPort types.Int64  `tfsdk:"external_port"`
	LocalIP      types.String `tfsdk:"local_ip"`
	LocalPort    types.Int64  `tfsdk:"local_port"`
}

// Metadata sets the resource type name to the provider name followed by
// "_nat44_dnat_rule".
func (r *nat44DNATRuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_nat44_dnat_rule"
}

// Schema declares one NAT44 destination-NAT rule.
func (r *nat44DNATRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A DNAT rule owned by a rookery_nat44_vnf.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"vnf_id": schema.Int64Attribute{
				Required:      true,
				Description:   "NAT44 VNF id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"protocol":      schema.StringAttribute{Required: true, Description: "tcp or udp."},
			"external_port": schema.Int64Attribute{Required: true, Description: "External port to forward."},
			"local_ip":      schema.StringAttribute{Required: true, Description: "Local IPv4 address inside the NAT44 subnet."},
			"local_port":    schema.Int64Attribute{Required: true, Description: "Local port to forward to."},
		},
	}
}

// Create adds the DNAT rule to its NAT44 VNF.
func (r *nat44DNATRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan nat44DNATRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create NAT44 DNAT rule", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	rule, err := client.NAT44VNFs.CreateNAT44DNATRule(callCtx, &rookeryv1.CreateNAT44DNATRuleRequest{
		VnfId:        plan.VNFID.ValueInt64(),
		Protocol:     plan.Protocol.ValueString(),
		ExternalPort: int32Value(plan.ExternalPort),
		LocalIp:      plan.LocalIP.ValueString(),
		LocalPort:    int32Value(plan.LocalPort),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create NAT44 DNAT rule", err.Error())
		return
	}
	setNAT44DNATRuleState(&plan, rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the DNAT rule, removing it from state when either the rule or
// its parent is gone.
func (r *nat44DNATRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state nat44DNATRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read NAT44 DNAT rule", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	rule, err := client.NAT44VNFs.GetNAT44DNATRule(callCtx, &rookeryv1.GetNAT44DNATRuleRequest{
		VnfId:  state.VNFID.ValueInt64(),
		RuleId: state.ID.ValueInt64(),
	})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read NAT44 DNAT rule", err.Error())
		return
	}
	setNAT44DNATRuleState(&state, rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the DNAT protocol, ports, or local address.
func (r *nat44DNATRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan nat44DNATRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update NAT44 DNAT rule", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	rule, err := client.NAT44VNFs.UpdateNAT44DNATRule(callCtx, &rookeryv1.UpdateNAT44DNATRuleRequest{
		VnfId:        plan.VNFID.ValueInt64(),
		RuleId:       plan.ID.ValueInt64(),
		Protocol:     plan.Protocol.ValueString(),
		ExternalPort: int32Value(plan.ExternalPort),
		LocalIp:      plan.LocalIP.ValueString(),
		LocalPort:    int32Value(plan.LocalPort),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update NAT44 DNAT rule", err.Error())
		return
	}
	setNAT44DNATRuleState(&plan, rule)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the DNAT rule, treating an already-absent rule as success.
func (r *nat44DNATRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state nat44DNATRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete NAT44 DNAT rule", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.NAT44VNFs.DeleteNAT44DNATRule(callCtx, &rookeryv1.DeleteNAT44DNATRuleRequest{
		VnfId:  state.VNFID.ValueInt64(),
		RuleId: state.ID.ValueInt64(),
	}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete NAT44 DNAT rule", err.Error())
	}
}

// ImportState adopts an existing DNAT rule by <vnf_id>:<rule_id>.
func (r *nat44DNATRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importChildIDs(ctx, req, resp)
}

// setNAT44DNATRuleState copies the server's DNAT rule into state.
func setNAT44DNATRuleState(state *nat44DNATRuleModel, rule *rookeryv1.DNATRule) {
	state.ID = types.Int64Value(rule.GetId())
	state.VNFID = types.Int64Value(rule.GetVnfId())
	state.Protocol = types.StringValue(rule.GetProtocol())
	state.ExternalPort = types.Int64Value(int64(rule.GetExternalPort()))
	state.LocalIP = types.StringValue(rule.GetLocalIp())
	state.LocalPort = types.Int64Value(int64(rule.GetLocalPort()))
}
