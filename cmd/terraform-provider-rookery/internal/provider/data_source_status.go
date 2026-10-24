package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

var (
	_ datasource.DataSource              = (*statusDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*statusDataSource)(nil)
)

type statusDataSource struct{ configuredDataSource }

// NewStatusDataSource creates the rookery_status data source.
func NewStatusDataSource() datasource.DataSource {
	return &statusDataSource{}
}

type statusModel struct {
	Ready     types.Bool   `tfsdk:"ready"`
	CheckedAt types.String `tfsdk:"checked_at"`
	ThinPool  types.Object `tfsdk:"thin_pool"`
	VPP       types.Object `tfsdk:"vpp"`
}

type statusThinPoolModel struct {
	Ready     types.Bool   `tfsdk:"ready"`
	State     types.String `tfsdk:"state"`
	Detail    types.String `tfsdk:"detail"`
	CheckedAt types.String `tfsdk:"checked_at"`
	VG        types.String `tfsdk:"vg"`
	Name      types.String `tfsdk:"name"`
}

type statusVPPModel struct {
	Ready     types.Bool   `tfsdk:"ready"`
	State     types.String `tfsdk:"state"`
	Detail    types.String `tfsdk:"detail"`
	CheckedAt types.String `tfsdk:"checked_at"`
	Socket    types.String `tfsdk:"socket"`
}

var statusThinPoolAttrTypes = map[string]attr.Type{
	"ready":      types.BoolType,
	"state":      types.StringType,
	"detail":     types.StringType,
	"checked_at": types.StringType,
	"vg":         types.StringType,
	"name":       types.StringType,
}

var statusVPPAttrTypes = map[string]attr.Type{
	"ready":      types.BoolType,
	"state":      types.StringType,
	"detail":     types.StringType,
	"checked_at": types.StringType,
	"socket":     types.StringType,
}

func (d *statusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_status"
}

func (d *statusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		Description: "Current Rookery host readiness: thin pool and VPP.",
		Attributes: map[string]dschema.Attribute{
			"ready":      dschema.BoolAttribute{Computed: true},
			"checked_at": dschema.StringAttribute{Computed: true},
			"thin_pool": dschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dschema.Attribute{
					"ready":      dschema.BoolAttribute{Computed: true},
					"state":      dschema.StringAttribute{Computed: true},
					"detail":     dschema.StringAttribute{Computed: true},
					"checked_at": dschema.StringAttribute{Computed: true},
					"vg":         dschema.StringAttribute{Computed: true},
					"name":       dschema.StringAttribute{Computed: true},
				},
			},
			"vpp": dschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dschema.Attribute{
					"ready":      dschema.BoolAttribute{Computed: true},
					"state":      dschema.StringAttribute{Computed: true},
					"detail":     dschema.StringAttribute{Computed: true},
					"checked_at": dschema.StringAttribute{Computed: true},
					"socket":     dschema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

func (d *statusDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	client, err := d.requireClient()
	if err != nil {
		resp.Diagnostics.AddError("Read status", err.Error())
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	status, err := client.System.GetStatus(callCtx, &rookeryv1.GetStatusRequest{})
	if err != nil {
		resp.Diagnostics.AddError("Read status", err.Error())
		return
	}
	thinPool, diags := types.ObjectValueFrom(ctx, statusThinPoolAttrTypes, statusThinPoolModel{
		Ready:     types.BoolValue(status.GetThinPool().GetReady()),
		State:     types.StringValue(status.GetThinPool().GetState()),
		Detail:    optionalStringValue(status.GetThinPool().GetDetail()),
		CheckedAt: timestampString(status.GetThinPool().GetCheckedAt()),
		VG:        optionalStringValue(status.GetThinPool().GetVg()),
		Name:      optionalStringValue(status.GetThinPool().GetName()),
	})
	resp.Diagnostics.Append(diags...)
	vpp, diags := types.ObjectValueFrom(ctx, statusVPPAttrTypes, statusVPPModel{
		Ready:     types.BoolValue(status.GetVpp().GetReady()),
		State:     types.StringValue(status.GetVpp().GetState()),
		Detail:    optionalStringValue(status.GetVpp().GetDetail()),
		CheckedAt: timestampString(status.GetVpp().GetCheckedAt()),
		Socket:    optionalStringValue(status.GetVpp().GetSocket()),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state := statusModel{
		Ready:     types.BoolValue(status.GetReady()),
		CheckedAt: timestampString(status.GetCheckedAt()),
		ThinPool:  thinPool,
		VPP:       vpp,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
