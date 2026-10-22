package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/encoding/protojson"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*vnfResource)(nil)
	_ resource.ResourceWithConfigure   = (*vnfResource)(nil)
	_ resource.ResourceWithImportState = (*vnfResource)(nil)
)

type vnfResource struct{ configuredResource }

// NewVNFResource creates the rookery_vnf resource.
func NewVNFResource() resource.Resource {
	return &vnfResource{}
}

type vnfModel struct {
	ID           types.Int64  `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	NetworkID    types.Int64  `tfsdk:"network_id"`
	NetworkName  types.String `tfsdk:"network_name"`
	Type         types.String `tfsdk:"type"`
	Enabled      types.Bool   `tfsdk:"enabled"`
	ConfigJSON   types.String `tfsdk:"config_json"`
	TapSWIfIndex types.Int64  `tfsdk:"tap_sw_if_index"`
	TapHostName  types.String `tfsdk:"tap_host_name"`
	Status       types.String `tfsdk:"status"`
	StatusDetail types.String `tfsdk:"status_detail"`
	LeaseCount   types.Int64  `tfsdk:"lease_count"`
}

func (r *vnfResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vnf"
}

func (r *vnfResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A network function (DHCPv4, DHCPv6, NAT44, BGP, or DNS) attached to a Rookery network.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{Required: true, Description: "VNF name."},
			"network_id": schema.Int64Attribute{
				Required:      true,
				Description:   "Network id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"network_name": schema.StringAttribute{Computed: true},
			"type": schema.StringAttribute{
				Required:      true,
				Description:   "dhcpv4, dhcpv6, nat44, bgp, or dns.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"enabled": schema.BoolAttribute{Optional: true, Computed: true, Description: "Whether the VNF is active."},
			"config_json": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				Description: "VNF configuration as protobuf JSON. Use jsonencode({...}) with proto field names. Hidden in plans because it can contain BGP passwords.",
			},
			"tap_sw_if_index": schema.Int64Attribute{Computed: true},
			"tap_host_name":   schema.StringAttribute{Computed: true},
			"status":          schema.StringAttribute{Computed: true},
			"status_detail":   schema.StringAttribute{Computed: true},
			"lease_count":     schema.Int64Attribute{Computed: true},
		},
	}
}

func (r *vnfResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vnfModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create VNF", err.Error())
		return
	}
	config, err := vnfConfigFromJSON(plan.ConfigJSON)
	if err != nil {
		resp.Diagnostics.AddError("Create VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.VNFs.CreateVNF(callCtx, &rookeryv1.CreateVNFRequest{
		Name:      plan.Name.ValueString(),
		NetworkId: plan.NetworkID.ValueInt64(),
		Type:      plan.Type.ValueString(),
		Enabled:   boolPointer(plan.Enabled),
		Config:    config,
	})
	if err != nil {
		resp.Diagnostics.AddError("Create VNF", err.Error())
		return
	}
	resp.Diagnostics.Append(setVNFState(&plan, vnf)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vnfResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vnfModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.VNFs.GetVNF(callCtx, &rookeryv1.GetVNFRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read VNF", err.Error())
		return
	}
	resp.Diagnostics.Append(setVNFState(&state, vnf)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vnfResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vnfModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update VNF", err.Error())
		return
	}
	config, err := vnfConfigFromJSON(plan.ConfigJSON)
	if err != nil {
		resp.Diagnostics.AddError("Update VNF", err.Error())
		return
	}
	if config == nil && !state.ConfigJSON.IsNull() && state.ConfigJSON.ValueString() != "{}" {
		config = &rookeryv1.VNFConfig{}
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vnf, err := client.VNFs.UpdateVNF(callCtx, &rookeryv1.UpdateVNFRequest{
		Id:      plan.ID.ValueInt64(),
		Name:    stringPointer(plan.Name),
		Enabled: boolPointer(plan.Enabled),
		Config:  config,
	})
	if err != nil {
		resp.Diagnostics.AddError("Update VNF", err.Error())
		return
	}
	resp.Diagnostics.Append(setVNFState(&plan, vnf)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vnfResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vnfModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete VNF", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.VNFs.DeleteVNF(callCtx, &rookeryv1.DeleteVNFRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete VNF", err.Error())
	}
}

func (r *vnfResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func vnfConfigFromJSON(value types.String) (*rookeryv1.VNFConfig, error) {
	if value.IsNull() || value.IsUnknown() || strings.TrimSpace(value.ValueString()) == "" {
		return nil, nil
	}
	config := &rookeryv1.VNFConfig{}
	if err := protojson.Unmarshal([]byte(value.ValueString()), config); err != nil {
		return nil, fmt.Errorf("parse config_json: %w", err)
	}
	return config, nil
}

func setVNFState(state *vnfModel, vnf *rookeryv1.VNF) diag.Diagnostics {
	var diags diag.Diagnostics
	state.ID = types.Int64Value(vnf.GetId())
	state.Name = types.StringValue(vnf.GetName())
	state.NetworkID = types.Int64Value(vnf.GetNetworkId())
	state.NetworkName = optionalStringValue(vnf.GetNetworkName())
	state.Type = types.StringValue(vnf.GetType())
	state.Enabled = types.BoolValue(vnf.GetEnabled())
	configJSON := []byte("{}")
	if vnf.GetConfig() != nil {
		if encoded, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(vnf.GetConfig()); err == nil {
			configJSON = encoded
		} else {
			diags.AddError("Encode VNF config", err.Error())
		}
	}
	state.ConfigJSON = types.StringValue(string(configJSON))
	state.TapSWIfIndex = types.Int64Value(int64(vnf.GetTapSwIfIndex()))
	state.TapHostName = optionalStringValue(vnf.GetTapHostName())
	state.Status = optionalStringValue(vnf.GetStatus())
	state.StatusDetail = optionalStringValue(vnf.GetStatusDetail())
	state.LeaseCount = types.Int64Value(int64(vnf.GetLeaseCount()))
	return diags
}
