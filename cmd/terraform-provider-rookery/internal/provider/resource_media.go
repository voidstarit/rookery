package provider

import (
	"context"
	"errors"

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
}

func (r *mediaResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_media"
}

func (r *mediaResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A read-only installation media file registered with Rookery.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
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
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

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
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *mediaResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update media", errors.New("media records are immutable; replace the resource instead").Error())
}

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

func (r *mediaResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importInt64ID(ctx, req, resp)
}

func setMediaState(state *mediaModel, media *rookeryv1.Media) {
	state.ID = types.Int64Value(media.GetId())
	state.Name = types.StringValue(media.GetName())
	state.Path = types.StringValue(media.GetPath())
	state.SizeBytes = types.Int64Value(media.GetSizeBytes())
}
