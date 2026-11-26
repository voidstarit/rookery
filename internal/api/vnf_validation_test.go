package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

// normalizerStore opens a store with one network and returns a DHCPv4 VNF for
// the child validators.
func normalizerStore(t *testing.T) (*store.Store, store.Network, store.VNF) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "normalizers.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	network, err := st.CreateNetwork(ctx, store.Network{
		Name: "lab", BridgeDomainID: 1, IPv4CIDR: "10.20.0.0/24", IPv6CIDR: "fd00:20::/64",
	})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	vnf, err := st.CreateDHCPVNF(ctx, store.NewDHCPVNF{
		Name: "dhcp", NetworkID: network.ID, Family: store.VNFTypeDHCPv4, Enabled: true,
		Config: store.DHCPVNFConfig{
			Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
			LeaseTime: "12h", BindIP: "10.20.0.2/24",
		},
	})
	if err != nil {
		t.Fatalf("create dhcp vnf: %v", err)
	}
	return st, network, vnf
}

// expectErrorContains asserts err is non-nil and mentions want.
func expectErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error mentioning %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to mention %q", err, want)
	}
}

func validDHCPConfig() store.DHCPVNFConfig {
	return store.DHCPVNFConfig{
		Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
		LeaseTime: "12h", DNS: "10.20.0.2",
	}
}

func TestNormalizeDHCPVNFConfigBranches(t *testing.T) {
	server := &Server{}
	tests := []struct {
		name   string
		family string
		mutate func(*store.DHCPVNFConfig)
		want   string
	}{
		{name: "bad family", family: "nope", mutate: func(*store.DHCPVNFConfig) {}, want: "unsupported DHCP family"},
		{name: "missing gateway", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.Gateway = "" }, want: "gateway is required"},
		{name: "missing range", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.RangeStart = "" }, want: "range_start"},
		{name: "bad transport", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.Transport = "x" }, want: "transport must be"},
		{name: "bad gateway", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.Gateway = "x" }, want: "invalid gateway CIDR"},
		{name: "v6 gateway for v4", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.Gateway = "fd00::1/64" }, want: "IPv4 CIDR"},
		{name: "v4 gateway too small", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) {
			c.Gateway = "10.20.0.1/31"
			c.RangeStart = "10.20.0.2"
			c.RangeEnd = "10.20.0.3"
		}, want: "/30 or smaller"},
		{name: "gateway network address", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.Gateway = "10.20.0.0/24" }, want: "usable host"},
		{name: "range not ipv4", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.RangeStart = "x" }, want: "valid IPv4"},
		{name: "range outside subnet", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.RangeEnd = "10.21.0.20" }, want: "inside the gateway subnet"},
		{name: "range network", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.RangeStart = "10.20.0.0" }, want: "network or broadcast"},
		{name: "range gateway", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.RangeStart = "10.20.0.1"; c.RangeEnd = "10.20.0.100" }, want: "gateway address"},
		{name: "reversed range", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.RangeStart = "10.20.0.200"; c.RangeEnd = "10.20.0.100" }, want: "start must not be after"},
		{name: "range too large", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) {
			c.Gateway = "10.0.0.1/8"
			c.RangeStart = "10.0.1.0"
			c.RangeEnd = "10.1.255.254"
		}, want: "at most"},
		{name: "bad dns", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.DNS = "x" }, want: "invalid DNS address"},
		{name: "wrong family dns", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.DNS = "fd00::2" }, want: "not IPv4"},
		{name: "bad lease time", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.LeaseTime = "soon" }, want: "invalid lease_time"},
		{name: "bind not cidr", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.BindIP = "x" }, want: "invalid bind_ip"},
		{name: "bind wrong family", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.BindIP = "fd00:20::2/64" }, want: "must be IPv4"},
		{name: "bind outside subnet", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.BindIP = "10.30.0.2/24" }, want: "inside the gateway subnet"},
		{name: "bind equals gateway", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.BindIP = "10.20.0.1/24" }, want: "cannot equal the gateway"},
		{name: "bind wrong prefix", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.BindIP = "10.20.0.2/25" }, want: "prefix length"},
		{name: "bind network address", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.BindIP = "10.20.0.0/24" }, want: "usable host"},
		{name: "bind inside range", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.BindIP = "10.20.0.150/24" }, want: "bind_ip cannot be inside"},
		{name: "bad v6 gateway", family: store.VNFTypeDHCPv6, mutate: func(c *store.DHCPVNFConfig) { c.Gateway = "10.20.0.1/24" }, want: "IPv6 CIDR"},
		{name: "v6 unspecified gateway", family: store.VNFTypeDHCPv6, mutate: func(c *store.DHCPVNFConfig) { c.Gateway = "::/64"; c.RangeStart = "::1"; c.RangeEnd = "::2" }, want: "usable address"},
		{name: "bad v6 range", family: store.VNFTypeDHCPv6, mutate: func(c *store.DHCPVNFConfig) { c.RangeStart = "x" }, want: "range start"},
		{name: "v6 range outside", family: store.VNFTypeDHCPv6, mutate: func(c *store.DHCPVNFConfig) { c.RangeEnd = "fd00:21::2" }, want: "inside the gateway prefix"},
		{name: "v6 reversed range", family: store.VNFTypeDHCPv6, mutate: func(c *store.DHCPVNFConfig) { c.RangeStart = "fd00:20::200"; c.RangeEnd = "fd00:20::100" }, want: "start must not be after"},
		{name: "v6 range gateway", family: store.VNFTypeDHCPv6, mutate: func(c *store.DHCPVNFConfig) { c.RangeStart = "fd00:20::1"; c.RangeEnd = "fd00:20::100" }, want: "gateway address"},
		{name: "v6 anycast range", family: store.VNFTypeDHCPv6, mutate: func(c *store.DHCPVNFConfig) {
			c.Gateway = "fd00:20::5/64"
			c.RangeStart = "fd00:20::"
			c.RangeEnd = "fd00:20::4"
		}, want: "subnet-router anycast"},
		{name: "dns vnf negative", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.DNSVNFID = -1 }, want: "must be positive"},
		{name: "dns domain without id", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.DNSDomain = "lab.test" }, want: "dns_vnf_id is required"},
		{name: "dns id without domain", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.DNSVNFID = 3 }, want: "dns_domain is required"},
		{name: "bad dns domain", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.DNSVNFID = 3; c.DNSDomain = "bad domain" }, want: "without wildcards"},
		{name: "dns ttl too large", family: store.VNFTypeDHCPv4, mutate: func(c *store.DHCPVNFConfig) { c.DNSVNFID = 3; c.DNSDomain = "lab.test"; c.DNSTTL = 90000 }, want: "at most 86400"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validDHCPConfig()
			if test.family == store.VNFTypeDHCPv6 {
				config.Gateway = "fd00:20::1/64"
				config.RangeStart = "fd00:20::100"
				config.RangeEnd = "fd00:20::200"
				config.DNS = "fd00:20::2"
			}
			test.mutate(&config)
			_, err := server.NormalizeDHCPVNFConfig(test.family, config)
			expectErrorContains(t, err, test.want)
		})
	}
}

