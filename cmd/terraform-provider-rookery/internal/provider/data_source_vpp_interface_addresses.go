package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ datasource.DataSource              = (*vppInterfaceAddressesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*vppInterfaceAddressesDataSource)(nil)
)

type vppInterfaceAddressesDataSource struct{ configuredDataSource }

// NewVPPInterfaceAddressesDataSource creates the
// rookery_vpp_interface_addresses data source.
func NewVPPInterfaceAddressesDataSource() datasource.DataSource {
	return &vppInterfaceAddressesDataSource{}
}

func (d *vppInterfaceAddressesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpp_interface_addresses"
}

func (d *vppInterfaceAddressesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Reads managed addresses and DHCP state for a Rookery VPP interface.",
		Attributes: map[string]dschema.Attribute{
			"name":                   dschema.StringAttribute{Required: true},
			"addresses":              dschema.SetAttribute{Computed: true, ElementType: types.StringType},
			"dhcp_client_enabled":    dschema.BoolAttribute{Computed: true},
			"live_addresses":         dschema.ListAttribute{Computed: true, ElementType: types.StringType},
			"dhcp_client_configured": dschema.BoolAttribute{Computed: true},
			"dhcp_client_status":     dschema.StringAttribute{Computed: true},
			"status_detail":          dschema.StringAttribute{Computed: true},
		},
	}
}

func (d *vppInterfaceAddressesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config vppInterfaceAddressesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read VPP interface addresses", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.Interfaces.GetVPPInterfaceAddresses(callCtx, &rookeryv1.GetVPPInterfaceAddressesRequest{Name: config.Name.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Read VPP interface addresses", err.Error())
		return
	}
	resp.Diagnostics.Append(setVPPInterfaceAddressesState(ctx, &config, response)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
