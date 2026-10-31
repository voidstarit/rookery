package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*aclResource)(nil)
	_ resource.ResourceWithConfigure   = (*aclResource)(nil)
	_ resource.ResourceWithImportState = (*aclResource)(nil)
)

type aclResource struct{ configuredResource }

// NewACLResource creates the rookery_acl resource.
func NewACLResource() resource.Resource {
	return &aclResource{}
}

type aclModel struct {
	ID          types.Int64  `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Title       types.String `tfsdk:"title"`
	Description types.String `tfsdk:"description"`
	Rules       types.List   `tfsdk:"rules"`
	VPPIndex    types.Int64  `tfsdk:"vpp_index"`
	Labels      types.Set    `tfsdk:"labels"`
}

type aclRuleModel struct {
	Action               types.String `tfsdk:"action"`
	Description          types.String `tfsdk:"description"`
	Protocol             types.String `tfsdk:"protocol"`
	Source               types.String `tfsdk:"source"`
	Destination          types.String `tfsdk:"destination"`
	SourcePortStart      types.Int64  `tfsdk:"source_port_start"`
	SourcePortEnd        types.Int64  `tfsdk:"source_port_end"`
	DestinationPortStart types.Int64  `tfsdk:"destination_port_start"`
	DestinationPortEnd   types.Int64  `tfsdk:"destination_port_end"`
}

var aclRuleAttrTypes = map[string]attr.Type{
	"action":                 types.StringType,
	"description":            types.StringType,
	"protocol":               types.StringType,
	"source":                 types.StringType,
	"destination":            types.StringType,
	"source_port_start":      types.Int64Type,
	"source_port_end":        types.Int64Type,
	"destination_port_start": types.Int64Type,
	"destination_port_end":   types.Int64Type,
}

var aclRuleObjectType = types.ObjectType{AttrTypes: aclRuleAttrTypes}

func (r *aclResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_acl"
}

func (r *aclResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A named ordered VPP ACL rule set.",
		Attributes: map[string]schema.Attribute{
			"labels":      labelsResourceAttribute(),
			"id":          schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name":        schema.StringAttribute{Required: true, Description: "ACL name."},
			"title":       schema.StringAttribute{Optional: true, Computed: true},
			"description": schema.StringAttribute{Optional: true, Computed: true},
			"vpp_index":   schema.Int64Attribute{Computed: true, Description: "VPP ACL index."},
			"rules": schema.ListNestedAttribute{
				Required:    true,
				Description: "Ordered rules evaluated from top to bottom.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"action":                 schema.StringAttribute{Required: true, Description: "permit or deny."},
						"description":            schema.StringAttribute{Optional: true, Computed: true},
						"protocol":               schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("any"), Description: "IP protocol: any, tcp, udp, icmp, or icmpv6."},
						"source":                 schema.StringAttribute{Required: true, Description: "Source CIDR."},
						"destination":            schema.StringAttribute{Required: true, Description: "Destination CIDR."},
						"source_port_start":      schema.Int64Attribute{Optional: true, Computed: true},
						"source_port_end":        schema.Int64Attribute{Optional: true, Computed: true},
						"destination_port_start": schema.Int64Attribute{Optional: true, Computed: true},
						"destination_port_end":   schema.Int64Attribute{Optional: true, Computed: true},
					},
				},
			},
		},
	}
}

func (r *aclResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan aclModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create ACL", err.Error())
		return
	}
	rules, diags := aclRulesFromList(ctx, plan.Rules)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	acl, err := client.ACLs.CreateACL(callCtx, &rookeryv1.CreateACLRequest{
		Name:        plan.Name.ValueString(),
		Title:       plan.Title.ValueString(),
		Description: plan.Description.ValueString(),
		Rules:       aclRulesToProto(rules),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create ACL", err.Error())
		return
	}
	resp.Diagnostics.Append(setACLState(ctx, &plan, acl)...)
	entityLabels, err := applyEntityLabels(callCtx, client, "acl", fmt.Sprintf("%d", acl.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create ACL labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *aclResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state aclModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read ACL", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	acl, err := client.ACLs.GetACL(callCtx, &rookeryv1.GetACLRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read ACL", err.Error())
		return
	}
	resp.Diagnostics.Append(setACLState(ctx, &state, acl)...)
	entityLabels, err := readEntityLabels(callCtx, client, "acl", fmt.Sprintf("%d", state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Read ACL labels", err.Error())
		return
	}
	state.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *aclResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan aclModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update ACL", err.Error())
		return
	}
	rules, diags := aclRulesFromList(ctx, plan.Rules)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	acl, err := client.ACLs.UpdateACL(callCtx, &rookeryv1.UpdateACLRequest{
		Id:          plan.ID.ValueInt64(),
		Name:        plan.Name.ValueString(),
		Title:       plan.Title.ValueString(),
		Description: plan.Description.ValueString(),
		Rules:       aclRulesToProto(rules),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update ACL", err.Error())
		return
	}
	resp.Diagnostics.Append(setACLState(ctx, &plan, acl)...)
	entityLabels, err := applyEntityLabels(callCtx, client, "acl", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update ACL labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *aclResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state aclModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete ACL", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.ACLs.DeleteACL(callCtx, &rookeryv1.DeleteACLRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete ACL", err.Error())
	}
}

func (r *aclResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func setACLState(ctx context.Context, state *aclModel, acl *rookeryv1.ACL) diag.Diagnostics {
	var diags diag.Diagnostics
	state.ID = types.Int64Value(acl.GetId())
	state.Name = types.StringValue(acl.GetName())
	state.Title = optionalStringValue(acl.GetTitle())
	state.Description = optionalStringValue(acl.GetDescription())
	state.VPPIndex = types.Int64Value(int64(acl.GetVppIndex()))
	rules, ruleDiags := aclRulesToList(ctx, acl.GetRules())
	diags.Append(ruleDiags...)
	state.Rules = rules
	return diags
}

func aclRulesFromList(ctx context.Context, list types.List) ([]aclRuleModel, diag.Diagnostics) {
	var rules []aclRuleModel
	diags := list.ElementsAs(ctx, &rules, false)
	return rules, diags
}

func aclRulesToProto(rules []aclRuleModel) []*rookeryv1.ACLRule {
	result := make([]*rookeryv1.ACLRule, 0, len(rules))
	for _, rule := range rules {
		result = append(result, &rookeryv1.ACLRule{
			Action:               rule.Action.ValueString(),
			Description:          rule.Description.ValueString(),
			Protocol:             rule.Protocol.ValueString(),
			Source:               rule.Source.ValueString(),
			Destination:          rule.Destination.ValueString(),
			SourcePortStart:      int32Value(rule.SourcePortStart),
			SourcePortEnd:        int32Value(rule.SourcePortEnd),
			DestinationPortStart: int32Value(rule.DestinationPortStart),
			DestinationPortEnd:   int32Value(rule.DestinationPortEnd),
		})
	}
	return result
}

func aclRulesToList(ctx context.Context, rules []*rookeryv1.ACLRule) (types.List, diag.Diagnostics) {
	models := make([]aclRuleModel, 0, len(rules))
	for _, rule := range rules {
		models = append(models, aclRuleModel{
			Action:               types.StringValue(rule.GetAction()),
			Description:          optionalStringValue(rule.GetDescription()),
			Protocol:             optionalStringValue(rule.GetProtocol()),
			Source:               types.StringValue(rule.GetSource()),
			Destination:          types.StringValue(rule.GetDestination()),
			SourcePortStart:      types.Int64Value(int64(rule.GetSourcePortStart())),
			SourcePortEnd:        types.Int64Value(int64(rule.GetSourcePortEnd())),
			DestinationPortStart: types.Int64Value(int64(rule.GetDestinationPortStart())),
			DestinationPortEnd:   types.Int64Value(int64(rule.GetDestinationPortEnd())),
		})
	}
	return types.ListValueFrom(ctx, aclRuleObjectType, models)
}
