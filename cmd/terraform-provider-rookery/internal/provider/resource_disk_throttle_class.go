package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*diskThrottleClassResource)(nil)
	_ resource.ResourceWithConfigure   = (*diskThrottleClassResource)(nil)
	_ resource.ResourceWithImportState = (*diskThrottleClassResource)(nil)
)

type diskThrottleClassResource struct{ configuredResource }

// NewDiskThrottleClassResource creates the rookery_disk_throttle_class resource.
func NewDiskThrottleClassResource() resource.Resource {
	return &diskThrottleClassResource{}
}

// Metadata registers this resource as rookery_disk_throttle_class under the configured provider
// type name.
func (r *diskThrottleClassResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_disk_throttle_class"
}

// Schema builds the class attributes: id, name, optional description, computed validation_error
// and disk_count, plus numeric throttle-limit attributes with generated descriptions.
func (r *diskThrottleClassResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := map[string]schema.Attribute{
		"id": schema.Int64Attribute{
			Computed:      true,
			PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
		},
		"name": schema.StringAttribute{
			Required:    true,
			Description: "Unique class name.",
		},
		"description": schema.StringAttribute{
			Optional:      true,
			Computed:      true,
			Description:   "Optional free-form description.",
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"validation_error": schema.StringAttribute{
			Computed:    true,
			Description: "Non-empty when the stored class limits cannot be decoded or validated.",
		},
		"disk_count": schema.Int64Attribute{
			Computed:    true,
			Description: "Number of disks using this class.",
		},
	}
	for name, description := range diskThrottleNumericDescriptions() {
		attributes[name] = schema.Int64Attribute{
			Optional:      true,
			Computed:      true,
			Description:   description,
			PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
		}
	}
	resp.Schema = schema.Schema{
		Description: "A reusable QEMU disk throttling policy. Limits apply at the next VM start.",
		Attributes:  attributes,
	}
}

// Create validates the class name, description, and limits, creates the throttling class, and
// stores the returned server copy, which may carry validation errors or a disk count.
func (r *diskThrottleClassResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan diskThrottleClassModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if diags := diskThrottleClassModelDiagnostics(plan); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create disk throttle class", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	created, err := client.DiskThrottleClasses.CreateDiskThrottleClass(callCtx, &rookeryv1.CreateDiskThrottleClassRequest{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
		Limits:      diskThrottleClassToProto(plan).GetLimits(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create disk throttle class", err.Error())
		return
	}
	state := diskThrottleClassModelFromProto(created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Read fetches the class by id and replaces state with the server copy, removing the resource
// when the class no longer exists.
func (r *diskThrottleClassResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state diskThrottleClassModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read disk throttle class", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	class, err := client.DiskThrottleClasses.GetDiskThrottleClass(callCtx, &rookeryv1.GetDiskThrottleClassRequest{
		Id: state.ID.ValueInt64(),
	})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read disk throttle class", err.Error())
		return
	}
	state = diskThrottleClassModelFromProto(class)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update validates the class name, description, and limits, then sends them; the returned class
// replaces state.
func (r *diskThrottleClassResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan diskThrottleClassModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if diags := diskThrottleClassModelDiagnostics(plan); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update disk throttle class", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	updated, err := client.DiskThrottleClasses.UpdateDiskThrottleClass(callCtx, &rookeryv1.UpdateDiskThrottleClassRequest{
		Id:          plan.ID.ValueInt64(),
		Name:        stringPointer(plan.Name),
		Description: stringPointer(plan.Description),
		Limits:      diskThrottleClassToProto(plan).GetLimits(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Update disk throttle class", err.Error())
		return
	}
	state := diskThrottleClassModelFromProto(updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Delete removes the class by id, treating a not-found response as success.
func (r *diskThrottleClassResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state diskThrottleClassModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete disk throttle class", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.DiskThrottleClasses.DeleteDiskThrottleClass(callCtx, &rookeryv1.DeleteDiskThrottleClassRequest{
		Id: state.ID.ValueInt64(),
	}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete disk throttle class", err.Error())
	}
}

// ImportState parses the Terraform import id as a positive integer and stores it as the class
// id, reporting a diagnostic for invalid input.
func (r *diskThrottleClassResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	value, err := parseInt64ID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Import disk throttle class", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), value)...)
}

// parseInt64ID parses a base-10 id and rejects values that are non-numeric or less than one.
func parseInt64ID(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid integer id %q", value)
	}
	return parsed, nil
}

// diskThrottleNumericDescriptions returns schema descriptions for the BPS, IOPS, burst, and
// I/O-size limit attributes, keyed by attribute name.
func diskThrottleNumericDescriptions() map[string]string {
	base := map[string]string{
		"bps_total":             "Total BPS limit in bytes per second; 0 means unset.",
		"bps_total_max":         "Total BPS burst maximum in bytes per second.",
		"bps_total_max_length":  "Total BPS burst length in seconds.",
		"bps_read":              "Read BPS limit in bytes per second.",
		"bps_read_max":          "Read BPS burst maximum.",
		"bps_read_max_length":   "Read BPS burst length in seconds.",
		"bps_write":             "Write BPS limit in bytes per second.",
		"bps_write_max":         "Write BPS burst maximum.",
		"bps_write_max_length":  "Write BPS burst length in seconds.",
		"iops_total":            "Total IOPS limit.",
		"iops_total_max":        "Total IOPS burst maximum.",
		"iops_total_max_length": "Total IOPS burst length in seconds.",
		"iops_read":             "Read IOPS limit.",
		"iops_read_max":         "Read IOPS burst maximum.",
		"iops_read_max_length":  "Read IOPS burst length in seconds.",
		"iops_write":            "Write IOPS limit.",
		"iops_write_max":        "Write IOPS burst maximum.",
		"iops_write_max_length": "Write IOPS burst length in seconds.",
		"iops_size":             "I/O size in bytes; requires an IOPS limit.",
	}
	return base
}
