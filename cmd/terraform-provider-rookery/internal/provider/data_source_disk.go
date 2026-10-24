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
	_ datasource.DataSource              = (*diskDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*diskDataSource)(nil)
)

type diskDataSource struct{ configuredDataSource }

// NewDiskDataSource creates the rookery_disk data source.
func NewDiskDataSource() datasource.DataSource {
	return &diskDataSource{}
}

func (d *diskDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_disk"
}

func (d *diskDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a disk by id or name.",
		Attributes: map[string]dschema.Attribute{
			"id":                   dschema.Int64Attribute{Optional: true, Computed: true},
			"name":                 dschema.StringAttribute{Optional: true, Computed: true},
			"size_gb":              dschema.Int64Attribute{Computed: true},
			"storage_backend_id":   dschema.Int64Attribute{Computed: true},
			"storage_backend_name": dschema.StringAttribute{Computed: true},
			"vg":                   dschema.StringAttribute{Computed: true},
			"thin_pool":            dschema.StringAttribute{Computed: true},
			"device":               dschema.StringAttribute{Computed: true},
			"attached_vm_id":       dschema.StringAttribute{Computed: true},
			"attached_vm_name":     dschema.StringAttribute{Computed: true},
			"is_template":          dschema.BoolAttribute{Computed: true},
			"template_disk_id":     dschema.Int64Attribute{Computed: true},
			"template_disk_name":   dschema.StringAttribute{Computed: true},
		},
	}
}

func (d *diskDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config diskModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read disk", err.Error())
		return
	}
	hasID := hasInt64(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read disk", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var disk *rookeryv1.Disk
	if hasID {
		disk, err = client.Disks.GetDisk(callCtx, &rookeryv1.GetDiskRequest{Id: config.ID.ValueInt64()})
	} else {
		var response *rookeryv1.ListDisksResponse
		response, err = client.Disks.ListDisks(callCtx, &rookeryv1.ListDisksRequest{})
		if err == nil {
			disk, err = findDisk(response.GetDisks(), config.Name.ValueString())
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read disk", err.Error())
		return
	}
	setDiskState(&config, disk)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func findDisk(disks []*rookeryv1.Disk, name string) (*rookeryv1.Disk, error) {
	var found *rookeryv1.Disk
	for _, disk := range disks {
		if strings.EqualFold(disk.GetName(), name) {
			if found != nil {
				return nil, fmt.Errorf("multiple disks match name %q", name)
			}
			found = disk
		}
	}
	if found == nil {
		return nil, fmt.Errorf("disk %q not found", name)
	}
	return found, nil
}
