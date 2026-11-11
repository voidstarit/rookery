package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/proto"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

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
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	VCPUs          types.Int64  `tfsdk:"vcpus"`
	MemoryMB       types.Int64  `tfsdk:"memory_mb"`
	ConsoleEnabled types.Bool   `tfsdk:"console_enabled"`
	CgroupClass    types.String `tfsdk:"cgroup_class"`
	KernelID       types.Int64  `tfsdk:"kernel_id"`
	KernelName     types.String `tfsdk:"kernel_name"`
	InitramfsID    types.Int64  `tfsdk:"initramfs_id"`
	InitramfsName  types.String `tfsdk:"initramfs_name"`
	Image          types.String `tfsdk:"image"`
	Env            types.List   `tfsdk:"env"`
	ExecutablePath types.String `tfsdk:"executable_path"`
	ExecutableArgs types.List   `tfsdk:"executable_args"`
	Append         types.String `tfsdk:"append"`
	InterfaceIDs   types.List   `tfsdk:"interface_ids"`
	DiskIDs        types.List   `tfsdk:"disk_ids"`
	VirtioFSShares types.List   `tfsdk:"virtiofs_shares"`
	Consoles       types.List   `tfsdk:"consoles"`
	Status         types.String `tfsdk:"status"`
	DesiredState   types.String `tfsdk:"desired_state"`
	RunState       types.String `tfsdk:"run_state"`
	ConsoleSocket  types.String `tfsdk:"console_socket"`
	Labels         types.Set    `tfsdk:"labels"`
}

type lightVMEnvModel struct {
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
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

func (r *lightVMResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lightvm"
}

func (r *lightVMResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A container-backed Rookery LightVM. Power actions are not managed by this resource.",
		Attributes: map[string]schema.Attribute{
			"labels":          labelsResourceAttribute(),
			"id":              schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":            schema.StringAttribute{Required: true, Description: "LightVM name."},
			"description":     schema.StringAttribute{Optional: true, Computed: true, Description: "Optional description."},
			"vcpus":           schema.Int64Attribute{Optional: true, Computed: true, Description: "Virtual CPUs."},
			"memory_mb":       schema.Int64Attribute{Optional: true, Computed: true, Description: "Memory in MiB."},
			"console_enabled": schema.BoolAttribute{Optional: true, Computed: true, Description: "Allow access to the virtio console."},
			"cgroup_class":    schema.StringAttribute{Optional: true, Computed: true, Description: "Optional leaf cgroup class name."},
			"kernel_id":       schema.Int64Attribute{Required: true, Description: "Kernel record id."},
			"kernel_name":     schema.StringAttribute{Computed: true, Description: "Kernel record name."},
			"initramfs_id":    schema.Int64Attribute{Required: true, Description: "Initramfs record id."},
			"initramfs_name":  schema.StringAttribute{Computed: true, Description: "Initramfs record name."},
			"image":           schema.StringAttribute{Required: true, Description: "Container image used to prepare the root filesystem."},
			"env": schema.ListNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Environment variables written to the root filesystem .env file.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":  schema.StringAttribute{Required: true, Description: "Variable name."},
					"value": schema.StringAttribute{Optional: true, Computed: true, Description: "Variable value."},
				}},
			},
			"executable_path": schema.StringAttribute{Required: true, Description: "Executable path written to the guest entrypoint."},
			"executable_args": schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType, Description: "Executable arguments."},
			"append": schema.StringAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Kernel command line. Defaults to console=hvc0 rdinit=/init.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"interface_ids": schema.ListAttribute{Optional: true, Computed: true, ElementType: types.Int64Type, Description: "Attached network interface ids."},
			"disk_ids": schema.ListAttribute{
				Optional:      true,
				Computed:      true,
				ElementType:   types.Int64Type,
				Description:   "Attached disk ids, in attachment order.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
			"virtiofs_shares": schema.ListNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Additional host directories exposed to the guest as virtiofs devices.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":        schema.Int64Attribute{Computed: true},
					"host_path": schema.StringAttribute{Required: true, Description: "Absolute path to an existing host directory."},
					"tag":       schema.StringAttribute{Required: true, Description: "Guest virtiofs tag. The tag rootfs is reserved."},
					"read_only": schema.BoolAttribute{Optional: true, Computed: true, Description: "Export the directory read-only. Defaults to true."},
					"position":  schema.Int64Attribute{Computed: true},
				}},
			},
			"consoles": schema.ListNestedAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Virtio console ports. The server assigns ids and port names.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":        schema.Int64Attribute{Computed: true},
					"name":      schema.StringAttribute{Required: true, Description: "Console name."},
					"port_name": schema.StringAttribute{Computed: true, Description: "Guest port name."},
				}},
			},
			"status":         schema.StringAttribute{Computed: true, Description: "Current lifecycle status."},
			"desired_state":  schema.StringAttribute{Computed: true, Description: "Persisted desired power state."},
			"run_state":      schema.StringAttribute{Computed: true, Description: "Observed QEMU run state while running."},
			"console_socket": schema.StringAttribute{Computed: true, Description: "First console socket while running."},
		},
	}
}

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
}

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

