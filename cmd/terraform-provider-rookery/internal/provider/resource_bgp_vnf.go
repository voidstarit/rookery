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
	_ resource.Resource                = (*bgpVNFResource)(nil)
	_ resource.ResourceWithConfigure   = (*bgpVNFResource)(nil)
	_ resource.ResourceWithImportState = (*bgpVNFResource)(nil)
)

type bgpVNFResource struct{ configuredResource }

// NewBGPVNFResource creates the rookery_bgp_vnf resource.
func NewBGPVNFResource() resource.Resource {
	return &bgpVNFResource{}
}

type bgpVNFModel struct {
	ID                    types.Int64  `tfsdk:"id"`
	Name                  types.String `tfsdk:"name"`
	NetworkID             types.Int64  `tfsdk:"network_id"`
	NetworkName           types.String `tfsdk:"network_name"`
	Enabled               types.Bool   `tfsdk:"enabled"`
	Transport             types.String `tfsdk:"transport"`
	LocalAS               types.Int64  `tfsdk:"local_as"`
	RouterID              types.String `tfsdk:"router_id"`
	PeerAS                types.Int64  `tfsdk:"peer_as"`
	PeerIP                types.String `tfsdk:"peer_ip"`
	LocalIP               types.String `tfsdk:"local_ip"`
	PeerGateway           types.String `tfsdk:"peer_gateway"`
	MultihopTTL           types.Int64  `tfsdk:"multihop_ttl"`
	Password              types.String `tfsdk:"password"`
	Passive               types.Bool   `tfsdk:"passive"`
	TargetTableID         types.Int64  `tfsdk:"target_table_id"`
	NextHop               types.String `tfsdk:"next_hop"`
	AdvertisedPrefixCount types.Int64  `tfsdk:"advertised_prefix_count"`
	Status                types.String `tfsdk:"status"`
	StatusDetail          types.String `tfsdk:"status_detail"`
	TapSWIfIndex          types.Int64  `tfsdk:"tap_sw_if_index"`
	TapHostName           types.String `tfsdk:"tap_host_name"`
	CreatedAt             types.String `tfsdk:"created_at"`
	UpdatedAt             types.String `tfsdk:"updated_at"`
	Labels                types.Set    `tfsdk:"labels"`
}

// Metadata sets the resource type name to the provider name followed by
// "_bgp_vnf".
func (r *bgpVNFResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bgp_vnf"
}

// Schema declares the BGP parent attributes. Advertised prefixes are a separate
// child resource, and the password is write-only and sensitive.
func (r *bgpVNFResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A BGP VNF attached to a Rookery network. Advertised prefixes are a separate resource.",
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id":     schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name":   schema.StringAttribute{Required: true, Description: "VNF name."},
			"network_id": schema.Int64Attribute{
				Required:      true,
				Description:   "Network id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"network_name": schema.StringAttribute{Computed: true},
			"enabled":      schema.BoolAttribute{Optional: true, Computed: true, Description: "Whether the VNF is active. Defaults to true."},
			"transport":    schema.StringAttribute{Optional: true, Computed: true, Description: "memif or tap. Defaults to memif."},
			"local_as":     schema.Int64Attribute{Required: true, Description: "Local autonomous system number."},
			"router_id":    schema.StringAttribute{Required: true, Description: "BGP router id as an IPv4 address."},
			"peer_as":      schema.Int64Attribute{Required: true, Description: "Peer autonomous system number."},
			"peer_ip":      schema.StringAttribute{Required: true, Description: "Peer IP address."},
			"local_ip":     schema.StringAttribute{Required: true, Description: "Local IP address as a CIDR."},
			"peer_gateway": schema.StringAttribute{Optional: true, Computed: true, Description: "Optional peer gateway; required when the peer is outside the local subnet."},
			"multihop_ttl": schema.Int64Attribute{Optional: true, Computed: true, Description: "Multihop TTL. Defaults to 64."},
			"password": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "TCP MD5 password. Write-only: the API never returns it, and it requires tap transport.",
			},
			"passive":                 schema.BoolAttribute{Optional: true, Computed: true, Description: "Whether the session is passive."},
			"target_table_id":         schema.Int64Attribute{Optional: true, Computed: true, Description: "VPP route table id for learned routes; 0 means the default table."},
			"next_hop":                schema.StringAttribute{Optional: true, Computed: true, Description: "Optional next hop for advertised prefixes."},
			"advertised_prefix_count": schema.Int64Attribute{Computed: true},
			"status":                  schema.StringAttribute{Computed: true},
			"status_detail":           schema.StringAttribute{Computed: true},
			"tap_sw_if_index":         schema.Int64Attribute{Computed: true},
			"tap_host_name":           schema.StringAttribute{Computed: true},
			"created_at":              schema.StringAttribute{Computed: true},
			"updated_at":              schema.StringAttribute{Computed: true},
		},
	}
}

