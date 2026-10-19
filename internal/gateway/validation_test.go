package gateway_test

import (
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func TestRequestValidation(t *testing.T) {
	if err := (&rookeryv1.CreateNetworkRequest{Name: "lab-1"}).Validate(); err != nil {
		t.Fatalf("valid network rejected: %v", err)
	}
	if err := (&rookeryv1.CreateNetworkRequest{Name: "not valid"}).Validate(); err == nil {
		t.Fatal("invalid network name accepted")
	}
	if err := (&rookeryv1.GetVMRequest{Id: "not-a-uuid"}).Validate(); err == nil {
		t.Fatal("invalid VM UUID accepted")
	}
	if err := (&rookeryv1.CreateVMRequest{Name: "vm-01"}).Validate(); err != nil {
		t.Fatalf("valid VM rejected: %v", err)
	}
	if err := (&rookeryv1.CreateVMRequest{Name: "bad name"}).Validate(); err == nil {
		t.Fatal("invalid VM name accepted")
	}

	validRule := &rookeryv1.ACLRule{
		Action:      "deny",
		Protocol:    "any",
		Source:      "0.0.0.0/0",
		Destination: "0.0.0.0/0",
	}
	if err := (&rookeryv1.CreateACLRequest{Name: "block-all", Rules: []*rookeryv1.ACLRule{validRule}}).Validate(); err != nil {
		t.Fatalf("valid ACL rejected: %v", err)
	}
	if err := (&rookeryv1.CreateACLRequest{Name: "empty"}).Validate(); err == nil {
		t.Fatal("empty ACL accepted")
	}

	if err := (&rookeryv1.StartCaptureRequest{
		Interface:  "any",
		Direction:  "both",
		MaxBytes:   65536,
		SnapLength: 64,
	}).Validate(); err != nil {
		t.Fatalf("valid capture rejected: %v", err)
	}
	if err := (&rookeryv1.StartCaptureRequest{
		Interface:  "any",
		Direction:  "sideways",
		MaxBytes:   1000,
		SnapLength: 64,
	}).Validate(); err == nil {
		t.Fatal("invalid capture accepted")
	}

	if err := (&rookeryv1.CreateVNFRequest{Name: "dhcp", NetworkId: 1, Type: "bad"}).Validate(); err == nil {
		t.Fatal("invalid VNF type accepted")
	}
}