func TestNormalizeDHCPVNFConfigCanonical(t *testing.T) {
	server := &Server{}
	config, err := server.NormalizeDHCPVNFConfig(store.VNFTypeDHCPv4, store.DHCPVNFConfig{
		Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
		DNS: "10.20.0.2, 10.20.0.3", LeaseTime: "1h3", Transport: "memif",
		DNSVNFID: 3, DNSDomain: "Lab.Test.", DNSTTL: 0,
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if config.Transport != vnfTransportDedicated || config.LeaseTime != "1h30m0s" ||
		config.DNS != "10.20.0.2,10.20.0.3" || config.DNSDomain != "lab.test" || config.DNSTTL != 300 {
		t.Fatalf("canonical config = %+v", config)
	}
	v6, err := server.NormalizeDHCPVNFConfig(store.VNFTypeDHCPv6, store.DHCPVNFConfig{
		Gateway: "fd00:20::1/64", RangeStart: "fd00:20::100", RangeEnd: "fd00:20::200",
		Transport: "shared", LeaseTime: "",
	})
	if err != nil {
		t.Fatalf("normalize v6: %v", err)
	}
	if v6.Transport != vnfTransportTap || v6.LeaseTime != "12h" {
		t.Fatalf("v6 canonical config = %+v", v6)
	}
}

func TestNormalizeDHCPStaticAssignmentBranches(t *testing.T) {
	st, network, vnf := normalizerStore(t)
	server := &Server{store: st}
	tests := []struct {
		name string
		vnf  store.VNF
		item store.DHCPStaticAssignment
		want string
	}{
		{name: "wrong type", vnf: store.VNF{Type: store.VNFTypeDNS, Config: vnf.Config}, item: store.DHCPStaticAssignment{MAC: "02:00:00:00:00:01", IP: "10.20.0.50"}, want: "DHCPv4 VNFs only"},
		{name: "bad gateway", vnf: store.VNF{ID: vnf.ID, Type: store.VNFTypeDHCPv4, NetworkID: network.ID, Config: store.VNFConfig{Gateway: "x"}}, item: store.DHCPStaticAssignment{MAC: "02:00:00:00:00:01", IP: "10.20.0.50"}, want: "invalid DHCP gateway"},
		{name: "bad mac", vnf: vnf, item: store.DHCPStaticAssignment{MAC: "x", IP: "10.20.0.50"}, want: "valid Ethernet MAC"},
		{name: "bad ip", vnf: vnf, item: store.DHCPStaticAssignment{MAC: "02:00:00:00:00:01", IP: "x"}, want: "valid IPv4"},
		{name: "outside subnet", vnf: vnf, item: store.DHCPStaticAssignment{MAC: "02:00:00:00:00:01", IP: "10.30.0.50"}, want: "inside the DHCP subnet"},
		{name: "network address", vnf: vnf, item: store.DHCPStaticAssignment{MAC: "02:00:00:00:00:01", IP: "10.20.0.0"}, want: "usable host"},
		{name: "gateway", vnf: vnf, item: store.DHCPStaticAssignment{MAC: "02:00:00:00:00:01", IP: "10.20.0.1"}, want: "DHCP gateway"},
		{name: "bind", vnf: vnf, item: store.DHCPStaticAssignment{MAC: "02:00:00:00:00:01", IP: "10.20.0.2"}, want: "service address"},
		{name: "reserved network host", vnf: vnf, item: store.DHCPStaticAssignment{MAC: "02:00:00:00:00:01", IP: "10.20.0.4"}, want: "reserved addresses"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := server.NormalizeDHCPStaticAssignment(context.Background(), test.vnf, test.item)
			expectErrorContains(t, err, test.want)
		})
	}
	normalized, err := server.NormalizeDHCPStaticAssignment(context.Background(), vnf, store.DHCPStaticAssignment{
		MAC: "02:00:00:00:00:01", IP: "10.20.0.50",
	})
	if err != nil || normalized.MAC != "02:00:00:00:00:01" || normalized.IP != "10.20.0.50" {
		t.Fatalf("normalized assignment = %+v, err = %v", normalized, err)
	}
}

func TestNormalizeDHCPOptionBranches(t *testing.T) {
	_, _, vnf := normalizerStore(t)
	server := &Server{}
	tests := []struct {
		name string
		vnf  store.VNF
		item store.DHCPOption
		want string
	}{
		{name: "zero code", vnf: vnf, item: store.DHCPOption{Code: 0}, want: "greater than zero"},
		{name: "reserved v4", vnf: vnf, item: store.DHCPOption{Code: 51, Format: "uint32", Value: "60"}, want: "managed by the server"},
		{name: "bad format", vnf: vnf, item: store.DHCPOption{Code: 15, Format: "nope", Value: "x"}, want: "format must be"},
		{name: "empty string", vnf: vnf, item: store.DHCPOption{Code: 15, Format: "string", Value: ""}, want: "value is required"},
		{name: "bad ipv4", vnf: vnf, item: store.DHCPOption{Code: 42, Format: "ipv4", Value: "x"}, want: "invalid IP"},
		{name: "v6 reserved", vnf: store.VNF{Type: store.VNFTypeDHCPv6}, item: store.DHCPOption{Code: 3, Format: "string", Value: "x"}, want: "managed by the server"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := server.NormalizeDHCPOption(context.Background(), test.vnf, test.item)
			expectErrorContains(t, err, test.want)
		})
	}
	if option, err := server.NormalizeDHCPOption(context.Background(), vnf, store.DHCPOption{Code: 15, Format: "string", Value: "lab.test"}); err != nil || option.Code != 15 {
		t.Fatalf("normalized option = %+v, err = %v", option, err)
	}
}

