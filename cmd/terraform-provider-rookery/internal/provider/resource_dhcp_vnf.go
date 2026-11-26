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
	_ resource.Resource                = (*dhcpVNFResource)(nil)
	_ resource.ResourceWithConfigure   = (*dhcpVNFResource)(nil)
	_ resource.ResourceWithImportState = (*dhcpVNFResource)(nil)
)

type dhcpVNFResource struct{ configuredResource }

// NewDHCPVNFResource creates the rookery_dhcp_vnf resource.
func NewDHCPVNFResource() resource.Resource {
	return &dhcpVNFResource{}
}

type dhcpVNFModel struct {
	ID                    types.Int64  `tfsdk:"id"`
	Name                  types.String `tfsdk:"name"`
	NetworkID             types.Int64  `tfsdk:"network_id"`
	NetworkName           types.String `tfsdk:"network_name"`
	Family                types.String `tfsdk:"family"`
	Enabled               types.Bool   `tfsdk:"enabled"`
	Gateway               types.String `tfsdk:"gateway"`
	RangeStart            types.String `tfsdk:"range_start"`
	RangeEnd              types.String `tfsdk:"range_end"`
	LeaseTime             types.String `tfsdk:"lease_time"`
	DNS                   types.String `tfsdk:"dns"`
	BindIP                types.String `tfsdk:"bind_ip"`
	Transport             types.String `tfsdk:"transport"`
	DNSVNFID              types.Int64  `tfsdk:"dns_vnf_id"`
	DNSDomain             types.String `tfsdk:"dns_domain"`
	DNSTTL                types.Int64  `tfsdk:"dns_ttl"`
	StaticAssignmentCount types.Int64  `tfsdk:"static_assignment_count"`
	OptionCount           types.Int64  `tfsdk:"option_count"`
	LeaseCount            types.Int64  `tfsdk:"lease_count"`
	Status                types.String `tfsdk:"status"`
	StatusDetail          types.String `tfsdk:"status_detail"`
	TapSWIfIndex          types.Int64  `tfsdk:"tap_sw_if_index"`
	TapHostName           types.String `tfsdk:"tap_host_name"`
	CreatedAt             types.String `tfsdk:"created_at"`
	UpdatedAt             types.String `tfsdk:"updated_at"`
	Labels                types.Set    `tfsdk:"labels"`
}

// Metadata sets the resource type name to the provider name followed by
// "_dhcp_vnf".
func (r *dhcpVNFResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dhcp_vnf"
}

// Schema declares the DHCPv4/DHCPv6 parent attributes. Family and network are
// immutable because they select one storage and runtime type.
func (r *dhcpVNFResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A DHCPv4 or DHCPv6 VNF attached to a Rookery network. Static assignments and options are separate resources.",
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
			"family": schema.StringAttribute{
				Required:      true,
				Description:   "dhcpv4 or dhcpv6. The family is fixed at creation.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"enabled":     schema.BoolAttribute{Optional: true, Computed: true, Description: "Whether the VNF is active. Defaults to true."},
			"gateway":     schema.StringAttribute{Required: true, Description: "Gateway address as a CIDR, for example 10.20.0.1/16."},
			"range_start": schema.StringAttribute{Required: true, Description: "First address in the dynamic lease range."},
			"range_end":   schema.StringAttribute{Required: true, Description: "Last address in the dynamic lease range."},
			"lease_time":  schema.StringAttribute{Optional: true, Computed: true, Description: "Lease duration, for example 12h. Defaults to 12h."},
			"dns":         schema.StringAttribute{Optional: true, Computed: true, Description: "DNS server address handed to clients."},
			"bind_ip":     schema.StringAttribute{Optional: true, Computed: true, Description: "Optional bind address as a CIDR; the server derives one when omitted."},
			"transport":   schema.StringAttribute{Optional: true, Computed: true, Description: "Transport: shared, dedicated, tap, or empty for the network default."},
			"dns_vnf_id": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Description: "DNS VNF that maintains dynamic records for this VNF; 0 means none.",
			},
			"dns_domain":              schema.StringAttribute{Optional: true, Computed: true, Description: "Domain suffix for dynamic DNS records."},
			"dns_ttl":                 schema.Int64Attribute{Optional: true, Computed: true, Description: "TTL for dynamic DNS records. Defaults to 300; 0 disables."},
			"static_assignment_count": schema.Int64Attribute{Computed: true},
			"option_count":            schema.Int64Attribute{Computed: true},
			"lease_count":             schema.Int64Attribute{Computed: true},
			"status":                  schema.StringAttribute{Computed: true},
			"status_detail":           schema.StringAttribute{Computed: true},
			"tap_sw_if_index":         schema.Int64Attribute{Computed: true},
			"tap_host_name":           schema.StringAttribute{Computed: true},
			"created_at":              schema.StringAttribute{Computed: true},
			"updated_at":              schema.StringAttribute{Computed: true},
		},
	}
}

