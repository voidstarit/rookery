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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*storageBackendResource)(nil)
	_ resource.ResourceWithConfigure   = (*storageBackendResource)(nil)
	_ resource.ResourceWithImportState = (*storageBackendResource)(nil)
)

type storageBackendResource struct{ configuredResource }

// NewStorageBackendResource creates the rookery_storage_backend resource.
func NewStorageBackendResource() resource.Resource {
	return &storageBackendResource{}
}

type storageBackendModel struct {
	ID          types.Int64  `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Title       types.String `tfsdk:"title"`
	Description types.String `tfsdk:"description"`
	Driver      types.String `tfsdk:"driver"`
	Config      types.Object `tfsdk:"config"`
	DiskCount   types.Int64  `tfsdk:"disk_count"`
	Labels      types.Set    `tfsdk:"labels"`
}

type storageBackendConfigModel struct {
	VG                            types.String `tfsdk:"vg"`
	ThinPool                      types.String `tfsdk:"thin_pool"`
	SupportsTemplates             types.Bool   `tfsdk:"supports_templates"`
	PreparationScript             types.String `tfsdk:"preparation_script"`
	PreparationTimeout            types.String `tfsdk:"preparation_timeout"`
	AllowTemplateDeleteWithClones types.Bool   `tfsdk:"allow_template_delete_with_clones"`
	Kouja                         types.Object `tfsdk:"kouja"`
}

type koujaConfigModel struct {
	Endpoint       types.String `tfsdk:"endpoint"`
	Protocol       types.String `tfsdk:"protocol"`
	StorageClass   types.String `tfsdk:"storage_class"`
	Initiator      types.String `tfsdk:"initiator"`
	HostNQN        types.String `tfsdk:"host_nqn"`
	CAFile         types.String `tfsdk:"ca_file"`
	ClientCertFile types.String `tfsdk:"client_cert_file"`
	ClientKeyFile  types.String `tfsdk:"client_key_file"`
	ServerName     types.String `tfsdk:"server_name"`
	Portal         types.String `tfsdk:"portal"`
}

var koujaConfigAttrTypes = map[string]attr.Type{
	"endpoint":         types.StringType,
	"protocol":         types.StringType,
	"storage_class":    types.StringType,
	"initiator":        types.StringType,
	"host_nqn":         types.StringType,
	"ca_file":          types.StringType,
	"client_cert_file": types.StringType,
	"client_key_file":  types.StringType,
	"server_name":      types.StringType,
	"portal":           types.StringType,
}

var storageBackendConfigAttrTypes = map[string]attr.Type{
	"vg":                                types.StringType,
	"thin_pool":                         types.StringType,
	"supports_templates":                types.BoolType,
	"preparation_script":                types.StringType,
	"preparation_timeout":               types.StringType,
	"allow_template_delete_with_clones": types.BoolType,
	"kouja":                             types.ObjectType{AttrTypes: koujaConfigAttrTypes},
}

func (r *storageBackendResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_backend"
}

func (r *storageBackendResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Rookery storage backend. Supports lvm-thin and kouja drivers.",
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id": schema.Int64Attribute{
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"name":        schema.StringAttribute{Required: true, Description: "Backend name."},
			"title":       schema.StringAttribute{Optional: true, Computed: true, Description: "Human-readable title."},
			"description": schema.StringAttribute{Optional: true, Computed: true, Description: "Backend description."},
			"driver": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Storage driver: lvm-thin or kouja. Defaults to lvm-thin.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"config": schema.SingleNestedAttribute{
				Required:    true,
				Description: "Driver-specific configuration. Use vg/thin_pool for LVM or kouja for a kouja backend.",
				Attributes: map[string]schema.Attribute{
					"vg":                  schema.StringAttribute{Optional: true, Computed: true, Description: "Volume group for lvm-thin."},
					"thin_pool":           schema.StringAttribute{Optional: true, Computed: true, Description: "Thin pool for lvm-thin."},
					"supports_templates":  schema.BoolAttribute{Optional: true, Computed: true, Description: "Allow template disks on this backend."},
					"preparation_script":  schema.StringAttribute{Optional: true, Computed: true, Description: "Script used to clone template disks."},
					"preparation_timeout": schema.StringAttribute{Optional: true, Computed: true, Description: "Preparation script timeout."},
					"allow_template_delete_with_clones": schema.BoolAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Allow deleting a template while clones exist.",
					},
					"kouja": schema.SingleNestedAttribute{
						Optional:    true,
						Description: "Kouja endpoint, storage class, and mTLS client configuration.",
						Attributes: map[string]schema.Attribute{
							"endpoint":         schema.StringAttribute{Optional: true, Computed: true},
							"protocol":         schema.StringAttribute{Optional: true, Computed: true},
							"storage_class":    schema.StringAttribute{Optional: true, Computed: true},
							"initiator":        schema.StringAttribute{Optional: true, Computed: true},
							"host_nqn":         schema.StringAttribute{Optional: true, Computed: true},
							"ca_file":          schema.StringAttribute{Optional: true, Computed: true},
							"client_cert_file": schema.StringAttribute{Optional: true, Computed: true},
							"client_key_file":  schema.StringAttribute{Optional: true, Computed: true},
							"server_name":      schema.StringAttribute{Optional: true, Computed: true},
							"portal":           schema.StringAttribute{Optional: true, Computed: true},
						},
					},
				},
			},
			"disk_count": schema.Int64Attribute{Computed: true, Description: "Number of disks on this backend."},
		},
	}
}

func (r *storageBackendResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan storageBackendModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create storage backend", err.Error())
		return
	}
	config, diags := storageBackendConfigFromObject(ctx, plan.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	backend, err := client.StorageBackends.CreateStorageBackend(callCtx, &rookeryv1.CreateStorageBackendRequest{
		Name:        plan.Name.ValueString(),
		Title:       plan.Title.ValueString(),
		Description: plan.Description.ValueString(),
		Driver:      plan.Driver.ValueString(),
		Config:      config,
	})
	if err != nil {
		resp.Diagnostics.AddError("Create storage backend", err.Error())
		return
	}
	resp.Diagnostics.Append(setStorageBackendState(ctx, &plan, backend)...)
	entityLabels, err := applyEntityLabels(callCtx, client, "storage_backend", fmt.Sprintf("%d", backend.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create storage backend labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *storageBackendResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state storageBackendModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read storage backend", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	backend, err := client.StorageBackends.GetStorageBackend(callCtx, &rookeryv1.GetStorageBackendRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read storage backend", err.Error())
		return
	}
	resp.Diagnostics.Append(setStorageBackendState(ctx, &state, backend)...)
	entityLabels, err := readEntityLabels(callCtx, client, "storage_backend", fmt.Sprintf("%d", state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Read storage backend labels", err.Error())
		return
	}
	state.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *storageBackendResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan storageBackendModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update storage backend", err.Error())
		return
	}
	config, diags := storageBackendConfigFromObject(ctx, plan.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	backend, err := client.StorageBackends.UpdateStorageBackend(callCtx, &rookeryv1.UpdateStorageBackendRequest{
		Id:          plan.ID.ValueInt64(),
		Name:        plan.Name.ValueString(),
		Title:       plan.Title.ValueString(),
		Description: plan.Description.ValueString(),
		Driver:      plan.Driver.ValueString(),
		Config:      config,
	})
	if err != nil {
		resp.Diagnostics.AddError("Update storage backend", err.Error())
		return
	}
	resp.Diagnostics.Append(setStorageBackendState(ctx, &plan, backend)...)
	entityLabels, err := applyEntityLabels(callCtx, client, "storage_backend", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update storage backend labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *storageBackendResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state storageBackendModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete storage backend", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.StorageBackends.DeleteStorageBackend(callCtx, &rookeryv1.DeleteStorageBackendRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete storage backend", err.Error())
	}
}

func (r *storageBackendResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func setStorageBackendState(ctx context.Context, state *storageBackendModel, backend *rookeryv1.StorageBackend) diag.Diagnostics {
	var diags diag.Diagnostics
	state.ID = types.Int64Value(backend.GetId())
	state.Name = types.StringValue(backend.GetName())
	state.Title = optionalStringValue(backend.GetTitle())
	state.Description = optionalStringValue(backend.GetDescription())
	state.Driver = types.StringValue(backend.GetDriver())
	state.DiskCount = types.Int64Value(int64(backend.GetDiskCount()))
	cfg := backend.GetConfig()
	kouja := types.ObjectNull(koujaConfigAttrTypes)
	if cfg.GetKouja() != nil {
		value := koujaConfigModel{
			Endpoint: types.StringValue(cfg.GetKouja().GetEndpoint()), Protocol: optionalStringValue(cfg.GetKouja().GetProtocol()),
			StorageClass: types.StringValue(cfg.GetKouja().GetStorageClass()), Initiator: optionalStringValue(cfg.GetKouja().GetInitiator()),
			HostNQN: optionalStringValue(cfg.GetKouja().GetHostNqn()), CAFile: types.StringValue(cfg.GetKouja().GetCaFile()),
			ClientCertFile: types.StringValue(cfg.GetKouja().GetClientCertFile()), ClientKeyFile: types.StringValue(cfg.GetKouja().GetClientKeyFile()),
			ServerName: optionalStringValue(cfg.GetKouja().GetServerName()), Portal: optionalStringValue(cfg.GetKouja().GetPortal()),
		}
		object, objectDiags := types.ObjectValueFrom(ctx, koujaConfigAttrTypes, value)
		diags.Append(objectDiags...)
		kouja = object
	}
	config := storageBackendConfigModel{
		VG:                            types.StringValue(cfg.GetVg()),
		ThinPool:                      types.StringValue(cfg.GetThinPool()),
		SupportsTemplates:             types.BoolValue(cfg.GetSupportsTemplates()),
		PreparationScript:             optionalStringValue(cfg.GetPreparationScript()),
		PreparationTimeout:            optionalStringValue(cfg.GetPreparationTimeout()),
		AllowTemplateDeleteWithClones: types.BoolValue(cfg.GetAllowTemplateDeleteWithClones()),
		Kouja:                         kouja,
	}
	object, objectDiags := types.ObjectValueFrom(ctx, storageBackendConfigAttrTypes, config)
	diags.Append(objectDiags...)
	state.Config = object
	return diags
}

func storageBackendConfigFromObject(ctx context.Context, object types.Object) (*rookeryv1.StorageBackendConfig, diag.Diagnostics) {
	var config storageBackendConfigModel
	diags := object.As(ctx, &config, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, diags
	}
	result := &rookeryv1.StorageBackendConfig{
		Vg:                            config.VG.ValueString(),
		ThinPool:                      config.ThinPool.ValueString(),
		SupportsTemplates:             config.SupportsTemplates.ValueBool(),
		PreparationScript:             config.PreparationScript.ValueString(),
		PreparationTimeout:            config.PreparationTimeout.ValueString(),
		AllowTemplateDeleteWithClones: config.AllowTemplateDeleteWithClones.ValueBool(),
	}
	if !config.Kouja.IsNull() && !config.Kouja.IsUnknown() {
		var kouja koujaConfigModel
		diags.Append(config.Kouja.As(ctx, &kouja, basetypes.ObjectAsOptions{})...)
		if diags.HasError() {
			return nil, diags
		}
		result.DriverConfig = &rookeryv1.StorageBackendConfig_Kouja{Kouja: &rookeryv1.KoujaConfig{
			Endpoint: kouja.Endpoint.ValueString(), Protocol: kouja.Protocol.ValueString(),
			StorageClass: kouja.StorageClass.ValueString(), Initiator: kouja.Initiator.ValueString(),
			HostNqn: kouja.HostNQN.ValueString(), CaFile: kouja.CAFile.ValueString(),
			ClientCertFile: kouja.ClientCertFile.ValueString(), ClientKeyFile: kouja.ClientKeyFile.ValueString(),
			ServerName: kouja.ServerName.ValueString(), Portal: kouja.Portal.ValueString(),
		}}
	}
	return result, diags
}
