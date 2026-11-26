package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestVNFPreflightValidationRules opens a schema-60 fixture for every preflight
// rule and proves Open refuses the migration with a named problem.
func TestVNFPreflightValidationRules(t *testing.T) {
	const (
		dhcpBase  = `"gateway": "10.20.0.1/24", "range_start": "10.20.0.100", "range_end": "10.20.0.200"`
		dhcp6Base = `"gateway": "fd00::1/64", "range_start": "fd00::100", "range_end": "fd00::200"`
		natBase   = `"gateway": "10.30.0.1/24", "outside_interface": "eth0"`
		bgpBase   = `"local_as": 65000, "router_id": "10.40.0.1", "peer_as": 65001, "peer_ip": "10.40.0.2", "local_ip": "10.40.0.1/24"`
	)

	cases := []struct {
		name    string
		vnfType string
		config  string
		want    string
	}{
		// DHCPv4 scalar and unexpected-field rules.
		{"dhcpv4 missing gateway", VNFTypeDHCPv4,
			`{"range_start": "10.20.0.100", "range_end": "10.20.0.200"}`, "config.gateway is required"},
		{"dhcpv4 missing range", VNFTypeDHCPv4,
			`{"gateway": "10.20.0.1/24"}`, "config.range_start and config.range_end are required"},
		{"dhcpv4 unexpected dnat", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "dnat_rules": [{"protocol": "tcp", "external_port": 80, "local_ip": "10.20.0.5", "local_port": 80}]}`,
			"config.dnat_rules is not supported for a dhcpv4 VNF"},
		{"dhcpv4 unexpected dns records", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "dns_records": [{"name": "a.lab", "type": "A", "value": "10.0.0.1"}]}`,
			"config.dns_records is not supported for a dhcpv4 VNF"},
		{"dhcpv4 unexpected forwarders", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "dns_forwarders": ["1.1.1.1"]}`,
			"config.dns_forwarders is not supported for a dhcpv4 VNF"},
		{"dhcpv4 unexpected prefixes", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "advertise_prefixes": ["10.50.0.0/24"]}`,
			"config.advertise_prefixes is not supported for a dhcpv4 VNF"},
		{"dhcpv4 bad transport", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "transport": "bogus"}`, `config.transport "bogus" is not supported`},
		{"dhcpv4 dns ttl", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "dns_ttl": 90000}`, "config.dns_ttl must be at most 86400"},
		{"dhcpv4 assignment mac", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "static_assignments": [{"ip": "10.20.0.50"}]}`,
			"config.static_assignments[0].mac is required"},
		{"dhcpv4 assignment ip", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "static_assignments": [{"mac": "02:00:00:00:00:01"}]}`,
			"config.static_assignments[0].ip is required"},
		{"dhcpv4 duplicate assignment ip", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "static_assignments": [
				{"mac": "02:00:00:00:00:01", "ip": "10.20.0.50"},
				{"mac": "02:00:00:00:00:02", "ip": "10.20.0.50"}]}`,
			"config.static_assignments[1].ip duplicates entry 0"},
		{"dhcpv4 option code range", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "dhcp_options": [{"code": 255, "format": "string", "value": "x"}]}`,
			"config.dhcp_options[0].code 255 must be between 1 and 254 for DHCPv4"},
		{"dhcpv4 option code zero", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "dhcp_options": [{"code": 0, "format": "string", "value": "x"}]}`,
			"config.dhcp_options[0].code must be greater than zero"},
		{"dhcpv4 duplicate option", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "dhcp_options": [
				{"code": 15, "format": "string", "value": "a"},
				{"code": 15, "format": "string", "value": "b"}]}`,
			"config.dhcp_options[1].code 15 duplicates entry 0"},

		// DHCPv6 rules.
		{"dhcpv6 unexpected assignments", VNFTypeDHCPv6,
			`{` + dhcp6Base + `, "static_assignments": [{"mac": "02:00:00:00:00:01", "ip": "fd00::50"}]}`,
			"config.static_assignments is not supported for a dhcpv6 VNF"},
		{"dhcpv6 bad transport", VNFTypeDHCPv6,
			`{` + dhcp6Base + `, "transport": "shared"}`, `config.transport "shared" is not a DHCPv6 transport`},
		{"dhcpv6 dns ttl", VNFTypeDHCPv6,
			`{` + dhcp6Base + `, "dns_ttl": 90000}`, "config.dns_ttl must be at most 86400"},
		{"dhcpv6 option code zero", VNFTypeDHCPv6,
			`{` + dhcp6Base + `, "dhcp_options": [{"code": 0, "format": "string", "value": "x"}]}`,
			"config.dhcp_options[0].code must be greater than zero"},

		// DNS unexpected-field and child rules.
		{"dns unexpected assignments", VNFTypeDNS,
			`{"static_assignments": [{"mac": "02:00:00:00:00:01", "ip": "10.20.0.50"}]}`,
			"config.static_assignments is not supported for a dns VNF"},
		{"dns unexpected options", VNFTypeDNS,
			`{"dhcp_options": [{"code": 15, "format": "string", "value": "x"}]}`,
			"config.dhcp_options is not supported for a dns VNF"},
		{"dns unexpected dnat", VNFTypeDNS,
			`{"dnat_rules": [{"protocol": "tcp", "external_port": 80, "local_ip": "10.20.0.5", "local_port": 80}]}`,
			"config.dnat_rules is not supported for a dns VNF"},
		{"dns unexpected prefixes", VNFTypeDNS,
			`{"advertise_prefixes": ["10.50.0.0/24"]}`,
			"config.advertise_prefixes is not supported for a dns VNF"},
		{"dns record name", VNFTypeDNS,
			`{"dns_records": [{"type": "A", "value": "10.20.0.50"}]}`,
			"config.dns_records[0].name is required"},
		{"dns record value", VNFTypeDNS,
			`{"dns_records": [{"name": "a.lab", "type": "A"}]}`,
			"config.dns_records[0].value is required"},
		{"dns record ttl", VNFTypeDNS,
			`{"dns_records": [{"name": "a.lab", "type": "A", "value": "10.0.0.1", "ttl": 90000}]}`,
			"config.dns_records[0].ttl must be at most 86400"},
		{"dns duplicate record", VNFTypeDNS,
			`{"dns_records": [
				{"name": "a.lab", "type": "A", "value": "10.0.0.1"},
				{"name": "a.lab", "type": "a", "value": "10.0.0.1"}]}`,
			"config.dns_records[1] duplicates entry 0"},
		{"dns bad forwarder", VNFTypeDNS,
			`{"dns_forwarders": ["example.com"]}`, "is not an IP address or IP:port"},
		{"dns duplicate forwarder", VNFTypeDNS,
			`{"dns_forwarders": ["1.1.1.1", "1.1.1.1"]}`, "config.dns_forwarders[1] duplicates entry 0"},

		// NAT44 rules.
		{"nat44 missing gateway", VNFTypeNAT44,
			`{"outside_interface": "eth0"}`, "config.gateway is required"},
		{"nat44 missing outside interface", VNFTypeNAT44,
			`{"gateway": "10.30.0.1/24"}`, "config.outside_interface is required"},
		{"nat44 unexpected assignments", VNFTypeNAT44,
			`{` + natBase + `, "static_assignments": [{"mac": "02:00:00:00:00:01", "ip": "10.30.0.50"}]}`,
			"config.static_assignments is not supported for a nat44 VNF"},
		{"nat44 unexpected options", VNFTypeNAT44,
			`{` + natBase + `, "dhcp_options": [{"code": 15, "format": "string", "value": "x"}]}`,
			"config.dhcp_options is not supported for a nat44 VNF"},
		{"nat44 unexpected dns records", VNFTypeNAT44,
			`{` + natBase + `, "dns_records": [{"name": "a.lab", "type": "A", "value": "10.0.0.1"}]}`,
			"config.dns_records is not supported for a nat44 VNF"},
		{"nat44 unexpected forwarders", VNFTypeNAT44,
			`{` + natBase + `, "dns_forwarders": ["1.1.1.1"]}`,
			"config.dns_forwarders is not supported for a nat44 VNF"},
		{"nat44 unexpected prefixes", VNFTypeNAT44,
			`{` + natBase + `, "advertise_prefixes": ["10.50.0.0/24"]}`,
			"config.advertise_prefixes is not supported for a nat44 VNF"},
		{"nat44 dnat protocol", VNFTypeNAT44,
			`{` + natBase + `, "dnat_rules": [{"protocol": "icmp", "external_port": 80, "local_ip": "10.30.0.10", "local_port": 80}]}`,
			`config.dnat_rules[0].protocol "icmp" must be tcp or udp`},
		{"nat44 dnat external port", VNFTypeNAT44,
			`{` + natBase + `, "dnat_rules": [{"protocol": "tcp", "external_port": 0, "local_ip": "10.30.0.10", "local_port": 80}]}`,
			"config.dnat_rules[0].external_port must be between 1 and 65535"},
		{"nat44 dnat local port", VNFTypeNAT44,
			`{` + natBase + `, "dnat_rules": [{"protocol": "tcp", "external_port": 80, "local_ip": "10.30.0.10", "local_port": 70000}]}`,
			"config.dnat_rules[0].local_port must be between 1 and 65535"},
		{"nat44 dnat local ip", VNFTypeNAT44,
			`{` + natBase + `, "dnat_rules": [{"protocol": "tcp", "external_port": 80, "local_port": 80}]}`,
			"config.dnat_rules[0].local_ip is required"},
		{"nat44 duplicate dnat", VNFTypeNAT44,
			`{` + natBase + `, "dnat_rules": [
				{"protocol": "tcp", "external_port": 80, "local_ip": "10.30.0.10", "local_port": 8080},
				{"protocol": "TCP", "external_port": 80, "local_ip": "10.30.0.11", "local_port": 8081}]}`,
			"config.dnat_rules[1] duplicates the external port of entry 0"},

		// BGP rules.
		{"bgp missing local as", VNFTypeBGP,
			`{"router_id": "10.40.0.1", "peer_as": 65001, "peer_ip": "10.40.0.2", "local_ip": "10.40.0.1/24"}`,
			"config.local_as is required"},
		{"bgp missing peer as", VNFTypeBGP,
			`{"local_as": 65000, "router_id": "10.40.0.1", "peer_ip": "10.40.0.2", "local_ip": "10.40.0.1/24"}`,
			"config.peer_as is required"},
		{"bgp missing router id", VNFTypeBGP,
			`{"local_as": 65000, "peer_as": 65001, "peer_ip": "10.40.0.2", "local_ip": "10.40.0.1/24"}`,
			"config.router_id is required"},
		{"bgp missing peer ip", VNFTypeBGP,
			`{"local_as": 65000, "router_id": "10.40.0.1", "peer_as": 65001, "local_ip": "10.40.0.1/24"}`,
			"config.peer_ip is required"},
		{"bgp missing local ip", VNFTypeBGP,
			`{"local_as": 65000, "router_id": "10.40.0.1", "peer_as": 65001, "peer_ip": "10.40.0.2"}`,
			"config.local_ip is required"},
		{"bgp bad transport", VNFTypeBGP,
			`{` + bgpBase + `, "transport": "shared"}`, `config.transport "shared" is not a BGP transport`},
		{"bgp multihop ttl", VNFTypeBGP,
			`{` + bgpBase + `, "multihop_ttl": 300}`, "config.multihop_ttl must be at most 255"},
		{"bgp unexpected assignments", VNFTypeBGP,
			`{` + bgpBase + `, "static_assignments": [{"mac": "02:00:00:00:00:01", "ip": "10.40.0.50"}]}`,
			"config.static_assignments is not supported for a bgp VNF"},
		{"bgp unexpected options", VNFTypeBGP,
			`{` + bgpBase + `, "dhcp_options": [{"code": 15, "format": "string", "value": "x"}]}`,
			"config.dhcp_options is not supported for a bgp VNF"},
		{"bgp unexpected dns records", VNFTypeBGP,
			`{` + bgpBase + `, "dns_records": [{"name": "a.lab", "type": "A", "value": "10.0.0.1"}]}`,
			"config.dns_records is not supported for a bgp VNF"},
		{"bgp unexpected forwarders", VNFTypeBGP,
			`{` + bgpBase + `, "dns_forwarders": ["1.1.1.1"]}`,
			"config.dns_forwarders is not supported for a bgp VNF"},
		{"bgp unexpected dnat", VNFTypeBGP,
			`{` + bgpBase + `, "dnat_rules": [{"protocol": "tcp", "external_port": 80, "local_ip": "10.40.0.5", "local_port": 80}]}`,
			"config.dnat_rules is not supported for a bgp VNF"},
		{"bgp empty prefix", VNFTypeBGP,
			`{` + bgpBase + `, "advertise_prefixes": [" "]}`,
			"config.advertise_prefixes[0] is empty"},
		{"bgp duplicate prefix", VNFTypeBGP,
			`{` + bgpBase + `, "advertise_prefixes": ["10.50.0.0/24", "10.50.0.0/24"]}`,
			"config.advertise_prefixes[1] duplicates entry 0"},

		// Config decoding rules.
		{"empty config", VNFTypeDHCPv4, ``, "config is empty"},
		{"invalid config json", VNFTypeDHCPv4, `not json`, "config is not a valid JSON object"},
		{"config is not an object", VNFTypeDHCPv4, `[]`, "config is not a valid JSON object"},
		{"config field type", VNFTypeDHCPv4,
			`{` + dhcpBase + `, "static_assignments": {"mac": "02:00:00:00:00:01"}}`,
			"unexpected field type"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := openPreflightFixture(t, tc.vnfType, tc.config)
			assertPreflightRejected(t, path, tc.want)
		})
	}
}

