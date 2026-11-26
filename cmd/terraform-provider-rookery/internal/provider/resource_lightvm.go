package provider

import (
	"context"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/proto"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

// lightVMExitPollInterval is how often wait_for_exit checks whether the
// LightVM is still running. It is a variable so tests can shorten the wait.
var lightVMExitPollInterval = time.Second

var (
	_ resource.Resource                = (*lightVMResource)(nil)
	_ resource.ResourceWithConfigure   = (*lightVMResource)(nil)
	_ resource.ResourceWithImportState = (*lightVMResource)(nil)
)

type lightVMResource struct{ configuredResource }

// NewLightVMResource creates the rookery_lightvm resource.
func NewLightVMResource() resource.Resource {
	return &lightVMResource{}
}

type lightVMModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	VCPUs            types.Int64  `tfsdk:"vcpus"`
	MemoryMB         types.Int64  `tfsdk:"memory_mb"`
	ConsoleEnabled   types.Bool   `tfsdk:"console_enabled"`
	Ephemeral        types.Bool   `tfsdk:"ephemeral"`
	WaitForExit      types.Bool   `tfsdk:"wait_for_exit"`
	CgroupClass      types.String `tfsdk:"cgroup_class"`
	KernelID         types.Int64  `tfsdk:"kernel_id"`
	KernelName       types.String `tfsdk:"kernel_name"`
	InitramfsID      types.Int64  `tfsdk:"initramfs_id"`
	InitramfsName    types.String `tfsdk:"initramfs_name"`
	Image            types.String `tfsdk:"image"`
	Env              types.List   `tfsdk:"env"`
	ConfigFiles      types.List   `tfsdk:"config_files"`
	ExecutablePath   types.String `tfsdk:"executable_path"`
	ExecutableScript types.String `tfsdk:"executable_script"`
	ExecutableArgs   types.List   `tfsdk:"executable_args"`
	Append           types.String `tfsdk:"append"`
	InterfaceIDs     types.List   `tfsdk:"interface_ids"`
	DiskIDs          types.List   `tfsdk:"disk_ids"`
	DiskSerials      types.Map    `tfsdk:"disk_serials"`
	VirtioFSShares   types.List   `tfsdk:"virtiofs_shares"`
	Consoles         types.List   `tfsdk:"consoles"`
	Status           types.String `tfsdk:"status"`
	DesiredState     types.String `tfsdk:"desired_state"`
	RunState         types.String `tfsdk:"run_state"`
	ConsoleSocket    types.String `tfsdk:"console_socket"`
	Labels           types.Set    `tfsdk:"labels"`
}

type lightVMEnvModel struct {
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
}

type lightVMConfigFileModel struct {
	Name    types.String `tfsdk:"name"`
	Content types.String `tfsdk:"content"`
}

