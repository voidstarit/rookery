package provider

import (
	"context"
	"errors"

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
	_ resource.Resource                = (*hostInterfaceResource)(nil)
	_ resource.ResourceWithConfigure   = (*hostInterfaceResource)(nil)
	_ resource.ResourceWithImportState = (*hostInterfaceResource)(nil)
)

type hostInterfaceResource struct{ configuredResource }

// NewHostInterfaceResource creates the rookery_host_interface resource.
func NewHostInterfaceResource() resource.Resource {
	return &hostInterfaceResource{}
}

type hostInterfaceModel struct {
	NIC          types.String `tfsdk:"nic"`
	Up           types.Bool   `tfsdk:"up"`
	Addresses    types.List   `tfsdk:"addresses"`
	DefaultRoute types.Bool   `tfsdk:"default_route"`
	Physical     types.Bool   `tfsdk:"physical"`
	Wireless     types.Bool   `tfsdk:"wireless"`
	InVPP        types.Bool   `tfsdk:"in_vpp"`
	VPPChecked   types.Bool   `tfsdk:"vpp_checked"`
	VPPName      types.String `tfsdk:"vpp_name"`
	VPPUp        types.Bool   `tfsdk:"vpp_up"`
	VPPType      types.String `tfsdk:"vpp_type"`
	Managed      types.Bool   `tfsdk:"managed"`
}

func (r *hostInterfaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_host_interface"
}

func (r *hostInterfaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Takes a physical host NIC into VPP. Destroying the resource detaches it from VPP.",
		Attributes: map[string]schema.Attribute{
			"nic": schema.StringAttribute{
				Required:      true,
				Description:   "Host network interface name, for example enp1s0.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"up":            schema.BoolAttribute{Computed: true},
			"addresses":     schema.ListAttribute{Computed: true, ElementType: types.StringType},
			"default_route": schema.BoolAttribute{Computed: true},
			"physical":      schema.BoolAttribute{Computed: true},
			"wireless":      schema.BoolAttribute{Computed: true},
			"in_vpp":        schema.BoolAttribute{Computed: true},
			"vpp_checked":   schema.BoolAttribute{Computed: true},
			"vpp_name":      schema.StringAttribute{Computed: true},
			"vpp_up":        schema.BoolAttribute{Computed: true},
			"vpp_type":      schema.StringAttribute{Computed: true},
			"managed":       schema.BoolAttribute{Computed: true},
		},
	}
}

func (r *hostInterfaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan hostInterfaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Attach host interface", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	iface, err := client.HostInterfaces.AttachHostInterface(callCtx, &rookeryv1.AttachHostInterfaceRequest{Name: plan.NIC.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Attach host interface", err.Error())
		return
	}
	resp.Diagnostics.Append(setHostInterfaceState(ctx, &plan, iface)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *hostInterfaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state hostInterfaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read host interface", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.HostInterfaces.ListHostInterfaces(callCtx, &rookeryv1.ListHostInterfacesRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Read host interface", err.Error())
		return
	}
	for _, iface := range response.GetInterfaces() {
		if iface.GetName() == state.NIC.ValueString() {
			resp.Diagnostics.Append(setHostInterfaceState(ctx, &state, iface)...)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *hostInterfaceResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update host interface", errors.New("host interface attachments are immutable; replace the resource instead").Error())
}

func (r *hostInterfaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state hostInterfaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Detach host interface", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.HostInterfaces.DetachHostInterface(callCtx, &rookeryv1.DetachHostInterfaceRequest{Name: state.NIC.ValueString()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Detach host interface", err.Error())
	}
}

func (r *hostInterfaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("nic"), types.StringValue(req.ID))...)
}

func setHostInterfaceState(ctx context.Context, state *hostInterfaceModel, iface *rookeryv1.HostInterface) diag.Diagnostics {
	var diags diag.Diagnostics
	state.NIC = types.StringValue(iface.GetName())
	state.Up = types.BoolValue(iface.GetUp())
	addresses, addressDiags := stringList(ctx, iface.GetAddresses())
	diags.Append(addressDiags...)
	state.Addresses = addresses
	state.DefaultRoute = types.BoolValue(iface.GetDefaultRoute())
	state.Physical = types.BoolValue(iface.GetPhysical())
	state.Wireless = types.BoolValue(iface.GetWireless())
	state.InVPP = types.BoolValue(iface.GetInVpp())
	state.VPPChecked = types.BoolValue(iface.GetVppChecked())
	state.VPPName = optionalStringValue(iface.GetVppName())
	state.VPPUp = types.BoolValue(iface.GetVppUp())
	state.VPPType = optionalStringValue(iface.GetVppType())
	state.Managed = types.BoolValue(iface.GetManaged())
	return diags
}
