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
	_ datasource.DataSource              = (*routeDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*routeDataSource)(nil)
)

type routeDataSource struct{ configuredDataSource }

// NewRouteDataSource creates the rookery_route data source.
func NewRouteDataSource() datasource.DataSource {
	return &routeDataSource{}
}

func (d *routeDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_route"
}

func (d *routeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a route by id or prefix. route_table_id and ipv6 are required as lookup context; use 0 for the default table.",
		Attributes: map[string]dschema.Attribute{
			"labels":                labelsDataSourceAttribute(),
			"id":                    dschema.Int64Attribute{Optional: true, Computed: true},
			"route_table_id":        dschema.Int64Attribute{Optional: true, Computed: true},
			"ipv6":                  dschema.BoolAttribute{Optional: true, Computed: true},
			"prefix":                dschema.StringAttribute{Optional: true, Computed: true},
			"table_id":              dschema.Int64Attribute{Computed: true},
			"next_hop":              dschema.StringAttribute{Computed: true},
			"interface_id":          dschema.Int64Attribute{Computed: true},
			"interface_name":        dschema.StringAttribute{Computed: true},
			"egress_interface":      dschema.StringAttribute{Computed: true},
			"description":           dschema.StringAttribute{Computed: true},
			"preference":            dschema.Int64Attribute{Computed: true},
			"route_count":           dschema.Int64Attribute{Computed: true},
			"next_hop_table":        dschema.StringAttribute{Computed: true},
			"weight":                dschema.Int64Attribute{Computed: true},
			"udp_encap":             dschema.Int64Attribute{Computed: true},
			"ipv4_lookup_table":     dschema.StringAttribute{Computed: true},
			"ipv6_lookup_table":     dschema.StringAttribute{Computed: true},
			"mpls_lookup_table":     dschema.Int64Attribute{Computed: true},
			"resolve_via_host":      dschema.BoolAttribute{Computed: true},
			"resolve_via_connected": dschema.BoolAttribute{Computed: true},
			"rx_ipv4_interface":     dschema.StringAttribute{Computed: true},
			"rx_ipv6_interface":     dschema.StringAttribute{Computed: true},
			"out_labels":            dschema.ListAttribute{Computed: true, ElementType: types.Int64Type},
			"drop":                  dschema.BoolAttribute{Computed: true},
		},
	}
}

func (d *routeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config routeModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read route", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasPrefix := hasString(config.Prefix)
	hasRouteTable := hasInt64(config.RouteTableID)
	hasIPv6 := !config.IPv6.IsNull() && !config.IPv6.IsUnknown()
	if hasID == hasPrefix {
		resp.Diagnostics.AddError("Read route", "set exactly one of id or prefix")
		return
	}
	if !hasRouteTable || !hasIPv6 {
		resp.Diagnostics.AddError("Read route", "route_table_id and ipv6 are required")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.RouteTables.ListRoutes(callCtx, &rookeryv1.ListRoutesRequest{
		RouteTableId: config.RouteTableID.ValueInt64(),
		Ipv6:         config.IPv6.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Read route", err.Error())
		return
	}
	var found *rookeryv1.Route
	for _, route := range response.GetRoutes() {
		match := route.GetId() == config.ID.ValueInt64()
		if hasPrefix {
			match = route.GetPrefix() == config.Prefix.ValueString()
		}
		if match {
			found = route
			break
		}
	}
	if found == nil {
		resp.Diagnostics.AddError("Read route", "route not found")
		return
	}
	resp.Diagnostics.Append(setRouteState(ctx, &config, found)...)
	entityLabels, err := readEntityLabels(callCtx, client, "route", fmt.Sprintf("%d", found.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read route labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
