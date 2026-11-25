package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/proto"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*lightVMTemplateResource)(nil)
	_ resource.ResourceWithConfigure   = (*lightVMTemplateResource)(nil)
	_ resource.ResourceWithImportState = (*lightVMTemplateResource)(nil)
)

type lightVMTemplateResource struct{ configuredResource }

func NewLightVMTemplateResource() resource.Resource {
	return &lightVMTemplateResource{}
}

type lightVMTemplateModel struct {
	ID               types.Int64              `tfsdk:"id"`
	Name             types.String             `tfsdk:"name"`
	Description      types.String             `tfsdk:"description"`
	CurrentVersionID types.Int64              `tfsdk:"current_version_id"`
	LatestVersionID  types.Int64              `tfsdk:"latest_version_id"`
	LatestVersion    types.Int64              `tfsdk:"latest_version"`
	VersionCount     types.Int64              `tfsdk:"version_count"`
	Spec             lightVMTemplateSpecModel `tfsdk:"spec"`
}

type lightVMTemplateSpecModel struct {
	VCPUs            types.Int64  `tfsdk:"vcpus"`
	MemoryMB         types.Int64  `tfsdk:"memory_mb"`
	CgroupClass      types.String `tfsdk:"cgroup_class"`
	KernelID         types.Int64  `tfsdk:"kernel_id"`
	InitramfsID      types.Int64  `tfsdk:"initramfs_id"`
	Image            types.String `tfsdk:"image"`
	Env              types.List   `tfsdk:"env"`
	ExecutablePath   types.String `tfsdk:"executable_path"`
	ExecutableScript types.String `tfsdk:"executable_script"`
	ExecutableArgs   types.List   `tfsdk:"executable_args"`
	Append           types.String `tfsdk:"append"`
	ConsoleEnabled   types.Bool   `tfsdk:"console_enabled"`
	Disks            types.List   `tfsdk:"disks"`
	Interfaces       types.List   `tfsdk:"interfaces"`
}

type lightVMTemplateDiskModel struct {
	StorageBackendID types.Int64  `tfsdk:"storage_backend_id"`
	SizeGB           types.Int64  `tfsdk:"size_gb"`
	ThrottleClassID  types.Int64  `tfsdk:"throttle_class_id"`
	Serial           types.String `tfsdk:"serial"`
}

type lightVMTemplateInterfaceModel struct {
	NetworkID     types.Int64 `tfsdk:"network_id"`
	IngressACLIDs types.List  `tfsdk:"ingress_acl_ids"`
	EgressACLIDs  types.List  `tfsdk:"egress_acl_ids"`
}

func (r *lightVMTemplateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lightvm_template"
}

func (r *lightVMTemplateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An immutable-versioned LightVM template used by managed groups. Editing the spec creates a new version.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name":               schema.StringAttribute{Required: true, Description: "Template name."},
			"description":        schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"current_version_id": schema.Int64Attribute{Computed: true, Description: "Version this resource created or adopted."},
			"latest_version_id":  schema.Int64Attribute{Computed: true, Description: "Newest version id returned by the server."},
			"latest_version":     schema.Int64Attribute{Computed: true, Description: "Newest version number."},
			"version_count":      schema.Int64Attribute{Computed: true, Description: "Number of versions."},
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"vcpus":        schema.Int64Attribute{Required: true, Description: "Virtual CPUs."},
					"memory_mb":    schema.Int64Attribute{Required: true, Description: "Memory in MiB."},
					"cgroup_class": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
					"kernel_id":    schema.Int64Attribute{Required: true},
					"initramfs_id": schema.Int64Attribute{Required: true},
					"image":        schema.StringAttribute{Required: true, Description: "Digest-qualified container image."},
					"env": schema.ListNestedAttribute{
						Optional: true, Computed: true, PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"name":  schema.StringAttribute{Required: true},
							"value": schema.StringAttribute{Optional: true, Computed: true},
						}},
					},
					"executable_path":   schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
					"executable_script": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
					"executable_args": schema.ListAttribute{
						Optional: true, Computed: true, ElementType: types.StringType,
						PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
					},
					"append":          schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
					"console_enabled": schema.BoolAttribute{Optional: true, Computed: true},
					"disks": schema.ListNestedAttribute{
						Optional: true, Computed: true, PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"storage_backend_id": schema.Int64Attribute{Required: true},
							"size_gb":            schema.Int64Attribute{Required: true},
							"throttle_class_id":  schema.Int64Attribute{Optional: true},
							"serial":             schema.StringAttribute{Optional: true, Computed: true},
						}},
					},
					"interfaces": schema.ListNestedAttribute{
						Optional: true, Computed: true, PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"network_id": schema.Int64Attribute{Required: true},
							"ingress_acl_ids": schema.ListAttribute{
								Optional: true, Computed: true, ElementType: types.Int64Type,
								PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
							},
							"egress_acl_ids": schema.ListAttribute{
								Optional: true, Computed: true, ElementType: types.Int64Type,
								PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
							},
						}},
					},
				},
			},
		},
	}
}

