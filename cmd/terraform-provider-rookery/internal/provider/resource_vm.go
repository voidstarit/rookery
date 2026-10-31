package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/protobuf/proto"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*vmResource)(nil)
	_ resource.ResourceWithConfigure   = (*vmResource)(nil)
	_ resource.ResourceWithImportState = (*vmResource)(nil)
)

type vmResource struct{ configuredResource }

// NewVMResource creates the rookery_vm resource.
func NewVMResource() resource.Resource {
	return &vmResource{}
}

type vmModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	Status         types.String `tfsdk:"status"`
	VCPUs          types.Int64  `tfsdk:"vcpus"`
	MemoryMB       types.Int64  `tfsdk:"memory_mb"`
	DiskIDs        types.List   `tfsdk:"disk_ids"`
	InterfaceIDs   types.List   `tfsdk:"interface_ids"`
	BootMediaID    types.Int64  `tfsdk:"boot_media_id"`
	ConsoleEnabled types.Bool   `tfsdk:"console_enabled"`
	CgroupClass    types.String `tfsdk:"cgroup_class"`
}

func (r *vmResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm"
}

func (r *vmResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A QEMU/KVM virtual machine. Creating a VM starts it; power actions are otherwise not managed by this resource.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":        schema.StringAttribute{Required: true, Description: "VM name."},
			"description": schema.StringAttribute{Optional: true, Computed: true, Description: "VM description."},
			"status":      schema.StringAttribute{Computed: true, Description: "Current VM status."},
			"vcpus":       schema.Int64Attribute{Optional: true, Computed: true, Description: "Virtual CPUs."},
			"memory_mb":   schema.Int64Attribute{Optional: true, Computed: true, Description: "Memory in MiB."},
			"disk_ids": schema.ListAttribute{
				Required:    true,
				ElementType: types.Int64Type,
				Description: "Disk ids attached to the VM, in boot order.",
			},
			"interface_ids": schema.ListAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
				Description: "Network interface ids attached to the VM.",
			},
			"boot_media_id": schema.Int64Attribute{
				Optional:    true,
				Description: "Optional installation media id used as a read-only CD-ROM. Omit to clear it on update.",
			},
			"console_enabled": schema.BoolAttribute{Optional: true, Computed: true, Description: "Allow VNC console access."},
			"cgroup_class": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Optional leaf cgroup class name defined in rookery_cgroup_settings, such as prod/web/frontend.",
			},
		},
	}
}

