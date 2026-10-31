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
	_ datasource.DataSource              = (*interfaceDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*interfaceDataSource)(nil)
)

type interfaceDataSource struct{ configuredDataSource }

// NewInterfaceDataSource creates the rookery_interface data source.
func NewInterfaceDataSource() datasource.DataSource {
	return &interfaceDataSource{}
}

func (d *interfaceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_interface"
}

func (d *interfaceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a vhost-user interface by id or name.",
		Attributes: map[string]dschema.Attribute{
			"labels":           labelsDataSourceAttribute(),
			"id":               dschema.Int64Attribute{Optional: true, Computed: true},
			"name":             dschema.StringAttribute{Optional: true, Computed: true},
			"network_id":       dschema.Int64Attribute{Computed: true},
			"network_name":     dschema.StringAttribute{Computed: true},
			"attached_vm_id":   dschema.StringAttribute{Computed: true},
			"attached_vm_name": dschema.StringAttribute{Computed: true},
			"socket_path":      dschema.StringAttribute{Computed: true},
			"mac":              dschema.StringAttribute{Computed: true},
			"sw_if_index":      dschema.Int64Attribute{Computed: true},
			"ingress_acl_name": dschema.StringAttribute{Computed: true},
			"egress_acl_name":  dschema.StringAttribute{Computed: true},
			"ingress_acls":     dschema.ListAttribute{Computed: true, ElementType: types.StringType},
			"egress_acls":      dschema.ListAttribute{Computed: true, ElementType: types.StringType},
			"ipv4_table_id":    dschema.Int64Attribute{Computed: true},
			"ipv6_table_id":    dschema.Int64Attribute{Computed: true},
			"ipv4_table_name":  dschema.StringAttribute{Computed: true},
			"ipv6_table_name":  dschema.StringAttribute{Computed: true},
		},
	}
}

func (d *interfaceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config interfaceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read interface", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read interface", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var iface *rookeryv1.VirtualInterface
	if hasID {
		iface, err = client.Interfaces.GetInterface(callCtx, &rookeryv1.GetInterfaceRequest{Id: config.ID.ValueInt64()})
	} else {
		var response *rookeryv1.ListInterfacesResponse
		response, err = client.Interfaces.ListInterfaces(callCtx, &rookeryv1.ListInterfacesRequest{})
		if err == nil {
			iface, err = findInterface(response.GetInterfaces(), config.Name.ValueString())
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read interface", err.Error())
		return
	}
	resp.Diagnostics.Append(setInterfaceState(ctx, &config, iface)...)
	entityLabels, err := readEntityLabels(callCtx, client, "interface", fmt.Sprintf("%d", iface.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read interface labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func findInterface(interfaces []*rookeryv1.VirtualInterface, name string) (*rookeryv1.VirtualInterface, error) {
	var found *rookeryv1.VirtualInterface
	for _, iface := range interfaces {
		if strings.EqualFold(iface.GetName(), name) {
			if found != nil {
				return nil, fmt.Errorf("multiple interfaces match name %q", name)
			}
			found = iface
		}
	}
	if found == nil {
		return nil, fmt.Errorf("interface %q not found", name)
	}
	return found, nil
}