// TestVNFPreflightDNSReferenceRules covers the three dns_vnf_id reference
// classes across a multi-VNF fixture.
func TestVNFPreflightDNSReferenceRules(t *testing.T) {
	const dhcpConfig = `{"gateway": "10.20.0.1/24", "range_start": "10.20.0.100", "range_end": "10.20.0.200", "dns_vnf_id": 2}`

	t.Run("not a dns VNF", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "vnf-dns-reference.db")
		db := openRawDB(t, dbPath)
		migrateToVersion(t, db, 60)
		insertLegacyNetwork(t, db, 1, "lab")
		insertLegacyVNF(t, db, 1, 1, "lab-dhcp", VNFTypeDHCPv4, dhcpConfig)
		insertLegacyVNF(t, db, 2, 1, "lab-dhcp6", VNFTypeDHCPv6,
			`{"gateway": "fd00::1/64", "range_start": "fd00::100", "range_end": "fd00::200"}`)
		closeRawDB(t, db)
		assertPreflightRejected(t, dbPath, "not a DNS VNF")
	})

	t.Run("another network", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "vnf-dns-network.db")
		db := openRawDB(t, dbPath)
		migrateToVersion(t, db, 60)
		insertLegacyNetwork(t, db, 1, "lab")
		insertLegacyNetwork(t, db, 2, "other")
		insertLegacyVNF(t, db, 1, 1, "lab-dhcp", VNFTypeDHCPv4, dhcpConfig)
		insertLegacyVNF(t, db, 2, 2, "other-dns", VNFTypeDNS, `{"dns_forward": true}`)
		closeRawDB(t, db)
		assertPreflightRejected(t, dbPath, "references a DNS VNF on another network")
	})
}

