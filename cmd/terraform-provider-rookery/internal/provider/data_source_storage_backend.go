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
	_ datasource.DataSource              = (*storageBackendDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*storageBackendDataSource)(nil)
)

type storageBackendDataSource struct{ configuredDataSource }

// NewStorageBackendDataSource creates the rookery_storage_backend data source.
func NewStorageBackendDataSource() datasource.DataSource {
	return &storageBackendDataSource{}
}

func (d *storageBackendDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage_backend"
}

func (d *storageBackendDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a storage backend by id or name.",
		Attributes: map[string]dschema.Attribute{
			"labels":      labelsDataSourceAttribute(),
			"id":          dschema.Int64Attribute{Optional: true, Computed: true, Description: "Backend id."},
			"name":        dschema.StringAttribute{Optional: true, Computed: true, Description: "Backend name."},
			"title":       dschema.StringAttribute{Computed: true},
			"description": dschema.StringAttribute{Computed: true},
			"driver":      dschema.StringAttribute{Computed: true},
			"config": dschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dschema.Attribute{
					"vg":                                dschema.StringAttribute{Computed: true},
					"thin_pool":                         dschema.StringAttribute{Computed: true},
					"supports_templates":                dschema.BoolAttribute{Computed: true},
					"preparation_script":                dschema.StringAttribute{Computed: true},
					"preparation_timeout":               dschema.StringAttribute{Computed: true},
					"allow_template_delete_with_clones": dschema.BoolAttribute{Computed: true},
				},
			},
			"disk_count": dschema.Int64Attribute{Computed: true},
		},
	}
}

func (d *storageBackendDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config storageBackendModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read storage backend", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read storage backend", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var backend *rookeryv1.StorageBackend
	if hasID {
		backend, err = client.StorageBackends.GetStorageBackend(callCtx, &rookeryv1.GetStorageBackendRequest{Id: config.ID.ValueInt64()})
	} else {
		var response *rookeryv1.ListStorageBackendsResponse
		response, err = client.StorageBackends.ListStorageBackends(callCtx, &rookeryv1.ListStorageBackendsRequest{})
		if err == nil {
			backend, err = findStorageBackend(response.GetBackends(), config.Name.ValueString())
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read storage backend", err.Error())
		return
	}
	resp.Diagnostics.Append(setStorageBackendState(ctx, &config, backend)...)
	entityLabels, err := readEntityLabels(callCtx, client, "storage_backend", fmt.Sprintf("%d", backend.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read storage backend labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func findStorageBackend(backends []*rookeryv1.StorageBackend, name string) (*rookeryv1.StorageBackend, error) {
	var found *rookeryv1.StorageBackend
	for _, backend := range backends {
		if strings.EqualFold(backend.GetName(), name) {
			if found != nil {
				return nil, fmt.Errorf("multiple storage backends match name %q", name)
			}
			found = backend
		}
	}
	if found == nil {
		return nil, fmt.Errorf("storage backend %q not found", name)
	}
	return found, nil
}
