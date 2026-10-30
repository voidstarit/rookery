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
	_ datasource.DataSource              = (*networkDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*networkDataSource)(nil)
)

type networkDataSource struct{ configuredDataSource }

// NewNetworkDataSource creates the rookery_network data source.
func NewNetworkDataSource() datasource.DataSource {
	return &networkDataSource{}
}

func (d *networkDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network"
}

func (d *networkDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a Rookery network by id or name.",
		Attributes: map[string]dschema.Attribute{
			"labels":           labelsDataSourceAttribute(),
			"id":               dschema.Int64Attribute{Optional: true, Computed: true},
			"name":             dschema.StringAttribute{Optional: true, Computed: true},
			"ipv4_cidr":        dschema.StringAttribute{Computed: true},
			"ipv6_cidr":        dschema.StringAttribute{Computed: true},
			"bridge_domain_id": dschema.Int64Attribute{Computed: true},
			"interface_count":  dschema.Int64Attribute{Computed: true},
			"bvi_host_name":    dschema.StringAttribute{Computed: true},
			"bvi_sw_if_index":  dschema.Int64Attribute{Computed: true},
			"ipv4_gateway":     dschema.StringAttribute{Computed: true},
			"route_table_id":   dschema.Int64Attribute{Computed: true},
		},
	}
}

func (d *networkDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config networkModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read network", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read network", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var network *rookeryv1.Network
	if hasID {
		network, err = client.Networks.GetNetwork(callCtx, &rookeryv1.GetNetworkRequest{Id: config.ID.ValueInt64()})
	} else {
		var response *rookeryv1.ListNetworksResponse
		response, err = client.Networks.ListNetworks(callCtx, &rookeryv1.ListNetworksRequest{})
		if err == nil {
			network, err = findNetwork(response.GetNetworks(), config.Name.ValueString())
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read network", err.Error())
		return
	}
	setNetworkState(&config, network)
	entityLabels, err := readEntityLabels(callCtx, client, "network", fmt.Sprintf("%d", network.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read network labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func findNetwork(networks []*rookeryv1.Network, name string) (*rookeryv1.Network, error) {
	var found *rookeryv1.Network
	for _, network := range networks {
		if strings.EqualFold(network.GetName(), name) {
			if found != nil {
				return nil, fmt.Errorf("multiple networks match name %q", name)
			}
			found = network
		}
	}
	if found == nil {
		return nil, fmt.Errorf("network %q not found", name)
	}
	return found, nil
}
