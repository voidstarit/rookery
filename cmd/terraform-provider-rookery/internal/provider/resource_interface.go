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
	"google.golang.org/protobuf/proto"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*interfaceResource)(nil)
	_ resource.ResourceWithConfigure   = (*interfaceResource)(nil)
	_ resource.ResourceWithImportState = (*interfaceResource)(nil)
)

type interfaceResource struct{ configuredResource }

// NewInterfaceResource creates the rookery_interface resource.
func NewInterfaceResource() resource.Resource {
	return &interfaceResource{}
}

type interfaceModel struct {
	ID             types.Int64  `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	NetworkID      types.Int64  `tfsdk:"network_id"`
	NetworkName    types.String `tfsdk:"network_name"`
	AttachedVMID   types.String `tfsdk:"attached_vm_id"`
	AttachedVMName types.String `tfsdk:"attached_vm_name"`
	SocketPath     types.String `tfsdk:"socket_path"`
	MAC            types.String `tfsdk:"mac"`
	SWIfIndex      types.Int64  `tfsdk:"sw_if_index"`
	IngressACLName types.String `tfsdk:"ingress_acl_name"`
	EgressACLName  types.String `tfsdk:"egress_acl_name"`
	IngressACLs    types.List   `tfsdk:"ingress_acls"`
	EgressACLs     types.List   `tfsdk:"egress_acls"`
	IPv4TableID    types.Int64  `tfsdk:"ipv4_table_id"`
	IPv6TableID    types.Int64  `tfsdk:"ipv6_table_id"`
	IPv4TableName  types.String `tfsdk:"ipv4_table_name"`
	IPv6TableName  types.String `tfsdk:"ipv6_table_name"`
	VPPAddresses   types.List   `tfsdk:"vpp_addresses"`
	Labels         types.Set    `tfsdk:"labels"`
}

func (r *interfaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_interface"
}

func (r *interfaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Rookery vhost-user interface in a network bridge domain.",
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id":     schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Interface name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"network_id": schema.Int64Attribute{
				Required:      true,
				Description:   "Network id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"network_name":     schema.StringAttribute{Computed: true},
			"attached_vm_id":   schema.StringAttribute{Optional: true, Description: "Optional VM UUID to attach this interface to."},
			"attached_vm_name": schema.StringAttribute{Computed: true},
			"socket_path":      schema.StringAttribute{Computed: true},
			"mac":              schema.StringAttribute{Computed: true},
			"sw_if_index":      schema.Int64Attribute{Computed: true},
			"ingress_acl_name": schema.StringAttribute{Computed: true},
			"egress_acl_name":  schema.StringAttribute{Computed: true},
			"ingress_acls": schema.ListAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Description: "Ordered ACL names applied from the interface (ingress).",
			},
			"egress_acls": schema.ListAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Description: "Ordered ACL names applied to the interface (egress).",
			},
			"ipv4_table_id":   schema.Int64Attribute{Optional: true, Computed: true, Description: "IPv4 route table id bound to this interface."},
			"ipv6_table_id":   schema.Int64Attribute{Optional: true, Computed: true, Description: "IPv6 route table id bound to this interface."},
			"ipv4_table_name": schema.StringAttribute{Computed: true},
			"ipv6_table_name": schema.StringAttribute{Computed: true},
			"vpp_addresses":   schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Live VPP-side addresses on the interface."},
		},
	}
}

func (r *interfaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan interfaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create interface", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	iface, err := client.Interfaces.CreateInterface(callCtx, &rookeryv1.CreateInterfaceRequest{
		Name:         plan.Name.ValueString(),
		NetworkId:    plan.NetworkID.ValueInt64(),
		AttachedVmId: stringPointer(plan.AttachedVMID),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create interface", err.Error())
		return
	}

	ingress, egress, aclConfigured, aclDiags := interfaceACLsFromPlan(ctx, plan, interfaceModel{})
	resp.Diagnostics.Append(aclDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if aclConfigured {
		iface, err = client.Interfaces.SetInterfaceACLs(callCtx, &rookeryv1.SetInterfaceACLsRequest{
			Id: iface.GetId(), IngressAcls: ingress, EgressAcls: egress,
		})
		if err != nil {
			resp.Diagnostics.AddError("Configure interface ACLs", err.Error())
			return
		}
	}
	if !plan.IPv4TableID.IsNull() && !plan.IPv4TableID.IsUnknown() || !plan.IPv6TableID.IsNull() && !plan.IPv6TableID.IsUnknown() {
		iface, err = client.RouteTables.SetInterfaceRouteTables(callCtx, &rookeryv1.SetInterfaceRouteTablesRequest{
			Id: iface.GetId(), Ipv4TableId: uint32(plan.IPv4TableID.ValueInt64()), Ipv6TableId: uint32(plan.IPv6TableID.ValueInt64()),
		})
		if err != nil {
			resp.Diagnostics.AddError("Configure interface route tables", err.Error())
			return
		}
	}
	resp.Diagnostics.Append(setInterfaceState(ctx, &plan, iface)...)
	entityLabels, err := applyEntityLabels(callCtx, client, "interface", fmt.Sprintf("%d", iface.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create interface labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *interfaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state interfaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read interface", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	iface, err := client.Interfaces.GetInterface(callCtx, &rookeryv1.GetInterfaceRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read interface", err.Error())
		return
	}
	resp.Diagnostics.Append(setInterfaceState(ctx, &state, iface)...)
	entityLabels, err := readEntityLabels(callCtx, client, "interface", fmt.Sprintf("%d", state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Read interface labels", err.Error())
		return
	}
	state.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *interfaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state interfaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update interface", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	iface, err := client.Interfaces.GetInterface(callCtx, &rookeryv1.GetInterfaceRequest{Id: plan.ID.ValueInt64()})
	if err != nil {
		resp.Diagnostics.AddError("Read interface before update", err.Error())
		return
	}
	if !plan.AttachedVMID.Equal(state.AttachedVMID) {
		value := ""
		if !plan.AttachedVMID.IsNull() && !plan.AttachedVMID.IsUnknown() {
			value = plan.AttachedVMID.ValueString()
		}
		iface, err = client.Interfaces.UpdateInterface(callCtx, &rookeryv1.UpdateInterfaceRequest{
			Id: plan.ID.ValueInt64(), AttachedVmId: proto.String(value),
		})
		if err != nil {
			resp.Diagnostics.AddError("Update interface attachment", err.Error())
			return
		}
	}
	ingress, egress, aclConfigured, aclDiags := interfaceACLsFromPlan(ctx, plan, state)
	resp.Diagnostics.Append(aclDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if aclConfigured && (!plan.IngressACLs.Equal(state.IngressACLs) || !plan.EgressACLs.Equal(state.EgressACLs)) {
		iface, err = client.Interfaces.SetInterfaceACLs(callCtx, &rookeryv1.SetInterfaceACLsRequest{
			Id: iface.GetId(), IngressAcls: ingress, EgressAcls: egress,
		})
		if err != nil {
			resp.Diagnostics.AddError("Update interface ACLs", err.Error())
			return
		}
	}
	if !plan.IPv4TableID.IsNull() && !plan.IPv4TableID.IsUnknown() && !plan.IPv6TableID.IsNull() && !plan.IPv6TableID.IsUnknown() &&
		(!plan.IPv4TableID.Equal(state.IPv4TableID) || !plan.IPv6TableID.Equal(state.IPv6TableID)) {
		iface, err = client.RouteTables.SetInterfaceRouteTables(callCtx, &rookeryv1.SetInterfaceRouteTablesRequest{
			Id: iface.GetId(), Ipv4TableId: uint32(plan.IPv4TableID.ValueInt64()), Ipv6TableId: uint32(plan.IPv6TableID.ValueInt64()),
		})
		if err != nil {
			resp.Diagnostics.AddError("Update interface route tables", err.Error())
			return
		}
	}
	resp.Diagnostics.Append(setInterfaceState(ctx, &plan, iface)...)
	entityLabels, err := applyEntityLabels(callCtx, client, "interface", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update interface labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *interfaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state interfaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete interface", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.Interfaces.DeleteInterface(callCtx, &rookeryv1.DeleteInterfaceRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete interface", err.Error())
	}
}

func (r *interfaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func setInterfaceState(ctx context.Context, state *interfaceModel, iface *rookeryv1.VirtualInterface) diag.Diagnostics {
	var diags diag.Diagnostics
	state.ID = types.Int64Value(iface.GetId())
	state.Name = types.StringValue(iface.GetName())
	state.NetworkID = types.Int64Value(iface.GetNetworkId())
	state.NetworkName = optionalStringValue(iface.GetNetworkName())
	state.AttachedVMID = optionalStringValue(iface.GetAttachedVmId())
	state.AttachedVMName = optionalStringValue(iface.GetAttachedVmName())
	state.SocketPath = optionalStringValue(iface.GetSocketPath())
	state.MAC = optionalStringValue(iface.GetMac())
	state.SWIfIndex = types.Int64Value(int64(iface.GetSwIfIndex()))
	state.IngressACLName = optionalStringValue(iface.GetIngressAclName())
	state.EgressACLName = optionalStringValue(iface.GetEgressAclName())
	ingress := iface.GetIngressAcls()
	if len(ingress) == 0 && iface.GetIngressAclName() != "" {
		ingress = []string{iface.GetIngressAclName()}
	}
	egress := iface.GetEgressAcls()
	if len(egress) == 0 && iface.GetEgressAclName() != "" {
		egress = []string{iface.GetEgressAclName()}
	}
	ingressList, ingressDiags := stringList(ctx, ingress)
	diags.Append(ingressDiags...)
	egressList, egressDiags := stringList(ctx, egress)
	diags.Append(egressDiags...)
	state.IngressACLs = ingressList
	state.EgressACLs = egressList
	state.IPv4TableID = types.Int64Value(int64(iface.GetIpv4TableId()))
	state.IPv6TableID = types.Int64Value(int64(iface.GetIpv6TableId()))
	state.IPv4TableName = optionalStringValue(iface.GetIpv4TableName())
	state.IPv6TableName = optionalStringValue(iface.GetIpv6TableName())
	addresses, addressDiags := stringList(ctx, iface.GetVppAddresses())
	diags.Append(addressDiags...)
	state.VPPAddresses = addresses
	return diags
}

func interfaceACLsFromPlan(ctx context.Context, plan, state interfaceModel) ([]string, []string, bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	ingress, ingressSet, ingressDiags := interfaceStringList(ctx, plan.IngressACLs)
	diags.Append(ingressDiags...)
	egress, egressSet, egressDiags := interfaceStringList(ctx, plan.EgressACLs)
	diags.Append(egressDiags...)
	if !ingressSet {
		if values, ok, d := interfaceStringList(ctx, state.IngressACLs); ok {
			ingress = values
			diags.Append(d...)
		}
	}
	if !egressSet {
		if values, ok, d := interfaceStringList(ctx, state.EgressACLs); ok {
			egress = values
			diags.Append(d...)
		}
	}
	return ingress, egress, ingressSet || egressSet, diags
}

func interfaceStringList(ctx context.Context, list types.List) ([]string, bool, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, false, nil
	}
	var values []string
	diags := list.ElementsAs(ctx, &values, false)
	return values, true, diags
}
