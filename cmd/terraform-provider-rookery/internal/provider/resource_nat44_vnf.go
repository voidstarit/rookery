package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*nat44VNFResource)(nil)
	_ resource.ResourceWithConfigure   = (*nat44VNFResource)(nil)
	_ resource.ResourceWithImportState = (*nat44VNFResource)(nil)
)

type nat44VNFResource struct{ configuredResource }

// NewNAT44VNFResource creates the rookery_nat44_vnf resource.
func NewNAT44VNFResource() resource.Resource {
	return &nat44VNFResource{}
}

type nat44VNFModel struct {
	ID               types.Int64  `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	NetworkID        types.Int64  `tfsdk:"network_id"`
	NetworkName      types.String `tfsdk:"network_name"`
	Enabled          types.Bool   `tfsdk:"enabled"`
	Gateway          types.String `tfsdk:"gateway"`
	OutsideInterface types.String `tfsdk:"outside_interface"`
	OutsideAddress   types.String `tfsdk:"outside_address"`
	OutsideGateway   types.String `tfsdk:"outside_gateway"`
	NAT44Pool        types.String `tfsdk:"nat44_pool"`
	DNATRuleCount    types.Int64  `tfsdk:"dnat_rule_count"`
	Status           types.String `tfsdk:"status"`
	StatusDetail     types.String `tfsdk:"status_detail"`
	TapSWIfIndex     types.Int64  `tfsdk:"tap_sw_if_index"`
	TapHostName      types.String `tfsdk:"tap_host_name"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
	Labels           types.Set    `tfsdk:"labels"`
}

// Metadata sets the resource type name to the provider name followed by
// "_nat44_vnf".
func (r *nat44VNFResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_nat44_vnf"
}

// Schema declares the NAT44 parent attributes. DNAT rules are a separate child
// resource.
func (r *nat44VNFResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A NAT44 VNF attached to a Rookery network. DNAT rules are a separate resource.",
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id":     schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name":   schema.StringAttribute{Required: true, Description: "VNF name."},
			"network_id": schema.Int64Attribute{
				Required:      true,
				Description:   "Network id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"network_name":      schema.StringAttribute{Computed: true},
			"enabled":           schema.BoolAttribute{Optional: true, Computed: true, Description: "Whether the VNF is active. Defaults to true."},
			"gateway":           schema.StringAttribute{Required: true, Description: "Gateway address as an IPv4 CIDR, /30 or smaller."},
			"outside_interface": schema.StringAttribute{Required: true, Description: "Outside interface used for NAT44."},
			"outside_address":   schema.StringAttribute{Optional: true, Computed: true, Description: "Optional outside address as an IPv4 CIDR."},
			"outside_gateway":   schema.StringAttribute{Optional: true, Computed: true, Description: "Optional outside gateway inside the outside subnet."},
			"nat44_pool":        schema.StringAttribute{Optional: true, Computed: true, Description: "Optional NAT44 pool: a single IPv4 address or start-end range."},
			"dnat_rule_count":   schema.Int64Attribute{Computed: true},
			"status":            schema.StringAttribute{Computed: true},
			"status_detail":     schema.StringAttribute{Computed: true},
			"tap_sw_if_index":   schema.Int64Attribute{Computed: true},
			"tap_host_name":     schema.StringAttribute{Computed: true},
			"created_at":        schema.StringAttribute{Computed: true},
			"updated_at":        schema.StringAttribute{Computed: true},
		},
	}
}

// Create adds a typed NAT44 VNF, applies labels, and stores the server record.
func (r *nat44VNFResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan nat44VNFModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create NAT44 VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.NAT44VNFs.CreateNAT44VNF(callCtx, &rookeryv1.CreateNAT44VNFRequest{
		Name:             plan.Name.ValueString(),
		NetworkId:        plan.NetworkID.ValueInt64(),
		Enabled:          boolPointer(plan.Enabled),
		Gateway:          stringValue(plan.Gateway),
		OutsideInterface: stringValue(plan.OutsideInterface),
		OutsideAddress:   stringValue(plan.OutsideAddress),
		OutsideGateway:   stringValue(plan.OutsideGateway),
		Nat44Pool:        stringValue(plan.NAT44Pool),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create NAT44 VNF", err.Error())
		return
	}
	setNAT44VNFState(&plan, vnf)
	entityLabels, err := applyEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", vnf.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create NAT44 VNF labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the NAT44 VNF and its labels, removing it from state when the
// server reports it is gone.
func (r *nat44VNFResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state nat44VNFModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read NAT44 VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.NAT44VNFs.GetNAT44VNF(callCtx, &rookeryv1.GetNAT44VNFRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read NAT44 VNF", err.Error())
		return
	}
	setNAT44VNFState(&state, vnf)
	entityLabels, err := readEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Read NAT44 VNF labels", err.Error())
		return
	}
	state.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the NAT44 VNF scalar configuration and reapplies labels.
func (r *nat44VNFResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan nat44VNFModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update NAT44 VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.NAT44VNFs.UpdateNAT44VNF(callCtx, &rookeryv1.UpdateNAT44VNFRequest{
		Id:               plan.ID.ValueInt64(),
		Name:             stringPointer(plan.Name),
		Enabled:          boolPointer(plan.Enabled),
		Gateway:          stringPointer(plan.Gateway),
		OutsideInterface: stringPointer(plan.OutsideInterface),
		OutsideAddress:   stringPointer(plan.OutsideAddress),
		OutsideGateway:   stringPointer(plan.OutsideGateway),
		Nat44Pool:        stringPointer(plan.NAT44Pool),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update NAT44 VNF", err.Error())
		return
	}
	setNAT44VNFState(&plan, vnf)
	entityLabels, err := applyEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update NAT44 VNF labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the NAT44 VNF, treating an already-absent VNF as success.
func (r *nat44VNFResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state nat44VNFModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete NAT44 VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.NAT44VNFs.DeleteNAT44VNF(callCtx, &rookeryv1.DeleteNAT44VNFRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete NAT44 VNF", err.Error())
	}
}

// ImportState adopts an existing NAT44 VNF by its integer id.
func (r *nat44VNFResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

// setNAT44VNFState copies the server's NAT44 VNF into state.
func setNAT44VNFState(state *nat44VNFModel, vnf *rookeryv1.NAT44VNF) {
	state.ID = types.Int64Value(vnf.GetId())
	state.Name = types.StringValue(vnf.GetName())
	state.NetworkID = types.Int64Value(vnf.GetNetworkId())
	state.NetworkName = types.StringValue(vnf.GetNetworkName())
	state.Enabled = types.BoolValue(vnf.GetEnabled())
	state.Gateway = types.StringValue(vnf.GetGateway())
	state.OutsideInterface = types.StringValue(vnf.GetOutsideInterface())
	state.OutsideAddress = types.StringValue(vnf.GetOutsideAddress())
	state.OutsideGateway = types.StringValue(vnf.GetOutsideGateway())
	state.NAT44Pool = types.StringValue(vnf.GetNat44Pool())
	state.DNATRuleCount = int64Count(vnf.GetDnatRuleCount())
	state.Status = types.StringValue(vnf.GetStatus())
	state.StatusDetail = types.StringValue(vnf.GetStatusDetail())
	state.TapSWIfIndex = types.Int64Value(int64(vnf.GetTapSwIfIndex()))
	state.TapHostName = types.StringValue(vnf.GetTapHostName())
	state.CreatedAt = timestampString(vnf.GetCreatedAt())
	state.UpdatedAt = timestampString(vnf.GetUpdatedAt())
}
