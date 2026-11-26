package host

import (
	"strings"
	"testing"

	"github.com/miekg/dns"

	"github.com/voidstarit/rookery/internal/store"
)

func TestValidateDNSConfig(t *testing.T) {
	tests := []struct {
		name    string
		records []store.DNSRecord
		wantErr string
	}{
		{name: "empty config"},
		{name: "valid A", records: []store.DNSRecord{{Name: "vm1.lab1.test.", Type: "A", Value: "10.20.0.50"}}},
		{name: "valid AAAA", records: []store.DNSRecord{{Name: "vm1.lab1.test.", Type: "AAAA", Value: "fd00::50"}}},
		{name: "valid CNAME", records: []store.DNSRecord{{Name: "alias.lab1.test.", Type: "cname", Value: "vm1.lab1.test."}}},
		{name: "valid PTR", records: []store.DNSRecord{{Name: "50.0.20.10.in-addr.arpa.", Type: "PTR", Value: "vm1.lab1.test."}}},
		{name: "valid TXT", records: []store.DNSRecord{{Name: "txt.lab1.test.", Type: "TXT", Value: "hello"}}},
		{name: "missing name", records: []store.DNSRecord{{Name: " ", Type: "A", Value: "10.20.0.50"}}, wantErr: "name is required"},
		{name: "root name", records: []store.DNSRecord{{Name: ".", Type: "A", Value: "10.20.0.50"}}, wantErr: "name is required"},
		{name: "malformed name", records: []store.DNSRecord{{Name: "bad..name.", Type: "A", Value: "10.20.0.50"}}, wantErr: "invalid DNS record name"},
		{name: "A with IPv6 value", records: []store.DNSRecord{{Name: "vm1.lab1.test.", Type: "A", Value: "fd00::50"}}, wantErr: "needs an IPv4 value"},
		{name: "AAAA with IPv4 value", records: []store.DNSRecord{{Name: "vm1.lab1.test.", Type: "AAAA", Value: "10.20.0.50"}}, wantErr: "needs an IPv6 value"},
		{name: "CNAME with malformed value", records: []store.DNSRecord{{Name: "alias.lab1.test.", Type: "CNAME", Value: "bad..value."}}, wantErr: "needs a domain value"},
		{name: "PTR with empty value", records: []store.DNSRecord{{Name: "50.0.20.10.in-addr.arpa.", Type: "PTR", Value: ""}}, wantErr: "needs a domain value"},
		{name: "unsupported type", records: []store.DNSRecord{{Name: "vm1.lab1.test.", Type: "MX", Value: "mail.lab1.test."}}, wantErr: "unsupported DNS record type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDNSConfig(store.VNFConfig{DNSRecords: test.records})
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateDNSConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateDNSConfig() error = %v, want %q", err, test.wantErr)
			}
			if _, buildErr := buildDNSHandler(store.VNFConfig{DNSRecords: test.records}); buildErr == nil {
				t.Fatal("buildDNSHandler accepted an invalid config")
			}
		})
	}
}

func TestAddDNSRejectsInvalidRuntimeConfig(t *testing.T) {
	// addDNS validates before touching the netstack, so a zero sharedNetwork
	// is enough to exercise the rejection path.
	network := &sharedNetwork{networkID: 1}
	vnf := store.VNF{
		ID:   1,
		Type: store.VNFTypeDNS,
		Config: store.VNFConfig{
			DNSRecords: []store.DNSRecord{{Name: "broken.lab1.test.", Type: "A", Value: "not-an-ip"}},
		},
	}
	if err := network.addDNS(vnf); err == nil {
		t.Fatal("addDNS accepted an invalid record")
	}
	if network.dns != nil {
		t.Fatal("addDNS registered a component for an invalid config")
	}
}

func TestReloadDNSRequiresMatchingRuntime(t *testing.T) {
	network := &sharedNetwork{networkID: 2}
	if err := network.reloadDNS(store.VNF{ID: 1, Type: store.VNFTypeDNS}); err == nil ||
		!strings.Contains(err.Error(), "not running") {
		t.Fatalf("reloadDNS without a component = %v", err)
	}

	component := &sharedDNS{vnfID: 5, done: make(chan struct{})}
	network = &sharedNetwork{networkID: 2, dns: component}
	err := network.reloadDNS(store.VNF{ID: 6, Type: store.VNFTypeDNS})
	if err == nil || !strings.Contains(err.Error(), "serves DNS VNF 5") {
		t.Fatalf("reloadDNS for a different VNF = %v", err)
	}
	if component.current() != nil {
		t.Fatal("mismatched reload published a handler")
	}
}

func TestSharedDNSDispatchAndHandlerPublication(t *testing.T) {
	handler, err := buildDNSHandler(store.VNFConfig{
		DNSRecords: []store.DNSRecord{{Name: "vm1.lab1.test.", Type: "A", Value: "10.20.0.50", TTL: 60}},
	})
	if err != nil {
		t.Fatalf("buildDNSHandler: %v", err)
	}
	component := &sharedDNS{vnfID: 1, done: make(chan struct{})}
	network := &sharedNetwork{networkID: 1, dns: component}

	if got := network.dnsHandler(); got != nil {
		t.Fatalf("dnsHandler without a handler = %v", got)
	}
	component.handler.Store(handler)
	if got := network.dnsHandler(); got != handler {
		t.Fatal("dnsHandler did not return the published handler")
	}

	request := new(dns.Msg)
	request.SetQuestion("vm1.lab1.test.", dns.TypeA)
	writer := &dnsTestWriter{}
	component.ServeDNS(writer, request)
	if writer.msg == nil || len(writer.msg.Answer) != 1 {
		t.Fatalf("dispatched response = %+v", writer.msg)
	}

	// A nil handler leaves the listener alive but drops the request.
	component.handler.Store(nil)
	writer = &dnsTestWriter{}
	component.ServeDNS(writer, request)
	if writer.msg != nil {
		t.Fatalf("nil handler produced a response: %+v", writer.msg)
	}
}
