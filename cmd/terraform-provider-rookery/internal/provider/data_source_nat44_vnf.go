package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ datasource.DataSource              = (*nat44VNFDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*nat44VNFDataSource)(nil)
)

type nat44VNFDataSource struct{ configuredDataSource }

// NewNAT44VNFDataSource creates the rookery_nat44_vnf data source.
func NewNAT44VNFDataSource() datasource.DataSource {
	return &nat44VNFDataSource{}
}

// Metadata registers this data source under the provider-prefixed type name
// <provider>_nat44_vnf.
func (d *nat44VNFDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_nat44_vnf"
}

// Schema defines the read-only NAT44 VNF attributes.
func (d *nat44VNFDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a NAT44 VNF by id or name.",
		Attributes: map[string]dschema.Attribute{
			"labels":            labelsDataSourceAttribute(),
			"id":                dschema.Int64Attribute{Optional: true, Computed: true},
			"name":              dschema.StringAttribute{Optional: true, Computed: true},
			"network_id":        dschema.Int64Attribute{Computed: true},
			"network_name":      dschema.StringAttribute{Computed: true},
			"enabled":           dschema.BoolAttribute{Computed: true},
			"gateway":           dschema.StringAttribute{Computed: true},
			"outside_interface": dschema.StringAttribute{Computed: true},
			"outside_address":   dschema.StringAttribute{Computed: true},
			"outside_gateway":   dschema.StringAttribute{Computed: true},
			"nat44_pool":        dschema.StringAttribute{Computed: true},
			"dnat_rule_count":   dschema.Int64Attribute{Computed: true},
			"status":            dschema.StringAttribute{Computed: true},
			"status_detail":     dschema.StringAttribute{Computed: true},
			"tap_sw_if_index":   dschema.Int64Attribute{Computed: true},
			"tap_host_name":     dschema.StringAttribute{Computed: true},
			"created_at":        dschema.StringAttribute{Computed: true},
			"updated_at":        dschema.StringAttribute{Computed: true},
		},
	}
}

// Read resolves the NAT44 VNF by exactly one of id or name, storing its state
// and labels.
func (d *nat44VNFDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config nat44VNFModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read NAT44 VNF", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read NAT44 VNF", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var vnf *rookeryv1.NAT44VNF
	if hasID {
		vnf, err = client.NAT44VNFs.GetNAT44VNF(callCtx, &rookeryv1.GetNAT44VNFRequest{Id: config.ID.ValueInt64()})
	} else {
		var response *rookeryv1.ListNAT44VNFsResponse
		response, err = client.NAT44VNFs.ListNAT44VNFs(callCtx, &rookeryv1.ListNAT44VNFsRequest{})
		if err == nil {
			vnf, err = findVNFByName(response.GetVnfs(), config.Name.ValueString(), func(item *rookeryv1.NAT44VNF) string { return item.GetName() })
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read NAT44 VNF", err.Error())
		return
	}
	setNAT44VNFState(&config, vnf)
	entityLabels, err := readEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", vnf.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read NAT44 VNF labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