func TestNormalizeNAT44VNFConfigBranches(t *testing.T) {
	server := &Server{}
	valid := store.NAT44VNFConfig{Gateway: "10.30.0.1/24", OutsideInterface: "eth0"}
	tests := []struct {
		name   string
		mutate func(*store.NAT44VNFConfig)
		want   string
	}{
		{name: "bad gateway", mutate: func(c *store.NAT44VNFConfig) { c.Gateway = "x" }, want: "invalid NAT44 gateway"},
		{name: "v6 gateway", mutate: func(c *store.NAT44VNFConfig) { c.Gateway = "fd00::1/64" }, want: "IPv4 CIDR"},
		{name: "small prefix", mutate: func(c *store.NAT44VNFConfig) { c.Gateway = "10.30.0.1/31" }, want: "/30 or smaller"},
		{name: "network gateway", mutate: func(c *store.NAT44VNFConfig) { c.Gateway = "10.30.0.0/24" }, want: "usable host"},
		{name: "missing outside", mutate: func(c *store.NAT44VNFConfig) { c.OutsideInterface = "" }, want: "outside_interface is required"},
		{name: "bad outside address", mutate: func(c *store.NAT44VNFConfig) { c.OutsideAddress = "x" }, want: "outside_address must be an IPv4 CIDR"},
		{name: "zero outside prefix", mutate: func(c *store.NAT44VNFConfig) { c.OutsideAddress = "0.0.0.0/0" }, want: "nonzero IPv4 prefix"},
		{name: "gateway without address", mutate: func(c *store.NAT44VNFConfig) { c.OutsideGateway = "192.0.2.1" }, want: "requires config.outside_address"},
		{name: "bad outside gateway", mutate: func(c *store.NAT44VNFConfig) {
			c.OutsideAddress = "192.0.2.10/24"
			c.OutsideGateway = "x"
		}, want: "outside_gateway must be an IPv4 address"},
		{name: "outside gateway outside subnet", mutate: func(c *store.NAT44VNFConfig) {
			c.OutsideAddress = "192.0.2.10/24"
			c.OutsideGateway = "198.51.100.1"
		}, want: "inside the outside subnet"},
		{name: "bad pool", mutate: func(c *store.NAT44VNFConfig) { c.NAT44Pool = "x" }, want: "single IPv4 address"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			_, err := server.NormalizeNAT44VNFConfig(config)
			expectErrorContains(t, err, test.want)
		})
	}
	canonical, err := server.NormalizeNAT44VNFConfig(store.NAT44VNFConfig{
		Gateway: "10.30.0.1/24", OutsideInterface: "eth0", OutsideAddress: "192.0.2.10/24",
		OutsideGateway: "192.0.2.1", NAT44Pool: "192.0.2.100-192.0.2.200",
	})
	if err != nil || canonical.Gateway != "10.30.0.1/24" || canonical.NAT44Pool == "" {
		t.Fatalf("canonical NAT44 = %+v, err = %v", canonical, err)
	}
}

