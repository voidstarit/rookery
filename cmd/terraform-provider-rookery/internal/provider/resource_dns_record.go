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
	_ resource.Resource                = (*dnsRecordResource)(nil)
	_ resource.ResourceWithConfigure   = (*dnsRecordResource)(nil)
	_ resource.ResourceWithImportState = (*dnsRecordResource)(nil)
)

type dnsRecordResource struct{ configuredResource }

// NewDNSRecordResource creates the rookery_dns_record resource.
func NewDNSRecordResource() resource.Resource {
	return &dnsRecordResource{}
}

type dnsRecordModel struct {
	ID    types.Int64  `tfsdk:"id"`
	VNFID types.Int64  `tfsdk:"vnf_id"`
	Name  types.String `tfsdk:"name"`
	Type  types.String `tfsdk:"type"`
	Value types.String `tfsdk:"value"`
	TTL   types.Int64  `tfsdk:"ttl"`
}

// Metadata sets the resource type name to the provider name followed by
// "_dns_record".
func (r *dnsRecordResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_record"
}

// Schema declares one DNS record. The value is sensitive because TXT records can
// carry credentials.
func (r *dnsRecordResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A DNS record owned by a rookery_dns_vnf.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"vnf_id": schema.Int64Attribute{
				Required:      true,
				Description:   "DNS VNF id.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{Required: true, Description: "Fully qualified domain name; wildcards are supported."},
			"type": schema.StringAttribute{Required: true, Description: "Record type: A, AAAA, CNAME, TXT, or PTR."},
			"value": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Record value; sensitive because TXT records can carry credentials.",
			},
			"ttl": schema.Int64Attribute{Optional: true, Computed: true, Description: "Record TTL in seconds. Defaults to 300."},
		},
	}
}

// Create adds the record to its DNS VNF.
func (r *dnsRecordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dnsRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create DNS record", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	record, err := client.DNSVNFs.CreateDNSVNFRecord(callCtx, &rookeryv1.CreateDNSVNFRecordRequest{
		VnfId: plan.VNFID.ValueInt64(),
		Name:  plan.Name.ValueString(),
		Type:  plan.Type.ValueString(),
		Value: plan.Value.ValueString(),
		Ttl:   uint32Value(plan.TTL),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create DNS record", err.Error())
		return
	}
	setDNSRecordState(&plan, record)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the record, removing it from state when either the record or
// its parent is gone.
func (r *dnsRecordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dnsRecordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read DNS record", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	record, err := client.DNSVNFs.GetDNSVNFRecord(callCtx, &rookeryv1.GetDNSVNFRecordRequest{
		VnfId:    state.VNFID.ValueInt64(),
		RecordId: state.ID.ValueInt64(),
	})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read DNS record", err.Error())
		return
	}
	setDNSRecordState(&state, record)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the record name, type, value, or TTL.
func (r *dnsRecordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dnsRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update DNS record", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	record, err := client.DNSVNFs.UpdateDNSVNFRecord(callCtx, &rookeryv1.UpdateDNSVNFRecordRequest{
		VnfId:    plan.VNFID.ValueInt64(),
		RecordId: plan.ID.ValueInt64(),
		Name:     plan.Name.ValueString(),
		Type:     plan.Type.ValueString(),
		Value:    plan.Value.ValueString(),
		Ttl:      uint32Value(plan.TTL),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update DNS record", err.Error())
		return
	}
	setDNSRecordState(&plan, record)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the record, treating an already-absent record as success.
func (r *dnsRecordResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dnsRecordModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete DNS record", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.DNSVNFs.DeleteDNSVNFRecord(callCtx, &rookeryv1.DeleteDNSVNFRecordRequest{
		VnfId:    state.VNFID.ValueInt64(),
		RecordId: state.ID.ValueInt64(),
	}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete DNS record", err.Error())
	}
}

// ImportState adopts an existing record by <vnf_id>:<record_id>.
func (r *dnsRecordResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importChildIDs(ctx, req, resp)
}

// setDNSRecordState copies the server's record into state.
func setDNSRecordState(state *dnsRecordModel, record *rookeryv1.DNSRecord) {
	state.ID = types.Int64Value(record.GetId())
	state.VNFID = types.Int64Value(record.GetVnfId())
	state.Name = types.StringValue(record.GetName())
	state.Type = types.StringValue(record.GetType())
	state.Value = types.StringValue(record.GetValue())
	state.TTL = types.Int64Value(int64(record.GetTtl()))
}
