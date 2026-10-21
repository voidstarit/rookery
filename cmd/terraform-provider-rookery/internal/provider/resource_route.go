package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
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
	_ resource.Resource                = (*routeResource)(nil)
	_ resource.ResourceWithConfigure   = (*routeResource)(nil)
	_ resource.ResourceWithImportState = (*routeResource)(nil)
)

type routeResource struct{ configuredResource }

// NewRouteResource creates the rookery_route resource.
func NewRouteResource() resource.Resource {
	return &routeResource{}
}

type routeModel struct {
	ID              types.Int64  `tfsdk:"id"`
	RouteTableID    types.Int64  `tfsdk:"route_table_id"`
	TableID         types.Int64  `tfsdk:"table_id"`
	IPv6            types.Bool   `tfsdk:"ipv6"`
	Prefix          types.String `tfsdk:"prefix"`
	NextHop         types.String `tfsdk:"next_hop"`
	InterfaceID     types.Int64  `tfsdk:"interface_id"`
	InterfaceName   types.String `tfsdk:"interface_name"`
	EgressInterface types.String `tfsdk:"egress_interface"`
	Preference      types.Int64  `tfsdk:"preference"`
}

func (r *routeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_route"
}

func (r *routeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A static route in a Rookery route table. The next hop is optional when an egress interface is set.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"route_table_id": schema.Int64Attribute{
				Required:      true,
				Description:   "Rookery route table record id (the id attribute of rookery_route_table), or 0 for the default table.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"table_id": schema.Int64Attribute{Computed: true, Description: "VPP table id."},
			"ipv6": schema.BoolAttribute{
				Required:      true,
				Description:   "Route address family.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"prefix": schema.StringAttribute{
				Required:      true,
				Description:   "Destination prefix.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"next_hop": schema.StringAttribute{
				Optional:      true,
				Description:   "Next-hop address. Omit for an attached/egress-only route.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"interface_id": schema.Int64Attribute{
				Optional:      true,
				Description:   "Optional Rookery interface id used as the egress.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"interface_name": schema.StringAttribute{Computed: true},
			"egress_interface": schema.StringAttribute{
				Optional:      true,
				Description:   "Optional arbitrary VPP interface name used as the egress.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"preference": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Path preference metric.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
		},
	}
}

func (r *routeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan routeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create route", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	route, err := client.RouteTables.CreateRoute(callCtx, &rookeryv1.CreateRouteRequest{
		RouteTableId:    plan.RouteTableID.ValueInt64(),
		Ipv6:            plan.IPv6.ValueBool(),
		Prefix:          plan.Prefix.ValueString(),
		NextHop:         plan.NextHop.ValueString(),
		InterfaceId:     int64Pointer(plan.InterfaceID),
		EgressInterface: plan.EgressInterface.ValueString(),
		Preference:      uint32(plan.Preference.ValueInt64()),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create route", err.Error())
		return
	}
	setRouteState(&plan, route)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *routeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state routeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read route", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.RouteTables.ListRoutes(callCtx, &rookeryv1.ListRoutesRequest{
		RouteTableId: state.RouteTableID.ValueInt64(),
		Ipv6:         state.IPv6.ValueBool(),
	})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read route", err.Error())
		return
	}
	for _, route := range response.GetRoutes() {
		if route.GetId() == state.ID.ValueInt64() {
			setRouteState(&state, route)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *routeResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update route", "routes are immutable; replace the resource instead")
}

func (r *routeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state routeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete route", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.RouteTables.DeleteRoute(callCtx, &rookeryv1.DeleteRouteRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete route", err.Error())
	}
}

// Import uses the composite id "<route_table_id>/<ipv6>/<route_id>".
func (r *routeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 3 {
		resp.Diagnostics.AddError("Import route", "import id must be <route_table_id>/<ipv6>/<route_id>")
		return
	}
	tableID, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || tableID < 0 {
		resp.Diagnostics.AddError("Import route", fmt.Sprintf("invalid route table id %q", parts[0]))
		return
	}
	ipv6, err := strconv.ParseBool(strings.TrimSpace(parts[1]))
	if err != nil {
		resp.Diagnostics.AddError("Import route", fmt.Sprintf("invalid ipv6 value %q", parts[1]))
		return
	}
	routeID, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
	if err != nil || routeID <= 0 {
		resp.Diagnostics.AddError("Import route", fmt.Sprintf("invalid route id %q", parts[2]))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), types.Int64Value(routeID))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("route_table_id"), types.Int64Value(tableID))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("ipv6"), types.BoolValue(ipv6))...)
}

func setRouteState(state *routeModel, route *rookeryv1.Route) {
	state.ID = types.Int64Value(route.GetId())
	state.RouteTableID = types.Int64Value(route.GetRouteTableId())
	state.TableID = types.Int64Value(int64(route.GetTableId()))
	state.IPv6 = types.BoolValue(route.GetIpv6())
	state.Prefix = types.StringValue(route.GetPrefix())
	state.NextHop = optionalStringValue(route.GetNextHop())
	state.InterfaceID = optionalInt64Value(route.InterfaceId)
	state.InterfaceName = optionalStringValue(route.GetInterfaceName())
	state.EgressInterface = optionalStringValue(route.GetEgressInterface())
	state.Preference = types.Int64Value(int64(route.GetPreference()))
}
