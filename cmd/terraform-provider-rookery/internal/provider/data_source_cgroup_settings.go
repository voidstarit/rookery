package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ datasource.DataSource              = (*cgroupSettingsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*cgroupSettingsDataSource)(nil)
)

type cgroupSettingsDataSource struct{ configuredDataSource }

// NewCgroupSettingsDataSource creates the rookery_cgroup_settings data source.
func NewCgroupSettingsDataSource() datasource.DataSource {
	return &cgroupSettingsDataSource{}
}

func (d *cgroupSettingsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cgroup_settings"
}

func (d *cgroupSettingsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Reads the host-wide cgroup v2 configuration and runtime status.",
		Attributes: map[string]dschema.Attribute{
			"id":               dschema.StringAttribute{Computed: true},
			"enabled":          dschema.BoolAttribute{Computed: true},
			"cpus":             dschema.StringAttribute{Computed: true},
			"mems":             dschema.StringAttribute{Computed: true},
			"memory_max_bytes": dschema.Int64Attribute{Computed: true},
			"cpu_weight":       dschema.Int64Attribute{Computed: true},
			"io_limits": dschema.ListNestedAttribute{
				Computed: true,
				NestedObject: dschema.NestedAttributeObject{
					Attributes: cgroupIOLimitDataSourceAttributes(),
				},
			},
			"available":   dschema.BoolAttribute{Computed: true},
			"configured":  dschema.BoolAttribute{Computed: true},
			"reason":      dschema.StringAttribute{Computed: true},
			"mount":       dschema.StringAttribute{Computed: true},
			"root":        dschema.StringAttribute{Computed: true},
			"online_cpus": dschema.ListAttribute{Computed: true, ElementType: types.Int64Type},
			"cpu_model":   dschema.StringAttribute{Computed: true},
			"updated_at":  dschema.StringAttribute{Computed: true},
		},
	}
}

func (d *cgroupSettingsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config cgroupSettingsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read cgroup settings", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	settings, err := client.System.GetCgroupSettings(callCtx, &rookeryv1.GetCgroupSettingsRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Read cgroup settings", err.Error())
		return
	}
	resp.Diagnostics.Append(setCgroupSettingsState(ctx, &config, settings)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
