package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ datasource.DataSource              = (*hostInterfaceDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*hostInterfaceDataSource)(nil)
)

type hostInterfaceDataSource struct{ configuredDataSource }

// NewHostInterfaceDataSource creates the rookery_host_interface data source.
func NewHostInterfaceDataSource() datasource.DataSource {
	return &hostInterfaceDataSource{}
}

func (d *hostInterfaceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_host_interface"
}

func (d *hostInterfaceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a physical host interface by NIC name.",
		Attributes: map[string]dschema.Attribute{
			"nic":           dschema.StringAttribute{Required: true, Description: "Host network interface name."},
			"up":            dschema.BoolAttribute{Computed: true},
			"addresses":     dschema.ListAttribute{Computed: true, ElementType: types.StringType},
			"default_route": dschema.BoolAttribute{Computed: true},
			"physical":      dschema.BoolAttribute{Computed: true},
			"wireless":      dschema.BoolAttribute{Computed: true},
			"in_vpp":        dschema.BoolAttribute{Computed: true},
			"vpp_checked":   dschema.BoolAttribute{Computed: true},
			"vpp_name":      dschema.StringAttribute{Computed: true},
			"vpp_up":        dschema.BoolAttribute{Computed: true},
			"vpp_type":      dschema.StringAttribute{Computed: true},
			"managed":       dschema.BoolAttribute{Computed: true},
			"vpp_addresses": dschema.ListAttribute{Computed: true, ElementType: types.StringType},
		},
	}
}

func (d *hostInterfaceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config hostInterfaceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read host interface", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.HostInterfaces.ListHostInterfaces(callCtx, &rookeryv1.ListHostInterfacesRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Read host interface", err.Error())
		return
	}
	var found *rookeryv1.HostInterface
	for _, iface := range response.GetInterfaces() {
		if iface.GetName() == config.NIC.ValueString() {
			found = iface
			break
		}
	}
	if found == nil {
		resp.Diagnostics.AddError("Read host interface", fmt.Sprintf("host interface %q not found", config.NIC.ValueString()))
		return
	}
	resp.Diagnostics.Append(setHostInterfaceState(ctx, &config, found)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
