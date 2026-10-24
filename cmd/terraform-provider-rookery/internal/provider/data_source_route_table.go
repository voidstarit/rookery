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
	_ datasource.DataSource              = (*routeTableDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*routeTableDataSource)(nil)
)

type routeTableDataSource struct{ configuredDataSource }

// NewRouteTableDataSource creates the rookery_route_table data source.
func NewRouteTableDataSource() datasource.DataSource {
	return &routeTableDataSource{}
}

func (d *routeTableDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_route_table"
}

func (d *routeTableDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a route table by id, by table_id plus ipv6, or by name. Use table_id 0 for a default table and set ipv6.",
		Attributes: map[string]dschema.Attribute{
			"id":              dschema.Int64Attribute{Optional: true, Computed: true, Description: "Route table record id. Set ipv6 too for the default tables, which both use id 0."},
			"name":            dschema.StringAttribute{Optional: true, Computed: true},
			"table_id":        dschema.Int64Attribute{Optional: true, Computed: true},
			"ipv6":            dschema.BoolAttribute{Optional: true, Computed: true},
			"is_default":      dschema.BoolAttribute{Computed: true},
			"interface_count": dschema.Int64Attribute{Computed: true},
			"route_count":     dschema.Int64Attribute{Computed: true},
			"network_id":      dschema.Int64Attribute{Computed: true},
			"managed":         dschema.BoolAttribute{Computed: true},
		},
	}
}

func (d *routeTableDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config routeTableModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read route table", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasTableID := hasInt64(config.TableID)
	hasName := hasString(config.Name)
	hasIPv6 := !config.IPv6.IsNull() && !config.IPv6.IsUnknown()
	modes := 0
	for _, set := range []bool{hasID, hasTableID, hasName} {
		if set {
			modes++
		}
	}
	if modes != 1 {
		resp.Diagnostics.AddError("Read route table", "set exactly one of id, table_id, or name")
		return
	}
	if hasTableID && !hasIPv6 {
		resp.Diagnostics.AddError("Read route table", "ipv6 is required when looking up by table_id")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	response, err := client.RouteTables.ListRouteTables(callCtx, &rookeryv1.ListRouteTablesRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Read route table", err.Error())
		return
	}
	var found *rookeryv1.RouteTable
	for _, table := range response.GetRouteTables() {
		match := false
		switch {
		case hasID:
			match = table.GetId() == config.ID.ValueInt64() &&
				(!hasIPv6 || table.GetIpv6() == config.IPv6.ValueBool())
		case hasTableID:
			match = int64(table.GetTableId()) == config.TableID.ValueInt64() && table.GetIpv6() == config.IPv6.ValueBool()
		case hasName:
			match = strings.EqualFold(table.GetName(), config.Name.ValueString()) &&
				(!hasIPv6 || table.GetIpv6() == config.IPv6.ValueBool())
		}
		if !match {
			continue
		}
		if found != nil {
			returnHint := "specify ipv6 to disambiguate"
			if hasIPv6 {
				returnHint = "the route table lookup is ambiguous"
			}
			resp.Diagnostics.AddError("Read route table", fmt.Sprintf("multiple route tables match (%s)", returnHint))
			return
		}
		found = table
	}
	if found == nil {
		resp.Diagnostics.AddError("Read route table", "route table not found")
		return
	}
	setRouteTableState(&config, found)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
