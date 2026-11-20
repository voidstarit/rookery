package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

// TestStorageBackendDataSourceConfigType guards against schema drift between
// the shared storage backend state model and the data source: the state setter
// always writes the driver-specific config object, so a data source whose
// nested attribute type omits any field fails every read with a "Value
// Conversion Error".
func TestStorageBackendDataSourceConfigType(t *testing.T) {
	ctx := context.Background()
	dataSourceImpl := NewStorageBackendDataSource()
	var resp datasource.SchemaResponse
	dataSourceImpl.Schema(ctx, datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	attribute, ok := resp.Schema.Attributes["config"]
	if !ok {
		t.Fatal("config attribute is missing")
	}
	want := types.ObjectType{AttrTypes: storageBackendConfigAttrTypes}
	if got := attribute.GetType(); !got.Equal(want) {
		t.Fatalf("config attribute type = %s, want %s", got, want)
	}
}
