package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ datasource.DataSource              = (*hostTapDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*hostTapDataSource)(nil)
)

type hostTapDataSource struct{ configuredDataSource }

// NewHostTapDataSource creates the rookery_host_tap data source.
func NewHostTapDataSource() datasource.DataSource {
	return &hostTapDataSource{}
}

func (d *hostTapDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_host_tap"
}

func (d *hostTapDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a network host tap by id or name.",
		Attributes: map[string]dschema.Attribute{
			"labels":        labelsDataSourceAttribute(),
			"id":            dschema.Int64Attribute{Optional: true, Computed: true},
			"network_id":    dschema.Int64Attribute{Computed: true},
			"network_name":  dschema.StringAttribute{Computed: true},
			"name":          dschema.StringAttribute{Optional: true, Computed: true},
			"sw_if_index":   dschema.Int64Attribute{Computed: true},
			"ipv4_address":  dschema.StringAttribute{Computed: true},
			"vpp_addresses": dschema.ListAttribute{Computed: true, ElementType: types.StringType},
		},
	}
}

func (d *hostTapDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config hostTapModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read host tap", "set exactly one of id or name")
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read host tap", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.HostInterfaces.ListHostTaps(callCtx, &rookeryv1.ListHostTapsRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Read host tap", err.Error())
		return
	}
	var found *rookeryv1.HostTap
	for _, tap := range response.GetTaps() {
		match := hasID && tap.GetId() == config.ID.ValueInt64() ||
			hasName && strings.EqualFold(tap.GetName(), config.Name.ValueString())
		if !match {
			continue
		}
		if found != nil {
			resp.Diagnostics.AddError("Read host tap", "multiple host taps match the lookup")
			return
		}
		found = tap
	}
	if found == nil {
		resp.Diagnostics.AddError("Read host tap", "host tap not found")
		return
	}
	resp.Diagnostics.Append(setHostTapState(ctx, &config, found)...)
	entityLabels, err := readEntityLabels(callCtx, client, "host_tap", fmt.Sprintf("%d", found.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read host tap labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
