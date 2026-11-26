package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// insertLegacyNetwork inserts a network at the pre-0061 schema.
func insertLegacyNetwork(t *testing.T, db *sql.DB, id int64, name string) {
	t.Helper()
	execSQL(t, db, `
		INSERT INTO networks (id, name, bridge_domain_id, created_at, updated_at)
		VALUES (`+strconv.FormatInt(id, 10)+`, '`+name+`', `+strconv.FormatInt(100+id, 10)+`, 1000, 1000)`)
}

// insertLegacyVNF inserts a VNF with raw JSON config at the pre-0061 schema.
func insertLegacyVNF(t *testing.T, db *sql.DB, id, networkID int64, name, vnfType, config string) {
	t.Helper()
	execSQL(t, db, `
		INSERT INTO vnfs
			(id, name, network_id, type, enabled, config, tap_sw_if_index,
			 tap_host_name, memif_sw_if_index, memif_socket, memif_host_name,
			 created_at, updated_at)
		VALUES (`+strconv.FormatInt(id, 10)+`, '`+name+`', `+strconv.FormatInt(networkID, 10)+`, '`+vnfType+`', 1,
		        '`+strings.ReplaceAll(config, "'", "''")+`', 7, 'vnf-tap', 9,
		        '/run/rookery/memif/9.sock', '`+name+`', 1000, 2000)`)
}

// legacyVNFConfigs returns one full JSON config per VNF type. The DNS VNF is
// expected to have ID 1 and the DHCP VNFs reference it.
func legacyVNFConfigs() map[string]string {
	return map[string]string{
		VNFTypeDNS: `{
			"dns_forward": true,
			"dns_forwarders": ["1.1.1.1", "9.9.9.9:5353"],
			"dns_records": [
				{"name": "host.lab1.test", "type": "a", "value": "10.20.0.50", "ttl": 0},
				{"name": "*.lab1.test", "type": "A", "value": "10.20.0.51", "ttl": 600}
			]
		}`,
		VNFTypeDHCPv4: `{
			"gateway": "10.20.0.1/24",
			"range_start": "10.20.0.100",
			"range_end": "10.20.0.200",
			"dns": "10.20.0.2",
			"lease_time": "1h3",
			"bind_ip": "10.20.0.2/24",
			"transport": "memif",
			"dns_vnf_id": 1,
			"dns_domain": "lab1.test",
			"dns_ttl": 600,
			"static_assignments": [{"mac": "02:00:00:00:00:01", "ip": "10.20.0.50"}],
			"dhcp_options": [{"code": 15, "format": "string", "value": "lab1.test"}]
		}`,
		VNFTypeDHCPv6: `{
			"gateway": "fd00::1/64",
			"range_start": "fd00::100",
			"range_end": "fd00::200",
			"lease_time": "3",
			"transport": "tap",
			"dhcp_options": [{"code": 23, "format": "ipv6", "value": "fd00::2"}]
		}`,
		VNFTypeNAT44: `{
			"gateway": "10.30.0.1/24",
			"outside_interface": "eth0",
			"outside_address": "192.0.2.10/24",
			"outside_gateway": "192.0.2.1",
			"nat44_pool": "192.0.2.100-192.0.2.200",
			"dnat_rules": [{"protocol": "tcp", "external_port": 80, "local_ip": "10.30.0.10", "local_port": 8080}]
		}`,
		VNFTypeBGP: `{
			"transport": "tap",
			"local_as": 65000,
			"router_id": "10.40.0.1",
			"peer_as": 65001,
			"peer_ip": "10.40.0.2",
			"local_ip": "10.40.0.1/24",
			"multihop_ttl": 64,
			"password": "s3cret",
			"passive": false,
			"target_table_id": 200,
			"advertise_prefixes": ["10.50.0.0/24", "10.60.0.0/24"]
		}`,
	}
}