type lightVMConsoleModel struct {
	ID       types.Int64  `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	PortName types.String `tfsdk:"port_name"`
}

type lightVMVirtioFSShareModel struct {
	ID       types.Int64  `tfsdk:"id"`
	HostPath types.String `tfsdk:"host_path"`
	Tag      types.String `tfsdk:"tag"`
	ReadOnly types.Bool   `tfsdk:"read_only"`
	Position types.Int64  `tfsdk:"position"`
}

// Metadata sets the resource type name to the provider type name plus "_lightvm".
func (r *lightVMResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lightvm"
}

// Schema describes the rookery_lightvm resource, covering lifecycle, executable,
// attachment, and computed runtime attributes.
func (r *lightVMResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A container-backed Rookery LightVM. Power actions are not managed by this resource.",
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id":     schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":   schema.StringAttribute{Required: true, Description: "LightVM name."},
			"description": schema.StringAttribute{
				Optional: true, Computed: true, Description: "Optional description.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"vcpus": schema.Int64Attribute{
				Optional: true, Computed: true, Description: "Virtual CPUs.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"memory_mb": schema.Int64Attribute{
				Optional: true, Computed: true, Description: "Memory in MiB.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"console_enabled": schema.BoolAttribute{
				Optional: true, Computed: true, Description: "Allow access to the virtio console.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"ephemeral": schema.BoolAttribute{
				Optional: true, Computed: true,
				Description:   "Terminate QEMU, virtiofsd, and the attempt container when the guest exits (reboot, panic, or poweroff). Attached disks and interfaces remain.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"wait_for_exit": schema.BoolAttribute{
				Optional: true,
				Description: "Block the create until the LightVM is no longer running. Intended for one-shot workloads together with ephemeral = true: " +
					"the server deletes the definition after the guest exits, so a later plan removes the resource and a later apply starts a new run. " +
					"The wait has no built-in limit; cancel Terraform to stop waiting. It only affects creation.",
			},
			"cgroup_class": schema.StringAttribute{
				Optional: true, Computed: true, Description: "Optional leaf cgroup class name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"kernel_id": schema.Int64Attribute{Required: true, Description: "Kernel record id."},
			"kernel_name": schema.StringAttribute{
				Computed: true, Description: "Kernel record name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"initramfs_id": schema.Int64Attribute{Required: true, Description: "Initramfs record id."},
			"initramfs_name": schema.StringAttribute{
				Computed: true, Description: "Initramfs record name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"image": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Optional container image used to prepare the root filesystem. Omit for a disk-backed LightVM with disk_ids.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"env": schema.ListNestedAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Environment variables. Image-backed LightVMs receive them in the root filesystem .env file; disk-backed LightVMs receive them in the rookery-config virtio-blk device tar archive.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":  schema.StringAttribute{Required: true, Description: "Variable name."},
					"value": schema.StringAttribute{Optional: true, Computed: true, Description: "Variable value."},
				}},
			},
			"config_files": schema.ListNestedAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "User-supplied files included in the rookery-config tar for disk-backed LightVMs. Names are relative to /run/rookery.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":    schema.StringAttribute{Required: true, Description: "Relative file name."},
					"content": schema.StringAttribute{Required: true, Description: "File content."},
				}},
			},
			"executable_path": schema.StringAttribute{
				Optional:    true,
				Description: "Absolute executable path. Image-backed LightVMs receive it in /.entrypoint; disk-backed LightVMs receive it in rookery-config.tar. Mutually exclusive with executable_script.",
			},
			"executable_script": schema.StringAttribute{
				Optional:    true,
				Description: "Inline startup script. Image-backed LightVMs receive it in /.entrypoint; disk-backed LightVMs receive it in rookery-config.tar. Mutually exclusive with executable_path.",
			},
			"executable_args": schema.ListAttribute{
				Optional: true, Computed: true, ElementType: types.StringType, Description: "Executable arguments.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
			"append": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Kernel command line. Defaults to console=hvc0 rdinit=/init.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"interface_ids": schema.ListAttribute{
				Optional: true, Computed: true, ElementType: types.Int64Type, Description: "Attached network interface ids.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
			"disk_ids": schema.ListAttribute{
				Optional:      true,
				Computed:      true,
				ElementType:   types.Int64Type,
				Description:   "Attached disk ids, in attachment order.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
			"disk_serials": schema.MapAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Description: "Guest-visible virtio-blk serials keyed by disk id. Serials are at most 20 bytes and may contain letters, digits, '.', '_', ':', and '-'.",
			},
			"virtiofs_shares": schema.ListNestedAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Additional host directories exposed to the guest as virtiofs devices.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":        schema.Int64Attribute{Computed: true},
					"host_path": schema.StringAttribute{Required: true, Description: "Absolute path to an existing host directory."},
					"tag":       schema.StringAttribute{Required: true, Description: "Guest virtiofs tag. The tag rootfs is reserved."},
					"read_only": schema.BoolAttribute{Optional: true, Computed: true, Description: "Export the directory read-only. Defaults to true."},
					"position":  schema.Int64Attribute{Computed: true},
				}},
			},
			"consoles": schema.ListNestedAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Virtio console ports. The server assigns ids and port names.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":        schema.Int64Attribute{Computed: true},
					"name":      schema.StringAttribute{Required: true, Description: "Console name."},
					"port_name": schema.StringAttribute{Computed: true, Description: "Guest port name."},
				}},
			},
			"status": schema.StringAttribute{
				Computed: true, Description: "Current lifecycle status.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"desired_state": schema.StringAttribute{
				Computed: true, Description: "Persisted desired power state.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"run_state": schema.StringAttribute{
				Computed: true, Description: "Observed QEMU run state while running.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"console_socket": schema.StringAttribute{
				Computed: true, Description: "First console socket while running.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// Create creates a LightVM from the plan, attaches labels, and, when wait_for_exit is
// set, blocks until the instance stops; failures are reported as diagnostics.
func (r *lightVMResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan lightVMModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create LightVM", err.Error())
		return
	}
	request, diags := lightVMCreateRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vm, err := client.LightVMs.CreateLightVM(callCtx, request)
	if err != nil {
		resp.Diagnostics.AddError("Create LightVM", err.Error())
		return
	}
	resp.Diagnostics.Append(setLightVMState(ctx, &plan, vm)...)
	labels, err := applyEntityLabels(callCtx, client, "lightvm", vm.GetId(), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create LightVM labels", err.Error())
		return
	}
	plan.Labels = labels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.WaitForExit.ValueBool() {
		if err := waitForLightVMExit(ctx, client, vm.GetId(), vm.GetEphemeral()); err != nil {
			resp.Diagnostics.AddError("Wait for LightVM exit", err.Error())
		}
	}
}

// Read refreshes state from the API and drops the resource from state when the
// LightVM no longer exists.
func (r *lightVMResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state lightVMModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read LightVM", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vm, err := client.LightVMs.GetLightVM(callCtx, &rookeryv1.GetLightVMRequest{Id: state.ID.ValueString()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read LightVM", err.Error())
		return
	}
	resp.Diagnostics.Append(setLightVMState(ctx, &state, vm)...)
	labels, err := readEntityLabels(callCtx, client, "lightvm", vm.GetId())
	if err != nil {
		resp.Diagnostics.AddError("Read LightVM labels", err.Error())
		return
	}
	state.Labels = labels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update sends the planned configuration to the API, replaces the LightVM's labels,
// and saves the response; the request replaces disk IDs rather than merging them.
func (r *lightVMResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan lightVMModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update LightVM", err.Error())
		return
	}
	request, diags := lightVMUpdateRequest(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vm, err := client.LightVMs.UpdateLightVM(callCtx, request)
	if err != nil {
		resp.Diagnostics.AddError("Update LightVM", err.Error())
		return
	}
	resp.Diagnostics.Append(setLightVMState(ctx, &plan, vm)...)
	labels, err := applyEntityLabels(callCtx, client, "lightvm", vm.GetId(), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update LightVM labels", err.Error())
		return
	}
	plan.Labels = labels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the LightVM through the API, treating a not-found response as successful deletion.
func (r *lightVMResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state lightVMModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete LightVM", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.LightVMs.DeleteLightVM(callCtx, &rookeryv1.DeleteLightVMRequest{Id: state.ID.ValueString()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete LightVM", err.Error())
	}
}

// ImportState adopts an existing LightVM by passing the import ID through to the "id" attribute.
func (r *lightVMResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// waitForLightVMExit polls live process metrics until the LightVM exits or its
// definition is deleted. For an ephemeral LightVM it waits for the definition
// to disappear so disk and interface attachments are released.
func waitForLightVMExit(ctx context.Context, client *Client, id string, ephemeral bool) error {
	ticker := time.NewTicker(lightVMExitPollInterval)
	defer ticker.Stop()
	for {
		callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
		metrics, err := client.LightVMs.GetLightVMMetrics(callCtx, &rookeryv1.GetLightVMMetricsRequest{Id: id})
		cancel()
		switch {
		case isNotFound(err):
			return nil
		case err != nil:
			return err
		case !metrics.GetRunning() && !ephemeral:
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// validateLightVMExecution rejects conflicting executable_path, executable_script,
// and executable_args combinations and requires an executable for image-backed
// LightVMs.
func validateLightVMExecution(plan lightVMModel, args []string) diag.Diagnostics {
	var diags diag.Diagnostics
	executablePath := strings.TrimSpace(plan.ExecutablePath.ValueString())
	executableScript := strings.TrimSpace(plan.ExecutableScript.ValueString())
	image := strings.TrimSpace(plan.Image.ValueString())
	switch {
	case executablePath != "" && executableScript != "":
		diags.AddError("Invalid LightVM executable", "executable_path and executable_script are mutually exclusive")
	case executableScript != "" && len(args) > 0:
		diags.AddError("Invalid LightVM executable", "executable_args cannot be used with executable_script")
	case len(args) > 0 && executablePath == "":
		diags.AddError("Invalid LightVM executable", "executable_args requires executable_path")
	case image != "" && executablePath == "" && executableScript == "":
		diags.AddError("Invalid LightVM executable", "exactly one of executable_path or executable_script is required")
	}
	return diags
}

// lightVMCreateRequest converts the plan into a create request, rejecting invalid
// nested values, config_files combined with an image, and conflicting executable
// settings.
func lightVMCreateRequest(ctx context.Context, plan lightVMModel) (*rookeryv1.CreateLightVMRequest, diag.Diagnostics) {
	var diags diag.Diagnostics
	env, envDiags := lightVMEnvFromModel(ctx, plan.Env)
	diags.Append(envDiags...)
	configFiles, configFileDiags := lightVMConfigFilesFromModel(ctx, plan.ConfigFiles)
	diags.Append(configFileDiags...)
	if strings.TrimSpace(plan.Image.ValueString()) != "" && len(configFiles) > 0 {
		diags.AddError("Invalid LightVM config files", "config_files can only be used with a disk-backed LightVM")
	}
	consoles, consoleDiags := lightVMConsolesFromModel(ctx, plan.Consoles)
	diags.Append(consoleDiags...)
	shares, shareDiags := lightVMVirtioFSFromModel(ctx, plan.VirtioFSShares)
	diags.Append(shareDiags...)
	args, argDiags := stringListValues(ctx, plan.ExecutableArgs)
	diags.Append(argDiags...)
	diskSerials, serialDiags := diskSerialsFromModel(ctx, plan.DiskSerials)
	diags.Append(serialDiags...)
	diags.Append(validateLightVMExecution(plan, args)...)
	if diags.HasError() {
		return nil, diags
	}
	return &rookeryv1.CreateLightVMRequest{
		Name:             plan.Name.ValueString(),
		Description:      plan.Description.ValueString(),
		Vcpus:            int32Pointer(plan.VCPUs),
		MemoryMb:         int32Pointer(plan.MemoryMB),
		ConsoleEnabled:   boolPointer(plan.ConsoleEnabled),
		Ephemeral:        boolPointer(plan.Ephemeral),
		CgroupClass:      plan.CgroupClass.ValueString(),
		KernelId:         plan.KernelID.ValueInt64(),
		InitramfsId:      plan.InitramfsID.ValueInt64(),
		Image:            plan.Image.ValueString(),
		Env:              env,
		ConfigFiles:      configFiles,
		ExecutablePath:   plan.ExecutablePath.ValueString(),
		ExecutableScript: plan.ExecutableScript.ValueString(),
		ExecutableArgs:   args,
		InterfaceIds:     lightVMInt64Values(ctx, plan.InterfaceIDs),
		Append:           stringPointer(plan.Append),
		Consoles:         consoles,
		DiskIds:          lightVMInt64Values(ctx, plan.DiskIDs),
		DiskSerials:      diskSerials,
		VirtiofsShares:   shares,
	}, diags
}

// lightVMUpdateRequest converts the plan into an update request that replaces disk
// IDs, rejecting invalid nested values and conflicting executable settings.
func lightVMUpdateRequest(ctx context.Context, plan lightVMModel) (*rookeryv1.UpdateLightVMRequest, diag.Diagnostics) {
	var diags diag.Diagnostics
	env, envDiags := lightVMEnvFromModel(ctx, plan.Env)
	diags.Append(envDiags...)
	configFiles, configFileDiags := lightVMConfigFilesFromModel(ctx, plan.ConfigFiles)
	diags.Append(configFileDiags...)
	if strings.TrimSpace(plan.Image.ValueString()) != "" && len(configFiles) > 0 {
		diags.AddError("Invalid LightVM config files", "config_files can only be used with a disk-backed LightVM")
	}
	consoles, consoleDiags := lightVMConsolesFromModel(ctx, plan.Consoles)
	diags.Append(consoleDiags...)
	shares, shareDiags := lightVMVirtioFSFromModel(ctx, plan.VirtioFSShares)
	diags.Append(shareDiags...)
	args, argDiags := stringListValues(ctx, plan.ExecutableArgs)
	diags.Append(argDiags...)
	diskSerials, serialDiags := diskSerialsFromModel(ctx, plan.DiskSerials)
	diags.Append(serialDiags...)
	diags.Append(validateLightVMExecution(plan, args)...)
	if diags.HasError() {
		return nil, diags
	}
	return &rookeryv1.UpdateLightVMRequest{
		Id:               plan.ID.ValueString(),
		Name:             plan.Name.ValueString(),
		Description:      plan.Description.ValueString(),
		Vcpus:            int32(plan.VCPUs.ValueInt64()),
		MemoryMb:         int32(plan.MemoryMB.ValueInt64()),
		ConsoleEnabled:   plan.ConsoleEnabled.ValueBool(),
		Ephemeral:        plan.Ephemeral.ValueBool(),
		CgroupClass:      plan.CgroupClass.ValueString(),
		KernelId:         plan.KernelID.ValueInt64(),
		InitramfsId:      plan.InitramfsID.ValueInt64(),
		Image:            plan.Image.ValueString(),
		Env:              env,
		ConfigFiles:      configFiles,
		ExecutablePath:   plan.ExecutablePath.ValueString(),
		ExecutableScript: plan.ExecutableScript.ValueString(),
		ExecutableArgs:   args,
		InterfaceIds:     lightVMInt64Values(ctx, plan.InterfaceIDs),
		Append:           plan.Append.ValueString(),
		Consoles:         consoles,
		DiskIds:          lightVMInt64Values(ctx, plan.DiskIDs),
		DiskSerials:      diskSerials,
		ReplaceDiskIds:   proto.Bool(true),
		VirtiofsShares:   shares,
	}, diags
}

// setLightVMState copies the API LightVM into the Terraform model, converting nested
// env, console, share, disk, and interface values and collecting conversion
// diagnostics.
func setLightVMState(ctx context.Context, state *lightVMModel, vm *rookeryv1.LightVM) diag.Diagnostics {
	var diags diag.Diagnostics
	state.ID = types.StringValue(vm.GetId())
	state.Name = types.StringValue(vm.GetName())
	state.Description = optionalStringValue(vm.GetDescription())
	state.VCPUs = types.Int64Value(int64(vm.GetVcpus()))
	state.MemoryMB = types.Int64Value(int64(vm.GetMemoryMb()))
	state.ConsoleEnabled = types.BoolValue(vm.GetConsoleEnabled())
	state.Ephemeral = types.BoolValue(vm.GetEphemeral())
	state.CgroupClass = optionalStringValue(vm.GetCgroupClass())
	state.KernelID = types.Int64Value(vm.GetKernelId())
	state.KernelName = types.StringValue(vm.GetKernelName())
	state.InitramfsID = types.Int64Value(vm.GetInitramfsId())
	state.InitramfsName = types.StringValue(vm.GetInitramfsName())
	state.Image = types.StringValue(vm.GetImage())
	state.ExecutablePath = optionalStringValue(vm.GetExecutablePath())
	state.ExecutableScript = optionalStringValue(vm.GetExecutableScript())
	state.Append = types.StringValue(vm.GetAppend())
	state.Status = types.StringValue(vm.GetStatus())
	state.DesiredState = types.StringValue(vm.GetDesiredState())
	state.RunState = optionalStringValue(vm.GetRunState())
	state.ConsoleSocket = optionalStringValue(vm.GetConsoleSocket())

	env, envDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: lightVMEnvAttrTypes()}, lightVMEnvValues(vm.GetEnv()))
	diags.Append(envDiags...)
	state.Env = env
	configFiles, configFileDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: lightVMConfigFileAttrTypes()}, lightVMConfigFileValues(vm.GetConfigFiles()))
	diags.Append(configFileDiags...)
	state.ConfigFiles = configFiles
	consoles, consoleDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: lightVMConsoleAttrTypes()}, lightVMConsoleValues(vm.GetConsoles()))
	diags.Append(consoleDiags...)
	state.Consoles = consoles
	shares, shareDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: lightVMShareAttrTypes()}, lightVMShareValues(vm.GetVirtiofsShares()))
	diags.Append(shareDiags...)
	state.VirtioFSShares = shares
	args, argDiags := stringList(ctx, append([]string{}, vm.GetExecutableArgs()...))
	diags.Append(argDiags...)
	state.ExecutableArgs = args
	interfaceIDs := make([]int64, 0, len(vm.GetInterfaces()))
	for _, iface := range vm.GetInterfaces() {
		interfaceIDs = append(interfaceIDs, iface.GetId())
	}
	interfaceList, interfaceDiags := int64List(ctx, interfaceIDs)
	diags.Append(interfaceDiags...)
	state.InterfaceIDs = interfaceList
	diskList, diskDiags := int64List(ctx, append([]int64{}, vm.GetDiskIds()...))
	diags.Append(diskDiags...)
	state.DiskIDs = diskList
	diskSerials, serialDiags := diskSerialsValue(ctx, vm.GetDisks())
	diags.Append(serialDiags...)
	state.DiskSerials = diskSerials
	return diags
}

// lightVMEnvFromModel converts the plan's env list into API env vars, returning nil
// when the list is null or unknown.
func lightVMEnvFromModel(ctx context.Context, list types.List) ([]*rookeryv1.LightVMEnvVar, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var values []lightVMEnvModel
	diags := list.ElementsAs(ctx, &values, false)
	result := make([]*rookeryv1.LightVMEnvVar, 0, len(values))
	for _, value := range values {
		result = append(result, &rookeryv1.LightVMEnvVar{Name: value.Name.ValueString(), Value: value.Value.ValueString()})
	}
	return result, diags
}

// lightVMConfigFilesFromModel converts the plan's config_files list into API config
// files, returning nil when the list is null or unknown.
func lightVMConfigFilesFromModel(ctx context.Context, list types.List) ([]*rookeryv1.LightVMConfigFile, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var values []lightVMConfigFileModel
	diags := list.ElementsAs(ctx, &values, false)
	result := make([]*rookeryv1.LightVMConfigFile, 0, len(values))
	for _, value := range values {
		result = append(result, &rookeryv1.LightVMConfigFile{
			Name:    value.Name.ValueString(),
			Content: value.Content.ValueString(),
		})
	}
	return result, diags
}

// lightVMConsolesFromModel converts the plan's consoles list into API console
// definitions, returning nil when the list is null or unknown.
func lightVMConsolesFromModel(ctx context.Context, list types.List) ([]*rookeryv1.LightVMConsole, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var values []lightVMConsoleModel
	diags := list.ElementsAs(ctx, &values, false)
	result := make([]*rookeryv1.LightVMConsole, 0, len(values))
	for _, value := range values {
		result = append(result, &rookeryv1.LightVMConsole{Name: value.Name.ValueString()})
	}
	return result, diags
}

// lightVMVirtioFSFromModel converts the plan's virtiofs_shares list into API shares,
// returning nil when the list is null or unknown.
func lightVMVirtioFSFromModel(ctx context.Context, list types.List) ([]*rookeryv1.LightVMVirtioFSShare, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var values []lightVMVirtioFSShareModel
	diags := list.ElementsAs(ctx, &values, false)
	result := make([]*rookeryv1.LightVMVirtioFSShare, 0, len(values))
	for _, value := range values {
		result = append(result, &rookeryv1.LightVMVirtioFSShare{
			HostPath: value.HostPath.ValueString(),
			Tag:      value.Tag.ValueString(),
			ReadOnly: boolPointer(value.ReadOnly),
		})
	}
	return result, diags
}

// lightVMInt64Values extracts integer IDs from a list, returning nil for a null or
// unknown list and ignoring decoding errors.
func lightVMInt64Values(ctx context.Context, list types.List) []int64 {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}
	var values []int64
	_ = list.ElementsAs(ctx, &values, false)
	return values
}

// lightVMEnvAttrTypes returns the attribute types for an env object: a name and a
// value, both strings.
func lightVMEnvAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"name":  types.StringType,
		"value": types.StringType,
	}
}

// lightVMConfigFileAttrTypes returns the attribute types for a config file object:
// a name and content, both strings.
func lightVMConfigFileAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"name":    types.StringType,
		"content": types.StringType,
	}
}

// lightVMConsoleAttrTypes returns the attribute types for a console object: integer
// id plus name and port_name strings.
func lightVMConsoleAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":        types.Int64Type,
		"name":      types.StringType,
		"port_name": types.StringType,
	}
}

// lightVMShareAttrTypes returns the attribute types for a virtiofs share object,
// including integer id and position plus host_path, tag, and read_only.
func lightVMShareAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":        types.Int64Type,
		"host_path": types.StringType,
		"tag":       types.StringType,
		"read_only": types.BoolType,
		"position":  types.Int64Type,
	}
}

// lightVMEnvValues maps API env vars into Terraform env models for list conversion.
func lightVMEnvValues(values []*rookeryv1.LightVMEnvVar) []lightVMEnvModel {
	result := make([]lightVMEnvModel, 0, len(values))
	for _, value := range values {
		result = append(result, lightVMEnvModel{
			Name:  types.StringValue(value.GetName()),
			Value: types.StringValue(value.GetValue()),
		})
	}
	return result
}

// lightVMConfigFileValues maps API config files into Terraform config file models
// for list conversion.
func lightVMConfigFileValues(values []*rookeryv1.LightVMConfigFile) []lightVMConfigFileModel {
	result := make([]lightVMConfigFileModel, 0, len(values))
	for _, value := range values {
		result = append(result, lightVMConfigFileModel{
			Name:    types.StringValue(value.GetName()),
			Content: types.StringValue(value.GetContent()),
		})
	}
	return result
}

// lightVMConsoleValues maps API consoles into Terraform console models for list conversion.
func lightVMConsoleValues(values []*rookeryv1.LightVMConsole) []lightVMConsoleModel {
	result := make([]lightVMConsoleModel, 0, len(values))
	for _, value := range values {
		result = append(result, lightVMConsoleModel{
			ID:       types.Int64Value(value.GetId()),
			Name:     types.StringValue(value.GetName()),
			PortName: types.StringValue(value.GetPortName()),
		})
	}
	return result
}

// lightVMShareValues maps API virtiofs shares into Terraform share models for list conversion.
func lightVMShareValues(values []*rookeryv1.LightVMVirtioFSShare) []lightVMVirtioFSShareModel {
	result := make([]lightVMVirtioFSShareModel, 0, len(values))
	for _, value := range values {
		result = append(result, lightVMVirtioFSShareModel{
			ID:       types.Int64Value(value.GetId()),
			HostPath: types.StringValue(value.GetHostPath()),
			Tag:      types.StringValue(value.GetTag()),
			ReadOnly: types.BoolValue(value.GetReadOnly()),
			Position: types.Int64Value(int64(value.GetPosition())),
		})
	}
	return result
}

// stringListValues decodes a list of strings, returning nil without diagnostics when
// the list is null or unknown.
func stringListValues(ctx context.Context, list types.List) ([]string, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var values []string
	diags := list.ElementsAs(ctx, &values, false)
	return values, diags
}
