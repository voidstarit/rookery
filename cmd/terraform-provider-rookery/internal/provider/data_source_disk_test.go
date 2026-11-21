package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
)

func TestDiskDataSourceExposesForceDelete(t *testing.T) {
	dataSource := NewDiskDataSource()
	var response datasource.SchemaResponse
	dataSource.Schema(context.Background(), datasource.SchemaRequest{}, &response)

	attribute, ok := response.Schema.Attributes["force_delete"].(dschema.BoolAttribute)
	if !ok {
		t.Fatal("force_delete must be present on the disk data source schema")
	}
	if !attribute.Computed {
		t.Fatal("force_delete must be computed on the disk data source schema")
	}
}
