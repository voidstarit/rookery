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
	_ resource.Resource                = (*dnsForwarderResource)(nil)
	_ resource.ResourceWithConfigure   = (*dnsForwarderResource)(nil)
	_ resource.ResourceWithImportState = (*dnsForwarderResource)(nil)
)

type dnsForwarderResource struct{ configuredResource }

// NewDNSForwarderResource creates the rookery_dns_forwarder resource.
func NewDNSForwarderResource() resource.Resource {
	return &dnsForwarderResource{}
}

type dnsForwarderModel struct {
	ID       types.Int64  `tfsdk:"id"`
	VNFID    types.Int64  `tfsdk:"vnf_id"`
	Address  types.String `tfsdk:"address"`
	Position types.Int64  `tfsdk:"position"`
}

// Metadata sets the resource type name to the provider name followed by
// "_dns_forwarder".
func (r *dnsForwarderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_forwarder"
}

// Schema declares one upstream DNS forwarder.
func (r *dnsForwarderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An upstream DNS forwarder owned by a rookery_dns_vnf.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"vnf_id": schema.Int64Attribute{
				Required:      true,
				Description:   "DNS VNF id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"address":  schema.StringAttribute{Required: true, Description: "Upstream resolver address."},
			"position": schema.Int64Attribute{Optional: true, Computed: true, Description: "Order of the forwarder. Defaults to 0."},
		},
	}
}

// Create adds the forwarder to its DNS VNF.
func (r *dnsForwarderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dnsForwarderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create DNS forwarder", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	forwarder, err := client.DNSVNFs.CreateDNSVNFForwarder(callCtx, &rookeryv1.CreateDNSVNFForwarderRequest{
		VnfId:    plan.VNFID.ValueInt64(),
		Address:  plan.Address.ValueString(),
		Position: int32Value(plan.Position),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create DNS forwarder", err.Error())
		return
	}
	setDNSForwarderState(&plan, forwarder)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the forwarder, removing it from state when either the
// forwarder or its parent is gone.
func (r *dnsForwarderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dnsForwarderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read DNS forwarder", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	forwarder, err := client.DNSVNFs.GetDNSVNFForwarder(callCtx, &rookeryv1.GetDNSVNFForwarderRequest{
		VnfId:       state.VNFID.ValueInt64(),
		ForwarderId: state.ID.ValueInt64(),
	})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read DNS forwarder", err.Error())
		return
	}
	setDNSForwarderState(&state, forwarder)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the forwarder address or position.
func (r *dnsForwarderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dnsForwarderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update DNS forwarder", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	forwarder, err := client.DNSVNFs.UpdateDNSVNFForwarder(callCtx, &rookeryv1.UpdateDNSVNFForwarderRequest{
		VnfId:       plan.VNFID.ValueInt64(),
		ForwarderId: plan.ID.ValueInt64(),
		Address:     plan.Address.ValueString(),
		Position:    int32Value(plan.Position),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update DNS forwarder", err.Error())
		return
	}
	setDNSForwarderState(&plan, forwarder)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the forwarder, treating an already-absent forwarder as
// success.
func (r *dnsForwarderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dnsForwarderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete DNS forwarder", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.DNSVNFs.DeleteDNSVNFForwarder(callCtx, &rookeryv1.DeleteDNSVNFForwarderRequest{
		VnfId:       state.VNFID.ValueInt64(),
		ForwarderId: state.ID.ValueInt64(),
	}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete DNS forwarder", err.Error())
	}
}

// ImportState adopts an existing forwarder by <vnf_id>:<forwarder_id>.
func (r *dnsForwarderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importChildIDs(ctx, req, resp)
}

// setDNSForwarderState copies the server's forwarder into state.
func setDNSForwarderState(state *dnsForwarderModel, forwarder *rookeryv1.DNSForwarder) {
	state.ID = types.Int64Value(forwarder.GetId())
	state.VNFID = types.Int64Value(forwarder.GetVnfId())
	state.Address = types.StringValue(forwarder.GetAddress())
	state.Position = types.Int64Value(int64(forwarder.GetPosition()))
}
