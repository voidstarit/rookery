package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/lightvm"
)

var (
	_ resource.Resource                = (*lightVMArtifactResource)(nil)
	_ resource.ResourceWithConfigure   = (*lightVMArtifactResource)(nil)
	_ resource.ResourceWithImportState = (*lightVMArtifactResource)(nil)
)

// lightVMArtifactResource implements the kernel and initramfs resources. Both
// API shapes are identical apart from the service used.
type lightVMArtifactResource struct {
	configuredResource
	kind       string
	typeName   string
	entityType string
	noun       string
}

// NewLightVMKernelResource creates the rookery_lightvm_kernel resource.
func NewLightVMKernelResource() resource.Resource {
	return &lightVMArtifactResource{kind: lightvm.KindKernel, typeName: "lightvm_kernel", entityType: "lightvm_kernel", noun: "kernel"}
}

// NewLightVMInitramfsResource creates the rookery_lightvm_initramfs resource.
func NewLightVMInitramfsResource() resource.Resource {
	return &lightVMArtifactResource{kind: lightvm.KindInitramfs, typeName: "lightvm_initramfs", entityType: "lightvm_initramfs", noun: "initramfs"}
}

type lightVMArtifactModel struct {
	ID          types.Int64  `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	SourcePath  types.String `tfsdk:"source_path"`
	SourceHash  types.String `tfsdk:"source_hash"`
	Path        types.String `tfsdk:"path"`
	FileName    types.String `tfsdk:"file_name"`
	SizeBytes   types.Int64  `tfsdk:"size_bytes"`
	Managed     types.Bool   `tfsdk:"managed"`
	Labels      types.Set    `tfsdk:"labels"`
}

type lightVMArtifactRecord struct {
	ID          int64
	Name        string
	Description string
	Path        string
	FileName    string
	SizeBytes   int64
	Managed     bool
}

func (r *lightVMArtifactResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.typeName
}

func (r *lightVMArtifactResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: fmt.Sprintf("A LightVM %s image. Use source_path to upload a local file, or path to register a file already present on the Rookery host.", r.noun),
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id":     schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{
				Required:    true,
				Description: fmt.Sprintf("%s name.", capitalize(r.noun)),
			},
			"description": schema.StringAttribute{
				Optional: true, Computed: true, Description: "Optional description.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"source_path": schema.StringAttribute{
				Optional:    true,
				Description: "Path to a local kernel/initramfs file to stream to Rookery. Mutually exclusive with a user-supplied path.",
			},
			"source_hash": schema.StringAttribute{
				Optional:    true,
				Description: "Optional hash of source_path, for example filemd5(\"image\"). Change it to trigger re-upload when the file content changes.",
			},
			"path": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Absolute path on the Rookery host. Computed when source_path uploads a file.",
				PlanModifiers: []planmodifier.String{artifactPathPlanModifier{}},
			},
			"file_name": schema.StringAttribute{
				Optional: true, Computed: true, Description: "Display file name. Defaults to the uploaded or host file basename.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"size_bytes": schema.Int64Attribute{
				Computed: true, Description: "Server-derived file size.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"managed": schema.BoolAttribute{
				Computed: true, Description: "Whether Rookery manages and may delete the file.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *lightVMArtifactResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan lightVMArtifactModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create LightVM "+r.noun, err.Error())
		return
	}
	path, fileName, uploaded, err := r.resolveArtifact(ctx, client, plan, true)
	if err != nil {
		resp.Diagnostics.AddError("Create LightVM "+r.noun, err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	record, err := r.createRecord(callCtx, client, plan, path, fileName)
	if err != nil {
		if uploaded {
			_ = client.DeleteLightVMArtifact(callCtx, path)
		}
		resp.Diagnostics.AddError("Create LightVM "+r.noun, err.Error())
		return
	}
	if uploaded {
		plan.SourcePath = types.StringValue(strings.TrimSpace(plan.SourcePath.ValueString()))
	}
	setLightVMArtifactState(&plan, record)
	labels, err := applyEntityLabels(callCtx, client, r.entityType, fmt.Sprintf("%d", record.ID), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create LightVM "+r.noun+" labels", err.Error())
		return
	}
	plan.Labels = labels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lightVMArtifactResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state lightVMArtifactModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read LightVM "+r.noun, err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	record, err := r.getRecord(callCtx, client, state.ID.ValueInt64())
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read LightVM "+r.noun, err.Error())
		return
	}
	setLightVMArtifactState(&state, record)
	labels, err := readEntityLabels(callCtx, client, r.entityType, fmt.Sprintf("%d", record.ID))
	if err != nil {
		resp.Diagnostics.AddError("Read LightVM "+r.noun+" labels", err.Error())
		return
	}
	state.Labels = labels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *lightVMArtifactResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state lightVMArtifactModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update LightVM "+r.noun, err.Error())
		return
	}
	upload := strings.TrimSpace(plan.SourcePath.ValueString()) != "" &&
		(!plan.SourcePath.Equal(state.SourcePath) || !plan.SourceHash.Equal(state.SourceHash))
	path, fileName, uploaded, err := r.resolveArtifact(ctx, client, plan, upload)
	if err != nil {
		resp.Diagnostics.AddError("Update LightVM "+r.noun, err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	record, err := r.updateRecord(callCtx, client, plan, path, fileName)
	if err != nil {
		if uploaded {
			_ = client.DeleteLightVMArtifact(callCtx, path)
		}
		resp.Diagnostics.AddError("Update LightVM "+r.noun, err.Error())
		return
	}
	setLightVMArtifactState(&plan, record)
	labels, err := applyEntityLabels(callCtx, client, r.entityType, fmt.Sprintf("%d", record.ID), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update LightVM "+r.noun+" labels", err.Error())
		return
	}
	plan.Labels = labels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *lightVMArtifactResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state lightVMArtifactModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete LightVM "+r.noun, err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if err := r.deleteRecord(callCtx, client, state.ID.ValueInt64()); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete LightVM "+r.noun, err.Error())
	}
}

func (r *lightVMArtifactResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func (r *lightVMArtifactResource) resolveArtifact(ctx context.Context, client *Client, plan lightVMArtifactModel, upload bool) (string, string, bool, error) {
	sourcePath := strings.TrimSpace(plan.SourcePath.ValueString())
	hostPath := strings.TrimSpace(plan.Path.ValueString())
	fileName := strings.TrimSpace(plan.FileName.ValueString())
	if sourcePath != "" && upload {
		uploaded, err := client.UploadLightVMArtifact(ctx, r.kind, sourcePath)
		if err != nil {
			return "", "", false, err
		}
		if fileName == "" {
			fileName = uploaded.GetFileName()
		}
		return uploaded.GetPath(), fileName, true, nil
	}
	if hostPath == "" {
		return "", "", false, fmt.Errorf("either source_path or path is required")
	}
	return hostPath, fileName, false, nil
}

// artifactPathPlanModifier preserves the managed path across no-op plans and
// marks it unknown when a source upload is created or replaced.
type artifactPathPlanModifier struct{}

func (artifactPathPlanModifier) Description(context.Context) string {
	return "Use the managed upload path unless source_path or source_hash changes."
}

func (artifactPathPlanModifier) MarkdownDescription(ctx context.Context) string {
	return artifactPathPlanModifier{}.Description(ctx)
}

func (artifactPathPlanModifier) PlanModifyString(ctx context.Context, request planmodifier.StringRequest, response *planmodifier.StringResponse) {
	var plan lightVMArtifactModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if strings.TrimSpace(plan.SourcePath.ValueString()) == "" {
		return
	}
	if !request.State.Raw.IsNull() {
		var state lightVMArtifactModel
		response.Diagnostics.Append(request.State.Get(ctx, &state)...)
		if response.Diagnostics.HasError() {
			return
		}
		if plan.SourcePath.Equal(state.SourcePath) && plan.SourceHash.Equal(state.SourceHash) {
			response.PlanValue = request.StateValue
			return
		}
	}
	response.PlanValue = types.StringUnknown()
}

func capitalize(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func (r *lightVMArtifactResource) createRecord(ctx context.Context, client *Client, plan lightVMArtifactModel, path, fileName string) (lightVMArtifactRecord, error) {
	name := strings.TrimSpace(plan.Name.ValueString())
	description := strings.TrimSpace(plan.Description.ValueString())
	if r.kind == lightvm.KindKernel {
		created, err := client.LightVMKernels.CreateKernel(ctx, &rookeryv1.CreateKernelRequest{
			Name: name, Description: description, Path: path, FileName: fileName,
		})
		if err != nil {
			return lightVMArtifactRecord{}, err
		}
		return artifactRecordFromKernel(created), nil
	}
	created, err := client.LightVMInitramfs.CreateInitramfs(ctx, &rookeryv1.CreateInitramfsRequest{
		Name: name, Description: description, Path: path, FileName: fileName,
	})
	if err != nil {
		return lightVMArtifactRecord{}, err
	}
	return artifactRecordFromInitramfs(created), nil
}

func (r *lightVMArtifactResource) updateRecord(ctx context.Context, client *Client, plan lightVMArtifactModel, path, fileName string) (lightVMArtifactRecord, error) {
	name := strings.TrimSpace(plan.Name.ValueString())
	description := strings.TrimSpace(plan.Description.ValueString())
	if r.kind == lightvm.KindKernel {
		updated, err := client.LightVMKernels.UpdateKernel(ctx, &rookeryv1.UpdateKernelRequest{
			Id: plan.ID.ValueInt64(), Name: name, Description: description, Path: path, FileName: fileName,
		})
		if err != nil {
			return lightVMArtifactRecord{}, err
		}
		return artifactRecordFromKernel(updated), nil
	}
	updated, err := client.LightVMInitramfs.UpdateInitramfs(ctx, &rookeryv1.UpdateInitramfsRequest{
		Id: plan.ID.ValueInt64(), Name: name, Description: description, Path: path, FileName: fileName,
	})
	if err != nil {
		return lightVMArtifactRecord{}, err
	}
	return artifactRecordFromInitramfs(updated), nil
}

func (r *lightVMArtifactResource) getRecord(ctx context.Context, client *Client, id int64) (lightVMArtifactRecord, error) {
	if r.kind == lightvm.KindKernel {
		kernel, err := client.LightVMKernels.GetKernel(ctx, &rookeryv1.GetKernelRequest{Id: id})
		if err != nil {
			return lightVMArtifactRecord{}, err
		}
		return artifactRecordFromKernel(kernel), nil
	}
	image, err := client.LightVMInitramfs.GetInitramfs(ctx, &rookeryv1.GetInitramfsRequest{Id: id})
	if err != nil {
		return lightVMArtifactRecord{}, err
	}
	return artifactRecordFromInitramfs(image), nil
}

func (r *lightVMArtifactResource) deleteRecord(ctx context.Context, client *Client, id int64) error {
	if r.kind == lightvm.KindKernel {
		_, err := client.LightVMKernels.DeleteKernel(ctx, &rookeryv1.DeleteKernelRequest{Id: id})
		return err
	}
	_, err := client.LightVMInitramfs.DeleteInitramfs(ctx, &rookeryv1.DeleteInitramfsRequest{Id: id})
	return err
}

func artifactRecordFromKernel(kernel *rookeryv1.LightVMKernel) lightVMArtifactRecord {
	return lightVMArtifactRecord{
		ID: kernel.GetId(), Name: kernel.GetName(), Description: kernel.GetDescription(),
		Path: kernel.GetPath(), FileName: kernel.GetFileName(), SizeBytes: kernel.GetSizeBytes(),
		Managed: kernel.GetManaged(),
	}
}

func artifactRecordFromInitramfs(image *rookeryv1.LightVMInitramfs) lightVMArtifactRecord {
	return lightVMArtifactRecord{
		ID: image.GetId(), Name: image.GetName(), Description: image.GetDescription(),
		Path: image.GetPath(), FileName: image.GetFileName(), SizeBytes: image.GetSizeBytes(),
		Managed: image.GetManaged(),
	}
}

func setLightVMArtifactState(state *lightVMArtifactModel, record lightVMArtifactRecord) {
	state.ID = types.Int64Value(record.ID)
	state.Name = types.StringValue(record.Name)
	state.Description = optionalStringValue(record.Description)
	state.Path = types.StringValue(record.Path)
	state.FileName = optionalStringValue(record.FileName)
	state.SizeBytes = types.Int64Value(record.SizeBytes)
	state.Managed = types.BoolValue(record.Managed)
}
