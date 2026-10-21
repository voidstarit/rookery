package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// configuredDataSource is embedded by every data source to receive the
// provider's shared mTLS gRPC client.
type configuredDataSource struct {
	client *Client
}

func (d *configuredDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok || client == nil {
		resp.Diagnostics.AddError("Configure Rookery data source", "Unexpected provider data type")
		return
	}
	d.client = client
}

func (d *configuredDataSource) requireClient() (*Client, error) {
	if d.client == nil {
		return nil, errors.New("Rookery provider is not configured")
	}
	return d.client, nil
}

func hasString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && value.ValueString() != ""
}

func hasInt64(value types.Int64) bool {
	return !value.IsNull() && !value.IsUnknown()
}
