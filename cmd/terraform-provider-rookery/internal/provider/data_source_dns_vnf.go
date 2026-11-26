package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ datasource.DataSource              = (*dnsVNFDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*dnsVNFDataSource)(nil)
)

type dnsVNFDataSource struct{ configuredDataSource }

// NewDNSVNFDataSource creates the rookery_dns_vnf data source.
func NewDNSVNFDataSource() datasource.DataSource {
	return &dnsVNFDataSource{}
}

// Metadata registers this data source under the provider-prefixed type name
// <provider>_dns_vnf.
func (d *dnsVNFDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_vnf"
}

// Schema defines the read-only DNS VNF attributes.
func (d *dnsVNFDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a DNS VNF by id or name.",
		Attributes: map[string]dschema.Attribute{
			"labels":          labelsDataSourceAttribute(),
			"id":              dschema.Int64Attribute{Optional: true, Computed: true},
			"name":            dschema.StringAttribute{Optional: true, Computed: true},
			"network_id":      dschema.Int64Attribute{Computed: true},
			"network_name":    dschema.StringAttribute{Computed: true},
			"enabled":         dschema.BoolAttribute{Computed: true},
			"forward":         dschema.BoolAttribute{Computed: true},
			"record_count":    dschema.Int64Attribute{Computed: true},
			"forwarder_count": dschema.Int64Attribute{Computed: true},
			"status":          dschema.StringAttribute{Computed: true},
			"status_detail":   dschema.StringAttribute{Computed: true},
			"tap_sw_if_index": dschema.Int64Attribute{Computed: true},
			"tap_host_name":   dschema.StringAttribute{Computed: true},
			"created_at":      dschema.StringAttribute{Computed: true},
			"updated_at":      dschema.StringAttribute{Computed: true},
		},
	}
}

// Read resolves the DNS VNF by exactly one of id or name, storing its state and
// labels.
func (d *dnsVNFDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config dnsVNFModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read DNS VNF", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read DNS VNF", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var vnf *rookeryv1.DNSVNF
	if hasID {
		vnf, err = client.DNSVNFs.GetDNSVNF(callCtx, &rookeryv1.GetDNSVNFRequest{Id: config.ID.ValueInt64()})
	} else {
		var response *rookeryv1.ListDNSVNFsResponse
		response, err = client.DNSVNFs.ListDNSVNFs(callCtx, &rookeryv1.ListDNSVNFsRequest{})
		if err == nil {
			vnf, err = findVNFByName(response.GetVnfs(), config.Name.ValueString(), func(item *rookeryv1.DNSVNF) string { return item.GetName() })
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read DNS VNF", err.Error())
		return
	}
	setDNSVNFState(&config, vnf)
	entityLabels, err := readEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", vnf.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read DNS VNF labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
