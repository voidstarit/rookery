package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*hostTapResource)(nil)
	_ resource.ResourceWithConfigure   = (*hostTapResource)(nil)
	_ resource.ResourceWithImportState = (*hostTapResource)(nil)
)

type hostTapResource struct{ configuredResource }

// NewHostTapResource creates the rookery_host_tap resource.
func NewHostTapResource() resource.Resource {
	return &hostTapResource{}
}

type hostTapModel struct {
	ID           types.Int64  `tfsdk:"id"`
	NetworkID    types.Int64  `tfsdk:"network_id"`
	NetworkName  types.String `tfsdk:"network_name"`
	Name         types.String `tfsdk:"name"`
	SwIfIndex    types.Int64  `tfsdk:"sw_if_index"`
	IPv4Address  types.String `tfsdk:"ipv4_address"`
	VPPAddresses types.List   `tfsdk:"vpp_addresses"`
	Labels       types.Set    `tfsdk:"labels"`
}

func (r *hostTapResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_host_tap"
}

func (r *hostTapResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A persistent VPP tap connecting a Rookery Network to the Linux host.",
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id":     schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"network_id": schema.Int64Attribute{
				Required:      true,
				Description:   "Network id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"network_name": schema.StringAttribute{Computed: true},
			"name": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Linux interface name. Defaults to hostnet<network id>.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
			},
			"sw_if_index": schema.Int64Attribute{Computed: true},
			"ipv4_address": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Optional IPv4 address/CIDR assigned to the host-side tap.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
			},
			"vpp_addresses": schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Live VPP-side addresses on the tap."},
		},
	}
}

func (r *hostTapResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan hostTapModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create host tap", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	tap, err := client.HostInterfaces.CreateHostTap(callCtx, &rookeryv1.CreateHostTapRequest{
		NetworkId:   plan.NetworkID.ValueInt64(),
		Name:        plan.Name.ValueString(),
		Ipv4Address: plan.IPv4Address.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create host tap", err.Error())
		return
	}
	resp.Diagnostics.Append(setHostTapState(ctx, &plan, tap)...)
	entityLabels, err := applyEntityLabels(callCtx, client, "host_tap", fmt.Sprintf("%d", tap.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create host tap labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *hostTapResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state hostTapModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read host tap", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.HostInterfaces.ListHostTaps(callCtx, &rookeryv1.ListHostTapsRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Read host tap", err.Error())
		return
	}
	for _, tap := range response.GetTaps() {
		if tap.GetId() == state.ID.ValueInt64() {
			resp.Diagnostics.Append(setHostTapState(ctx, &state, tap)...)
			entityLabels, err := readEntityLabels(callCtx, client, "host_tap", fmt.Sprintf("%d", state.ID.ValueInt64()))
			if err != nil {
				resp.Diagnostics.AddError("Read host tap labels", err.Error())
				return
			}
			state.Labels = entityLabels
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *hostTapResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan hostTapModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update host tap labels", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	entityLabels, err := applyEntityLabels(callCtx, client, "host_tap", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update host tap labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *hostTapResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state hostTapModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete host tap", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.HostInterfaces.DeleteHostTap(callCtx, &rookeryv1.DeleteHostTapRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete host tap", err.Error())
	}
}

func (r *hostTapResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func setHostTapState(ctx context.Context, state *hostTapModel, tap *rookeryv1.HostTap) diag.Diagnostics {
	var diags diag.Diagnostics
	state.ID = types.Int64Value(tap.GetId())
	state.NetworkID = types.Int64Value(tap.GetNetworkId())
	state.NetworkName = types.StringValue(tap.GetNetworkName())
	state.Name = types.StringValue(tap.GetName())
	state.SwIfIndex = types.Int64Value(int64(tap.GetSwIfIndex()))
	state.IPv4Address = optionalStringValue(tap.GetIpv4Address())
	addresses, addressDiags := stringList(ctx, tap.GetVppAddresses())
	diags.Append(addressDiags...)
	state.VPPAddresses = addresses
	return diags
}
