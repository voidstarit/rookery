package provider

import (
	"context"
	"fmt"

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
	_ resource.Resource                = (*routeTableResource)(nil)
	_ resource.ResourceWithConfigure   = (*routeTableResource)(nil)
	_ resource.ResourceWithImportState = (*routeTableResource)(nil)
)

type routeTableResource struct{ configuredResource }

// NewRouteTableResource creates the rookery_route_table resource.
func NewRouteTableResource() resource.Resource {
	return &routeTableResource{}
}

type routeTableModel struct {
	ID             types.Int64  `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	TableID        types.Int64  `tfsdk:"table_id"`
	IPv6           types.Bool   `tfsdk:"ipv6"`
	IsDefault      types.Bool   `tfsdk:"is_default"`
	InterfaceCount types.Int64  `tfsdk:"interface_count"`
	RouteCount     types.Int64  `tfsdk:"route_count"`
	NetworkID      types.Int64  `tfsdk:"network_id"`
	Managed        types.Bool   `tfsdk:"managed"`
	Labels         types.Set    `tfsdk:"labels"`
}

// Metadata registers the resource under the provider type name with the _route_table suffix.
func (r *routeTableResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_route_table"
}

// Schema declares name, table_id, and ipv6 as replace-only inputs and exposes the default and
// managed flags plus interface/route counts as computed attributes.
func (r *routeTableResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A custom VPP route table (VRF). Table 0 is implicit and cannot be created.",
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id":     schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Route table name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"table_id": schema.Int64Attribute{
				Required:      true,
				Description:   "VPP table id. IDs 100000 and above are reserved for managed networks.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"ipv6": schema.BoolAttribute{
				Required:      true,
				Description:   "Use the IPv6 address family.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"is_default":      schema.BoolAttribute{Computed: true},
			"interface_count": schema.Int64Attribute{Computed: true},
			"route_count":     schema.Int64Attribute{Computed: true},
			"network_id":      schema.Int64Attribute{Computed: true},
			"managed":         schema.BoolAttribute{Computed: true},
		},
	}
}

// Create creates the VPP route table for the planned name, table id, and address family, then
// applies labels and stores the server response in state.
func (r *routeTableResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan routeTableModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create route table", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	table, err := client.RouteTables.CreateRouteTable(callCtx, &rookeryv1.CreateRouteTableRequest{
		Name:    plan.Name.ValueString(),
		TableId: uint32(plan.TableID.ValueInt64()),
		Ipv6:    plan.IPv6.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create route table", err.Error())
		return
	}
	setRouteTableState(&plan, table)
	entityLabels, err := applyEntityLabels(callCtx, client, "route_table", fmt.Sprintf("%d", table.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create route table labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read lists route tables, refreshes state and labels for the matching id, and removes the
// resource from state when no table matches.
func (r *routeTableResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state routeTableModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read route table", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.RouteTables.ListRouteTables(callCtx, &rookeryv1.ListRouteTablesRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Read route table", err.Error())
		return
	}
	for _, table := range response.GetRouteTables() {
		if table.GetId() == state.ID.ValueInt64() {
			setRouteTableState(&state, table)
			entityLabels, err := readEntityLabels(callCtx, client, "route_table", fmt.Sprintf("%d", state.ID.ValueInt64()))
			if err != nil {
				resp.Diagnostics.AddError("Read route table labels", err.Error())
				return
			}
			state.Labels = entityLabels
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

// Update applies only the planned labels, since the table's identifying attributes are
// replace-only, and writes the resulting label set to state.
func (r *routeTableResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan routeTableModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update route table labels", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	entityLabels, err := applyEntityLabels(callCtx, client, "route_table", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update route table labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the route table, treating a not-found response as success so destroys are
// idempotent.
func (r *routeTableResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state routeTableModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete route table", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.RouteTables.DeleteRouteTable(callCtx, &rookeryv1.DeleteRouteTableRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete route table", err.Error())
	}
}

// ImportState adopts an existing route table by its decimal id through the shared int64 helper.
func (r *routeTableResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

// setRouteTableState copies the table's name, VPP table id, address family, and computed
// flags/counters into the model.
func setRouteTableState(state *routeTableModel, table *rookeryv1.RouteTable) {
	state.ID = types.Int64Value(table.GetId())
	state.Name = types.StringValue(table.GetName())
	state.TableID = types.Int64Value(int64(table.GetTableId()))
	state.IPv6 = types.BoolValue(table.GetIpv6())
	state.IsDefault = types.BoolValue(table.GetIsDefault())
	state.InterfaceCount = types.Int64Value(table.GetInterfaceCount())
	state.RouteCount = types.Int64Value(table.GetRouteCount())
	state.NetworkID = types.Int64Value(table.GetNetworkId())
	state.Managed = types.BoolValue(table.GetManaged())
}
