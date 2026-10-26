package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func TestSetNetworkPipeStatePreservesRequestedOrder(t *testing.T) {
	var state networkPipeModel
	state.NetworkAID = types.Int64Value(2)
	state.NetworkBID = types.Int64Value(1)
	setNetworkPipeState(&state, &rookeryv1.NetworkPipe{
		Id:                 9,
		NetworkAId:         1,
		NetworkAName:       "app",
		NetworkBId:         2,
		NetworkBName:       "edge",
		Instance:           4,
		ParentSwIfIndex:    10,
		EndpointASwIfIndex: 11,
		EndpointBSwIfIndex: 12,
	}, true)
	if state.NetworkAID.ValueInt64() != 2 || state.NetworkAName.ValueString() != "edge" {
		t.Fatalf("network A = %+v / %+v", state.NetworkAID, state.NetworkAName)
	}
	if state.NetworkBID.ValueInt64() != 1 || state.NetworkBName.ValueString() != "app" {
		t.Fatalf("network B = %+v / %+v", state.NetworkBID, state.NetworkBName)
	}
	if state.EndpointAName.ValueString() != "pipe4.1" || state.EndpointBName.ValueString() != "pipe4.0" {
		t.Fatalf("endpoint names = %q / %q", state.EndpointAName.ValueString(), state.EndpointBName.ValueString())
	}
	if state.EndpointASwIfIndex.ValueInt64() != 12 || state.EndpointBSwIfIndex.ValueInt64() != 11 {
		t.Fatalf("endpoint sw_if_indexes = %d / %d", state.EndpointASwIfIndex.ValueInt64(), state.EndpointBSwIfIndex.ValueInt64())
	}
}

func TestSetVPPInterfaceAddressesState(t *testing.T) {
	ctx := context.Background()
	var state vppInterfaceAddressesModel
	diags := setVPPInterfaceAddressesState(ctx, &state, &rookeryv1.VPPInterfaceAddresses{
		Name:                 "app-vm0",
		Addresses:            []string{"10.20.0.50/16", "fd00:20::50/64"},
		DhcpClientEnabled:    true,
		LiveAddresses:        []string{"10.20.0.50/16"},
		DhcpClientConfigured: true,
		DhcpClientStatus:     "bound",
		StatusDetail:         "ok",
	})
	if diags.HasError() {
		t.Fatalf("set addresses state: %v", diags)
	}
	if !state.Name.Equal(types.StringValue("app-vm0")) || !state.DHCPClientEnabled.ValueBool() {
		t.Fatalf("state = %+v", state)
	}
	values, diags := stringSetValues(ctx, state.Addresses)
	if diags.HasError() {
		t.Fatalf("read address set: %v", diags)
	}
	if len(values) != 2 {
		t.Fatalf("addresses = %v", values)
	}
	if !state.LiveAddresses.Equal(mustStringList(t, ctx, []string{"10.20.0.50/16"})) {
		t.Fatalf("live addresses = %+v", state.LiveAddresses)
	}
}

func TestSetVPPInterfaceRouteTablesStateDefaults(t *testing.T) {
	var state vppInterfaceRouteTablesModel
	setVPPInterfaceRouteTablesState(&state, &rookeryv1.VPPInterfaceRouteTables{
		Name:          "hostnet1",
		Ipv4TableId:   0,
		Ipv6TableId:   0,
		Ipv4TableName: "default",
		Ipv6TableName: "default",
	})
	if state.IPv4TableID.ValueInt64() != 0 || state.IPv4TableName.ValueString() != "default" {
		t.Fatalf("state = %+v", state)
	}
}

func mustStringList(t *testing.T, ctx context.Context, values []string) types.List {
	t.Helper()
	list, diags := stringList(ctx, values)
	if diags.HasError() {
		t.Fatalf("build string list: %v", diags)
	}
	return list
}
