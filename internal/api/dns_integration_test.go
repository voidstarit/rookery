package api

import (
	"strings"
	"testing"
)

func TestNormalizeDHCPDNSIntegration(t *testing.T) {
	payload := vnfConfigPayload{
		Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
		DNS: "10.20.0.2", LeaseTime: "12h",
		DNSVNFID: 7, DNSDomain: "Lab.Test.", DNSTTL: 0,
	}
	config, err := payload.toConfig("dhcpv4")
	if err != nil {
		t.Fatal(err)
	}
	if config.DNSVNFID != 7 || config.DNSDomain != "lab.test" || config.DNSTTL != 300 {
		t.Fatalf("DNS integration = %+v", config)
	}

	payload.DNSVNFID = 0
	if _, err := payload.toConfig("dhcpv4"); err == nil || !strings.Contains(err.Error(), "dns_vnf_id") {
		t.Fatalf("expected missing DNS VNF error, got %v", err)
	}
	payload.DNSVNFID = 7
	payload.DNSDomain = ""
	if _, err := payload.toConfig("dhcpv4"); err == nil || !strings.Contains(err.Error(), "dns_domain") {
		t.Fatalf("expected missing domain error, got %v", err)
	}
	payload.DNSDomain = "lab.test"
	payload.DNSTTL = 90000
	if _, err := payload.toConfig("dhcpv4"); err == nil || !strings.Contains(err.Error(), "dns_ttl") {
		t.Fatalf("expected ttl error, got %v", err)
	}
}

func TestNormalizeDNSPTRRecord(t *testing.T) {
	config, err := normalizeDNSConfig(vnfConfigPayload{
		DNSRecords: []dnsRecordPayload{{Name: "50.0.20.10.in-addr.arpa", Type: "PTR", Value: "web-01.lab.test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(config.DNSRecords) != 1 || config.DNSRecords[0].Type != "PTR" || config.DNSRecords[0].Value != "web-01.lab.test." {
		t.Fatalf("PTR record = %+v", config.DNSRecords)
	}
}
