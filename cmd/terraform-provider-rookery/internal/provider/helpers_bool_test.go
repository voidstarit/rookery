package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestBoolPointerDefault covers the null, unknown, and explicit create-default paths.
func TestBoolPointerDefault(t *testing.T) {
	nullDefault := boolPointerDefault(types.BoolNull(), true)
	if nullDefault == nil || !*nullDefault {
		t.Fatalf("null fallback = %v, want true", nullDefault)
	}
	unknownDefault := boolPointerDefault(types.BoolUnknown(), true)
	if unknownDefault == nil || !*unknownDefault {
		t.Fatalf("unknown fallback = %v, want true", unknownDefault)
	}
	explicitFalse := boolPointerDefault(types.BoolValue(false), true)
	if explicitFalse == nil || *explicitFalse {
		t.Fatalf("explicit false = %v, want false", explicitFalse)
	}
	explicitTrue := boolPointerDefault(types.BoolValue(true), false)
	if explicitTrue == nil || !*explicitTrue {
		t.Fatalf("explicit true = %v, want true", explicitTrue)
	}
}