func TestNormalizeDNATRuleBranches(t *testing.T) {
	server := &Server{}
	natVNF := store.VNF{ID: 1, Type: store.VNFTypeNAT44, Config: store.VNFConfig{Gateway: "10.20.0.1/24"}}
	valid := store.DNATRule{Protocol: "tcp", ExternalPort: 80, LocalIP: "10.20.0.50", LocalPort: 8080}
	tests := []struct {
		name string
		vnf  store.VNF
		rule store.DNATRule
		want string
	}{
		{name: "wrong type", vnf: store.VNF{Type: store.VNFTypeBGP}, rule: valid, want: "NAT44 VNFs only"},
		{name: "bad parent gateway", vnf: store.VNF{Type: store.VNFTypeNAT44, Config: store.VNFConfig{Gateway: "x"}}, rule: valid, want: "invalid NAT44 gateway"},
		{name: "bad protocol", vnf: natVNF, rule: store.DNATRule{Protocol: "icmp", ExternalPort: 80, LocalIP: "10.20.0.50", LocalPort: 8080}, want: "tcp or udp"},
		{name: "bad external port", vnf: natVNF, rule: store.DNATRule{Protocol: "tcp", ExternalPort: 0, LocalIP: "10.20.0.50", LocalPort: 8080}, want: "external_port"},
		{name: "bad local port", vnf: natVNF, rule: store.DNATRule{Protocol: "tcp", ExternalPort: 80, LocalIP: "10.20.0.50", LocalPort: 0}, want: "local_port"},
		{name: "bad local ip", vnf: natVNF, rule: store.DNATRule{Protocol: "tcp", ExternalPort: 80, LocalIP: "x", LocalPort: 8080}, want: "valid IPv4"},
		{name: "outside subnet", vnf: natVNF, rule: store.DNATRule{Protocol: "tcp", ExternalPort: 80, LocalIP: "10.30.0.50", LocalPort: 8080}, want: "inside the NAT44 subnet"},
		{name: "network address", vnf: natVNF, rule: store.DNATRule{Protocol: "tcp", ExternalPort: 80, LocalIP: "10.20.0.0", LocalPort: 8080}, want: "usable host"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := server.NormalizeDNATRule(context.Background(), test.vnf, nil, test.rule)
			expectErrorContains(t, err, test.want)
		})
	}
	rule, err := server.NormalizeDNATRule(context.Background(), natVNF, nil, store.DNATRule{
		Protocol: "TCP", ExternalPort: 80, LocalIP: "10.20.0.50", LocalPort: 8080,
	})
	if err != nil || rule.Protocol != "tcp" || rule.LocalIP != "10.20.0.50" {
		t.Fatalf("normalized rule = %+v, err = %v", rule, err)
	}
}

func TestNormalizeDNSRecordBranches(t *testing.T) {
	server := &Server{}
	dnsVNF := store.VNF{Type: store.VNFTypeDNS}
	tests := []struct {
		name     string
		vnf      store.VNF
		siblings []store.DNSRecord
		record   store.DNSRecord
		want     string
	}{
		{name: "wrong type", vnf: store.VNF{Type: store.VNFTypeBGP}, record: store.DNSRecord{Name: "a.lab.test", Type: "A", Value: "10.0.0.1"}, want: "DNS VNFs only"},
		{name: "empty name", vnf: dnsVNF, record: store.DNSRecord{Name: "", Type: "A", Value: "10.0.0.1"}, want: "name is required"},
		{name: "bad wildcard", vnf: dnsVNF, record: store.DNSRecord{Name: "a.*.test", Type: "A", Value: "10.0.0.1"}, want: "leading *"},
		{name: "wildcard suffix", vnf: dnsVNF, record: store.DNSRecord{Name: "*.", Type: "A", Value: "10.0.0.1"}, want: "domain suffix"},
		{name: "whitespace", vnf: dnsVNF, record: store.DNSRecord{Name: "a b.test", Type: "A", Value: "10.0.0.1"}, want: "whitespace"},
		{name: "root", vnf: dnsVNF, record: store.DNSRecord{Name: ".", Type: "A", Value: "10.0.0.1"}, want: "DNS root"},
		{name: "bad type", vnf: dnsVNF, record: store.DNSRecord{Name: "a.test", Type: "SRV", Value: "x"}, want: "A, AAAA, CNAME"},
		{name: "empty value", vnf: dnsVNF, record: store.DNSRecord{Name: "a.test", Type: "A", Value: ""}, want: "value is required"},
		{name: "bad A", vnf: dnsVNF, record: store.DNSRecord{Name: "a.test", Type: "A", Value: "fd00::1"}, want: "IPv4 address"},
		{name: "bad AAAA", vnf: dnsVNF, record: store.DNSRecord{Name: "a.test", Type: "AAAA", Value: "10.0.0.1"}, want: "IPv6 address"},
		{name: "cname wildcard target", vnf: dnsVNF, record: store.DNSRecord{Name: "a.test", Type: "CNAME", Value: "*.b.test"}, want: "cannot be wildcards"},
		{name: "long TXT", vnf: dnsVNF, record: store.DNSRecord{Name: "a.test", Type: "TXT", Value: strings.Repeat("x", 256)}, want: "255 bytes"},
		{name: "ptr wildcard", vnf: dnsVNF, record: store.DNSRecord{Name: "a.test", Type: "PTR", Value: "*.b.test"}, want: "cannot be wildcards"},
		{name: "ttl too large", vnf: dnsVNF, record: store.DNSRecord{Name: "a.test", Type: "A", Value: "10.0.0.1", TTL: 90000}, want: "at most 86400"},
		{name: "cname with sibling", vnf: dnsVNF, siblings: []store.DNSRecord{{Name: "a.test.", Type: "A", Value: "10.0.0.1"}}, record: store.DNSRecord{Name: "a.test", Type: "CNAME", Value: "b.test"}, want: "already has a record"},
		{name: "other type with cname sibling", vnf: dnsVNF, siblings: []store.DNSRecord{{Name: "a.test.", Type: "CNAME", Value: "b.test."}}, record: store.DNSRecord{Name: "a.test", Type: "A", Value: "10.0.0.1"}, want: "already has a CNAME"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := server.NormalizeDNSRecord(context.Background(), test.vnf, test.siblings, test.record)
			expectErrorContains(t, err, test.want)
		})
	}
	record, err := server.NormalizeDNSRecord(context.Background(), dnsVNF, nil, store.DNSRecord{
		Name: "*.Lab.Test.", Type: "a", Value: "10.0.0.1", TTL: 0,
	})
	if err != nil || record.Name != "*.lab.test." || record.Type != "A" || record.TTL != 300 {
		t.Fatalf("normalized record = %+v, err = %v", record, err)
	}
}

