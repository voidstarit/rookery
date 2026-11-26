package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ datasource.DataSource              = (*vppInterfaceRouteTablesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*vppInterfaceRouteTablesDataSource)(nil)
)

type vppInterfaceRouteTablesDataSource struct{ configuredDataSource }

// NewVPPInterfaceRouteTablesDataSource creates the
// rookery_vpp_interface_route_tables data source.
func NewVPPInterfaceRouteTablesDataSource() datasource.DataSource {
	return &vppInterfaceRouteTablesDataSource{}
}

// Metadata registers the data source as <provider>_vpp_interface_route_tables.
func (d *vppInterfaceRouteTablesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpp_interface_route_tables"
}

// Schema defines the required interface name and the computed IPv4/IPv6 route-table bindings.
func (d *vppInterfaceRouteTablesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Reads the effective IPv4/IPv6 route-table binding for a host interface, host tap, or pipe endpoint.",
		Attributes: map[string]dschema.Attribute{
			"name":            dschema.StringAttribute{Required: true},
			"ipv4_table_id":   dschema.Int64Attribute{Computed: true},
			"ipv6_table_id":   dschema.Int64Attribute{Computed: true},
			"ipv4_table_name": dschema.StringAttribute{Computed: true},
			"ipv6_table_name": dschema.StringAttribute{Computed: true},
		},
	}
}

// Read fetches the effective IPv4/IPv6 route-table binding for the required interface name.
func (d *vppInterfaceRouteTablesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config vppInterfaceRouteTablesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read interface route tables", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.Interfaces.GetVPPInterfaceRouteTables(callCtx, &rookeryv1.GetVPPInterfaceRouteTablesRequest{Name: config.Name.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Read interface route tables", err.Error())
		return
	}
	setVPPInterfaceRouteTablesState(&config, response)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