func (r *vmResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vmModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create VM", err.Error())
		return
	}
	diskIDs, diags := interfaceInt64List(ctx, plan.DiskIDs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(diskIDs) == 0 {
		resp.Diagnostics.AddError("Create VM", "at least one disk_id is required")
		return
	}
	interfaceIDs, interfaceSet, interfaceDiags := interfaceInt64ListOptional(ctx, plan.InterfaceIDs)
	resp.Diagnostics.Append(interfaceDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var interfaces []int64
	if interfaceSet {
		interfaces = interfaceIDs
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if err := validateVMCgroupClass(callCtx, client, plan.CgroupClass.ValueString()); err != nil {
		resp.Diagnostics.AddError("Create VM", err.Error())
		return
	}
	vm, err := client.VMs.CreateVM(callCtx, &rookeryv1.CreateVMRequest{
		Name:           plan.Name.ValueString(),
		Description:    plan.Description.ValueString(),
		Vcpus:          int32Pointer(plan.VCPUs),
		MemoryMb:       int32Pointer(plan.MemoryMB),
		BootMediaId:    int64Pointer(plan.BootMediaID),
		ConsoleEnabled: boolPointer(plan.ConsoleEnabled),
		InterfaceIds:   interfaces,
		DiskIds:        diskIDs,
		CgroupClass:    plan.CgroupClass.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create VM", err.Error())
		return
	}
	resp.Diagnostics.Append(setVMState(ctx, &plan, vm)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vmModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read VM", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	vm, err := client.VMs.GetVM(callCtx, &rookeryv1.GetVMRequest{Id: state.ID.ValueString()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read VM", err.Error())
		return
	}
	resp.Diagnostics.Append(setVMState(ctx, &state, vm)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vmResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vmModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update VM", err.Error())
		return
	}
	diskIDs, diags := interfaceInt64List(ctx, plan.DiskIDs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(diskIDs) == 0 {
		resp.Diagnostics.AddError("Update VM", "at least one disk_id is required")
		return
	}
	interfaceIDs, interfaceSet, interfaceDiags := interfaceInt64ListOptional(ctx, plan.InterfaceIDs)
	resp.Diagnostics.Append(interfaceDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	description := ""
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		description = plan.Description.ValueString()
	}
	request := &rookeryv1.UpdateVMRequest{
		Id:             plan.ID.ValueString(),
		Name:           stringPointer(plan.Name),
		Description:    proto.String(description),
		Vcpus:          int32Pointer(plan.VCPUs),
		MemoryMb:       int32Pointer(plan.MemoryMB),
		BootMediaId:    int64Pointer(plan.BootMediaID),
		ConsoleEnabled: boolPointer(plan.ConsoleEnabled),
	}
	if !plan.CgroupClass.IsUnknown() && !plan.CgroupClass.Equal(state.CgroupClass) {
		request.CgroupClass = proto.String(strings.TrimSpace(plan.CgroupClass.ValueString()))
	}
	if !plan.DiskIDs.Equal(state.DiskIDs) {
		request.DiskIds = diskIDs
		request.ReplaceDiskIds = proto.Bool(true)
	}
	if interfaceSet && !plan.InterfaceIDs.Equal(state.InterfaceIDs) {
		request.InterfaceIds = interfaceIDs
		request.ReplaceInterfaceIds = proto.Bool(true)
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if err := validateVMCgroupClass(callCtx, client, plan.CgroupClass.ValueString()); err != nil {
		resp.Diagnostics.AddError("Update VM", err.Error())
		return
	}
	vm, err := client.VMs.UpdateVM(callCtx, request)
	if err != nil {
		resp.Diagnostics.AddError("Update VM", err.Error())
		return
	}
	resp.Diagnostics.Append(setVMState(ctx, &plan, vm)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vmResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vmModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete VM", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.VMs.DeleteVM(callCtx, &rookeryv1.DeleteVMRequest{Id: state.ID.ValueString()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete VM", err.Error())
	}
}

func (r *vmResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func setVMState(ctx context.Context, state *vmModel, vm *rookeryv1.VM) diag.Diagnostics {
	var diags diag.Diagnostics
	state.ID = types.StringValue(vm.GetId())
	state.Name = types.StringValue(vm.GetName())
	state.Description = optionalStringValue(vm.GetDescription())
	state.Status = types.StringValue(vm.GetStatus())
	state.VCPUs = types.Int64Value(int64(vm.GetVcpus()))
	state.MemoryMB = types.Int64Value(int64(vm.GetMemoryMb()))
	state.BootMediaID = optionalInt64Value(vm.BootMediaId)
	state.ConsoleEnabled = types.BoolValue(vm.GetConsoleEnabled())
	diskIDs := vm.GetDiskIds()
	if len(diskIDs) == 0 {
		for _, disk := range vm.GetDisks() {
			diskIDs = append(diskIDs, disk.GetId())
		}
	}
	diskList, diskDiags := int64List(ctx, diskIDs)
	diags.Append(diskDiags...)
	state.DiskIDs = diskList
	interfaceIDs := make([]int64, 0, len(vm.GetInterfaces()))
	for _, iface := range vm.GetInterfaces() {
		interfaceIDs = append(interfaceIDs, iface.GetId())
	}
	interfaceList, interfaceDiags := int64List(ctx, interfaceIDs)
	diags.Append(interfaceDiags...)
	state.InterfaceIDs = interfaceList
	state.CgroupClass = types.StringValue(vm.GetCgroupClass())
	return diags
}

func interfaceInt64List(ctx context.Context, list types.List) ([]int64, diag.Diagnostics) {
	var values []int64
	diags := list.ElementsAs(ctx, &values, false)
	return values, diags
}

func interfaceInt64ListOptional(ctx context.Context, list types.List) ([]int64, bool, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, false, nil
	}
	values, diags := interfaceInt64List(ctx, list)
	return values, true, diags
}
