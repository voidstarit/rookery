package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func TestDiskSerialConversions(t *testing.T) {
	ctx := context.Background()
	value, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{"5": "root", "6": "data"})
	if diags.HasError() {
		t.Fatalf("map value: %v", diags)
	}
	serials, diags := diskSerialsFromModel(ctx, value)
	if diags.HasError() || serials[5] != "root" || serials[6] != "data" {
		t.Fatalf("serials = %+v, %v", serials, diags)
	}

	bad, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{"not-an-id": "root"})
	if diags.HasError() {
		t.Fatalf("bad map value: %v", diags)
	}
	if _, diags := diskSerialsFromModel(ctx, bad); !diags.HasError() {
		t.Fatal("invalid disk serial key should produce a diagnostic")
	}

	state, diags := diskSerialsValue(ctx, []*rookeryv1.VMDisk{
		{Id: 5, Serial: "root"},
		{Id: 6},
	})
	if diags.HasError() {
		t.Fatalf("state map: %v", diags)
	}
	var stateValues map[string]string
	if diags := state.ElementsAs(ctx, &stateValues, false); diags.HasError() {
		t.Fatalf("state values: %v", diags)
	}
	if len(stateValues) != 1 || stateValues["5"] != "root" {
		t.Fatalf("state values = %+v", stateValues)
	}
}