// TestMigration0061MovesTypedVNFs opens a schema-60 fixture with one VNF of
// every type and every child shape, runs the migration, and checks that the
// identity, scalar config, and child rows survive with vnfs.config gone.
func TestMigration0061MovesTypedVNFs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vnf-61.db")
	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 60)

	insertLegacyNetwork(t, db, 1, "lab")
	configs := legacyVNFConfigs()
	// DNS first so the DHCP dns_vnf_id foreign keys can resolve.
	insertLegacyVNF(t, db, 1, 1, "lab-dns", VNFTypeDNS, configs[VNFTypeDNS])
	insertLegacyVNF(t, db, 2, 1, "lab-dhcpv4", VNFTypeDHCPv4, configs[VNFTypeDHCPv4])
	insertLegacyVNF(t, db, 3, 1, "lab-dhcpv6", VNFTypeDHCPv6, configs[VNFTypeDHCPv6])
	insertLegacyVNF(t, db, 4, 1, "lab-nat44", VNFTypeNAT44, configs[VNFTypeNAT44])
	insertLegacyVNF(t, db, 5, 1, "lab-bgp", VNFTypeBGP, configs[VNFTypeBGP])
	closeRawDB(t, db)

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	var version int
	if err := st.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 61 {
		t.Fatalf("schema version = %d, want 61", version)
	}
	if schemaTableHasColumn(ctx, st.db, "vnfs", "config") {
		t.Fatal("vnfs.config still exists after migration")
	}

	// The shared identity row is unchanged.
	vnf, err := st.GetVNF(ctx, 2)
	if err != nil {
		t.Fatalf("get migrated dhcpv4 vnf: %v", err)
	}
	if vnf.Name != "lab-dhcpv4" || vnf.Type != VNFTypeDHCPv4 || !vnf.Enabled ||
		vnf.TapSwIfIndex != 7 || vnf.TapHostName != "vnf-tap" ||
		vnf.MemifSwIfIndex != 9 || vnf.MemifSocket != "/run/rookery/memif/9.sock" ||
		vnf.MemifHostName != "lab-dhcpv4" {
		t.Fatalf("migrated identity = %+v", vnf)
	}

	dhcp, err := st.GetDHCPVNF(ctx, 2)
	if err != nil {
		t.Fatalf("get typed dhcpv4 vnf: %v", err)
	}
	if dhcp.Family != VNFTypeDHCPv4 || dhcp.Config.Gateway != "10.20.0.1/24" ||
		dhcp.Config.RangeStart != "10.20.0.100" || dhcp.Config.RangeEnd != "10.20.0.200" ||
		dhcp.Config.DNS != "10.20.0.2" || dhcp.Config.LeaseTime != "1h3" ||
		dhcp.Config.BindIP != "10.20.0.2/24" || dhcp.Config.Transport != "dedicated" ||
		dhcp.Config.DNSVNFID != 1 || dhcp.Config.DNSDomain != "lab1.test" ||
		dhcp.Config.DNSTTL != 600 {
		t.Fatalf("migrated dhcpv4 config = %+v", dhcp)
	}
	if dhcp.StaticAssignmentCount != 1 || dhcp.OptionCount != 1 {
		t.Fatalf("migrated dhcpv4 counts = %+v", dhcp)
	}
	assignments, err := st.ListDHCPStaticAssignments(ctx, 2)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("migrated static assignments = %+v, err = %v", assignments, err)
	}
	if assignments[0].MAC != "02:00:00:00:00:01" || assignments[0].IP != "10.20.0.50" || assignments[0].ID < 1 {
		t.Fatalf("migrated static assignment = %+v", assignments[0])
	}
	options, err := st.ListDHCPOptions(ctx, 2)
	if err != nil || len(options) != 1 || options[0].Code != 15 || options[0].ID < 1 {
		t.Fatalf("migrated dhcp options = %+v, err = %v", options, err)
	}

	// The migrated DHCPv4 config hydrates with its children.
	if len(vnf.Config.StaticAssignments) != 1 || len(vnf.Config.DHCPOptions) != 1 ||
		vnf.Config.StaticAssignments[0].ID < 1 || vnf.Config.DHCPOptions[0].ID < 1 {
		t.Fatalf("hydrated dhcpv4 config = %+v", vnf.Config)
	}

	dhcp6, err := st.GetDHCPVNF(ctx, 3)
	if err != nil {
		t.Fatalf("get typed dhcpv6 vnf: %v", err)
	}
	if dhcp6.Family != VNFTypeDHCPv6 || dhcp6.Config.LeaseTime != "3" ||
		dhcp6.Config.Transport != "tap" || dhcp6.OptionCount != 1 || dhcp6.StaticAssignmentCount != 0 {
		t.Fatalf("migrated dhcpv6 config = %+v", dhcp6)
	}

	dns, err := st.GetDNSVNF(ctx, 1)
	if err != nil {
		t.Fatalf("get typed dns vnf: %v", err)
	}
	if !dns.Config.Forward || dns.RecordCount != 2 || dns.ForwarderCount != 2 {
		t.Fatalf("migrated dns vnf = %+v", dns)
	}
	records, err := st.ListDNSVNFRecords(ctx, 1)
	if err != nil || len(records) != 2 {
		t.Fatalf("migrated dns records = %+v, err = %v", records, err)
	}
	if records[0].Name != "host.lab1.test" || records[0].Type != "A" ||
		records[0].Value != "10.20.0.50" || records[0].TTL != 300 || records[0].ID < 1 {
		t.Fatalf("migrated dns record = %+v", records[0])
	}
	if records[1].TTL != 600 {
		t.Fatalf("migrated dns wildcard record = %+v", records[1])
	}
	forwarders, err := st.ListDNSVNFForwarders(ctx, 1)
	if err != nil || len(forwarders) != 2 {
		t.Fatalf("migrated dns forwarders = %+v, err = %v", forwarders, err)
	}
	if forwarders[0].Address != "1.1.1.1" || forwarders[0].Position != 0 ||
		forwarders[1].Address != "9.9.9.9:5353" || forwarders[1].Position != 1 {
		t.Fatalf("migrated dns forwarder order = %+v", forwarders)
	}

	nat44, err := st.GetNAT44VNF(ctx, 4)
	if err != nil {
		t.Fatalf("get typed nat44 vnf: %v", err)
	}
	if nat44.Config.Gateway != "10.30.0.1/24" || nat44.Config.OutsideInterface != "eth0" ||
		nat44.Config.OutsideAddress != "192.0.2.10/24" || nat44.Config.OutsideGateway != "192.0.2.1" ||
		nat44.Config.NAT44Pool != "192.0.2.100-192.0.2.200" || nat44.DNATRuleCount != 1 {
		t.Fatalf("migrated nat44 vnf = %+v", nat44)
	}
	rules, err := st.ListNAT44DNATRules(ctx, 4)
	if err != nil || len(rules) != 1 || rules[0].Protocol != "tcp" || rules[0].ExternalPort != 80 ||
		rules[0].LocalIP != "10.30.0.10" || rules[0].LocalPort != 8080 {
		t.Fatalf("migrated dnat rules = %+v, err = %v", rules, err)
	}

	bgp, err := st.GetBGPVNF(ctx, 5)
	if err != nil {
		t.Fatalf("get typed bgp vnf: %v", err)
	}
	if bgp.Config.Transport != "tap" || bgp.Config.LocalAS != 65000 || bgp.Config.PeerAS != 65001 ||
		bgp.Config.RouterID != "10.40.0.1" || bgp.Config.PeerIP != "10.40.0.2" ||
		bgp.Config.LocalIP != "10.40.0.1/24" || bgp.Config.MultihopTTL != 64 ||
		bgp.Config.TargetTableID != 200 || bgp.AdvertisedPrefixCount != 2 {
		t.Fatalf("migrated bgp vnf = %+v", bgp)
	}
	// The read model never exposes the password, but runtime hydration keeps it.
	if bgp.Config.Password != "" {
		t.Fatalf("read model exposed the bgp password: %+v", bgp.Config)
	}
	if vnf.Config.Password != "" {
		t.Fatalf("dhcp hydration unexpectedly has a bgp password")
	}
	prefixes, err := st.ListBGPVNFAdvertisedPrefixes(ctx, 5)
	if err != nil || len(prefixes) != 2 || prefixes[0].Prefix != "10.50.0.0/24" {
		t.Fatalf("migrated advertised prefixes = %+v, err = %v", prefixes, err)
	}
	hydrated, err := st.GetVNF(ctx, 5)
	if err != nil {
		t.Fatalf("get hydrated bgp vnf: %v", err)
	}
	if hydrated.Config.Password != "s3cret" {
		t.Fatalf("runtime hydration lost the bgp password: %+v", hydrated.Config)
	}

	// Labels keep working on the shared identity.
	if _, err := st.SetEntityLabels(ctx, "vnf", "2", []string{"role=dhcp"}); err != nil {
		t.Fatalf("set vnf labels: %v", err)
	}
	if err := st.DeleteVNF(ctx, 2); err != nil {
		t.Fatalf("delete vnf: %v", err)
	}
	labels, err := st.GetEntityLabels(ctx, "vnf", "2")
	if err != nil {
		t.Fatalf("get labels: %v", err)
	}
	if len(labels.Labels) != 0 {
		t.Fatalf("labels survived vnf delete: %+v", labels)
	}
	// The typed child rows cascade with the identity row.
	if _, err := st.ListDHCPOptions(ctx, 2); err == nil {
		t.Fatal("dhcp options survived vnf delete")
	}
}