func TestNormalizeDNSForwarderBranches(t *testing.T) {
	server := &Server{}
	dnsVNF := store.VNF{Type: store.VNFTypeDNS}
	tests := []struct {
		name string
		vnf  store.VNF
		item store.DNSForwarder
		want string
	}{
		{name: "wrong type", vnf: store.VNF{Type: store.VNFTypeDHCPv4}, item: store.DNSForwarder{Address: "1.1.1.1"}, want: "DNS VNFs only"},
		{name: "empty", vnf: dnsVNF, item: store.DNSForwarder{}, want: "server is required"},
		{name: "bad address", vnf: dnsVNF, item: store.DNSForwarder{Address: "x"}, want: "IP address"},
		{name: "bad port", vnf: dnsVNF, item: store.DNSForwarder{Address: "1.1.1.1:0"}, want: "port must be"},
		{name: "negative position", vnf: dnsVNF, item: store.DNSForwarder{Address: "1.1.1.1", Position: -1}, want: "position"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := server.NormalizeDNSForwarder(context.Background(), test.vnf, nil, test.item)
			expectErrorContains(t, err, test.want)
		})
	}
	forwarder, err := server.NormalizeDNSForwarder(context.Background(), dnsVNF, nil, store.DNSForwarder{Address: "9.9.9.9", Position: 2})
	if err != nil || forwarder.Address != "9.9.9.9:53" {
		t.Fatalf("normalized forwarder = %+v, err = %v", forwarder, err)
	}
}

func TestNormalizeBGPVNFConfigBranches(t *testing.T) {
	server := &Server{}
	valid := store.BGPVNFConfig{
		Transport: "tap", LocalAS: 65000, RouterID: "10.40.0.1", PeerAS: 65001,
		PeerIP: "10.40.0.2", LocalIP: "10.40.0.1/24",
	}
	tests := []struct {
		name   string
		mutate func(*store.BGPVNFConfig)
		want   string
	}{
		{name: "bad transport", mutate: func(c *store.BGPVNFConfig) { c.Transport = "x" }, want: "memif or tap"},
		{name: "missing local as", mutate: func(c *store.BGPVNFConfig) { c.LocalAS = 0 }, want: "local_as is required"},
		{name: "missing peer as", mutate: func(c *store.BGPVNFConfig) { c.PeerAS = 0 }, want: "peer_as is required"},
		{name: "bad router id", mutate: func(c *store.BGPVNFConfig) { c.RouterID = "x" }, want: "router_id"},
		{name: "bad local ip", mutate: func(c *store.BGPVNFConfig) { c.LocalIP = "x" }, want: "local_ip must be"},
		{name: "bad peer family", mutate: func(c *store.BGPVNFConfig) { c.PeerIP = "fd00::2" }, want: "same address family"},
		{name: "peer equals local", mutate: func(c *store.BGPVNFConfig) { c.PeerIP = "10.40.0.1" }, want: "cannot equal"},
		{name: "bad gateway family", mutate: func(c *store.BGPVNFConfig) { c.PeerGateway = "fd00::1" }, want: "peer_gateway must use"},
		{name: "gateway outside subnet", mutate: func(c *store.BGPVNFConfig) { c.PeerGateway = "10.50.0.1" }, want: "inside config.local_ip"},
		{name: "gateway required", mutate: func(c *store.BGPVNFConfig) { c.PeerIP = "10.50.0.2" }, want: "peer_gateway is required"},
		{name: "ttl too large", mutate: func(c *store.BGPVNFConfig) { c.MultihopTTL = 256 }, want: "255"},
		{name: "password with memif", mutate: func(c *store.BGPVNFConfig) { c.Transport = "memif"; c.Password = "s" }, want: "requires tap transport"},
		{name: "bad next hop", mutate: func(c *store.BGPVNFConfig) { c.NextHop = "x" }, want: "next_hop must use"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			_, err := server.NormalizeBGPVNFConfig(config)
			expectErrorContains(t, err, test.want)
		})
	}
	canonical, err := server.NormalizeBGPVNFConfig(store.BGPVNFConfig{
		LocalAS: 65000, RouterID: "10.40.0.1", PeerAS: 65001, PeerIP: "10.40.0.2",
		LocalIP: "10.40.0.1/24", PeerGateway: "10.40.0.3", MultihopTTL: 0,
	})
	if err != nil || canonical.Transport != "memif" || canonical.MultihopTTL != 64 {
		t.Fatalf("canonical BGP = %+v, err = %v", canonical, err)
	}
	v6, err := server.NormalizeBGPVNFConfig(store.BGPVNFConfig{
		Transport: "tap", LocalAS: 1, RouterID: "1.1.1.1", PeerAS: 2,
		PeerIP: "fd00::2", LocalIP: "fd00::1/64", PeerGateway: "fd00::3", NextHop: "fd00::4",
	})
	if err != nil || v6.NextHop != "fd00::4" {
		t.Fatalf("canonical v6 BGP = %+v, err = %v", v6, err)
	}
}

