package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
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
	ID                  types.Int64  `tfsdk:"id"`
	RouteTableID        types.Int64  `tfsdk:"route_table_id"`
	TableID             types.Int64  `tfsdk:"table_id"`
	IPv6                types.Bool   `tfsdk:"ipv6"`
	Prefix              types.String `tfsdk:"prefix"`
	NextHop             types.String `tfsdk:"next_hop"`
	InterfaceID         types.Int64  `tfsdk:"interface_id"`
	InterfaceName       types.String `tfsdk:"interface_name"`
	EgressInterface     types.String `tfsdk:"egress_interface"`
	Description         types.String `tfsdk:"description"`
	Preference          types.Int64  `tfsdk:"preference"`
	RouteCount          types.Int64  `tfsdk:"route_count"`
	NextHopTable        types.String `tfsdk:"next_hop_table"`
	Weight              types.Int64  `tfsdk:"weight"`
	UDPEncap            types.Int64  `tfsdk:"udp_encap"`
	IPv4LookupTable     types.String `tfsdk:"ipv4_lookup_table"`
	IPv6LookupTable     types.String `tfsdk:"ipv6_lookup_table"`
	MPLSLookupTable     types.Int64  `tfsdk:"mpls_lookup_table"`
	ResolveViaHost      types.Bool   `tfsdk:"resolve_via_host"`
	ResolveViaConnected types.Bool   `tfsdk:"resolve_via_connected"`
	RxIPv4Interface     types.String `tfsdk:"rx_ipv4_interface"`
	RxIPv6Interface     types.String `tfsdk:"rx_ipv6_interface"`
	OutLabels           types.List   `tfsdk:"out_labels"`
	Drop                types.Bool   `tfsdk:"drop"`
	Labels              types.Set    `tfsdk:"labels"`
}

func (r *routeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_route"
}