// TestMigration0061KeepsLegacyLeaseTimes proves the compact lease-time values
// the old normalizer stored are copied verbatim.
func TestMigration0061KeepsLegacyLeaseTimes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vnf-lease-times.db")
	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 60)
	for i, leaseTime := range []string{"1h3", "3", "1h1", "2h2"} {
		id := int64(i + 1)
		insertLegacyNetwork(t, db, id, "lab-"+strconv.FormatInt(id, 10))
		insertLegacyVNF(t, db, id, id, "dhcp-"+strconv.FormatInt(id, 10), VNFTypeDHCPv4, `{
			"gateway": "10.20.0.1/24",
			"range_start": "10.20.0.100",
			"range_end": "10.20.0.200",
			"lease_time": "`+leaseTime+`"
		}`)
	}
	closeRawDB(t, db)

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer st.Close()
	ctx := context.Background()
	for i, leaseTime := range []string{"1h3", "3", "1h1", "2h2"} {
		vnf, err := st.GetVNF(ctx, int64(i+1))
		if err != nil {
			t.Fatalf("get vnf %d: %v", i+1, err)
		}
		if vnf.Config.LeaseTime != leaseTime {
			t.Fatalf("vnf %d lease time = %q, want %q", i+1, vnf.Config.LeaseTime, leaseTime)
		}
	}
}

