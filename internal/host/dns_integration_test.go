package host

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/rfc1035label"
	"github.com/miekg/dns"

	"github.com/voidstarit/rookery/internal/store"
)

func TestDNSHandlerDynamicRecords(t *testing.T) {
	handler := newDNSHandler(store.VNFConfig{})
	handler.ReplaceDynamic("dhcp-1", []store.DNSRecord{
		{Name: "web-01.lab.test", Type: "A", Value: "10.20.0.50", TTL: 60},
		{Name: "50.0.20.10.in-addr.arpa", Type: "PTR", Value: "web-01.lab.test.", TTL: 60},
	})
	response := dnsQuery(t, handler, "web-01.lab.test", dns.TypeA)
	if len(response.Answer) != 1 || response.Answer[0].(*dns.A).A.String() != "10.20.0.50" {
		t.Fatalf("A answer = %+v", response.Answer)
	}
	response = dnsQuery(t, handler, "50.0.20.10.in-addr.arpa", dns.TypePTR)
	if len(response.Answer) != 1 || response.Answer[0].(*dns.PTR).Ptr != "web-01.lab.test." {
		t.Fatalf("PTR answer = %+v", response.Answer)
	}
	handler.RemoveDynamic("dhcp-1")
	response = dnsQuery(t, handler, "web-01.lab.test", dns.TypeA)
	if response.Rcode != dns.RcodeNameError || len(response.Answer) != 0 {
		t.Fatalf("removed dynamic answer = %+v", response)
	}
}

func TestDNSIntegrationFromDHCPLeases(t *testing.T) {
	manager, err := NewVNFManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	handler := newDNSHandler(store.VNFConfig{})
	handler.vnfID = 99
	manager.registerDNSRuntime(99, handler)

	v4 := store.VNF{ID: 1, NetworkID: 10, Type: store.VNFTypeDHCPv4, Config: store.VNFConfig{
		DNSVNFID: 99, DNSDomain: "lab.test", DNSTTL: 120,
	}}
	v6 := store.VNF{ID: 2, NetworkID: 10, Type: store.VNFTypeDHCPv6, Config: store.VNFConfig{
		DNSVNFID: 99, DNSDomain: "lab.test", DNSTTL: 120,
	}}
	expires := time.Now().Add(time.Hour)
	writeLeaseRecords(t, manager.leasePath(v4.ID), []leaseRecord{
		{ClientID: "02:00:00:00:00:01", Address: "10.20.0.50", Hostname: "web-01", Expires: expires},
	})
	writeLeaseRecords(t, manager.leasePath(v6.ID), []leaseRecord{
		{ClientID: "duid-02-00-00-00-00-02", Address: "fd00:20::50", Hostname: "web6", Expires: expires},
	})
	manager.registerDHCPConfig(v4)
	manager.registerDHCPConfig(v6)

	response := dnsQuery(t, handler, "web-01.lab.test", dns.TypeA)
	if len(response.Answer) != 1 || response.Answer[0].(*dns.A).A.String() != "10.20.0.50" {
		t.Fatalf("IPv4 lease answer = %+v", response.Answer)
	}
	response = dnsQuery(t, handler, "web6.lab.test", dns.TypeAAAA)
	if len(response.Answer) != 1 || response.Answer[0].(*dns.AAAA).AAAA.String() != "fd00:20::50" {
		t.Fatalf("IPv6 lease answer = %+v", response.Answer)
	}
	response = dnsQuery(t, handler, "50.0.20.10.in-addr.arpa", dns.TypePTR)
	if len(response.Answer) != 1 || response.Answer[0].(*dns.PTR).Ptr != "web-01.lab.test." {
		t.Fatalf("IPv4 PTR = %+v", response.Answer)
	}

	// Releasing the v4 lease removes its forward and reverse records.
	writeLeaseRecords(t, manager.leasePath(v4.ID), nil)
	manager.syncDNSRecords()
	response = dnsQuery(t, handler, "web-01.lab.test", dns.TypeA)
	if response.Rcode != dns.RcodeNameError {
		t.Fatalf("released name response = %+v", response)
	}
	response = dnsQuery(t, handler, "50.0.20.10.in-addr.arpa", dns.TypePTR)
	if response.Rcode != dns.RcodeNameError {
		t.Fatalf("released PTR response = %+v", response)
	}

	// An expired lease is also removed without a subsequent DHCP request.
	writeLeaseRecords(t, manager.leasePath(v6.ID), []leaseRecord{
		{ClientID: "duid-02-00-00-00-00-02", Address: "fd00:20::50", Hostname: "web6", Expires: time.Now().Add(-time.Minute)},
	})
	manager.syncDNSRecords()
	response = dnsQuery(t, handler, "web6.lab.test", dns.TypeAAAA)
	if response.Rcode != dns.RcodeNameError {
		t.Fatalf("expired IPv6 lease response = %+v", response)
	}
}

