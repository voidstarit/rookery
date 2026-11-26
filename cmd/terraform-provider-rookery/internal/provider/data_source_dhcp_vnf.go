package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ datasource.DataSource              = (*dhcpVNFDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*dhcpVNFDataSource)(nil)
)

type dhcpVNFDataSource struct{ configuredDataSource }

// NewDHCPVNFDataSource creates the rookery_dhcp_vnf data source.
func NewDHCPVNFDataSource() datasource.DataSource {
	return &dhcpVNFDataSource{}
}

// Metadata registers this data source under the provider-prefixed type name
// <provider>_dhcp_vnf.
func (d *dhcpVNFDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dhcp_vnf"
}

// Schema defines the read-only DHCP VNF attributes.
func (d *dhcpVNFDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a DHCPv4 or DHCPv6 VNF by id or name.",
		Attributes: map[string]dschema.Attribute{
			"labels":                  labelsDataSourceAttribute(),
			"id":                      dschema.Int64Attribute{Optional: true, Computed: true},
			"name":                    dschema.StringAttribute{Optional: true, Computed: true},
			"network_id":              dschema.Int64Attribute{Computed: true},
			"network_name":            dschema.StringAttribute{Computed: true},
			"family":                  dschema.StringAttribute{Computed: true},
			"enabled":                 dschema.BoolAttribute{Computed: true},
			"gateway":                 dschema.StringAttribute{Computed: true},
			"range_start":             dschema.StringAttribute{Computed: true},
			"range_end":               dschema.StringAttribute{Computed: true},
			"lease_time":              dschema.StringAttribute{Computed: true},
			"dns":                     dschema.StringAttribute{Computed: true},
			"bind_ip":                 dschema.StringAttribute{Computed: true},
			"transport":               dschema.StringAttribute{Computed: true},
			"dns_vnf_id":              dschema.Int64Attribute{Computed: true},
			"dns_domain":              dschema.StringAttribute{Computed: true},
			"dns_ttl":                 dschema.Int64Attribute{Computed: true},
			"static_assignment_count": dschema.Int64Attribute{Computed: true},
			"option_count":            dschema.Int64Attribute{Computed: true},
			"lease_count":             dschema.Int64Attribute{Computed: true},
			"status":                  dschema.StringAttribute{Computed: true},
			"status_detail":           dschema.StringAttribute{Computed: true},
			"tap_sw_if_index":         dschema.Int64Attribute{Computed: true},
			"tap_host_name":           dschema.StringAttribute{Computed: true},
			"created_at":              dschema.StringAttribute{Computed: true},
			"updated_at":              dschema.StringAttribute{Computed: true},
		},
	}
}

// Read resolves the DHCP VNF by exactly one of id or name, storing its state and
// labels.
func (d *dhcpVNFDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config dhcpVNFModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read DHCP VNF", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read DHCP VNF", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var vnf *rookeryv1.DHCPVNF
	if hasID {
		vnf, err = client.DHCPVNFs.GetDHCPVNF(callCtx, &rookeryv1.GetDHCPVNFRequest{Id: config.ID.ValueInt64()})
	} else {
		var response *rookeryv1.ListDHCPVNFsResponse
		response, err = client.DHCPVNFs.ListDHCPVNFs(callCtx, &rookeryv1.ListDHCPVNFsRequest{})
		if err == nil {
			vnf, err = findVNFByName(response.GetVnfs(), config.Name.ValueString(), func(item *rookeryv1.DHCPVNF) string { return item.GetName() })
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read DHCP VNF", err.Error())
		return
	}
	setDHCPVNFState(&config, vnf)
	entityLabels, err := readEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", vnf.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read DHCP VNF labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
