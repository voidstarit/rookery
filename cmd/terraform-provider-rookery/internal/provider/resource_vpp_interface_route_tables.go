package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*vppInterfaceRouteTablesResource)(nil)
	_ resource.ResourceWithConfigure   = (*vppInterfaceRouteTablesResource)(nil)
	_ resource.ResourceWithImportState = (*vppInterfaceRouteTablesResource)(nil)
)

type vppInterfaceRouteTablesResource struct{ configuredResource }

// NewVPPInterfaceRouteTablesResource creates the
// rookery_vpp_interface_route_tables resource.
func NewVPPInterfaceRouteTablesResource() resource.Resource {
	return &vppInterfaceRouteTablesResource{}
}

type vppInterfaceRouteTablesModel struct {
	Name          types.String `tfsdk:"name"`
	IPv4TableID   types.Int64  `tfsdk:"ipv4_table_id"`
	IPv6TableID   types.Int64  `tfsdk:"ipv6_table_id"`
	IPv4TableName types.String `tfsdk:"ipv4_table_name"`
	IPv6TableName types.String `tfsdk:"ipv6_table_name"`
}

func (r *vppInterfaceRouteTablesResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpp_interface_route_tables"
}

func (r *vppInterfaceRouteTablesResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Binds a Rookery host interface, host tap, or bridge-domain pipe endpoint to IPv4/IPv6 route tables. Destroying this resource removes the override and restores the automatic default (the network VRF for host taps). Vhost-user interfaces use rookery_interface.ipv4_table_id/ipv6_table_id instead.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "VPP interface name, for example host-enp1s0, hostnet1, or pipe0.0.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ipv4_table_id": schema.Int64Attribute{
				Required:    true,
				Description: "IPv4 VPP table id. Use 0 for the default table.",
			},
			"ipv6_table_id": schema.Int64Attribute{
				Required:    true,
				Description: "IPv6 VPP table id. Use 0 for the default table.",
			},
			"ipv4_table_name": schema.StringAttribute{Computed: true},
			"ipv6_table_name": schema.StringAttribute{Computed: true},
		},
	}
}

func (r *vppInterfaceRouteTablesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vppInterfaceRouteTablesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Configure interface route tables", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.Interfaces.UpdateVPPInterfaceRouteTables(callCtx, &rookeryv1.UpdateVPPInterfaceRouteTablesRequest{
		Name:        plan.Name.ValueString(),
		Ipv4TableId: uint32(plan.IPv4TableID.ValueInt64()),
		Ipv6TableId: uint32(plan.IPv6TableID.ValueInt64()),
	})
	if err != nil {
		resp.Diagnostics.AddError("Configure interface route tables", err.Error())
		return
	}
	setVPPInterfaceRouteTablesState(&plan, response)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vppInterfaceRouteTablesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vppInterfaceRouteTablesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read interface route tables", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.Interfaces.GetVPPInterfaceRouteTables(callCtx, &rookeryv1.GetVPPInterfaceRouteTablesRequest{Name: state.Name.ValueString()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read interface route tables", err.Error())
		return
	}
	setVPPInterfaceRouteTablesState(&state, response)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vppInterfaceRouteTablesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan vppInterfaceRouteTablesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update interface route tables", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.Interfaces.UpdateVPPInterfaceRouteTables(callCtx, &rookeryv1.UpdateVPPInterfaceRouteTablesRequest{
		Name:        plan.Name.ValueString(),
		Ipv4TableId: uint32(plan.IPv4TableID.ValueInt64()),
		Ipv6TableId: uint32(plan.IPv6TableID.ValueInt64()),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update interface route tables", err.Error())
		return
	}
	setVPPInterfaceRouteTablesState(&plan, response)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vppInterfaceRouteTablesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vppInterfaceRouteTablesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete interface route tables", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.Interfaces.DeleteVPPInterfaceRouteTables(callCtx, &rookeryv1.DeleteVPPInterfaceRouteTablesRequest{Name: state.Name.ValueString()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete interface route tables", err.Error())
	}
}

func (r *vppInterfaceRouteTablesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), types.StringValue(req.ID))...)
}

func setVPPInterfaceRouteTablesState(state *vppInterfaceRouteTablesModel, tables *rookeryv1.VPPInterfaceRouteTables) {
	state.Name = types.StringValue(tables.GetName())
	state.IPv4TableID = types.Int64Value(int64(tables.GetIpv4TableId()))
	state.IPv6TableID = types.Int64Value(int64(tables.GetIpv6TableId()))
	state.IPv4TableName = types.StringValue(tables.GetIpv4TableName())
	state.IPv6TableName = types.StringValue(tables.GetIpv6TableName())
}
