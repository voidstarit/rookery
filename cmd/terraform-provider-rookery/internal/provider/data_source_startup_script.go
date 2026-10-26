package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
)

var (
	_ datasource.DataSource              = (*startupScriptDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*startupScriptDataSource)(nil)
)

type startupScriptDataSource struct{ configuredDataSource }

// NewStartupScriptDataSource creates the rookery_startup_script data source.
func NewStartupScriptDataSource() datasource.DataSource {
	return &startupScriptDataSource{}
}

func (d *startupScriptDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_startup_script"
}

func (d *startupScriptDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Reads the Rookery startup script or VPP reconnect script status.",
		Attributes: map[string]dschema.Attribute{
			"kind":       dschema.StringAttribute{Required: true, Description: "Script kind: rookery or vpp."},
			"enabled":    dschema.BoolAttribute{Computed: true},
			"script":     dschema.StringAttribute{Computed: true},
			"status":     dschema.StringAttribute{Computed: true},
			"exit_code":  dschema.Int64Attribute{Computed: true},
			"output":     dschema.StringAttribute{Computed: true},
			"ran_at":     dschema.StringAttribute{Computed: true},
			"updated_at": dschema.StringAttribute{Computed: true},
		},
	}
}

func (d *startupScriptDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config startupScriptModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read startup script", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	script, err := getStartupScript(callCtx, client, config.Kind.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Read startup script", err.Error())
		return
	}
	setStartupScriptState(&config, script)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
