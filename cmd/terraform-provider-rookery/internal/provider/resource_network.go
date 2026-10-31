package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*networkResource)(nil)
	_ resource.ResourceWithConfigure   = (*networkResource)(nil)
	_ resource.ResourceWithImportState = (*networkResource)(nil)
)

type networkResource struct{ configuredResource }

// NewNetworkResource creates the rookery_network resource.
func NewNetworkResource() resource.Resource {
	return &networkResource{}
}

type networkModel struct {
	ID             types.Int64  `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	IPv4CIDR       types.String `tfsdk:"ipv4_cidr"`
	IPv6CIDR       types.String `tfsdk:"ipv6_cidr"`
	BridgeDomainID types.Int64  `tfsdk:"bridge_domain_id"`
	InterfaceCount types.Int64  `tfsdk:"interface_count"`
	BVIHostName    types.String `tfsdk:"bvi_host_name"`
	BVISwIfIndex   types.Int64  `tfsdk:"bvi_sw_if_index"`
	IPv4Gateway    types.String `tfsdk:"ipv4_gateway"`
	RouteTableID   types.Int64  `tfsdk:"route_table_id"`
	Labels         types.Set    `tfsdk:"labels"`
}

func (r *networkResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network"
}

func (r *networkResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Rookery VPP bridge-domain network with an isolated VRF, IPv4 and IPv6 prefixes, and a gateway BVI.",
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id":     schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Network name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ipv4_cidr": schema.StringAttribute{
				Required:    true,
				Description: "IPv4 prefix for the network. Can be updated while the network has no VNFs, host taps, or BVI.",
			},
			"ipv6_cidr": schema.StringAttribute{
				Required:    true,
				Description: "IPv6 prefix for the network. Can be updated while the network has no VNFs, host taps, or BVI.",
			},
			"bridge_domain_id": schema.Int64Attribute{Computed: true},
			"interface_count":  schema.Int64Attribute{Computed: true},
			"bvi_host_name":    schema.StringAttribute{Computed: true},
			"bvi_sw_if_index":  schema.Int64Attribute{Computed: true},
			"ipv4_gateway":     schema.StringAttribute{Computed: true},
			"route_table_id":   schema.Int64Attribute{Computed: true, Description: "VPP table id for the network VRF (100000 + network id)."},
		},
	}
}

func (r *networkResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan networkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create network", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	network, err := client.Networks.CreateNetwork(callCtx, &rookeryv1.CreateNetworkRequest{
		Name:     plan.Name.ValueString(),
		Ipv4Cidr: plan.IPv4CIDR.ValueString(),
		Ipv6Cidr: plan.IPv6CIDR.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create network", err.Error())
		return
	}
	setNetworkState(&plan, network)
	entityLabels, err := applyEntityLabels(callCtx, client, "network", fmt.Sprintf("%d", network.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create network labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *networkResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state networkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read network", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	network, err := client.Networks.GetNetwork(callCtx, &rookeryv1.GetNetworkRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read network", err.Error())
		return
	}
	setNetworkState(&state, network)
	entityLabels, err := readEntityLabels(callCtx, client, "network", fmt.Sprintf("%d", state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Read network labels", err.Error())
		return
	}
	state.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *networkResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan networkModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update network", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	network, err := client.Networks.UpdateNetwork(callCtx, &rookeryv1.UpdateNetworkRequest{
		Id:       plan.ID.ValueInt64(),
		Ipv4Cidr: plan.IPv4CIDR.ValueString(),
		Ipv6Cidr: plan.IPv6CIDR.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update network", err.Error())
		return
	}
	setNetworkState(&plan, network)
	entityLabels, err := applyEntityLabels(callCtx, client, "network", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update network labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *networkResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state networkModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete network", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.Networks.DeleteNetwork(callCtx, &rookeryv1.DeleteNetworkRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete network", err.Error())
	}
}

func (r *networkResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func setNetworkState(state *networkModel, network *rookeryv1.Network) {
	state.ID = types.Int64Value(network.GetId())
	state.Name = types.StringValue(network.GetName())
	state.IPv4CIDR = types.StringValue(network.GetIpv4Cidr())
	state.IPv6CIDR = types.StringValue(network.GetIpv6Cidr())
	state.BridgeDomainID = types.Int64Value(int64(network.GetBridgeDomainId()))
	state.InterfaceCount = types.Int64Value(int64(network.GetInterfaceCount()))
	state.BVIHostName = optionalStringValue(network.GetBviHostName())
	state.BVISwIfIndex = types.Int64Value(int64(network.GetBviSwIfIndex()))
	state.IPv4Gateway = optionalStringValue(network.GetIpv4Gateway())
	state.RouteTableID = types.Int64Value(int64(network.GetRouteTableId()))
}
