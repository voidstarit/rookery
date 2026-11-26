package gateway_test

import (
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
)

func TestRequestValidation(t *testing.T) {
	if err := (&rookeryv1.CreateNetworkRequest{Name: "lab-1", Ipv4Cidr: "10.20.0.0/24", Ipv6Cidr: "fd00:20::/64"}).Validate(); err != nil {
		t.Fatalf("valid network rejected: %v", err)
	}
	if err := (&rookeryv1.CreateNetworkRequest{Name: "not valid", Ipv4Cidr: "10.20.0.0/24", Ipv6Cidr: "fd00:20::/64"}).Validate(); err == nil {
		t.Fatal("invalid network name accepted")
	}
	if err := (&rookeryv1.CreateNetworkRequest{Name: "lab-1"}).Validate(); err == nil {
		t.Fatal("network without CIDRs accepted")
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

	if err := (&rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: 1, Family: "dhcpv4"}).Validate(); err != nil {
		t.Fatalf("valid DHCP VNF rejected: %v", err)
	}
	if err := (&rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: 1, Family: "bad"}).Validate(); err == nil {
		t.Fatal("invalid DHCP family accepted")
	}
	if err := (&rookeryv1.CreateDHCPVNFRequest{Name: "bad name", NetworkId: 1, Family: "dhcpv4"}).Validate(); err == nil {
		t.Fatal("invalid DHCP VNF name accepted")
	}
	if err := (&rookeryv1.CreateDNSVNFRequest{Name: "dns", NetworkId: 1}).Validate(); err != nil {
		t.Fatalf("valid DNS VNF rejected: %v", err)
	}
	if err := (&rookeryv1.CreateBGPVNFRequest{Name: "bgp", NetworkId: 1, LocalIp: "10.0.0.1/24"}).Validate(); err != nil {
		t.Fatalf("valid BGP VNF rejected: %v", err)
	}
	// Record types and DNAT protocols are case-insensitive in the API: the
	// server normalizes them, and unknown values are rejected with a 400 there.
	if err := (&rookeryv1.CreateDNSVNFRecordRequest{VnfId: 1, Name: "host", Type: "a", Value: "10.0.0.1"}).Validate(); err != nil {
		t.Fatalf("lowercase DNS record type rejected by protobuf validation: %v", err)
	}
	if err := (&rookeryv1.CreateNAT44DNATRuleRequest{VnfId: 1, Protocol: "TCP", ExternalPort: 1, LocalIp: "10.0.0.2", LocalPort: 2}).Validate(); err != nil {
		t.Fatalf("uppercase DNAT protocol rejected by protobuf validation: %v", err)
	}
}