// TestVNFPreflightSchemaShapeErrors covers a pre-0061 database whose shape does
// not match the migration.
func TestVNFPreflightSchemaShapeErrors(t *testing.T) {
	t.Run("no config column", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "vnf-no-config.db")
		db := openRawDB(t, dbPath)
		migrateToVersion(t, db, 60)
		execSQL(t, db, `ALTER TABLE vnfs DROP COLUMN config`)
		closeRawDB(t, db)
		assertPreflightRejected(t, dbPath, "has no vnfs.config column")
	})

	t.Run("typed tables already present", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "vnf-typed-tables.db")
		db := openRawDB(t, dbPath)
		migrateToVersion(t, db, 60)
		execSQL(t, db, `CREATE TABLE dhcpv4_vnfs (vnf_id INTEGER)`)
		closeRawDB(t, db)
		assertPreflightRejected(t, dbPath, "already has typed VNF tables")
	})

	t.Run("unreadable vnfs rows", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "vnf-bad-query.db")
		db := openRawDB(t, dbPath)
		migrateToVersion(t, db, 60)
		execSQL(t, db, `ALTER TABLE vnfs RENAME COLUMN name TO vnf_name`)
		closeRawDB(t, db)
		assertPreflightRejected(t, dbPath, "read vnfs for migration preflight")
	})
}

