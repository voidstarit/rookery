package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*networkPipeResource)(nil)
	_ resource.ResourceWithConfigure   = (*networkPipeResource)(nil)
	_ resource.ResourceWithImportState = (*networkPipeResource)(nil)
)

type networkPipeResource struct{ configuredResource }

// NewNetworkPipeResource creates the rookery_network_pipe resource.
func NewNetworkPipeResource() resource.Resource {
	return &networkPipeResource{}
}

type networkPipeModel struct {
	ID                 types.Int64  `tfsdk:"id"`
	NetworkAID         types.Int64  `tfsdk:"network_a_id"`
	NetworkAName       types.String `tfsdk:"network_a_name"`
	NetworkBID         types.Int64  `tfsdk:"network_b_id"`
	NetworkBName       types.String `tfsdk:"network_b_name"`
	Instance           types.Int64  `tfsdk:"instance"`
	ParentSwIfIndex    types.Int64  `tfsdk:"parent_sw_if_index"`
	EndpointASwIfIndex types.Int64  `tfsdk:"endpoint_a_sw_if_index"`
	EndpointBSwIfIndex types.Int64  `tfsdk:"endpoint_b_sw_if_index"`
	EndpointAName      types.String `tfsdk:"endpoint_a_name"`
	EndpointBName      types.String `tfsdk:"endpoint_b_name"`
}

func (r *networkPipeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network_pipe"
}

func (r *networkPipeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A VPP pipe pair connecting two Rookery Networks at layer 2.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"network_a_id": schema.Int64Attribute{
				Required:      true,
				Description:   "First network id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"network_a_name": schema.StringAttribute{Computed: true},
			"network_b_id": schema.Int64Attribute{
				Required:      true,
				Description:   "Second network id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"network_b_name":         schema.StringAttribute{Computed: true},
			"instance":               schema.Int64Attribute{Computed: true, Description: "VPP pipe instance."},
			"parent_sw_if_index":     schema.Int64Attribute{Computed: true},
			"endpoint_a_sw_if_index": schema.Int64Attribute{Computed: true},
			"endpoint_b_sw_if_index": schema.Int64Attribute{Computed: true},
			"endpoint_a_name":        schema.StringAttribute{Computed: true, Description: "VPP interface name for the A endpoint; use it with rookery_vpp_interface_addresses or rookery_vpp_interface_route_tables."},
			"endpoint_b_name":        schema.StringAttribute{Computed: true, Description: "VPP interface name for the B endpoint."},
		},
	}
}

func (r *networkPipeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan networkPipeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.NetworkAID.ValueInt64() == plan.NetworkBID.ValueInt64() {
		resp.Diagnostics.AddError("Create network pipe", "network_a_id and network_b_id must be different")
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create network pipe", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	pipe, err := client.Networks.CreateNetworkPipe(callCtx, &rookeryv1.CreateNetworkPipeRequest{
		NetworkAId: plan.NetworkAID.ValueInt64(),
		NetworkBId: plan.NetworkBID.ValueInt64(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create network pipe", err.Error())
		return
	}
	setNetworkPipeState(&plan, pipe, true)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *networkPipeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state networkPipeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read network pipe", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.Networks.ListNetworkPipes(callCtx, &rookeryv1.ListNetworkPipesRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Read network pipe", err.Error())
		return
	}
	for _, pipe := range response.GetPipes() {
		if pipe.GetId() == state.ID.ValueInt64() {
			setNetworkPipeState(&state, pipe, true)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *networkPipeResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update network pipe", errors.New("network pipes are immutable; replace the resource instead").Error())
}

func (r *networkPipeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state networkPipeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete network pipe", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.Networks.DeleteNetworkPipe(callCtx, &rookeryv1.DeleteNetworkPipeRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete network pipe", err.Error())
	}
}

func (r *networkPipeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func setNetworkPipeState(state *networkPipeModel, pipe *rookeryv1.NetworkPipe, preserveOrder bool) {
	aID, aName := pipe.GetNetworkAId(), pipe.GetNetworkAName()
	bID, bName := pipe.GetNetworkBId(), pipe.GetNetworkBName()
	aEndpoint, bEndpoint := fmt.Sprintf("pipe%d.0", pipe.GetInstance()), fmt.Sprintf("pipe%d.1", pipe.GetInstance())
	aSwIfIndex, bSwIfIndex := pipe.GetEndpointASwIfIndex(), pipe.GetEndpointBSwIfIndex()
	if preserveOrder && state.NetworkAID.ValueInt64() == bID && state.NetworkBID.ValueInt64() == aID {
		aID, bID = bID, aID
		aName, bName = bName, aName
		aEndpoint, bEndpoint = bEndpoint, aEndpoint
		aSwIfIndex, bSwIfIndex = bSwIfIndex, aSwIfIndex
	}
	state.ID = types.Int64Value(pipe.GetId())
	state.NetworkAID = types.Int64Value(aID)
	state.NetworkAName = types.StringValue(aName)
	state.NetworkBID = types.Int64Value(bID)
	state.NetworkBName = types.StringValue(bName)
	state.Instance = types.Int64Value(int64(pipe.GetInstance()))
	state.ParentSwIfIndex = types.Int64Value(int64(pipe.GetParentSwIfIndex()))
	state.EndpointASwIfIndex = types.Int64Value(int64(aSwIfIndex))
	state.EndpointBSwIfIndex = types.Int64Value(int64(bSwIfIndex))
	state.EndpointAName = types.StringValue(aEndpoint)
	state.EndpointBName = types.StringValue(bEndpoint)
}
