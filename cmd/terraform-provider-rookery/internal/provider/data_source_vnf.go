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
	_ datasource.DataSource              = (*vnfDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*vnfDataSource)(nil)
)

type vnfDataSource struct{ configuredDataSource }

// NewVNFDataSource creates the rookery_vnf data source.
func NewVNFDataSource() datasource.DataSource {
	return &vnfDataSource{}
}

func (d *vnfDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vnf"
}

func (d *vnfDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a network function by id or name.",
		Attributes: map[string]dschema.Attribute{
			"labels":          labelsDataSourceAttribute(),
			"id":              dschema.Int64Attribute{Optional: true, Computed: true},
			"name":            dschema.StringAttribute{Optional: true, Computed: true},
			"network_id":      dschema.Int64Attribute{Computed: true},
			"network_name":    dschema.StringAttribute{Computed: true},
			"type":            dschema.StringAttribute{Computed: true},
			"enabled":         dschema.BoolAttribute{Computed: true},
			"config_json":     dschema.StringAttribute{Computed: true, Sensitive: true},
			"tap_sw_if_index": dschema.Int64Attribute{Computed: true},
			"tap_host_name":   dschema.StringAttribute{Computed: true},
			"status":          dschema.StringAttribute{Computed: true},
			"status_detail":   dschema.StringAttribute{Computed: true},
			"lease_count":     dschema.Int64Attribute{Computed: true},
		},
	}
}

func (d *vnfDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config vnfModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read VNF", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read VNF", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var vnf *rookeryv1.VNF
	if hasID {
		vnf, err = client.VNFs.GetVNF(callCtx, &rookeryv1.GetVNFRequest{Id: config.ID.ValueInt64()})
	} else {
		var response *rookeryv1.ListVNFsResponse
		response, err = client.VNFs.ListVNFs(callCtx, &rookeryv1.ListVNFsRequest{})
		if err == nil {
			vnf, err = findVNF(response.GetVnfs(), config.Name.ValueString())
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read VNF", err.Error())
		return
	}
	resp.Diagnostics.Append(setVNFState(&config, vnf)...)
	entityLabels, err := readEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", vnf.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read VNF labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func findVNF(vnfs []*rookeryv1.VNF, name string) (*rookeryv1.VNF, error) {
	var found *rookeryv1.VNF
	for _, vnf := range vnfs {
		if strings.EqualFold(vnf.GetName(), name) {
			if found != nil {
				return nil, fmt.Errorf("multiple VNFs match name %q", name)
			}
			found = vnf
		}
	}
	if found == nil {
		return nil, fmt.Errorf("VNF %q not found", name)
	}
	return found, nil
}