func TestNormalizeBGPPrefixBranches(t *testing.T) {
	server := &Server{}
	vnf := store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{LocalIP: "10.40.0.1/24"}}
	tests := []struct {
		name   string
		vnf    store.VNF
		prefix string
		want   string
	}{
		{name: "wrong type", vnf: store.VNF{Type: store.VNFTypeDHCPv4}, prefix: "10.50.0.0/24", want: "BGP VNFs only"},
		{name: "bad local ip", vnf: store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{LocalIP: "x"}}, prefix: "10.50.0.0/24", want: "invalid BGP local_ip"},
		{name: "bad prefix", vnf: vnf, prefix: "x", want: "same address family"},
		{name: "wrong family", vnf: vnf, prefix: "fd00::/64", want: "same address family"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := server.NormalizeBGPPrefix(context.Background(), test.vnf, test.prefix)
			expectErrorContains(t, err, test.want)
		})
	}
	prefix, err := server.NormalizeBGPPrefix(context.Background(), vnf, "10.50.0.7/24")
	if err != nil || prefix.Prefix != "10.50.0.0/24" {
		t.Fatalf("normalized prefix = %+v, err = %v", prefix, err)
	}
}

func TestLeaseBelongsToVNF(t *testing.T) {
	v4 := store.VNF{Type: store.VNFTypeDHCPv4, Config: store.VNFConfig{
		Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
	}}
	if !leaseBelongsToVNF(v4, "10.20.0.150") || leaseBelongsToVNF(v4, "10.20.0.250") ||
		leaseBelongsToVNF(v4, "10.30.0.150") || leaseBelongsToVNF(v4, "x") {
		t.Fatal("DHCPv4 lease range check is wrong")
	}
	if leaseBelongsToVNF(store.VNF{Type: store.VNFTypeDHCPv4, Config: store.VNFConfig{Gateway: "x"}}, "10.20.0.1") {
		t.Fatal("invalid gateway matched a lease")
	}
	v6 := store.VNF{Type: store.VNFTypeDHCPv6, Config: store.VNFConfig{
		Gateway: "fd00::1/64", RangeStart: "fd00::100", RangeEnd: "fd00::200",
	}}
	if !leaseBelongsToVNF(v6, "fd00::150") || leaseBelongsToVNF(v6, "fd00::250") ||
		leaseBelongsToVNF(v6, "10.20.0.1") || leaseBelongsToVNF(v6, "x") {
		t.Fatal("DHCPv6 lease range check is wrong")
	}
	for _, bad := range []store.VNF{
		{Type: store.VNFTypeDHCPv6, Config: store.VNFConfig{Gateway: "x"}},
		{Type: store.VNFTypeDHCPv6, Config: store.VNFConfig{Gateway: "fd00::1/64", RangeStart: "x", RangeEnd: "fd00::200"}},
		{Type: store.VNFTypeBGP},
	} {
		if leaseBelongsToVNF(bad, "10.20.0.1") {
			t.Fatalf("bad VNF matched a lease: %+v", bad)
		}
	}
}

