package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ datasource.DataSource              = (*networkPipeDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*networkPipeDataSource)(nil)
)

type networkPipeDataSource struct{ configuredDataSource }

// NewNetworkPipeDataSource creates the rookery_network_pipe data source.
func NewNetworkPipeDataSource() datasource.DataSource {
	return &networkPipeDataSource{}
}

func (d *networkPipeDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network_pipe"
}

func (d *networkPipeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a bridge-domain pipe by id or by its two network ids.",
		Attributes: map[string]dschema.Attribute{
			"id":                     dschema.Int64Attribute{Optional: true, Computed: true},
			"network_a_id":           dschema.Int64Attribute{Optional: true, Computed: true},
			"network_a_name":         dschema.StringAttribute{Computed: true},
			"network_b_id":           dschema.Int64Attribute{Optional: true, Computed: true},
			"network_b_name":         dschema.StringAttribute{Computed: true},
			"instance":               dschema.Int64Attribute{Computed: true},
			"parent_sw_if_index":     dschema.Int64Attribute{Computed: true},
			"endpoint_a_sw_if_index": dschema.Int64Attribute{Computed: true},
			"endpoint_b_sw_if_index": dschema.Int64Attribute{Computed: true},
			"endpoint_a_name":        dschema.StringAttribute{Computed: true},
			"endpoint_b_name":        dschema.StringAttribute{Computed: true},
		},
	}
}

func (d *networkPipeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config networkPipeModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	hasID := hasInt64(config.ID)
	hasPair := hasInt64(config.NetworkAID) && hasInt64(config.NetworkBID)
	if hasID == hasPair {
		resp.Diagnostics.AddError("Read network pipe", "set exactly one of id or network_a_id plus network_b_id")
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read network pipe", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.Networks.ListNetworkPipes(callCtx, &rookeryv1.ListNetworkPipesRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Read network pipe", err.Error())
		return
	}
	var found *rookeryv1.NetworkPipe
	for _, pipe := range response.GetPipes() {
		match := hasID && pipe.GetId() == config.ID.ValueInt64()
		if hasPair {
			a, b := config.NetworkAID.ValueInt64(), config.NetworkBID.ValueInt64()
			match = pipe.GetNetworkAId() == a && pipe.GetNetworkBId() == b ||
				pipe.GetNetworkAId() == b && pipe.GetNetworkBId() == a
		}
		if !match {
			continue
		}
		if found != nil {
			resp.Diagnostics.AddError("Read network pipe", "multiple pipes match the lookup")
			return
		}
		found = pipe
	}
	if found == nil {
		resp.Diagnostics.AddError("Read network pipe", "network pipe not found")
		return
	}
	setNetworkPipeState(&config, found, false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
