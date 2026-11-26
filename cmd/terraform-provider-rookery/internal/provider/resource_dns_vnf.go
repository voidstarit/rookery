package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*dnsVNFResource)(nil)
	_ resource.ResourceWithConfigure   = (*dnsVNFResource)(nil)
	_ resource.ResourceWithImportState = (*dnsVNFResource)(nil)
)

type dnsVNFResource struct{ configuredResource }

// NewDNSVNFResource creates the rookery_dns_vnf resource.
func NewDNSVNFResource() resource.Resource {
	return &dnsVNFResource{}
}

type dnsVNFModel struct {
	ID             types.Int64  `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	NetworkID      types.Int64  `tfsdk:"network_id"`
	NetworkName    types.String `tfsdk:"network_name"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	Forward        types.Bool   `tfsdk:"forward"`
	RecordCount    types.Int64  `tfsdk:"record_count"`
	ForwarderCount types.Int64  `tfsdk:"forwarder_count"`
	Status         types.String `tfsdk:"status"`
	StatusDetail   types.String `tfsdk:"status_detail"`
	TapSWIfIndex   types.Int64  `tfsdk:"tap_sw_if_index"`
	TapHostName    types.String `tfsdk:"tap_host_name"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
	Labels         types.Set    `tfsdk:"labels"`
}

// Metadata sets the resource type name to the provider name followed by
// "_dns_vnf".
func (r *dnsVNFResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_vnf"
}

// Schema declares the DNS parent attributes. Records and forwarders are
// separate child resources.
func (r *dnsVNFResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A DNS VNF attached to a Rookery network. Records and forwarders are separate resources.",
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id":     schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name":   schema.StringAttribute{Required: true, Description: "VNF name."},
			"network_id": schema.Int64Attribute{
				Required:      true,
				Description:   "Network id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"network_name":    schema.StringAttribute{Computed: true},
			"enabled":         schema.BoolAttribute{Optional: true, Computed: true, Description: "Whether the VNF is active. Defaults to true."},
			"forward":         schema.BoolAttribute{Optional: true, Computed: true, Description: "Forward names without a local record to the configured forwarders."},
			"record_count":    schema.Int64Attribute{Computed: true},
			"forwarder_count": schema.Int64Attribute{Computed: true},
			"status":          schema.StringAttribute{Computed: true},
			"status_detail":   schema.StringAttribute{Computed: true},
			"tap_sw_if_index": schema.Int64Attribute{Computed: true},
			"tap_host_name":   schema.StringAttribute{Computed: true},
			"created_at":      schema.StringAttribute{Computed: true},
			"updated_at":      schema.StringAttribute{Computed: true},
		},
	}
}

// Create adds a typed DNS VNF, applies labels, and stores the server record.
func (r *dnsVNFResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dnsVNFModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create DNS VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.DNSVNFs.CreateDNSVNF(callCtx, &rookeryv1.CreateDNSVNFRequest{
		Name:      plan.Name.ValueString(),
		NetworkId: plan.NetworkID.ValueInt64(),
		Enabled:   boolPointerDefault(plan.Enabled, true),
		Forward:   boolPointer(plan.Forward),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create DNS VNF", err.Error())
		return
	}
	setDNSVNFState(&plan, vnf)
	entityLabels, err := applyEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", vnf.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create DNS VNF labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the DNS VNF and its labels, removing it from state when the
// server reports it is gone.
func (r *dnsVNFResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dnsVNFModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read DNS VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.DNSVNFs.GetDNSVNF(callCtx, &rookeryv1.GetDNSVNFRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read DNS VNF", err.Error())
		return
	}
	setDNSVNFState(&state, vnf)
	entityLabels, err := readEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Read DNS VNF labels", err.Error())
		return
	}
	state.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the DNS VNF name, enabled flag, and forward flag, then
// reapplies labels.
func (r *dnsVNFResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dnsVNFModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update DNS VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.DNSVNFs.UpdateDNSVNF(callCtx, &rookeryv1.UpdateDNSVNFRequest{
		Id:      plan.ID.ValueInt64(),
		Name:    stringPointer(plan.Name),
		Enabled: boolPointer(plan.Enabled),
		Forward: boolPointer(plan.Forward),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update DNS VNF", err.Error())
		return
	}
	setDNSVNFState(&plan, vnf)
	entityLabels, err := applyEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update DNS VNF labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the DNS VNF, treating an already-absent VNF as success.
func (r *dnsVNFResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dnsVNFModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete DNS VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.DNSVNFs.DeleteDNSVNF(callCtx, &rookeryv1.DeleteDNSVNFRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete DNS VNF", err.Error())
	}
}

// ImportState adopts an existing DNS VNF by its integer id.
func (r *dnsVNFResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

// setDNSVNFState copies the server's DNS VNF into state.
func setDNSVNFState(state *dnsVNFModel, vnf *rookeryv1.DNSVNF) {
	state.ID = types.Int64Value(vnf.GetId())
	state.Name = types.StringValue(vnf.GetName())
	state.NetworkID = types.Int64Value(vnf.GetNetworkId())
	state.NetworkName = types.StringValue(vnf.GetNetworkName())
	state.Enabled = types.BoolValue(vnf.GetEnabled())
	state.Forward = types.BoolValue(vnf.GetForward())
	state.RecordCount = int64Count(vnf.GetRecordCount())
	state.ForwarderCount = int64Count(vnf.GetForwarderCount())
	state.Status = types.StringValue(vnf.GetStatus())
	state.StatusDetail = types.StringValue(vnf.GetStatusDetail())
	state.TapSWIfIndex = types.Int64Value(int64(vnf.GetTapSwIfIndex()))
	state.TapHostName = types.StringValue(vnf.GetTapHostName())
	state.CreatedAt = timestampString(vnf.GetCreatedAt())
	state.UpdatedAt = timestampString(vnf.GetUpdatedAt())
}