func TestApplyNATGatewayToDHCPConfigBranches(t *testing.T) {
	network := store.Network{IPv4Gateway: "10.20.0.1/24", IPv4CIDR: "10.20.0.0/24"}
	config := store.DHCPVNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"}
	if err := applyNATGatewayToDHCPConfig(network, &config); err != nil || config.BindIP != "10.20.0.2/24" {
		t.Fatalf("apply NAT = %+v, err = %v", config, err)
	}
	if err := applyNATGatewayToDHCPConfig(store.Network{}, &config); err != nil {
		t.Fatalf("no gateway should be a no-op: %v", err)
	}
	badGateway := store.Network{IPv4Gateway: "x"}
	if err := applyNATGatewayToDHCPConfig(badGateway, &config); err == nil {
		t.Fatal("bad network gateway accepted")
	}
	outside := store.Network{IPv4Gateway: "10.20.0.1/24"}
	outsideConfig := store.DHCPVNFConfig{RangeStart: "10.30.0.100", RangeEnd: "10.30.0.200"}
	if err := applyNATGatewayToDHCPConfig(outside, &outsideConfig); err == nil {
		t.Fatal("range outside NAT subnet accepted")
	}
	reserved := store.Network{IPv4Gateway: "10.20.0.1/24", IPv4CIDR: "10.20.0.0/24"}
	reservedConfig := store.DHCPVNFConfig{RangeStart: "10.20.0.3", RangeEnd: "10.20.0.200"}
	if err := applyNATGatewayToDHCPConfig(reserved, &reservedConfig); err == nil {
		t.Fatal("reserved range accepted")
	}
	overBind := store.Network{IPv4Gateway: "10.20.0.1/24"}
	overBindConfig := store.DHCPVNFConfig{RangeStart: "10.20.0.2", RangeEnd: "10.20.0.200"}
	if err := applyNATGatewayToDHCPConfig(overBind, &overBindConfig); err == nil {
		t.Fatal("range over the service address accepted")
	}
}

func TestPersistVNFActivationBranches(t *testing.T) {
	st, _, vnf := normalizerStore(t)
	server := &Server{store: st}
	updated, err := server.persistVNFActivation(context.Background(), vnf, "10.20.0.1/24", "10.20.0.2/24", true)
	if err != nil || !updated.Enabled || updated.Config.BindIP != "10.20.0.2/24" {
		t.Fatalf("persist activation = %+v, err = %v", updated, err)
	}
	// A DNS VNF cannot take the DHCP derived setter; the failure path disables
	// the identity and returns the error.
	dns, err := st.CreateDNSVNF(context.Background(), store.NewDNSVNF{Name: "dns", NetworkID: vnf.NetworkID})
	if err != nil {
		t.Fatalf("create dns: %v", err)
	}
	if _, err := server.persistVNFActivation(context.Background(), dns, "10.20.0.1/24", "10.20.0.2/24", true); err == nil {
		t.Fatal("derived bindings were accepted for a DNS VNF")
	}
	reloaded, err := st.GetVNF(context.Background(), dns.ID)
	if err != nil {
		t.Fatalf("get dns: %v", err)
	}
	if reloaded.Enabled {
		t.Fatal("failed derived binding did not disable the VNF")
	}
}

func TestVNFStatusHelpers(t *testing.T) {
	st, _, vnf := normalizerStore(t)
	server := &Server{store: st, natRunning: map[int64]bool{}}
	if server.vnfRunning(context.Background(), vnf) {
		t.Fatal("VNF without a manager reported as running")
	}
	nat44 := store.VNF{ID: 99, Type: store.VNFTypeNAT44}
	server.setNAT44Running(nat44.ID, true)
	if !server.vnfRunning(context.Background(), nat44) {
		t.Fatal("running NAT44 VNF reported as stopped")
	}
	server.setNAT44Running(nat44.ID, false)
	if server.vnfRunning(context.Background(), nat44) {
		t.Fatal("stopped NAT44 VNF reported as running")
	}
	view := server.viewVNF(context.Background(), vnf)
	if view.Status != "degraded" {
		t.Fatalf("status = %q, want degraded without a runtime", view.Status)
	}
	status, detail, count := server.VNFStatus(context.Background(), vnf)
	if status != "degraded" || detail == "" || count != 0 {
		t.Fatalf("VNFStatus = %q, %q, %d", status, detail, count)
	}
	if _, err := server.VNFDNSRecords(context.Background(), vnf); err == nil {
		t.Fatal("DNS records accepted a DHCP VNF")
	}
	if _, err := server.VNFBGP(context.Background(), vnf); err == nil {
		t.Fatal("BGP status accepted a DHCP VNF")
	}
	if _, err := server.VNFSessions(context.Background(), vnf); err == nil {
		t.Fatal("sessions accepted a DHCP VNF")
	}
	if sessions, err := server.VNFSessions(context.Background(), nat44); err != nil || len(sessions) != 0 {
		t.Fatalf("nil NAT sessions = %+v, err = %v", sessions, err)
	}
}

func TestBackendGuardBranches(t *testing.T) {
	st, network, _ := normalizerStore(t)
	server := &Server{store: st}
	config := validDHCPConfig()
	if err := server.ApplyNATGatewayToDHCPConfig(context.Background(), network.ID, store.VNFTypeDHCPv6, &config); err != nil {
		t.Fatalf("DHCPv6 NAT rewrite should be a no-op: %v", err)
	}
	if err := server.ApplyNATGatewayToDHCPConfig(context.Background(), 9999, store.VNFTypeDHCPv4, &config); err == nil {
		t.Fatal("NAT rewrite for a missing network succeeded")
	}
	if err := server.ReloadVNF(context.Background(), store.VNF{Type: store.VNFTypeNAT44}); err == nil {
		t.Fatal("NAT44 reload should fail")
	}
	if err := server.ReloadVNF(context.Background(), store.VNF{Type: store.VNFTypeDHCPv4}); err == nil {
		t.Fatal("reload without a runtime should fail")
	}
	if err := server.ApplyNAT44DNATRules(context.Background(), store.VNF{Type: store.VNFTypeNAT44}); err == nil {
		t.Fatal("NAT44 mapping update without a runtime should fail")
	}
}

