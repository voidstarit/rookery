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
	_ resource.Resource                = (*dhcpOptionResource)(nil)
	_ resource.ResourceWithConfigure   = (*dhcpOptionResource)(nil)
	_ resource.ResourceWithImportState = (*dhcpOptionResource)(nil)
)

type dhcpOptionResource struct{ configuredResource }

// NewDHCPOptionResource creates the rookery_dhcp_option resource.
func NewDHCPOptionResource() resource.Resource {
	return &dhcpOptionResource{}
}

type dhcpOptionModel struct {
	ID     types.Int64  `tfsdk:"id"`
	VNFID  types.Int64  `tfsdk:"vnf_id"`
	Code   types.Int64  `tfsdk:"code"`
	Format types.String `tfsdk:"format"`
	Value  types.String `tfsdk:"value"`
}

// Metadata sets the resource type name to the provider name followed by
// "_dhcp_option".
func (r *dhcpOptionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dhcp_option"
}

// Schema declares one extra DHCP option. The option value is sensitive because
// it can carry credentials.
func (r *dhcpOptionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An extra DHCPv4 or DHCPv6 option owned by a rookery_dhcp_vnf.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"vnf_id": schema.Int64Attribute{
				Required:      true,
				Description:   "DHCP VNF id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"code":   schema.Int64Attribute{Required: true, Description: "Option code (1-254 for DHCPv4, 1-65535 for DHCPv6)."},
			"format": schema.StringAttribute{Optional: true, Computed: true, Description: "Value encoding: string, ipv4, ipv6, hex, uint8, uint16, uint32, or domain-list. Defaults to string."},
			"value": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				Description: "Option value encoded according to format.",
			},
		},
	}
}

// Create adds the option to its DHCP VNF.
func (r *dhcpOptionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dhcpOptionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create DHCP option", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	option, err := client.DHCPVNFs.CreateDHCPOption(callCtx, &rookeryv1.CreateDHCPOptionRequest{
		VnfId:  plan.VNFID.ValueInt64(),
		Code:   uint32Value(plan.Code),
		Format: stringValue(plan.Format),
		Value:  stringValue(plan.Value),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create DHCP option", err.Error())
		return
	}
	setDHCPOptionState(&plan, option)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the option, removing it from state when either the option or
// its parent is gone.
func (r *dhcpOptionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dhcpOptionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read DHCP option", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	option, err := client.DHCPVNFs.GetDHCPOption(callCtx, &rookeryv1.GetDHCPOptionRequest{
		VnfId:    state.VNFID.ValueInt64(),
		OptionId: state.ID.ValueInt64(),
	})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read DHCP option", err.Error())
		return
	}
	setDHCPOptionState(&state, option)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the option code, format, or value.
func (r *dhcpOptionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dhcpOptionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update DHCP option", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	option, err := client.DHCPVNFs.UpdateDHCPOption(callCtx, &rookeryv1.UpdateDHCPOptionRequest{
		VnfId:    plan.VNFID.ValueInt64(),
		OptionId: plan.ID.ValueInt64(),
		Code:     uint32Value(plan.Code),
		Format:   stringValue(plan.Format),
		Value:    stringValue(plan.Value),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update DHCP option", err.Error())
		return
	}
	setDHCPOptionState(&plan, option)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the option, treating an already-absent option as success.
func (r *dhcpOptionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dhcpOptionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete DHCP option", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.DHCPVNFs.DeleteDHCPOption(callCtx, &rookeryv1.DeleteDHCPOptionRequest{
		VnfId:    state.VNFID.ValueInt64(),
		OptionId: state.ID.ValueInt64(),
	}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete DHCP option", err.Error())
	}
}

// ImportState adopts an existing option by <vnf_id>:<option_id>.
func (r *dhcpOptionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importChildIDs(ctx, req, resp)
}

// setDHCPOptionState copies the server's option into state.
func setDHCPOptionState(state *dhcpOptionModel, option *rookeryv1.DHCPOption) {
	state.ID = types.Int64Value(option.GetId())
	state.VNFID = types.Int64Value(option.GetVnfId())
	state.Code = types.Int64Value(int64(option.GetCode()))
	state.Format = types.StringValue(option.GetFormat())
	state.Value = types.StringValue(option.GetValue())
}
