package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"google.golang.org/protobuf/types/known/structpb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*diskResource)(nil)
	_ resource.ResourceWithConfigure   = (*diskResource)(nil)
	_ resource.ResourceWithImportState = (*diskResource)(nil)
)

type diskResource struct{ configuredResource }

// NewDiskResource creates the rookery_disk resource.
func NewDiskResource() resource.Resource {
	return &diskResource{}
}

type diskModel struct {
	ID                  types.Int64  `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	SizeGB              types.Int64  `tfsdk:"size_gb"`
	StorageBackendID    types.Int64  `tfsdk:"storage_backend_id"`
	StorageBackendName  types.String `tfsdk:"storage_backend_name"`
	VG                  types.String `tfsdk:"vg"`
	ThinPool            types.String `tfsdk:"thin_pool"`
	Device              types.String `tfsdk:"device"`
	AttachedVMID        types.String `tfsdk:"attached_vm_id"`
	AttachedVMName      types.String `tfsdk:"attached_vm_name"`
	IsTemplate          types.Bool   `tfsdk:"is_template"`
	TemplateDiskID      types.Int64  `tfsdk:"template_disk_id"`
	TemplateDiskName    types.String `tfsdk:"template_disk_name"`
	ProvisioningScript  types.String `tfsdk:"provisioning_script"`
	ProvisioningTimeout types.String `tfsdk:"provisioning_timeout"`
	ProvisioningJSON    types.String `tfsdk:"provisioning_json"`
}

func (r *diskResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_disk"
}

func (r *diskResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A thin-LVM disk or template clone in Rookery.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Disk name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"size_gb": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Disk size in GiB. Required when not cloning a template.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace(), int64planmodifier.UseStateForUnknown()},
			},
			"storage_backend_id": schema.Int64Attribute{
				Optional:      true,
				Computed:      true,
				Description:   "Storage backend id. Defaults to the server's default backend.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace(), int64planmodifier.UseStateForUnknown()},
			},
			"storage_backend_name": schema.StringAttribute{Computed: true},
			"vg":                   schema.StringAttribute{Computed: true},
			"thin_pool":            schema.StringAttribute{Computed: true},
			"device":               schema.StringAttribute{Computed: true},
			"attached_vm_id":       schema.StringAttribute{Computed: true},
			"attached_vm_name":     schema.StringAttribute{Computed: true},
			"is_template": schema.BoolAttribute{
				Optional:      true,
				Computed:      true,
				Description:   "Mark the disk as a template after creation.",
				PlanModifiers: []planmodifier.Bool{boolUseState()},
			},
			"template_disk_id": schema.Int64Attribute{
				Optional:      true,
				Description:   "Template disk to clone. Requires a template-capable backend.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"template_disk_name": schema.StringAttribute{Computed: true},
			"provisioning_script": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Absolute path to the template provisioning script.",
			},
			"provisioning_timeout": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Template provisioning timeout, for example 5m.",
			},
			"provisioning_json": schema.StringAttribute{
				Optional:      true,
				Description:   "JSON object passed to a template clone provisioning script.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
		},
	}
}

func (r *diskResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan diskModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create disk", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	provisioning, err := diskProvisioningFromJSON(plan.ProvisioningJSON)
	if err != nil {
		resp.Diagnostics.AddError("Create disk", err.Error())
		return
	}
	disk, err := client.Disks.CreateDisk(callCtx, &rookeryv1.CreateDiskRequest{
		Name:                plan.Name.ValueString(),
		SizeGb:              int32Pointer(plan.SizeGB),
		StorageBackendId:    int64Pointer(plan.StorageBackendID),
		TemplateDiskId:      int64Pointer(plan.TemplateDiskID),
		IsTemplate:          boolPointer(plan.IsTemplate),
		ProvisioningScript:  plan.ProvisioningScript.ValueString(),
		ProvisioningTimeout: plan.ProvisioningTimeout.ValueString(),
		Provisioning:        provisioning,
	})
	if err != nil {
		resp.Diagnostics.AddError("Create disk", err.Error())
		return
	}
	setDiskState(&plan, disk)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *diskResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state diskModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read disk", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	disk, err := client.Disks.GetDisk(callCtx, &rookeryv1.GetDiskRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read disk", err.Error())
		return
	}
	setDiskState(&state, disk)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *diskResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan diskModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update disk", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	disk, err := client.Disks.UpdateDisk(callCtx, &rookeryv1.UpdateDiskRequest{
		Id:                  plan.ID.ValueInt64(),
		IsTemplate:          boolPointer(plan.IsTemplate),
		ProvisioningScript:  stringPointer(plan.ProvisioningScript),
		ProvisioningTimeout: stringPointer(plan.ProvisioningTimeout),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update disk", err.Error())
		return
	}
	setDiskState(&plan, disk)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *diskResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state diskModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete disk", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.Disks.DeleteDisk(callCtx, &rookeryv1.DeleteDiskRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete disk", err.Error())
	}
}

func (r *diskResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func setDiskState(state *diskModel, disk *rookeryv1.Disk) {
	state.ID = types.Int64Value(disk.GetId())
	state.Name = types.StringValue(disk.GetName())
	state.SizeGB = types.Int64Value(int64(disk.GetSizeGb()))
	state.StorageBackendID = optionalInt64Value(disk.StorageBackendId)
	state.StorageBackendName = optionalStringValue(disk.GetStorageBackendName())
	state.VG = types.StringValue(disk.GetVg())
	state.ThinPool = types.StringValue(disk.GetThinPool())
	state.Device = types.StringValue(disk.GetDevice())
	state.AttachedVMID = optionalStringValue(disk.GetAttachedVmId())
	state.AttachedVMName = optionalStringValue(disk.GetAttachedVmName())
	state.IsTemplate = types.BoolValue(disk.GetIsTemplate())
	state.TemplateDiskID = optionalInt64Value(disk.TemplateDiskId)
	state.TemplateDiskName = optionalStringValue(disk.GetTemplateDiskName())
	state.ProvisioningScript = optionalStringValue(disk.GetProvisioningScript())
	state.ProvisioningTimeout = optionalStringValue(disk.GetProvisioningTimeout())
}

func diskProvisioningFromJSON(value types.String) (*structpb.Struct, error) {
	if value.IsNull() || value.IsUnknown() || value.ValueString() == "" {
		return nil, nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(value.ValueString()), &data); err != nil {
		return nil, fmt.Errorf("provisioning_json must be a JSON object: %w", err)
	}
	if data == nil {
		return nil, fmt.Errorf("provisioning_json must be a JSON object")
	}
	return structpb.NewStruct(data)
}