func (r *lightVMResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func lightVMCreateRequest(ctx context.Context, plan lightVMModel) (*rookeryv1.CreateLightVMRequest, diag.Diagnostics) {
	var diags diag.Diagnostics
	env, envDiags := lightVMEnvFromModel(ctx, plan.Env)
	diags.Append(envDiags...)
	consoles, consoleDiags := lightVMConsolesFromModel(ctx, plan.Consoles)
	diags.Append(consoleDiags...)
	shares, shareDiags := lightVMVirtioFSFromModel(ctx, plan.VirtioFSShares)
	diags.Append(shareDiags...)
	args, argDiags := stringListValues(ctx, plan.ExecutableArgs)
	diags.Append(argDiags...)
	if diags.HasError() {
		return nil, diags
	}
	return &rookeryv1.CreateLightVMRequest{
		Name:           plan.Name.ValueString(),
		Description:    plan.Description.ValueString(),
		Vcpus:          int32Pointer(plan.VCPUs),
		MemoryMb:       int32Pointer(plan.MemoryMB),
		ConsoleEnabled: boolPointer(plan.ConsoleEnabled),
		CgroupClass:    plan.CgroupClass.ValueString(),
		KernelId:       plan.KernelID.ValueInt64(),
		InitramfsId:    plan.InitramfsID.ValueInt64(),
		Image:          plan.Image.ValueString(),
		Env:            env,
		ExecutablePath: plan.ExecutablePath.ValueString(),
		ExecutableArgs: args,
		InterfaceIds:   lightVMInt64Values(ctx, plan.InterfaceIDs),
		Append:         stringPointer(plan.Append),
		Consoles:       consoles,
		DiskIds:        lightVMInt64Values(ctx, plan.DiskIDs),
		VirtiofsShares: shares,
	}, diags
}

func lightVMUpdateRequest(ctx context.Context, plan lightVMModel) (*rookeryv1.UpdateLightVMRequest, diag.Diagnostics) {
	var diags diag.Diagnostics
	env, envDiags := lightVMEnvFromModel(ctx, plan.Env)
	diags.Append(envDiags...)
	consoles, consoleDiags := lightVMConsolesFromModel(ctx, plan.Consoles)
	diags.Append(consoleDiags...)
	shares, shareDiags := lightVMVirtioFSFromModel(ctx, plan.VirtioFSShares)
	diags.Append(shareDiags...)
	args, argDiags := stringListValues(ctx, plan.ExecutableArgs)
	diags.Append(argDiags...)
	if diags.HasError() {
		return nil, diags
	}
	return &rookeryv1.UpdateLightVMRequest{
		Id:             plan.ID.ValueString(),
		Name:           plan.Name.ValueString(),
		Description:    plan.Description.ValueString(),
		Vcpus:          int32(plan.VCPUs.ValueInt64()),
		MemoryMb:       int32(plan.MemoryMB.ValueInt64()),
		ConsoleEnabled: plan.ConsoleEnabled.ValueBool(),
		CgroupClass:    plan.CgroupClass.ValueString(),
		KernelId:       plan.KernelID.ValueInt64(),
		InitramfsId:    plan.InitramfsID.ValueInt64(),
		Image:          plan.Image.ValueString(),
		Env:            env,
		ExecutablePath: plan.ExecutablePath.ValueString(),
		ExecutableArgs: args,
		InterfaceIds:   lightVMInt64Values(ctx, plan.InterfaceIDs),
		Append:         plan.Append.ValueString(),
		Consoles:       consoles,
		DiskIds:        lightVMInt64Values(ctx, plan.DiskIDs),
		ReplaceDiskIds: proto.Bool(true),
		VirtiofsShares: shares,
	}, diags
}

func setLightVMState(ctx context.Context, state *lightVMModel, vm *rookeryv1.LightVM) diag.Diagnostics {
	var diags diag.Diagnostics
	state.ID = types.StringValue(vm.GetId())
	state.Name = types.StringValue(vm.GetName())
	state.Description = optionalStringValue(vm.GetDescription())
	state.VCPUs = types.Int64Value(int64(vm.GetVcpus()))
	state.MemoryMB = types.Int64Value(int64(vm.GetMemoryMb()))
	state.ConsoleEnabled = types.BoolValue(vm.GetConsoleEnabled())
	state.CgroupClass = optionalStringValue(vm.GetCgroupClass())
	state.KernelID = types.Int64Value(vm.GetKernelId())
	state.KernelName = types.StringValue(vm.GetKernelName())
	state.InitramfsID = types.Int64Value(vm.GetInitramfsId())
	state.InitramfsName = types.StringValue(vm.GetInitramfsName())
	state.Image = types.StringValue(vm.GetImage())
	state.ExecutablePath = types.StringValue(vm.GetExecutablePath())
	state.Append = types.StringValue(vm.GetAppend())
	state.Status = types.StringValue(vm.GetStatus())
	state.DesiredState = types.StringValue(vm.GetDesiredState())
	state.RunState = optionalStringValue(vm.GetRunState())
	state.ConsoleSocket = optionalStringValue(vm.GetConsoleSocket())

	env, envDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: lightVMEnvAttrTypes()}, lightVMEnvValues(vm.GetEnv()))
	diags.Append(envDiags...)
	state.Env = env
	consoles, consoleDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: lightVMConsoleAttrTypes()}, lightVMConsoleValues(vm.GetConsoles()))
	diags.Append(consoleDiags...)
	state.Consoles = consoles
	shares, shareDiags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: lightVMShareAttrTypes()}, lightVMShareValues(vm.GetVirtiofsShares()))
	diags.Append(shareDiags...)
	state.VirtioFSShares = shares
	args, argDiags := stringList(ctx, vm.GetExecutableArgs())
	diags.Append(argDiags...)
	state.ExecutableArgs = args
	interfaceIDs := make([]int64, 0, len(vm.GetInterfaces()))
	for _, iface := range vm.GetInterfaces() {
		interfaceIDs = append(interfaceIDs, iface.GetId())
	}
	interfaceList, interfaceDiags := int64List(ctx, interfaceIDs)
	diags.Append(interfaceDiags...)
	state.InterfaceIDs = interfaceList
	diskList, diskDiags := int64List(ctx, vm.GetDiskIds())
	diags.Append(diskDiags...)
	state.DiskIDs = diskList
	return diags
}

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

func lightVMInt64Values(ctx context.Context, list types.List) []int64 {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}
	var values []int64
	_ = list.ElementsAs(ctx, &values, false)
	return values
}

func lightVMEnvAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"name":  types.StringType,
		"value": types.StringType,
	}
}

func lightVMConsoleAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":        types.Int64Type,
		"name":      types.StringType,
		"port_name": types.StringType,
	}
}

func lightVMShareAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":        types.Int64Type,
		"host_path": types.StringType,
		"tag":       types.StringType,
		"read_only": types.BoolType,
		"position":  types.Int64Type,
	}
}

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

func stringListValues(ctx context.Context, list types.List) ([]string, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var values []string
	diags := list.ElementsAs(ctx, &values, false)
	return values, diags
}
