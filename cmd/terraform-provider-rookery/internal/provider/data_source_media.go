package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ datasource.DataSource              = (*mediaDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*mediaDataSource)(nil)
)

type mediaDataSource struct{ configuredDataSource }

// NewMediaDataSource creates the rookery_media data source.
func NewMediaDataSource() datasource.DataSource {
	return &mediaDataSource{}
}

func (d *mediaDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_media"
}

func (d *mediaDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up registered installation media by id or name.",
		Attributes: map[string]dschema.Attribute{
			"labels":     labelsDataSourceAttribute(),
			"id":         dschema.Int64Attribute{Optional: true, Computed: true},
			"name":       dschema.StringAttribute{Optional: true, Computed: true},
			"path":       dschema.StringAttribute{Computed: true},
			"size_bytes": dschema.Int64Attribute{Computed: true},
		},
	}
}

func (d *mediaDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config mediaModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read media", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read media", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var media *rookeryv1.Media
	if hasID {
		media, err = client.Media.GetMedia(callCtx, &rookeryv1.GetMediaRequest{Id: config.ID.ValueInt64()})
	} else {
		var response *rookeryv1.ListMediaResponse
		response, err = client.Media.ListMedia(callCtx, &rookeryv1.ListMediaRequest{})
		if err == nil {
			media, err = findMedia(response.GetMedia(), config.Name.ValueString())
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read media", err.Error())
		return
	}
	setMediaState(&config, media)
	entityLabels, err := readEntityLabels(callCtx, client, "media", fmt.Sprintf("%d", media.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read media labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func findMedia(entries []*rookeryv1.Media, name string) (*rookeryv1.Media, error) {
	var found *rookeryv1.Media
	for _, entry := range entries {
		if strings.EqualFold(entry.GetName(), name) {
			if found != nil {
				return nil, fmt.Errorf("multiple media entries match name %q", name)
			}
			found = entry
		}
	}
	if found == nil {
		return nil, fmt.Errorf("media %q not found", name)
	}
	return found, nil
}
