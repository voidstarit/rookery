package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = (*RookeryProvider)(nil)

// RookeryProvider implements the rookery Terraform provider.
type RookeryProvider struct {
	version string
}

// New returns a provider factory for the Terraform plugin server.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &RookeryProvider{version: version}
	}
}

func (p *RookeryProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "rookery"
	resp.Version = p.version
}

func (p *RookeryProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = providerschema.Schema{
		Description: "Manage Rookery VMs, disks, networks, interfaces, ACLs, VNFs, and storage backends over the public mTLS gRPC API.",
		Attributes: map[string]providerschema.Attribute{
			"endpoint": providerschema.StringAttribute{
				Optional:    true,
				Description: "Rookery mTLS gRPC endpoint, for example api.example.com:9443. Defaults to ROOKERY_ENDPOINT.",
			},
			"ca_file": providerschema.StringAttribute{
				Optional:    true,
				Description: "Path to the CA certificate used to verify the server. Defaults to ROOKERY_CA_FILE.",
			},
			"cert_file": providerschema.StringAttribute{
				Optional:    true,
				Description: "Path to the client certificate. Defaults to ROOKERY_CLIENT_CERT.",
			},
			"key_file": providerschema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Path to the client private key. Defaults to ROOKERY_CLIENT_KEY.",
			},
			"ca_pem": providerschema.StringAttribute{
				Optional:    true,
				Description: "CA certificate PEM. Used when ca_file is not set.",
			},
			"cert_pem": providerschema.StringAttribute{
				Optional:    true,
				Description: "Client certificate PEM. Used when cert_file is not set.",
			},
			"key_pem": providerschema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Client private key PEM. Used when key_file is not set.",
			},
			"server_name": providerschema.StringAttribute{
				Optional:    true,
				Description: "TLS server name override when the endpoint is an IP address or does not match a certificate SAN.",
			},
			"timeout": providerschema.StringAttribute{
				Optional:    true,
				Description: "Timeout for individual API calls, for example 30s or 2m. Defaults to 30s.",
			},
		},
	}
}

type providerModel struct {
	Endpoint   types.String `tfsdk:"endpoint"`
	CAFile     types.String `tfsdk:"ca_file"`
	CertFile   types.String `tfsdk:"cert_file"`
	KeyFile    types.String `tfsdk:"key_file"`
	CAPEM      types.String `tfsdk:"ca_pem"`
	CertPEM    types.String `tfsdk:"cert_pem"`
	KeyPEM     types.String `tfsdk:"key_pem"`
	ServerName types.String `tfsdk:"server_name"`
	Timeout    types.String `tfsdk:"timeout"`
}

func (p *RookeryProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	clientConfig := ClientConfig{
		Endpoint:   stringOrEnv(config.Endpoint, "ROOKERY_ENDPOINT"),
		CAFile:     stringOrEnv(config.CAFile, "ROOKERY_CA_FILE"),
		CertFile:   stringOrEnv(config.CertFile, "ROOKERY_CLIENT_CERT"),
		KeyFile:    stringOrEnv(config.KeyFile, "ROOKERY_CLIENT_KEY"),
		CAPEM:      config.CAPEM.ValueString(),
		CertPEM:    config.CertPEM.ValueString(),
		KeyPEM:     config.KeyPEM.ValueString(),
		ServerName: config.ServerName.ValueString(),
		TimeoutRaw: config.Timeout.ValueString(),
	}
	client, err := NewClient(clientConfig)
	if err != nil {
		resp.Diagnostics.AddError("Configure Rookery provider", err.Error())
		return
	}
	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *RookeryProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewStorageBackendResource,
		NewDiskResource,
		NewMediaResource,
		NewNetworkResource,
		NewInterfaceResource,
		NewACLResource,
		NewRouteTableResource,
		NewRouteResource,
		NewVNFResource,
		NewVMResource,
		NewHostInterfaceResource,
	}
}

func (p *RookeryProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewStatusDataSource,
		NewStorageBackendDataSource,
		NewDiskDataSource,
		NewMediaDataSource,
		NewNetworkDataSource,
		NewInterfaceDataSource,
		NewACLDataSource,
		NewRouteTableDataSource,
		NewRouteDataSource,
		NewVNFDataSource,
		NewVMDataSource,
		NewHostInterfaceDataSource,
	}
}

func stringOrEnv(value types.String, key string) string {
	if !value.IsNull() && value.ValueString() != "" {
		return value.ValueString()
	}
	return os.Getenv(key)
}