// Create adds a typed DHCP VNF, applies labels, and stores the server record.
func (r *dhcpVNFResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dhcpVNFModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create DHCP VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.DHCPVNFs.CreateDHCPVNF(callCtx, &rookeryv1.CreateDHCPVNFRequest{
		Name:       plan.Name.ValueString(),
		NetworkId:  plan.NetworkID.ValueInt64(),
		Family:     plan.Family.ValueString(),
		Enabled:    boolPointerDefault(plan.Enabled, true),
		Gateway:    stringValue(plan.Gateway),
		RangeStart: stringValue(plan.RangeStart),
		RangeEnd:   stringValue(plan.RangeEnd),
		LeaseTime:  stringValue(plan.LeaseTime),
		Dns:        stringValue(plan.DNS),
		BindIp:     stringValue(plan.BindIP),
		Transport:  stringValue(plan.Transport),
		DnsVnfId:   int64Value(plan.DNSVNFID),
		DnsDomain:  stringValue(plan.DNSDomain),
		DnsTtl:     uint32Value(plan.DNSTTL),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create DHCP VNF", err.Error())
		return
	}
	setDHCPVNFState(&plan, vnf)
	entityLabels, err := applyEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", vnf.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create DHCP VNF labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the DHCP VNF and its labels, removing it from state when the
// server reports it is gone.
func (r *dhcpVNFResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dhcpVNFModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read DHCP VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.DHCPVNFs.GetDHCPVNF(callCtx, &rookeryv1.GetDHCPVNFRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read DHCP VNF", err.Error())
		return
	}
	setDHCPVNFState(&state, vnf)
	entityLabels, err := readEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Read DHCP VNF labels", err.Error())
		return
	}
	state.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the DHCP VNF scalar configuration and reapplies labels.
func (r *dhcpVNFResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dhcpVNFModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update DHCP VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.DHCPVNFs.UpdateDHCPVNF(callCtx, &rookeryv1.UpdateDHCPVNFRequest{
		Id:         plan.ID.ValueInt64(),
		Name:       stringPointer(plan.Name),
		Enabled:    boolPointer(plan.Enabled),
		Gateway:    stringPointer(plan.Gateway),
		RangeStart: stringPointer(plan.RangeStart),
		RangeEnd:   stringPointer(plan.RangeEnd),
		LeaseTime:  stringPointer(plan.LeaseTime),
		Dns:        stringPointer(plan.DNS),
		BindIp:     stringPointer(plan.BindIP),
		Transport:  stringPointer(plan.Transport),
		DnsVnfId:   int64Pointer(plan.DNSVNFID),
		DnsDomain:  stringPointer(plan.DNSDomain),
		DnsTtl:     uint32Pointer(plan.DNSTTL),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update DHCP VNF", err.Error())
		return
	}
	setDHCPVNFState(&plan, vnf)
	entityLabels, err := applyEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update DHCP VNF labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the DHCP VNF, treating an already-absent VNF as success.
func (r *dhcpVNFResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dhcpVNFModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete DHCP VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.DHCPVNFs.DeleteDHCPVNF(callCtx, &rookeryv1.DeleteDHCPVNFRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete DHCP VNF", err.Error())
	}
}

// ImportState adopts an existing DHCP VNF by its integer id.
func (r *dhcpVNFResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

// setDHCPVNFState copies the server's DHCP VNF into state.
func setDHCPVNFState(state *dhcpVNFModel, vnf *rookeryv1.DHCPVNF) {
	state.ID = types.Int64Value(vnf.GetId())
	state.Name = types.StringValue(vnf.GetName())
	state.NetworkID = types.Int64Value(vnf.GetNetworkId())
	state.NetworkName = types.StringValue(vnf.GetNetworkName())
	state.Family = types.StringValue(vnf.GetFamily())
	state.Enabled = types.BoolValue(vnf.GetEnabled())
	state.Gateway = types.StringValue(vnf.GetGateway())
	state.RangeStart = types.StringValue(vnf.GetRangeStart())
	state.RangeEnd = types.StringValue(vnf.GetRangeEnd())
	state.LeaseTime = types.StringValue(vnf.GetLeaseTime())
	state.DNS = types.StringValue(vnf.GetDns())
	state.BindIP = types.StringValue(vnf.GetBindIp())
	state.Transport = types.StringValue(vnf.GetTransport())
	state.DNSVNFID = types.Int64Value(vnf.GetDnsVnfId())
	state.DNSDomain = types.StringValue(vnf.GetDnsDomain())
	state.DNSTTL = types.Int64Value(int64(vnf.GetDnsTtl()))
	state.StaticAssignmentCount = int64Count(vnf.GetStaticAssignmentCount())
	state.OptionCount = int64Count(vnf.GetOptionCount())
	state.LeaseCount = int64Count(vnf.GetLeaseCount())
	state.Status = types.StringValue(vnf.GetStatus())
	state.StatusDetail = types.StringValue(vnf.GetStatusDetail())
	state.TapSWIfIndex = types.Int64Value(int64(vnf.GetTapSwIfIndex()))
	state.TapHostName = types.StringValue(vnf.GetTapHostName())
	state.CreatedAt = timestampString(vnf.GetCreatedAt())
	state.UpdatedAt = timestampString(vnf.GetUpdatedAt())
}
