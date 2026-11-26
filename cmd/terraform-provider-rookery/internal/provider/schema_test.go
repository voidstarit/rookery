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

// TestProviderResourceAndDataSourceSets pins the provider's registered object
// names so a retired resource cannot linger and an added type cannot go
// unregistered.
func TestProviderResourceAndDataSourceSets(t *testing.T) {
	ctx := context.Background()
	instance := New("test")()
	wantResources := map[string]bool{
		"rookery_storage_backend": true, "rookery_disk": true, "rookery_disk_throttle_class": true,
		"rookery_media": true, "rookery_network": true, "rookery_network_pipe": true,
		"rookery_interface": true, "rookery_host_tap": true, "rookery_vpp_interface_addresses": true,
		"rookery_vpp_interface_route_tables": true, "rookery_acl": true, "rookery_route_table": true,
		"rookery_route": true, "rookery_dhcp_vnf": true, "rookery_dhcp_static_assignment": true,
		"rookery_dhcp_option": true, "rookery_dns_vnf": true, "rookery_dns_record": true,
		"rookery_dns_forwarder": true, "rookery_nat44_vnf": true, "rookery_nat44_dnat_rule": true,
		"rookery_bgp_vnf": true, "rookery_bgp_advertised_prefix": true, "rookery_vm": true,
		"rookery_host_interface": true, "rookery_startup_script": true, "rookery_cgroup_settings": true,
		"rookery_lightvm_kernel": true, "rookery_lightvm_initramfs": true, "rookery_lightvm": true,
	}
	wantDataSources := map[string]bool{
		"rookery_status": true, "rookery_storage_backend": true, "rookery_disk": true,
		"rookery_disk_throttle_class": true, "rookery_media": true, "rookery_network": true,
		"rookery_interface": true, "rookery_acl": true, "rookery_route_table": true,
		"rookery_route": true, "rookery_dhcp_vnf": true, "rookery_dns_vnf": true,
		"rookery_nat44_vnf": true, "rookery_bgp_vnf": true, "rookery_vm": true,
		"rookery_host_interface": true, "rookery_network_pipe": true, "rookery_host_tap": true,
		"rookery_vpp_interface_addresses": true, "rookery_vpp_interface_route_tables": true,
		"rookery_startup_script": true, "rookery_cgroup_settings": true,
	}

	gotResources := map[string]bool{}
	for _, factory := range instance.Resources(ctx) {
		var metadata resource.MetadataResponse
		factory().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "rookery"}, &metadata)
		gotResources[metadata.TypeName] = true
	}
	gotDataSources := map[string]bool{}
	for _, factory := range instance.DataSources(ctx) {
		var metadata datasource.MetadataResponse
		factory().Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "rookery"}, &metadata)
		gotDataSources[metadata.TypeName] = true
	}

	for name := range wantResources {
		if !gotResources[name] {
			t.Errorf("resource %q is not registered", name)
		}
		delete(gotResources, name)
	}
	for name := range gotResources {
		t.Errorf("unexpected resource %q is registered", name)
	}
	for name := range wantDataSources {
		if !gotDataSources[name] {
			t.Errorf("data source %q is not registered", name)
		}
		delete(gotDataSources, name)
	}
	for name := range gotDataSources {
		t.Errorf("unexpected data source %q is registered", name)
	}
	if wantResources["rookery_vnf"] || wantDataSources["rookery_vnf"] {
		t.Fatal("the retired rookery_vnf resource must not be in the expected sets")
	}
}