// stubNAT implements only the static-mapping calls used by the backend tests;
// the embedded interface satisfies the rest of NAT44Provisioner.
type stubNAT struct {
	NAT44Provisioner
	removeErr error
	applyErr  error
	applied   int
	removed   int
}

func (s *stubNAT) RemoveNAT44StaticMappings(context.Context, string, int64) error {
	s.removed++
	return s.removeErr
}

func (s *stubNAT) ApplyNAT44StaticMappings(context.Context, string, int64, []store.DNATRule) error {
	s.applied++
	return s.applyErr
}

// stubManager implements only ReloadVNF; the embedded interface satisfies the
// rest of VNFManager.
type stubManager struct {
	VNFManager
	err error
}

func (s *stubManager) ReloadVNF(context.Context, store.VNF) error { return s.err }

func TestApplyNAT44DNATRulesBranches(t *testing.T) {
	nat := &stubNAT{}
	server := &Server{nat: nat}
	vnf := store.VNF{ID: 7, Type: store.VNFTypeNAT44, Config: store.VNFConfig{
		OutsideInterface: "eth0", DNATRules: []store.DNATRule{{Protocol: "tcp", ExternalPort: 80, LocalIP: "10.20.0.5", LocalPort: 8080}},
	}}
	if err := server.ApplyNAT44DNATRules(context.Background(), vnf); err != nil {
		t.Fatalf("apply mappings: %v", err)
	}
	if nat.removed != 1 || nat.applied != 1 {
		t.Fatalf("remove=%d apply=%d", nat.removed, nat.applied)
	}
	if err := server.ApplyNAT44DNATRules(context.Background(), store.VNF{ID: 1, Type: store.VNFTypeBGP}); err == nil {
		t.Fatal("non-NAT44 VNF accepted")
	}
	if err := server.ApplyNAT44DNATRules(context.Background(), store.VNF{ID: 1, Type: store.VNFTypeNAT44}); err == nil {
		t.Fatal("NAT44 VNF without an outside interface accepted")
	}
	nat.removeErr = errors.New("remove failed")
	if err := server.ApplyNAT44DNATRules(context.Background(), vnf); err == nil {
		t.Fatal("remove failure was swallowed")
	}
	nat.removeErr = nil
	nat.applyErr = errors.New("apply failed")
	if err := server.ApplyNAT44DNATRules(context.Background(), vnf); err == nil {
		t.Fatalf("apply failure error = %v, removed = %d", err, nat.removed)
	}
}

func TestReloadVNFDelegates(t *testing.T) {
	manager := &stubManager{}
	server := &Server{vnfs: manager}
	if err := server.ReloadVNF(context.Background(), store.VNF{Type: store.VNFTypeDNS}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	manager.err = errors.New("reload failed")
	if err := server.ReloadVNF(context.Background(), store.VNF{Type: store.VNFTypeBGP}); err == nil {
		t.Fatal("manager reload error was swallowed")
	}
}

func TestNormalizeLeaseTimeBranches(t *testing.T) {
	for _, value := range []string{"", "1h3", "3", "1h1", "2h2", "12h"} {
		if _, err := normalizeLeaseTime(value); err != nil {
			t.Fatalf("normalizeLeaseTime(%q): %v", value, err)
		}
	}
	for _, value := range []string{"soon", "0s", "-1h"} {
		if _, err := normalizeLeaseTime(value); err == nil {
			t.Fatalf("normalizeLeaseTime(%q) accepted", value)
		}
	}
}

func TestNormalizeVNFDNSBranches(t *testing.T) {
	if value, err := normalizeVNFDNS("", false); err != nil || value != "" {
		t.Fatalf("empty DNS = %q, err = %v", value, err)
	}
	if _, err := normalizeVNFDNS("x", false); err == nil {
		t.Fatal("bad DNS accepted")
	}
	if _, err := normalizeVNFDNS("fd00::2", false); err == nil {
		t.Fatal("v6 DNS accepted for v4")
	}
	if _, err := normalizeVNFDNS("10.0.0.2", true); err == nil {
		t.Fatal("v4 DNS accepted for v6")
	}
	if value, err := normalizeVNFDNS("10.0.0.2,,10.0.0.3", false); err != nil || value != "10.0.0.2,10.0.0.3" {
		t.Fatalf("DNS list = %q, err = %v", value, err)
	}
}

func TestNormalizeDNSDomainBranches(t *testing.T) {
	if value, err := normalizeDNSDomain(""); err != nil || value != "" {
		t.Fatalf("empty domain = %q, err = %v", value, err)
	}
	if _, err := normalizeDNSDomain("bad domain"); err == nil {
		t.Fatal("space in domain accepted")
	}
	if value, err := normalizeDNSDomain("Lab.Test."); err != nil || value != "lab.test" {
		t.Fatalf("domain = %q, err = %v", value, err)
	}
}
