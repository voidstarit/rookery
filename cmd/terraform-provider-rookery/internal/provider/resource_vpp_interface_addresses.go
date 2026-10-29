package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*vppInterfaceAddressesResource)(nil)
	_ resource.ResourceWithConfigure   = (*vppInterfaceAddressesResource)(nil)
	_ resource.ResourceWithImportState = (*vppInterfaceAddressesResource)(nil)
)

type vppInterfaceAddressesResource struct{ configuredResource }

// NewVPPInterfaceAddressesResource creates the rookery_vpp_interface_addresses
// resource.
func NewVPPInterfaceAddressesResource() resource.Resource {
	return &vppInterfaceAddressesResource{}
}

type vppInterfaceAddressesModel struct {
	Name                 types.String `tfsdk:"name"`
	Addresses            types.Set    `tfsdk:"addresses"`
	DHCPClientEnabled    types.Bool   `tfsdk:"dhcp_client_enabled"`
	LiveAddresses        types.List   `tfsdk:"live_addresses"`
	DHCPClientConfigured types.Bool   `tfsdk:"dhcp_client_configured"`
	DHCPClientStatus     types.String `tfsdk:"dhcp_client_status"`
	StatusDetail         types.String `tfsdk:"status_detail"`
}

func (r *vppInterfaceAddressesResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpp_interface_addresses"
}

func (r *vppInterfaceAddressesResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages Rookery-managed IPv4/IPv6 addresses and the IPv4 DHCP client on a vhost-user, host, host-tap, pipe, or VNF interface. Destroying this resource clears the managed addresses and disables DHCP.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "VPP interface name, for example app-vm0, host-enp1s0, hostnet1, or pipe0.0.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"addresses": schema.SetAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Description: "IPv4/IPv6 CIDRs to assign. Set [] explicitly to clear addresses.",
			},
			"dhcp_client_enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Enable the IPv4 DHCP client on the interface.",
			},
			"live_addresses":         schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Addresses currently present in VPP."},
			"dhcp_client_configured": schema.BoolAttribute{Computed: true},
			"dhcp_client_status":     schema.StringAttribute{Computed: true},
			"status_detail":          schema.StringAttribute{Computed: true},
		},
	}
}

func (r *vppInterfaceAddressesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vppInterfaceAddressesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Configure VPP interface addresses", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	addresses, diags := stringSetValues(ctx, plan.Addresses)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	response, err := client.Interfaces.UpdateVPPInterfaceAddresses(callCtx, &rookeryv1.UpdateVPPInterfaceAddressesRequest{
		Name:              plan.Name.ValueString(),
		Addresses:         addresses,
		DhcpClientEnabled: plan.DHCPClientEnabled.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Configure VPP interface addresses", err.Error())
		return
	}
	resp.Diagnostics.Append(setVPPInterfaceAddressesState(ctx, &plan, response)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vppInterfaceAddressesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vppInterfaceAddressesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read VPP interface addresses", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.Interfaces.GetVPPInterfaceAddresses(callCtx, &rookeryv1.GetVPPInterfaceAddressesRequest{Name: state.Name.ValueString()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read VPP interface addresses", err.Error())
		return
	}
	resp.Diagnostics.Append(setVPPInterfaceAddressesState(ctx, &state, response)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vppInterfaceAddressesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan vppInterfaceAddressesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update VPP interface addresses", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	addresses, diags := stringSetValues(ctx, plan.Addresses)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	response, err := client.Interfaces.UpdateVPPInterfaceAddresses(callCtx, &rookeryv1.UpdateVPPInterfaceAddressesRequest{
		Name:              plan.Name.ValueString(),
		Addresses:         addresses,
		DhcpClientEnabled: plan.DHCPClientEnabled.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update VPP interface addresses", err.Error())
		return
	}
	resp.Diagnostics.Append(setVPPInterfaceAddressesState(ctx, &plan, response)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vppInterfaceAddressesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vppInterfaceAddressesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Clear VPP interface addresses", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.Interfaces.UpdateVPPInterfaceAddresses(callCtx, &rookeryv1.UpdateVPPInterfaceAddressesRequest{
		Name: state.Name.ValueString(),
	}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Clear VPP interface addresses", err.Error())
	}
}

func (r *vppInterfaceAddressesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), types.StringValue(req.ID))...)
}

func setVPPInterfaceAddressesState(ctx context.Context, state *vppInterfaceAddressesModel, response *rookeryv1.VPPInterfaceAddresses) diag.Diagnostics {
	var diags diag.Diagnostics
	state.Name = types.StringValue(response.GetName())
	addresses, addressDiags := stringSet(ctx, response.GetAddresses())
	diags.Append(addressDiags...)
	state.Addresses = addresses
	state.DHCPClientEnabled = types.BoolValue(response.GetDhcpClientEnabled())
	live, liveDiags := stringList(ctx, response.GetLiveAddresses())
	diags.Append(liveDiags...)
	state.LiveAddresses = live
	state.DHCPClientConfigured = types.BoolValue(response.GetDhcpClientConfigured())
	state.DHCPClientStatus = optionalStringValue(response.GetDhcpClientStatus())
	state.StatusDetail = optionalStringValue(response.GetStatusDetail())
	return diags
}