func (r *routeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A static route in a Rookery route table. The next hop is optional when an egress interface is set.",
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id":     schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
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
				Description:   "Path preference metric. Omit to leave it out; 0 is an explicit value.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"description":           schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"route_count":           schema.Int64Attribute{Optional: true, Computed: true, Description: "Route count. Omit to leave it out; 0 is not valid.", PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"next_hop_table":        schema.StringAttribute{Optional: true, Computed: true, Description: "Next-hop route-table name; use default for table 0.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"weight":                schema.Int64Attribute{Optional: true, Computed: true, Description: "Path weight. Omit to leave it out; 0 is an explicit value.", PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"udp_encap":             schema.Int64Attribute{Optional: true, Computed: true, Description: "UDP encapsulation id. Omit to leave it out; 0 is an explicit value.", PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"ipv4_lookup_table":     schema.StringAttribute{Optional: true, Computed: true, Description: "IPv4 lookup route-table name; use default for table 0.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"ipv6_lookup_table":     schema.StringAttribute{Optional: true, Computed: true, Description: "IPv6 lookup route-table name; use default for table 0.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"mpls_lookup_table":     schema.Int64Attribute{Optional: true, Computed: true, Description: "MPLS lookup table. Omit to leave it out; 0 is an explicit value.", PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"resolve_via_host":      schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()}},
			"resolve_via_connected": schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()}},
			"rx_ipv4_interface":     schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"rx_ipv6_interface":     schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"out_labels":            schema.ListAttribute{Optional: true, Computed: true, ElementType: types.Int64Type, PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplace()}},
			"drop":                  schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()}},
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
	labels, labelDiags := routeOutLabelsFromList(ctx, plan.OutLabels)
	resp.Diagnostics.Append(labelDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	route, err := client.RouteTables.CreateRoute(callCtx, &rookeryv1.CreateRouteRequest{
		RouteTableId:        plan.RouteTableID.ValueInt64(),
		Ipv6:                plan.IPv6.ValueBool(),
		Prefix:              plan.Prefix.ValueString(),
		NextHop:             plan.NextHop.ValueString(),
		InterfaceId:         int64Pointer(plan.InterfaceID),
		EgressInterface:     plan.EgressInterface.ValueString(),
		Description:         plan.Description.ValueString(),
		Preference:          uint32Pointer(plan.Preference),
		Count:               uint32Pointer(plan.RouteCount),
		NextHopTable:        plan.NextHopTable.ValueString(),
		Weight:              uint32Pointer(plan.Weight),
		UdpEncap:            uint32Pointer(plan.UDPEncap),
		Ipv4LookupTable:     plan.IPv4LookupTable.ValueString(),
		Ipv6LookupTable:     plan.IPv6LookupTable.ValueString(),
		MplsLookupTable:     uint32Pointer(plan.MPLSLookupTable),
		ResolveViaHost:      plan.ResolveViaHost.ValueBool(),
		ResolveViaConnected: plan.ResolveViaConnected.ValueBool(),
		RxIpv4Interface:     plan.RxIPv4Interface.ValueString(),
		RxIpv6Interface:     plan.RxIPv6Interface.ValueString(),
		OutLabels:           labels,
		Drop:                plan.Drop.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create route", err.Error())
		return
	}
	resp.Diagnostics.Append(setRouteState(ctx, &plan, route)...)
	entityLabels, err := applyEntityLabels(callCtx, client, "route", fmt.Sprintf("%d", route.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create route labels", err.Error())
		return
	}
	plan.Labels = entityLabels
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
			resp.Diagnostics.Append(setRouteState(ctx, &state, route)...)
			entityLabels, err := readEntityLabels(callCtx, client, "route", fmt.Sprintf("%d", state.ID.ValueInt64()))
			if err != nil {
				resp.Diagnostics.AddError("Read route labels", err.Error())
				return
			}
			state.Labels = entityLabels
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *routeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan routeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update route labels", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	entityLabels, err := applyEntityLabels(callCtx, client, "route", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update route labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
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

func setRouteState(ctx context.Context, state *routeModel, route *rookeryv1.Route) diag.Diagnostics {
	var diags diag.Diagnostics
	state.ID = types.Int64Value(route.GetId())
	state.RouteTableID = types.Int64Value(route.GetRouteTableId())
	state.TableID = types.Int64Value(int64(route.GetTableId()))
	state.IPv6 = types.BoolValue(route.GetIpv6())
	state.Prefix = types.StringValue(route.GetPrefix())
	state.NextHop = optionalStringValue(route.GetNextHop())
	state.InterfaceID = optionalInt64Value(route.InterfaceId)
	state.InterfaceName = optionalStringValue(route.GetInterfaceName())
	state.EgressInterface = optionalStringValue(route.GetEgressInterface())
	state.Description = optionalStringValue(route.GetDescription())
	state.Preference = optionalUint32Value(route.Preference)
	state.RouteCount = optionalUint32Value(route.Count)
	state.NextHopTable = optionalStringValue(route.GetNextHopTable())
	state.Weight = optionalUint32Value(route.Weight)
	state.UDPEncap = optionalUint32Value(route.UdpEncap)
	state.IPv4LookupTable = optionalStringValue(route.GetIpv4LookupTable())
	state.IPv6LookupTable = optionalStringValue(route.GetIpv6LookupTable())
	state.MPLSLookupTable = optionalUint32Value(route.MplsLookupTable)
	state.ResolveViaHost = types.BoolValue(route.GetResolveViaHost())
	state.ResolveViaConnected = types.BoolValue(route.GetResolveViaConnected())
	state.RxIPv4Interface = optionalStringValue(route.GetRxIpv4Interface())
	state.RxIPv6Interface = optionalStringValue(route.GetRxIpv6Interface())
	values := make([]int64, 0, len(route.GetOutLabels()))
	for _, label := range route.GetOutLabels() {
		values = append(values, int64(label))
	}
	list, listDiags := int64List(ctx, values)
	state.OutLabels = list
	diags.Append(listDiags...)
	state.Drop = types.BoolValue(route.GetDrop())
	return diags
}

func routeOutLabelsFromList(ctx context.Context, list types.List) ([]uint32, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var values []int64
	diags := list.ElementsAs(ctx, &values, false)
	labels := make([]uint32, 0, len(values))
	for _, value := range values {
		if value < 0 || value > int64(^uint32(0)) {
			diags.AddError("Invalid out_labels value", fmt.Sprintf("%d is outside the uint32 range", value))
			continue
		}
		labels = append(labels, uint32(value))
	}
	return labels, diags
}
