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
	_ datasource.DataSource              = (*bgpVNFDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*bgpVNFDataSource)(nil)
)

type bgpVNFDataSource struct{ configuredDataSource }

// NewBGPVNFDataSource creates the rookery_bgp_vnf data source.
func NewBGPVNFDataSource() datasource.DataSource {
	return &bgpVNFDataSource{}
}

type bgpVNFDataModel struct {
	ID                    types.Int64  `tfsdk:"id"`
	Name                  types.String `tfsdk:"name"`
	NetworkID             types.Int64  `tfsdk:"network_id"`
	NetworkName           types.String `tfsdk:"network_name"`
	Enabled               types.Bool   `tfsdk:"enabled"`
	Transport             types.String `tfsdk:"transport"`
	LocalAS               types.Int64  `tfsdk:"local_as"`
	RouterID              types.String `tfsdk:"router_id"`
	PeerAS                types.Int64  `tfsdk:"peer_as"`
	PeerIP                types.String `tfsdk:"peer_ip"`
	LocalIP               types.String `tfsdk:"local_ip"`
	PeerGateway           types.String `tfsdk:"peer_gateway"`
	MultihopTTL           types.Int64  `tfsdk:"multihop_ttl"`
	Passive               types.Bool   `tfsdk:"passive"`
	TargetTableID         types.Int64  `tfsdk:"target_table_id"`
	NextHop               types.String `tfsdk:"next_hop"`
	AdvertisedPrefixCount types.Int64  `tfsdk:"advertised_prefix_count"`
	Status                types.String `tfsdk:"status"`
	StatusDetail          types.String `tfsdk:"status_detail"`
	TapSWIfIndex          types.Int64  `tfsdk:"tap_sw_if_index"`
	TapHostName           types.String `tfsdk:"tap_host_name"`
	CreatedAt             types.String `tfsdk:"created_at"`
	UpdatedAt             types.String `tfsdk:"updated_at"`
	Labels                types.Set    `tfsdk:"labels"`
}

// Metadata registers this data source under the provider-prefixed type name
// <provider>_bgp_vnf.
func (d *bgpVNFDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bgp_vnf"
}

// Schema defines the read-only BGP VNF attributes. The write-only password is
// deliberately absent.
func (d *bgpVNFDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a BGP VNF by id or name.",
		Attributes: map[string]dschema.Attribute{
			"labels":                  labelsDataSourceAttribute(),
			"id":                      dschema.Int64Attribute{Optional: true, Computed: true},
			"name":                    dschema.StringAttribute{Optional: true, Computed: true},
			"network_id":              dschema.Int64Attribute{Computed: true},
			"network_name":            dschema.StringAttribute{Computed: true},
			"enabled":                 dschema.BoolAttribute{Computed: true},
			"transport":               dschema.StringAttribute{Computed: true},
			"local_as":                dschema.Int64Attribute{Computed: true},
			"router_id":               dschema.StringAttribute{Computed: true},
			"peer_as":                 dschema.Int64Attribute{Computed: true},
			"peer_ip":                 dschema.StringAttribute{Computed: true},
			"local_ip":                dschema.StringAttribute{Computed: true},
			"peer_gateway":            dschema.StringAttribute{Computed: true},
			"multihop_ttl":            dschema.Int64Attribute{Computed: true},
			"passive":                 dschema.BoolAttribute{Computed: true},
			"target_table_id":         dschema.Int64Attribute{Computed: true},
			"next_hop":                dschema.StringAttribute{Computed: true},
			"advertised_prefix_count": dschema.Int64Attribute{Computed: true},
			"status":                  dschema.StringAttribute{Computed: true},
			"status_detail":           dschema.StringAttribute{Computed: true},
			"tap_sw_if_index":         dschema.Int64Attribute{Computed: true},
			"tap_host_name":           dschema.StringAttribute{Computed: true},
			"created_at":              dschema.StringAttribute{Computed: true},
			"updated_at":              dschema.StringAttribute{Computed: true},
		},
	}
}

// Read resolves the BGP VNF by exactly one of id or name, storing its state and
// labels.
func (d *bgpVNFDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config bgpVNFDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read BGP VNF", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read BGP VNF", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var vnf *rookeryv1.BGPVNF
	if hasID {
		vnf, err = client.BGPVNFs.GetBGPVNF(callCtx, &rookeryv1.GetBGPVNFRequest{Id: config.ID.ValueInt64()})
	} else {
		var response *rookeryv1.ListBGPVNFsResponse
		response, err = client.BGPVNFs.ListBGPVNFs(callCtx, &rookeryv1.ListBGPVNFsRequest{})
		if err == nil {
			vnf, err = findVNFByName(response.GetVnfs(), config.Name.ValueString(), func(item *rookeryv1.BGPVNF) string { return item.GetName() })
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read BGP VNF", err.Error())
		return
	}
	setBGPDataState(&config, vnf)
	entityLabels, err := readEntityLabels(callCtx, client, "vnf", fmt.Sprintf("%d", vnf.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read BGP VNF labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

// setBGPDataState copies the server's BGP VNF into data source state without the
// write-only password.
func setBGPDataState(state *bgpVNFDataModel, vnf *rookeryv1.BGPVNF) {
	state.ID = types.Int64Value(vnf.GetId())
	state.Name = types.StringValue(vnf.GetName())
	state.NetworkID = types.Int64Value(vnf.GetNetworkId())
	state.NetworkName = types.StringValue(vnf.GetNetworkName())
	state.Enabled = types.BoolValue(vnf.GetEnabled())
	state.Transport = types.StringValue(vnf.GetTransport())
	state.LocalAS = types.Int64Value(int64(vnf.GetLocalAs()))
	state.RouterID = types.StringValue(vnf.GetRouterId())
	state.PeerAS = types.Int64Value(int64(vnf.GetPeerAs()))
	state.PeerIP = types.StringValue(vnf.GetPeerIp())
	state.LocalIP = types.StringValue(vnf.GetLocalIp())
	state.PeerGateway = types.StringValue(vnf.GetPeerGateway())
	state.MultihopTTL = types.Int64Value(int64(vnf.GetMultihopTtl()))
	state.Passive = types.BoolValue(vnf.GetPassive())
	state.TargetTableID = types.Int64Value(int64(vnf.GetTargetTableId()))
	state.NextHop = types.StringValue(vnf.GetNextHop())
	state.AdvertisedPrefixCount = int64Count(vnf.GetAdvertisedPrefixCount())
	state.Status = types.StringValue(vnf.GetStatus())
	state.StatusDetail = types.StringValue(vnf.GetStatusDetail())
	state.TapSWIfIndex = types.Int64Value(int64(vnf.GetTapSwIfIndex()))
	state.TapHostName = types.StringValue(vnf.GetTapHostName())
	state.CreatedAt = timestampString(vnf.GetCreatedAt())
	state.UpdatedAt = timestampString(vnf.GetUpdatedAt())
}