func (r *lightVMTemplateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan lightVMTemplateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create LightVM template", err.Error())
		return
	}
	spec, diags := lightVMTemplateSpecFromModel(ctx, plan.Spec)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	template, err := client.LightVMTemplates.CreateLightVMTemplate(callCtx, &rookeryv1.CreateLightVMTemplateRequest{
		Name: plan.Name.ValueString(), Description: plan.Description.ValueString(), Spec: spec,
	})
	if err != nil {
		resp.Diagnostics.AddError("Create LightVM template", err.Error())
		return
	}
	resp.Diagnostics.Append(setLightVMTemplateState(ctx, &plan, template, spec)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lightVMTemplateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state lightVMTemplateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read LightVM template", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	template, err := client.LightVMTemplates.GetLightVMTemplate(callCtx, &rookeryv1.GetLightVMTemplateRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read LightVM template", err.Error())
		return
	}
	versionID := state.CurrentVersionID.ValueInt64()
	if versionID == 0 {
		versionID = template.GetLatestVersionId()
	}
	version, err := client.LightVMTemplates.GetLightVMTemplateVersion(callCtx, &rookeryv1.GetLightVMTemplateVersionRequest{
		Id: template.GetId(), VersionId: versionID,
	})
	if err != nil {
		resp.Diagnostics.AddError("Read LightVM template version", err.Error())
		return
	}
	resp.Diagnostics.Append(setLightVMTemplateState(ctx, &state, template, version.GetSpec())...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *lightVMTemplateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan lightVMTemplateModel
	var state lightVMTemplateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update LightVM template", err.Error())
		return
	}
	spec, diags := lightVMTemplateSpecFromModel(ctx, plan.Spec)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	template, err := client.LightVMTemplates.UpdateLightVMTemplate(callCtx, &rookeryv1.UpdateLightVMTemplateRequest{
		Id: plan.ID.ValueInt64(), Name: stringPointer(plan.Name), Description: stringPointer(plan.Description),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update LightVM template", err.Error())
		return
	}
	currentVersionID := state.CurrentVersionID.ValueInt64()
	versionID := currentVersionID
	if currentVersionID == 0 {
		versionID = template.GetLatestVersionId()
	}
	version, err := client.LightVMTemplates.GetLightVMTemplateVersion(callCtx, &rookeryv1.GetLightVMTemplateVersionRequest{
		Id: template.GetId(), VersionId: versionID,
	})
	if err != nil {
		resp.Diagnostics.AddError("Read current LightVM template version", err.Error())
		return
	}
	if !proto.Equal(version.GetSpec(), spec) {
		created, err := client.LightVMTemplates.CreateLightVMTemplateVersion(callCtx, &rookeryv1.CreateLightVMTemplateVersionRequest{
			Id: template.GetId(), Spec: spec,
		})
		if err != nil {
			resp.Diagnostics.AddError("Create LightVM template version", err.Error())
			return
		}
		versionID = created.GetId()
	}
	template, err = client.LightVMTemplates.GetLightVMTemplate(callCtx, &rookeryv1.GetLightVMTemplateRequest{Id: template.GetId()})
	if err != nil {
		resp.Diagnostics.AddError("Read LightVM template", err.Error())
		return
	}
	resp.Diagnostics.Append(setLightVMTemplateState(ctx, &plan, template, spec)...)
	plan.CurrentVersionID = types.Int64Value(versionID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lightVMTemplateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state lightVMTemplateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete LightVM template", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.LightVMTemplates.DeleteLightVMTemplate(callCtx, &rookeryv1.DeleteLightVMTemplateRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete LightVM template", err.Error())
	}
}

