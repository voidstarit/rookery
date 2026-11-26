package api

import (
	"context"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestNormalizeDHCPDNSIntegration(t *testing.T) {
	server := &Server{}
	config, err := server.NormalizeDHCPVNFConfig("dhcpv4", store.DHCPVNFConfig{
		Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
		DNS: "10.20.0.2", LeaseTime: "12h",
		DNSVNFID: 7, DNSDomain: "Lab.Test.", DNSTTL: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.DNSVNFID != 7 || config.DNSDomain != "lab.test" || config.DNSTTL != 300 {
		t.Fatalf("DNS integration = %+v", config)
	}

	if _, err := server.NormalizeDHCPVNFConfig("dhcpv4", store.DHCPVNFConfig{
		Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
		DNSDomain: "lab.test",
	}); err == nil || !strings.Contains(err.Error(), "dns_vnf_id") {
		t.Fatalf("expected missing DNS VNF error, got %v", err)
	}
	if _, err := server.NormalizeDHCPVNFConfig("dhcpv4", store.DHCPVNFConfig{
		Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
		DNSVNFID: 7,
	}); err == nil || !strings.Contains(err.Error(), "dns_domain") {
		t.Fatalf("expected missing domain error, got %v", err)
	}
	if _, err := server.NormalizeDHCPVNFConfig("dhcpv4", store.DHCPVNFConfig{
		Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
		DNSVNFID: 7, DNSDomain: "lab.test", DNSTTL: 90000,
	}); err == nil || !strings.Contains(err.Error(), "dns_ttl") {
		t.Fatalf("expected ttl error, got %v", err)
	}
}

func TestNormalizeDNSPTRRecord(t *testing.T) {
	server := &Server{}
	record, err := server.NormalizeDNSRecord(context.Background(), store.VNF{Type: store.VNFTypeDNS}, nil, store.DNSRecord{
		Name: "50.0.20.10.in-addr.arpa", Type: "PTR", Value: "web-01.lab.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Type != "PTR" || record.Value != "web-01.lab.test." || record.TTL != 300 {
		t.Fatalf("PTR record = %+v", record)
	}
}