// openPreflightFixture builds a schema-60 database with one VNF and returns the
// database path.
func openPreflightFixture(t *testing.T, vnfType, config string) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "vnf-preflight.db")
	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 60)
	insertLegacyNetwork(t, db, 1, "lab")
	insertLegacyVNF(t, db, 1, 1, "lab-vnf", vnfType, config)
	closeRawDB(t, db)
	return dbPath
}

// assertPreflightRejected opens the database, requires a preflight error that
// mentions want, and proves user_version was not advanced.
func assertPreflightRejected(t *testing.T, dbPath, want string) {
	t.Helper()
	st, err := Open(dbPath)
	if err == nil {
		st.Close()
		t.Fatal("Open succeeded, want a preflight failure")
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("preflight error = %q, want it to mention %q", err, want)
	}
	db := openRawDB(t, dbPath)
	defer closeRawDB(t, db)
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 60 {
		t.Fatalf("schema version = %d, want 60 after a failed preflight", version)
	}
}

// TestVNFPreflightRejectsProblems checks the read-only preflight reports each
// class of data the typed schema cannot store.
func TestVNFPreflightRejectsProblems(t *testing.T) {
	t.Run("duplicate MAC", func(t *testing.T) {
		path := openPreflightFixture(t, VNFTypeDHCPv4, `{
			"gateway": "10.20.0.1/24", "range_start": "10.20.0.100", "range_end": "10.20.0.200",
			"static_assignments": [
				{"mac": "02:00:00:00:00:01", "ip": "10.20.0.50"},
				{"mac": "02:00:00:00:00:01", "ip": "10.20.0.51"}
			]}`)
		assertPreflightRejected(t, path, "duplicates entry")
	})

	t.Run("bad dns reference", func(t *testing.T) {
		path := openPreflightFixture(t, VNFTypeDHCPv4, `{
			"gateway": "10.20.0.1/24", "range_start": "10.20.0.100", "range_end": "10.20.0.200",
			"dns_vnf_id": 99}`)
		assertPreflightRejected(t, path, "dns_vnf_id")
	})

	t.Run("unknown type", func(t *testing.T) {
		path := openPreflightFixture(t, "wireguard", `{}`)
		assertPreflightRejected(t, path, "unsupported type")
	})

	t.Run("bad dns record type", func(t *testing.T) {
		path := openPreflightFixture(t, VNFTypeDNS, `{
			"dns_records": [{"name": "a.lab", "type": "SRV", "value": "x", "ttl": 60}]}`)
		assertPreflightRejected(t, path, "not A, AAAA, CNAME, TXT, or PTR")
	})

	t.Run("bad child shape", func(t *testing.T) {
		path := openPreflightFixture(t, VNFTypeDHCPv4, `{
			"gateway": "10.20.0.1/24", "range_start": "10.20.0.100", "range_end": "10.20.0.200",
			"static_assignments": {"mac": "02:00:00:00:00:01", "ip": "10.20.0.50"}}`)
		assertPreflightRejected(t, path, "unexpected field type")
	})

	t.Run("duplicate network type", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "vnf-duplicate-type.db")
		db := openRawDB(t, dbPath)
		migrateToVersion(t, db, 60)
		insertLegacyNetwork(t, db, 1, "lab")
		insertLegacyVNF(t, db, 1, 1, "lab-dhcp-a", VNFTypeDHCPv4, `{
			"gateway": "10.20.0.1/24", "range_start": "10.20.0.100", "range_end": "10.20.0.200"}`)
		insertLegacyVNF(t, db, 2, 1, "lab-dhcp-b", VNFTypeDHCPv4, `{
			"gateway": "10.20.0.1/24", "range_start": "10.20.0.100", "range_end": "10.20.0.200"}`)
		closeRawDB(t, db)
		assertPreflightRejected(t, dbPath, "already has a dhcpv4 VNF")
	})
}

// TestVNFPreflightRejectsPreReleaseStamp proves a database stamped as version
// 61 without the typed tables fails with a repair message instead of starting.
func TestVNFPreflightRejectsPreReleaseStamp(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vnf-pre-release.db")
	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 60)
	execSQL(t, db, `PRAGMA user_version = 61`)
	closeRawDB(t, db)

	st, err := Open(dbPath)
	if err == nil {
		st.Close()
		t.Fatal("Open succeeded, want a pre-release stamp failure")
	}
	if !strings.Contains(err.Error(), "pre-release 0061") {
		t.Fatalf("preflight error = %q, want a pre-release 0061 repair message", err)
	}
}