func (r *lightVMTemplateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func setLightVMTemplateState(ctx context.Context, model *lightVMTemplateModel, template *rookeryv1.LightVMTemplate, spec *rookeryv1.LightVMTemplateSpec) diag.Diagnostics {
	model.ID = types.Int64Value(template.GetId())
	model.Name = types.StringValue(template.GetName())
	model.Description = types.StringValue(template.GetDescription())
	model.LatestVersionID = types.Int64Value(template.GetLatestVersionId())
	model.LatestVersion = types.Int64Value(int64(template.GetLatestVersion()))
	model.VersionCount = types.Int64Value(int64(template.GetVersionCount()))
	if model.CurrentVersionID.IsNull() || model.CurrentVersionID.ValueInt64() == 0 {
		model.CurrentVersionID = types.Int64Value(template.GetLatestVersionId())
	}
	return setLightVMTemplateSpecState(ctx, &model.Spec, spec)
}

func setLightVMTemplateSpecState(_ context.Context, model *lightVMTemplateSpecModel, spec *rookeryv1.LightVMTemplateSpec) diag.Diagnostics {
	model.VCPUs = types.Int64Value(int64(spec.GetVcpus()))
	model.MemoryMB = types.Int64Value(int64(spec.GetMemoryMb()))
	model.CgroupClass = types.StringValue(spec.GetCgroupClass())
	model.KernelID = types.Int64Value(spec.GetKernelId())
	model.InitramfsID = types.Int64Value(spec.GetInitramfsId())
	model.Image = types.StringValue(spec.GetImage())
	model.ExecutablePath = types.StringValue(spec.GetExecutablePath())
	model.ExecutableScript = types.StringValue(spec.GetExecutableScript())
	model.Append = types.StringValue(spec.GetAppend())
	model.ConsoleEnabled = types.BoolValue(spec.GetConsoleEnabled())
	return nil
}

func lightVMTemplateSpecFromModel(ctx context.Context, model lightVMTemplateSpecModel) (*rookeryv1.LightVMTemplateSpec, diag.Diagnostics) {
	var diags diag.Diagnostics
	env, envDiags := lightVMEnvFromModel(ctx, model.Env)
	diags.Append(envDiags...)
	args, argDiags := stringListValues(ctx, model.ExecutableArgs)
	diags.Append(argDiags...)
	var disks []*rookeryv1.TemplateDiskEntry
	if !model.Disks.IsNull() && !model.Disks.IsUnknown() {
		var values []lightVMTemplateDiskModel
		diags.Append(model.Disks.ElementsAs(ctx, &values, false)...)
		for _, value := range values {
			entry := &rookeryv1.TemplateDiskEntry{
				StorageBackendId: value.StorageBackendID.ValueInt64(),
				SizeGb:           int32(value.SizeGB.ValueInt64()),
				Serial:           value.Serial.ValueString(),
			}
			if !value.ThrottleClassID.IsNull() && !value.ThrottleClassID.IsUnknown() {
				id := value.ThrottleClassID.ValueInt64()
				entry.ThrottleClassId = &id
			}
			disks = append(disks, entry)
		}
	}
	var interfaces []*rookeryv1.TemplateInterfaceEntry
	if !model.Interfaces.IsNull() && !model.Interfaces.IsUnknown() {
		var values []lightVMTemplateInterfaceModel
		diags.Append(model.Interfaces.ElementsAs(ctx, &values, false)...)
		for _, value := range values {
			ingress, ingressDiags := int64ListValues(ctx, value.IngressACLIDs)
			diags.Append(ingressDiags...)
			egress, egressDiags := int64ListValues(ctx, value.EgressACLIDs)
			diags.Append(egressDiags...)
			interfaces = append(interfaces, &rookeryv1.TemplateInterfaceEntry{
				NetworkId: value.NetworkID.ValueInt64(), IngressAclIds: ingress, EgressAclIds: egress,
			})
		}
	}
	appendValue := model.Append.ValueString()
	if appendValue == "" {
		appendValue = "console=hvc0 rdinit=/init"
	}
	return &rookeryv1.LightVMTemplateSpec{
		Vcpus: int32(model.VCPUs.ValueInt64()), MemoryMb: int32(model.MemoryMB.ValueInt64()),
		CgroupClass: model.CgroupClass.ValueString(), KernelId: model.KernelID.ValueInt64(),
		InitramfsId: model.InitramfsID.ValueInt64(), Image: model.Image.ValueString(),
		Env: env, ExecutablePath: model.ExecutablePath.ValueString(),
		ExecutableScript: model.ExecutableScript.ValueString(), ExecutableArgs: args,
		Append: appendValue, ConsoleEnabled: model.ConsoleEnabled.ValueBool(),
		Consoles: []*rookeryv1.LightVMConsole{{Name: "console"}},
		Disks:    disks, Interfaces: interfaces,
	}, diags
}

func int64ListValues(ctx context.Context, list types.List) ([]int64, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var values []int64
	diags := list.ElementsAs(ctx, &values, false)
	return values, diags
}
