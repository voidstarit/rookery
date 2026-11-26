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
	_ resource.Resource                = (*dhcpStaticAssignmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*dhcpStaticAssignmentResource)(nil)
	_ resource.ResourceWithImportState = (*dhcpStaticAssignmentResource)(nil)
)

type dhcpStaticAssignmentResource struct{ configuredResource }

// NewDHCPStaticAssignmentResource creates the rookery_dhcp_static_assignment
// resource.
func NewDHCPStaticAssignmentResource() resource.Resource {
	return &dhcpStaticAssignmentResource{}
}

type dhcpStaticAssignmentModel struct {
	ID    types.Int64  `tfsdk:"id"`
	VNFID types.Int64  `tfsdk:"vnf_id"`
	MAC   types.String `tfsdk:"mac"`
	IP    types.String `tfsdk:"ip"`
}

// Metadata sets the resource type name to the provider name followed by
// "_dhcp_static_assignment".
func (r *dhcpStaticAssignmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dhcp_static_assignment"
}

// Schema declares one DHCPv4 static MAC-to-IP reservation.
func (r *dhcpStaticAssignmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A DHCPv4 static assignment owned by a rookery_dhcp_vnf.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"vnf_id": schema.Int64Attribute{
				Required:      true,
				Description:   "DHCP VNF id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"mac": schema.StringAttribute{Required: true, Description: "Client MAC address."},
			"ip":  schema.StringAttribute{Required: true, Description: "Reserved IPv4 address."},
		},
	}
}

// Create adds the static assignment to its DHCP VNF.
func (r *dhcpStaticAssignmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dhcpStaticAssignmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create DHCP static assignment", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	assignment, err := client.DHCPVNFs.CreateDHCPStaticAssignment(callCtx, &rookeryv1.CreateDHCPStaticAssignmentRequest{
		VnfId: plan.VNFID.ValueInt64(),
		Mac:   plan.MAC.ValueString(),
		Ip:    plan.IP.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create DHCP static assignment", err.Error())
		return
	}
	setDHCPStaticAssignmentState(&plan, assignment)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the static assignment, removing it from state when either the
// assignment or its parent is gone.
func (r *dhcpStaticAssignmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dhcpStaticAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read DHCP static assignment", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	assignment, err := client.DHCPVNFs.GetDHCPStaticAssignment(callCtx, &rookeryv1.GetDHCPStaticAssignmentRequest{
		VnfId:        state.VNFID.ValueInt64(),
		AssignmentId: state.ID.ValueInt64(),
	})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read DHCP static assignment", err.Error())
		return
	}
	setDHCPStaticAssignmentState(&state, assignment)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the MAC or IP address of the static assignment.
func (r *dhcpStaticAssignmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dhcpStaticAssignmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update DHCP static assignment", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	assignment, err := client.DHCPVNFs.UpdateDHCPStaticAssignment(callCtx, &rookeryv1.UpdateDHCPStaticAssignmentRequest{
		VnfId:        plan.VNFID.ValueInt64(),
		AssignmentId: plan.ID.ValueInt64(),
		Mac:          plan.MAC.ValueString(),
		Ip:           plan.IP.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update DHCP static assignment", err.Error())
		return
	}
	setDHCPStaticAssignmentState(&plan, assignment)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the static assignment, treating an already-absent assignment
// as success.
func (r *dhcpStaticAssignmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dhcpStaticAssignmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete DHCP static assignment", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.DHCPVNFs.DeleteDHCPStaticAssignment(callCtx, &rookeryv1.DeleteDHCPStaticAssignmentRequest{
		VnfId:        state.VNFID.ValueInt64(),
		AssignmentId: state.ID.ValueInt64(),
	}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete DHCP static assignment", err.Error())
	}
}

// ImportState adopts an existing static assignment by <vnf_id>:<assignment_id>.
func (r *dhcpStaticAssignmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importChildIDs(ctx, req, resp)
}

// setDHCPStaticAssignmentState copies the server's assignment into state.
func setDHCPStaticAssignmentState(state *dhcpStaticAssignmentModel, assignment *rookeryv1.DHCPStaticAssignment) {
	state.ID = types.Int64Value(assignment.GetId())
	state.VNFID = types.Int64Value(assignment.GetVnfId())
	state.MAC = types.StringValue(assignment.GetMac())
	state.IP = types.StringValue(assignment.GetIp())
}