// TestVNFPreflightDatabaseHandleErrors covers the preflight branches that need a
// broken or empty database handle.
func TestVNFPreflightDatabaseHandleErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("closed database", func(t *testing.T) {
		db := openRawDB(t, filepath.Join(t.TempDir(), "closed.db"))
		closeRawDB(t, db)
		if err := preflightVNFMigration(ctx, db); err == nil || !strings.Contains(err.Error(), "read schema version") {
			t.Fatalf("closed-database error = %v", err)
		}
		if schemaTableHasColumn(ctx, db, "vnfs", "config") {
			t.Fatal("schemaTableHasColumn reported a column on a closed database")
		}
	})
	t.Run("no vnfs table", func(t *testing.T) {
		db := openRawDB(t, filepath.Join(t.TempDir(), "empty.db"))
		defer closeRawDB(t, db)
		if err := preflightVNFMigration(ctx, db); err != nil {
			t.Fatalf("empty database preflight = %v, want nil", err)
		}
	})
}

// TestValidateLegacyConfigEdgeCases covers the validator branches that do not
// produce a problem and therefore cannot be asserted through Open.
func TestValidateLegacyConfigEdgeCases(t *testing.T) {
	emptyForwarder := legacyVNFRow{
		ID: 1, Name: "lab-dns", NetworkID: 1, Type: VNFTypeDNS,
		Config: legacyVNFConfig{DNSForwarders: []string{"  "}},
	}
	if problems := validateLegacyVNFConfig(emptyForwarder); len(problems) != 0 {
		t.Fatalf("blank forwarder problems = %v", problems)
	}
	noReference := legacyVNFRow{
		ID: 1, Name: "lab-dhcp", NetworkID: 1, Type: VNFTypeDHCPv4,
		Config: legacyVNFConfig{
			Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
		},
	}
	if problems := validateLegacyVNFRows([]legacyVNFRow{noReference}); len(problems) != 0 {
		t.Fatalf("plain row problems = %v", problems)
	}
	// A valid DNS row exercises the positive path of every DNS child rule.
	validDNS := legacyVNFRow{
		ID: 2, Name: "lab-dns", NetworkID: 1, Type: VNFTypeDNS,
		Config: legacyVNFConfig{
			DNSForwarders: []string{"1.1.1.1", "9.9.9.9:5353"},
			DNSRecords: []struct {
				Name  string `json:"name"`
				Type  string `json:"type"`
				Value string `json:"value"`
				TTL   uint32 `json:"ttl"`
			}{{Name: "host.lab", Type: "a", Value: "10.0.0.1", TTL: 300}},
		},
	}
	if problems := validateLegacyVNFConfig(validDNS); len(problems) != 0 {
		t.Fatalf("valid dns problems = %v", problems)
	}
}
