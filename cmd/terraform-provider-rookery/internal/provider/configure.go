package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// configuredResource is embedded by every resource to receive the provider's
// shared mTLS gRPC client.
type configuredResource struct {
	client *Client
}

func (r *configuredResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok || client == nil {
		resp.Diagnostics.AddError("Configure Rookery resource", "Unexpected provider data type")
		return
	}
	r.client = client
}

func (r *configuredResource) requireClient() (*Client, error) {
	if r.client == nil {
		return nil, errors.New("Rookery provider is not configured")
	}
	return r.client, nil
}