// Create adds a typed BGP VNF, applies labels, and stores the server record.
func (r *bgpVNFResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bgpVNFModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create BGP VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.BGPVNFs.CreateBGPVNF(callCtx, &rookeryv1.CreateBGPVNFRequest{
		Name:          plan.Name.ValueString(),
		NetworkId:     plan.NetworkID.ValueInt64(),
		Enabled:       boolPointerDefault(plan.Enabled, true),
		Transport:     stringValue(plan.Transport),
		LocalAs:       uint32Value(plan.LocalAS),
		RouterId:      stringValue(plan.RouterID),
		PeerAs:        uint32Value(plan.PeerAS),
		PeerIp:        stringValue(plan.PeerIP),
		LocalIp:       stringValue(plan.LocalIP),
		PeerGateway:   stringValue(plan.PeerGateway),
		MultihopTtl:   uint32Value(plan.MultihopTTL),
		Password:      stringValue(plan.Password),
		Passive:       plan.Passive.ValueBool(),
		TargetTableId: uint32Value(plan.TargetTableID),
		NextHop:       stringValue(plan.NextHop),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create BGP VNF", err.Error())
		return
	}
	setBGPVNFState(&plan, vnf)
	plan.Password = sensitiveValue(plan.Password)
	entityLabels, err := applyEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", vnf.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create BGP VNF labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the BGP VNF and its labels, removing it from state when the
// server reports it is gone. The write-only password is preserved from state.
func (r *bgpVNFResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bgpVNFModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read BGP VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.BGPVNFs.GetBGPVNF(callCtx, &rookeryv1.GetBGPVNFRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read BGP VNF", err.Error())
		return
	}
	setBGPVNFState(&state, vnf)
	entityLabels, err := readEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Read BGP VNF labels", err.Error())
		return
	}
	state.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the BGP VNF scalar configuration and reapplies labels. An
// omitted password leaves the stored password unchanged.
func (r *bgpVNFResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan bgpVNFModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update BGP VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.BGPVNFs.UpdateBGPVNF(callCtx, &rookeryv1.UpdateBGPVNFRequest{
		Id:            plan.ID.ValueInt64(),
		Name:          stringPointer(plan.Name),
		Enabled:       boolPointer(plan.Enabled),
		Transport:     stringPointer(plan.Transport),
		LocalAs:       uint32Pointer(plan.LocalAS),
		RouterId:      stringPointer(plan.RouterID),
		PeerAs:        uint32Pointer(plan.PeerAS),
		PeerIp:        stringPointer(plan.PeerIP),
		LocalIp:       stringPointer(plan.LocalIP),
		PeerGateway:   stringPointer(plan.PeerGateway),
		MultihopTtl:   uint32Pointer(plan.MultihopTTL),
		Password:      stringPointer(plan.Password),
		Passive:       boolPointer(plan.Passive),
		TargetTableId: uint32Pointer(plan.TargetTableID),
		NextHop:       stringPointer(plan.NextHop),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update BGP VNF", err.Error())
		return
	}
	setBGPVNFState(&plan, vnf)
	plan.Password = sensitiveValue(plan.Password)
	entityLabels, err := applyEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update BGP VNF labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the BGP VNF, treating an already-absent VNF as success.
func (r *bgpVNFResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bgpVNFModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete BGP VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.BGPVNFs.DeleteBGPVNF(callCtx, &rookeryv1.DeleteBGPVNFRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete BGP VNF", err.Error())
	}
}

// ImportState adopts an existing BGP VNF by its integer id.
func (r *bgpVNFResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

// setBGPVNFState copies the server's BGP VNF into state. It never touches the
// password because the API accepts it on writes only.
func setBGPVNFState(state *bgpVNFModel, vnf *rookeryv1.BGPVNF) {
	state.ID = types.Int64Value(vnf.GetId())
	state.Name = types.StringValue(vnf.GetName())
	state.NetworkID = types.Int64Value(vnf.GetNetworkId())
	state.NetworkName = types.StringValue(vnf.GetNetworkName())
	state.Enabled = types.BoolValue(vnf.GetEnabled())
	state.Transport = types.StringValue(vnf.GetTransport())
	state.LocalAS = types.Int64Value(int64(vnf.GetLocalAs()))
	state.RouterID = types.StringValue(vnf.GetRouterId())
	state.PeerAS = types.Int64Value(int64(vnf.GetPeerAs()))
	state.PeerIP = types.StringValue(vnf.GetPeerIp())
	state.LocalIP = types.StringValue(vnf.GetLocalIp())
	state.PeerGateway = types.StringValue(vnf.GetPeerGateway())
	state.MultihopTTL = types.Int64Value(int64(vnf.GetMultihopTtl()))
	state.Passive = types.BoolValue(vnf.GetPassive())
	state.TargetTableID = types.Int64Value(int64(vnf.GetTargetTableId()))
	state.NextHop = types.StringValue(vnf.GetNextHop())
	state.AdvertisedPrefixCount = int64Count(vnf.GetAdvertisedPrefixCount())
	state.Status = types.StringValue(vnf.GetStatus())
	state.StatusDetail = types.StringValue(vnf.GetStatusDetail())
	state.TapSWIfIndex = types.Int64Value(int64(vnf.GetTapSwIfIndex()))
	state.TapHostName = types.StringValue(vnf.GetTapHostName())
	state.CreatedAt = timestampString(vnf.GetCreatedAt())
	state.UpdatedAt = timestampString(vnf.GetUpdatedAt())
}
