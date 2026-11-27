package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/dnsrecord"
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
// carry credentials. The TTL defaults to the API default so a plan and an apply
// agree on the value the server stores.
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
			"name": schema.StringAttribute{Required: true, Description: "Fully qualified domain name; wildcards are supported. The API stores the canonical trailing-dot form."},
			"type": schema.StringAttribute{Required: true, Description: "Record type: A, AAAA, CNAME, TXT, or PTR. The API stores it upper-case."},
			"value": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Record value; sensitive because TXT records can carry credentials. The API stores a CNAME or PTR target in canonical trailing-dot form.",
			},
			"ttl": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Default:       int64default.StaticInt64(int64(dnsrecord.DefaultTTL)),
				Description:   "Record TTL in seconds, between 1 and 86400. Defaults to 300.",
				PlanModifiers: []planmodifier.Int64{dnsRecordTTLPlanModifier{}},
			},
		},
	}
}

// dnsRecordTTLPlanModifier rejects a configured TTL the API would rewrite.
// Terraform core only accepts a planned value that equals the configuration or
// the prior state, so a TTL of 0 (the API's "use the default" sentinel) or one
// above the API limit fails the plan instead of being silently rewritten.
type dnsRecordTTLPlanModifier struct{}

// Description describes the TTL rule in the provider schema.
func (dnsRecordTTLPlanModifier) Description(context.Context) string {
	return "Requires a TTL between 1 and 86400; omit it to use the default 300."
}

// MarkdownDescription describes the TTL rule in the provider schema.
func (dnsRecordTTLPlanModifier) MarkdownDescription(context.Context) string {
	return "Requires a TTL between 1 and 86400; omit it to use the default 300."
}

// PlanModifyInt64 rejects a known TTL outside the range the API accepts. Null and
// unknown values pass through: an omitted TTL takes the schema default.
func (dnsRecordTTLPlanModifier) PlanModifyInt64(_ context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	if ttl := req.PlanValue.ValueInt64(); ttl < 1 || ttl > int64(dnsrecord.MaxTTL) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid DNS record TTL",
			fmt.Sprintf("ttl must be between 1 and %d; omit it to use the default %d seconds", dnsrecord.MaxTTL, dnsrecord.DefaultTTL))
	}
}

// canonicalDNSRecord is the form the API stores for one write.
type canonicalDNSRecord struct {
	Name  string
	Type  string
	Value string
	TTL   uint32
}

// canonicalDNSRecordFromModel validates and canonicalizes the configured fields
// for a create or update. The canonical form is only sent to the API; the
// configured spelling stays in state because Terraform rejects a plan that
// rewrites a configured value.
func canonicalDNSRecordFromModel(plan dnsRecordModel) (canonicalDNSRecord, error) {
	var canonical canonicalDNSRecord
	var err error
	if canonical.Name, err = dnsrecord.NormalizeName(plan.Name.ValueString()); err != nil {
		return canonicalDNSRecord{}, fmt.Errorf("name: %w", err)
	}
	if canonical.Type, err = dnsrecord.NormalizeType(plan.Type.ValueString()); err != nil {
		// The message already names the field, so wrapping it would read
		// "type: type must be ...".
		return canonicalDNSRecord{}, err
	}
	if canonical.Value, err = dnsrecord.NormalizeValue(canonical.Type, plan.Value.ValueString()); err != nil {
		return canonicalDNSRecord{}, fmt.Errorf("value: %w", err)
	}
	if canonical.TTL, err = dnsrecord.NormalizeTTL(uint32Value(plan.TTL)); err != nil {
		return canonicalDNSRecord{}, err
	}
	return canonical, nil
}

// Create adds the record to its DNS VNF.
func (r *dnsRecordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dnsRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	canonical, err := canonicalDNSRecordFromModel(plan)
	if err != nil {
		resp.Diagnostics.AddError("Create DNS record", err.Error())
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
		Name:  canonical.Name,
		Type:  canonical.Type,
		Value: canonical.Value,
		Ttl:   canonical.TTL,
	})
	if err != nil {
		resp.Diagnostics.AddError("Create DNS record", err.Error())
		return
	}
	applyDNSRecordResponse(&plan, record)
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
	state = reconcileDNSRecordState(state, record)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the record name, type, value, or TTL.
func (r *dnsRecordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dnsRecordModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	canonical, err := canonicalDNSRecordFromModel(plan)
	if err != nil {
		resp.Diagnostics.AddError("Update DNS record", err.Error())
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
		Name:     canonical.Name,
		Type:     canonical.Type,
		Value:    canonical.Value,
		Ttl:      canonical.TTL,
	})
	if err != nil {
		resp.Diagnostics.AddError("Update DNS record", err.Error())
		return
	}
	applyDNSRecordResponse(&plan, record)
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

// applyDNSRecordResponse stores the server identity and TTL in state while
// keeping the configured spelling of name, type, and value.
func applyDNSRecordResponse(state *dnsRecordModel, record *rookeryv1.DNSRecord) {
	state.ID = types.Int64Value(record.GetId())
	state.VNFID = types.Int64Value(record.GetVnfId())
	state.TTL = types.Int64Value(int64(record.GetTtl()))
}

// reconcileDNSRecordState merges a server record into state. It keeps the
// configured spelling of name, type, and value when that spelling still
// canonicalizes to the server's record, so a configuration without the canonical
// form does not show a perpetual diff. It overwrites a field when it is empty
// (import) or when the server value really changed (drift).
func reconcileDNSRecordState(state dnsRecordModel, record *rookeryv1.DNSRecord) dnsRecordModel {
	state.ID = types.Int64Value(record.GetId())
	state.VNFID = types.Int64Value(record.GetVnfId())
	state.TTL = types.Int64Value(int64(record.GetTtl()))

	nameMatches := false
	if !state.Name.IsNull() && !state.Name.IsUnknown() {
		if name, err := dnsrecord.NormalizeName(state.Name.ValueString()); err == nil {
			nameMatches = name == record.GetName()
		}
	}
	if !nameMatches {
		state.Name = types.StringValue(record.GetName())
	}

	typeMatches := false
	if !state.Type.IsNull() && !state.Type.IsUnknown() {
		if recordType, err := dnsrecord.NormalizeType(state.Type.ValueString()); err == nil {
			typeMatches = recordType == record.GetType()
		}
	}
	if !typeMatches {
		state.Type = types.StringValue(record.GetType())
	}

	valueMatches := false
	if !state.Value.IsNull() && !state.Value.IsUnknown() {
		if value, err := dnsrecord.NormalizeValue(record.GetType(), state.Value.ValueString()); err == nil {
			valueMatches = value == record.GetValue()
		}
	}
	if !valueMatches {
		state.Value = types.StringValue(record.GetValue())
	}
	return state
}
