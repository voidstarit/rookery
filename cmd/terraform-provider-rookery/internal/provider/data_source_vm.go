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
	_ datasource.DataSource              = (*vmDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*vmDataSource)(nil)
)

type vmDataSource struct{ configuredDataSource }

// NewVMDataSource creates the rookery_vm data source.
func NewVMDataSource() datasource.DataSource {
	return &vmDataSource{}
}

func (d *vmDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vm"
}

func (d *vmDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Looks up a VM by id or name.",
		Attributes: map[string]dschema.Attribute{
			"id":              dschema.StringAttribute{Optional: true, Computed: true},
			"name":            dschema.StringAttribute{Optional: true, Computed: true},
			"description":     dschema.StringAttribute{Computed: true},
			"status":          dschema.StringAttribute{Computed: true},
			"vcpus":           dschema.Int64Attribute{Computed: true},
			"memory_mb":       dschema.Int64Attribute{Computed: true},
			"disk_ids":        dschema.ListAttribute{Computed: true, ElementType: types.Int64Type},
			"interface_ids":   dschema.ListAttribute{Computed: true, ElementType: types.Int64Type},
			"boot_media_id":   dschema.Int64Attribute{Computed: true},
			"console_enabled": dschema.BoolAttribute{Computed: true},
			"cgroup": dschema.SingleNestedAttribute{
				Computed:    true,
				Description: "Per-VM cgroup v2 limits, if configured.",
				Attributes: map[string]dschema.Attribute{
					"enabled":          dschema.BoolAttribute{Computed: true},
					"cpus":             dschema.StringAttribute{Computed: true},
					"memory_max_bytes": dschema.Int64Attribute{Computed: true},
					"cpu_weight":       dschema.Int64Attribute{Computed: true},
					"io_limits": dschema.ListNestedAttribute{
						Computed: true,
						NestedObject: dschema.NestedAttributeObject{
							Attributes: cgroupIOLimitDataSourceAttributes(),
						},
					},
				},
			},
		},
	}
}

func (d *vmDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config vmModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read VM", err.Error())
		return
	}
	hasID := hasString(config.ID)
	hasName := hasString(config.Name)
	if hasID == hasName {
		resp.Diagnostics.AddError("Read VM", "set exactly one of id or name")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var vm *rookeryv1.VM
	if hasID {
		vm, err = client.VMs.GetVM(callCtx, &rookeryv1.GetVMRequest{Id: config.ID.ValueString()})
	} else {
		var response *rookeryv1.ListVMsResponse
		response, err = client.VMs.ListVMs(callCtx, &rookeryv1.ListVMsRequest{})
		if err == nil {
			vm, err = findVM(response.GetVms(), config.Name.ValueString())
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Read VM", err.Error())
		return
	}
	resp.Diagnostics.Append(setVMState(ctx, &config, vm)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}

func findVM(vms []*rookeryv1.VM, name string) (*rookeryv1.VM, error) {
	var found *rookeryv1.VM
	for _, vm := range vms {
		if strings.EqualFold(vm.GetName(), name) {
			if found != nil {
				return nil, fmt.Errorf("multiple VMs match name %q", name)
			}
			found = vm
		}
	}
	if found == nil {
		return nil, fmt.Errorf("VM %q not found", name)
	}
	return found, nil
}
