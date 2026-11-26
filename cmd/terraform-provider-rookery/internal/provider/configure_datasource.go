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

// Configure stores the provider's shared client for later use by the data
// source, ignoring calls that arrive without provider data.
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

// requireClient returns the configured client or an error when the data source
// was used without provider configuration.
func (d *configuredDataSource) requireClient() (*Client, error) {
	if d.client == nil {
		return nil, errors.New("Rookery provider is not configured")
	}
	return d.client, nil
}

// hasString reports whether value is known, non-null, and non-empty.
func hasString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && value.ValueString() != ""
}

// hasInt64 reports whether value is known and non-null.
func hasInt64(value types.Int64) bool {
	return !value.IsNull() && !value.IsUnknown()
}
