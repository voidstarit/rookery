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
	_ resource.Resource                = (*bgpAdvertisedPrefixResource)(nil)
	_ resource.ResourceWithConfigure   = (*bgpAdvertisedPrefixResource)(nil)
	_ resource.ResourceWithImportState = (*bgpAdvertisedPrefixResource)(nil)
)

type bgpAdvertisedPrefixResource struct{ configuredResource }

// NewBGPAdvertisedPrefixResource creates the rookery_bgp_advertised_prefix
// resource.
func NewBGPAdvertisedPrefixResource() resource.Resource {
	return &bgpAdvertisedPrefixResource{}
}

type bgpAdvertisedPrefixModel struct {
	ID     types.Int64  `tfsdk:"id"`
	VNFID  types.Int64  `tfsdk:"vnf_id"`
	Prefix types.String `tfsdk:"prefix"`
}

// Metadata sets the resource type name to the provider name followed by
// "_bgp_advertised_prefix".
func (r *bgpAdvertisedPrefixResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bgp_advertised_prefix"
}

// Schema declares one BGP advertised prefix.
func (r *bgpAdvertisedPrefixResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An advertised prefix owned by a rookery_bgp_vnf.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"vnf_id": schema.Int64Attribute{
				Required:      true,
				Description:   "BGP VNF id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"prefix": schema.StringAttribute{Required: true, Description: "CIDR prefix to advertise."},
		},
	}
}

// Create adds the advertised prefix to its BGP VNF.
func (r *bgpAdvertisedPrefixResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bgpAdvertisedPrefixModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create BGP advertised prefix", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	prefix, err := client.BGPVNFs.CreateBGPVNFAdvertisedPrefix(callCtx, &rookeryv1.CreateBGPVNFAdvertisedPrefixRequest{
		VnfId:  plan.VNFID.ValueInt64(),
		Prefix: plan.Prefix.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create BGP advertised prefix", err.Error())
		return
	}
	setBGPAdvertisedPrefixState(&plan, prefix)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the advertised prefix, removing it from state when either the
// prefix or its parent is gone.
func (r *bgpAdvertisedPrefixResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bgpAdvertisedPrefixModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read BGP advertised prefix", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	prefix, err := client.BGPVNFs.GetBGPVNFAdvertisedPrefix(callCtx, &rookeryv1.GetBGPVNFAdvertisedPrefixRequest{
		VnfId:    state.VNFID.ValueInt64(),
		PrefixId: state.ID.ValueInt64(),
	})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read BGP advertised prefix", err.Error())
		return
	}
	setBGPAdvertisedPrefixState(&state, prefix)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the advertised prefix.
func (r *bgpAdvertisedPrefixResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bgpAdvertisedPrefixModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update BGP advertised prefix", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	prefix, err := client.BGPVNFs.UpdateBGPVNFAdvertisedPrefix(callCtx, &rookeryv1.UpdateBGPVNFAdvertisedPrefixRequest{
		VnfId:    plan.VNFID.ValueInt64(),
		PrefixId: plan.ID.ValueInt64(),
		Prefix:   plan.Prefix.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update BGP advertised prefix", err.Error())
		return
	}
	setBGPAdvertisedPrefixState(&plan, prefix)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the advertised prefix, treating an already-absent prefix as
// success.
func (r *bgpAdvertisedPrefixResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bgpAdvertisedPrefixModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete BGP advertised prefix", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.BGPVNFs.DeleteBGPVNFAdvertisedPrefix(callCtx, &rookeryv1.DeleteBGPVNFAdvertisedPrefixRequest{
		VnfId:    state.VNFID.ValueInt64(),
		PrefixId: state.ID.ValueInt64(),
	}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete BGP advertised prefix", err.Error())
	}
}

// ImportState adopts an existing advertised prefix by <vnf_id>:<prefix_id>.
func (r *bgpAdvertisedPrefixResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importChildIDs(ctx, req, resp)
}

// setBGPAdvertisedPrefixState copies the server's advertised prefix into state.
func setBGPAdvertisedPrefixState(state *bgpAdvertisedPrefixModel, prefix *rookeryv1.BGPPrefix) {
	state.ID = types.Int64Value(prefix.GetId())
	state.VNFID = types.Int64Value(prefix.GetVnfId())
	state.Prefix = types.StringValue(prefix.GetPrefix())
}
