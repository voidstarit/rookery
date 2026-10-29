package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func TestSetRouteStatePreservesExplicitZeroOptions(t *testing.T) {
	defaultTable := "default"
	zero := uint32(0)
	route := &rookeryv1.Route{
		Ipv4LookupTable: defaultTable,
		Preference:      &zero,
	}
	var state routeModel
	if diags := setRouteState(context.Background(), &state, route); diags.HasError() {
		t.Fatalf("set route state: %v", diags)
	}
	if state.IPv4LookupTable.IsNull() || state.IPv4LookupTable.ValueString() != "default" {
		t.Fatalf("IPv4 lookup table = %+v", state.IPv4LookupTable)
	}
	if state.Preference.IsNull() || state.Preference.ValueInt64() != 0 {
		t.Fatalf("preference = %+v", state.Preference)
	}
	if !state.RouteCount.IsNull() || !state.NextHopTable.IsNull() || !state.Weight.IsNull() || !state.UDPEncap.IsNull() {
		t.Fatalf("unset numeric options were not null: %+v", state)
	}
	if state.OutLabels.IsNull() || len(state.OutLabels.Elements()) != 0 {
		t.Fatalf("out labels = %+v", state.OutLabels)
	}
}

func TestRouteOutLabelsFromListPreservesZero(t *testing.T) {
	ctx := context.Background()
	list, diags := types.ListValueFrom(ctx, types.Int64Type, []int64{0})
	if diags.HasError() {
		t.Fatalf("build labels: %v", diags)
	}
	labels, diags := routeOutLabelsFromList(ctx, list)
	if diags.HasError() {
		t.Fatalf("convert labels: %v", diags)
	}
	if len(labels) != 1 || labels[0] != 0 {
		t.Fatalf("labels = %v", labels)
	}

	empty, diags := types.ListValueFrom(ctx, types.Int64Type, []int64{})
	if diags.HasError() {
		t.Fatalf("build empty labels: %v", diags)
	}
	labels, diags = routeOutLabelsFromList(ctx, empty)
	if diags.HasError() {
		t.Fatalf("convert empty labels: %v", diags)
	}
	if len(labels) != 0 {
		t.Fatalf("empty labels = %v", labels)
	}
}
