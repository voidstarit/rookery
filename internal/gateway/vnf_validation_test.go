package gateway_test

import (
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func TestVNFConfigTransportValidation(t *testing.T) {
	for _, transport := range []string{"", "memif", "tap", "shared", "dedicated"} {
		if err := (&rookeryv1.VNFConfig{Transport: transport}).Validate(); err != nil {
			t.Errorf("transport %q should be valid: %v", transport, err)
		}
	}
	if err := (&rookeryv1.VNFConfig{Transport: "bogus"}).Validate(); err == nil {
		t.Fatal("invalid transport was accepted")
	}
}