func TestDNSIntegrationUsesMACFallbackAndConflictSuffix(t *testing.T) {
	manager, err := NewVNFManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	handler := newDNSHandler(store.VNFConfig{})
	manager.registerDNSRuntime(99, handler)
	vnf := store.VNF{ID: 1, NetworkID: 10, Type: store.VNFTypeDHCPv4, Config: store.VNFConfig{
		DNSVNFID: 99, DNSDomain: "lab.test", DNSTTL: 60,
	}}
	expires := time.Now().Add(time.Hour)
	writeLeaseRecords(t, manager.leasePath(vnf.ID), []leaseRecord{
		{ClientID: "02:00:00:00:00:01", Address: "10.20.0.50", Expires: expires},
		{ClientID: "02:00:00:00:00:02", Address: "10.20.0.51", Hostname: "web", Expires: expires},
		{ClientID: "02:00:00:00:00:03", Address: "10.20.0.52", Hostname: "web", Expires: expires},
	})
	manager.registerDHCPConfig(vnf)

	response := dnsQuery(t, handler, "host-02-00-00-00-00-01.lab.test", dns.TypeA)
	if len(response.Answer) != 1 {
		t.Fatalf("MAC fallback answer = %+v", response.Answer)
	}
	response = dnsQuery(t, handler, "web.lab.test", dns.TypeA)
	if len(response.Answer) != 1 || response.Answer[0].(*dns.A).A.String() != "10.20.0.51" {
		t.Fatalf("first hostname answer = %+v", response.Answer)
	}
	response = dnsQuery(t, handler, "host-02-00-00-00-00-03.lab.test", dns.TypeA)
	if len(response.Answer) != 1 || response.Answer[0].(*dns.A).A.String() != "10.20.0.52" {
		t.Fatalf("conflict fallback answer = %+v", response.Answer)
	}
}

func writeLeaseRecords(t *testing.T, path string, leases []leaseRecord) {
	t.Helper()
	data, err := json.Marshal(leaseFile{Leases: leases})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDHCPv4DNSNameReadsFQDN(t *testing.T) {
	labels := &rfc1035label.Labels{Labels: []string{"web-01", "lab", "test"}}
	payload := append([]byte{0x04, 0, 0}, labels.ToBytes()...)
	req := dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, net.HardwareAddr{0x02, 0, 0, 0, 0, 1})
	req.UpdateOption(dhcpv4.OptGeneric(dhcpv4.OptionFQDN, payload))
	if got := dhcpv4DNSName(req); got != "web-01.lab.test" {
		t.Fatalf("FQDN = %q", got)
	}

	plain := dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, net.HardwareAddr{0x02, 0, 0, 0, 0, 1})
	plain.UpdateOption(dhcpv4.OptGeneric(dhcpv4.OptionFQDN, []byte{0, 0, 0, 'h', 'o', 's', 't'}))
	if got := dhcpv4DNSName(plain); got != "host" {
		t.Fatalf("ASCII FQDN = %q", got)
	}
}

func TestDHCPv6DNSNameReadsFQDN(t *testing.T) {
	msg, err := dhcpv6.NewMessage(dhcpv6.WithFQDN(0, "web6.lab.test"))
	if err != nil {
		t.Fatal(err)
	}
	if got := dhcpv6DNSName(msg); got != "web6.lab.test" {
		t.Fatalf("DHCPv6 FQDN = %q", got)
	}
}
