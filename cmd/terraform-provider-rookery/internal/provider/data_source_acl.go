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
	_ datasource.DataSource              = (*aclDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*aclDataSource)(nil)
)

type aclDataSource struct{ configuredDataSource }

// NewACLDataSource creates the rookery_acl data source.
func NewACLDataSource() datasource.DataSource {
	return &aclDataSource{}
}

func (d *aclDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_acl"
}

func (d *aclDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a named ACL and its ordered rules by id or name.",
		Attributes: map[string]dschema.Attribute{
			"labels":      labelsDataSourceAttribute(),
			"id":          dschema.Int64Attribute{Optional: true, Computed: true},
			"name":        dschema.StringAttribute{Optional: true, Computed: true},
			"title":       dschema.StringAttribute{Computed: true},
			"description": dschema.StringAttribute{Computed: true},
			"vpp_index":   dschema.Int64Attribute{Computed: true},
			"rules": dschema.ListNestedAttribute{
				Computed: true,
				NestedObject: dschema.NestedAttributeObject{
					Attributes: map[string]dschema.Attribute{
						"action":                 dschema.StringAttribute{Computed: true},
						"description":            dschema.StringAttribute{Computed: true},
						"protocol":               dschema.StringAttribute{Computed: true},
						"source":                 dschema.StringAttribute{Computed: true},
						"destination":            dschema.StringAttribute{Computed: true},
						"source_port_start":      dschema.Int64Attribute{Computed: true},
						"source_port_end":        dschema.Int64Attribute{Computed: true},
						"destination_port_start": dschema.Int64Attribute{Computed: true},
						"destination_port_end":   dschema.Int64Attribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *aclDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config aclModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read ACL", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read ACL", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var acl *rookeryv1.ACL
	if hasID {
		acl, err = client.ACLs.GetACL(callCtx, &rookeryv1.GetACLRequest{Id: config.ID.ValueInt64()})
	} else {
		var response *rookeryv1.ListACLsResponse
		response, err = client.ACLs.ListACLs(callCtx, &rookeryv1.ListACLsRequest{})
		if err == nil {
			acl, err = findACL(response.GetAcls(), config.Name.ValueString())
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read ACL", err.Error())
		return
	}
	resp.Diagnostics.Append(setACLState(ctx, &config, acl)...)
	entityLabels, err := readEntityLabels(callCtx, client, "acl", fmt.Sprintf("%d", acl.GetId()))
	if err != nil {
		resp.Diagnostics.AddError("Read ACL labels", err.Error())
		return
	}
	config.Labels = entityLabels
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func findACL(acls []*rookeryv1.ACL, name string) (*rookeryv1.ACL, error) {
	var found *rookeryv1.ACL
	for _, acl := range acls {
		if strings.EqualFold(acl.GetName(), name) {
			if found != nil {
				return nil, fmt.Errorf("multiple ACLs match name %q", name)
			}
			found = acl
		}
	}
	if found == nil {
		return nil, fmt.Errorf("ACL %q not found", name)
	}
	return found, nil
}
