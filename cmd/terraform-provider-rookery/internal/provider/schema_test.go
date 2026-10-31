package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestAllProviderResourceAndDataSourceSchemas(t *testing.T) {
	ctx := context.Background()
	instance := New("test")()
	for _, factory := range instance.Resources(ctx) {
		resourceImpl := factory()
		var resp resource.SchemaResponse
		resourceImpl.Schema(ctx, resource.SchemaRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("%T schema diagnostics: %v", resourceImpl, resp.Diagnostics)
		}
		if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%T schema: %v", resourceImpl, diags)
		}
	}
	for _, factory := range instance.DataSources(ctx) {
		dataSourceImpl := factory()
		var resp datasource.SchemaResponse
		dataSourceImpl.Schema(ctx, datasource.SchemaRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("%T schema diagnostics: %v", dataSourceImpl, resp.Diagnostics)
		}
		if diags := resp.Schema.ValidateImplementation(ctx); diags.HasError() {
			t.Fatalf("%T schema: %v", dataSourceImpl, diags)
		}
	}
}
