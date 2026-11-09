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
	_ datasource.DataSource              = (*diskThrottleClassDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*diskThrottleClassDataSource)(nil)
)

type diskThrottleClassDataSource struct{ configuredDataSource }

// NewDiskThrottleClassDataSource creates the rookery_disk_throttle_class data source.
func NewDiskThrottleClassDataSource() datasource.DataSource {
	return &diskThrottleClassDataSource{}
}

func (d *diskThrottleClassDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_disk_throttle_class"
}

func (d *diskThrottleClassDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := map[string]dschema.Attribute{
		"id":               dschema.Int64Attribute{Optional: true, Computed: true},
		"name":             dschema.StringAttribute{Optional: true, Computed: true},
		"description":      dschema.StringAttribute{Computed: true},
		"validation_error": dschema.StringAttribute{Computed: true},
		"disk_count":       dschema.Int64Attribute{Computed: true},
	}
	for name := range diskThrottleNumericDescriptions() {
		attributes[name] = dschema.Int64Attribute{Computed: true}
	}
	resp.Schema = dschema.Schema{
		Description: "Looks up a reusable QEMU disk throttling class by id or name.",
		Attributes:  attributes,
	}
}

func (d *diskThrottleClassDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config diskThrottleClassModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read disk throttle class", "set exactly one of id or name")
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read disk throttle class", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var class *rookeryv1.DiskThrottleClass
	if hasID {
		class, err = client.DiskThrottleClasses.GetDiskThrottleClass(callCtx, &rookeryv1.GetDiskThrottleClassRequest{
			Id: config.ID.ValueInt64(),
		})
	} else {
		var response *rookeryv1.ListDiskThrottleClassesResponse
		response, err = client.DiskThrottleClasses.ListDiskThrottleClasses(callCtx, &rookeryv1.ListDiskThrottleClassesRequest{})
		if err == nil {
			class, err = findDiskThrottleClass(response.GetClasses(), config.Name.ValueString())
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read disk throttle class", err.Error())
		return
	}
	state := diskThrottleClassModelFromProto(class)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func findDiskThrottleClass(classes []*rookeryv1.DiskThrottleClass, name string) (*rookeryv1.DiskThrottleClass, error) {
	var found *rookeryv1.DiskThrottleClass
	for _, class := range classes {
		if strings.EqualFold(class.GetName(), name) {
			if found != nil {
				return nil, fmt.Errorf("multiple disk throttle classes match name %q", name)
			}
			found = class
		}
	}
	if found == nil {
		return nil, fmt.Errorf("disk throttle class %q not found", name)
	}
	return found, nil
}
