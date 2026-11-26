package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ resource.Resource                = (*mediaResource)(nil)
	_ resource.ResourceWithConfigure   = (*mediaResource)(nil)
	_ resource.ResourceWithImportState = (*mediaResource)(nil)
)

type mediaResource struct{ configuredResource }

// NewMediaResource creates the rookery_media resource.
func NewMediaResource() resource.Resource {
	return &mediaResource{}
}

type mediaModel struct {
	ID        types.Int64  `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Path      types.String `tfsdk:"path"`
	SizeBytes types.Int64  `tfsdk:"size_bytes"`
	Labels    types.Set    `tfsdk:"labels"`
}

// Metadata registers this resource as rookery_media under the configured provider type name.
func (r *mediaResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_media"
}

// Schema declares the media resource: name and path are required and force replacement when
// changed, id and size_bytes are server-computed, and labels use the shared label set.
func (r *mediaResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A read-only installation media file registered with Rookery.",
		Attributes: map[string]schema.Attribute{
			"labels": labelsResourceAttribute(),
			"id":     schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
			"name": schema.StringAttribute{
				Required:      true,
				Description:   "Media name.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"path": schema.StringAttribute{
				Required:      true,
				Description:   "Absolute path to the existing media file on the Rookery host.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"size_bytes": schema.Int64Attribute{Computed: true, Description: "Server-derived file size."},
		},
	}
}

// Create registers the media file, stores the server-assigned id and size, then applies the
// planned labels; a label failure is reported but the media registration is left in place.
func (r *mediaResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan mediaModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Create media", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	media, err := client.Media.CreateMedia(callCtx, &rookeryv1.CreateMediaRequest{
		Name: plan.Name.ValueString(),
		Path: plan.Path.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Create media", err.Error())
		return
	}
	setMediaState(&plan, media)
	entityLabels, err := applyEntityLabels(callCtx, client, "media", fmt.Sprintf("%d", media.GetId()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Create media labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes name, path, size, and labels from Rookery; a not-found media is removed from
// state so Terraform plans a replacement.
func (r *mediaResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state mediaModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read media", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	media, err := client.Media.GetMedia(callCtx, &rookeryv1.GetMediaRequest{Id: state.ID.ValueInt64()})
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Read media", err.Error())
		return
	}
	setMediaState(&state, media)
	entityLabels, err := readEntityLabels(callCtx, client, "media", fmt.Sprintf("%d", state.ID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Read media labels", err.Error())
		return
	}
	state.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update applies label changes only, since name and path force replacement, and writes the
// resulting label set back to state.
func (r *mediaResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan mediaModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Update media labels", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	entityLabels, err := applyEntityLabels(callCtx, client, "media", fmt.Sprintf("%d", plan.ID.ValueInt64()), plan.Labels)
	if err != nil {
		resp.Diagnostics.AddError("Update media labels", err.Error())
		return
	}
	plan.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the media registration and treats a not-found response as success.
func (r *mediaResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state mediaModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Delete media", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	if _, err := client.Media.DeleteMedia(callCtx, &rookeryv1.DeleteMediaRequest{Id: state.ID.ValueInt64()}); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Delete media", err.Error())
	}
}

// ImportState imports an existing media by numeric id, reporting a diagnostic when the id is
// not a valid positive integer.
func (r *mediaResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

// setMediaState copies the proto media fields, including the server-derived size, into the
// Terraform model.
func setMediaState(state *mediaModel, media *rookeryv1.Media) {
	state.ID = types.Int64Value(media.GetId())
	state.Name = types.StringValue(media.GetName())
	state.Path = types.StringValue(media.GetPath())
	state.SizeBytes = types.Int64Value(media.GetSizeBytes())
}
