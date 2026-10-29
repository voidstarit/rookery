package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMigrationConvertsNetworkDHCPToVNF(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-dhcp.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("create current database: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close current database: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE vnfs`); err != nil {
		t.Fatalf("drop vnfs table: %v", err)
	}
	for _, column := range []string{
		"bvi_host_name", "bvi_sw_if_index", "ipv4_gateway",
		"vnf_transport", "vnf_memif_socket", "vnf_memif_sw_if_index", "vnf_memif_host_name",
		"ipv4_cidr", "ipv6_cidr", "route_table_id",
	} {
		if _, err := db.Exec(`ALTER TABLE networks DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop networks.%s: %v", column, err)
		}
	}
	if _, err := db.Exec(`DROP TABLE startup_script`); err != nil {
		t.Fatalf("drop startup_script table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE vpp_interface_addresses`); err != nil {
		t.Fatalf("drop vpp_interface_addresses table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS network_pipes`); err != nil {
		t.Fatalf("drop network_pipes table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS vpp_interface_route_tables`); err != nil {
		t.Fatalf("drop vpp_interface_route_tables table: %v", err)
	}
	for _, column := range []string{
		"route_count", "next_hop_table", "weight", "udp_encap",
		"ipv4_lookup_table", "ipv6_lookup_table", "mpls_lookup_table",
		"resolve_via_host", "resolve_via_connected",
		"rx_ipv4_interface", "rx_ipv6_interface", "out_labels", "is_drop",
		"description", "preference_set", "route_count_set", "next_hop_table_set",
		"weight_set", "udp_encap_set", "ipv4_lookup_table_set",
		"ipv6_lookup_table_set", "mpls_lookup_table_set",
		"next_hop_table_name", "ipv4_lookup_table_name", "ipv6_lookup_table_name",
	} {
		if _, err := db.Exec(`ALTER TABLE routes DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop routes.%s: %v", column, err)
		}
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS vpp_interface_dhcp_clients`); err != nil {
		t.Fatalf("drop vpp_interface_dhcp_clients table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE acls`); err != nil {
		t.Fatalf("drop acls table: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE disks DROP COLUMN storage_backend_id`); err != nil {
		t.Fatalf("drop disks.storage_backend_id: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE storage_backends`); err != nil {
		t.Fatalf("drop storage_backends table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE vm_disks`); err != nil {
		t.Fatalf("drop vm_disks table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE interface_acls`); err != nil {
		t.Fatalf("drop interface_acls table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE routes`); err != nil {
		t.Fatalf("drop routes table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE route_tables`); err != nil {
		t.Fatalf("drop route_tables table: %v", err)
	}
	for _, column := range []string{"ipv4_table_id", "ipv6_table_id"} {
		if _, err := db.Exec(`ALTER TABLE virtual_interfaces DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop virtual_interfaces.%s: %v", column, err)
		}
	}
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_disks_template_disk_id`); err != nil {
		t.Fatalf("drop disk template index: %v", err)
	}
	for _, column := range []string{"is_template", "template_disk_id", "prepared_by_script", "provisioning_script", "provisioning_timeout"} {
		if _, err := db.Exec(`ALTER TABLE disks DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop disks.%s: %v", column, err)
		}
	}
	for _, column := range []string{"ingress_acl_id", "egress_acl_id"} {
		if _, err := db.Exec(`ALTER TABLE virtual_interfaces DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop virtual_interfaces.%s: %v", column, err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE vms DROP COLUMN desired_state`); err != nil {
		t.Fatalf("drop vms.desired_state: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO networks
			(name, bridge_domain_id, dhcp_enabled, dhcp_gateway,
			 dhcp_range_start, dhcp_range_end, dhcp_dns,
			 dhcp_tap_sw_if_index, dhcp_tap_host_name, created_at, updated_at)
		VALUES ('legacy-net', 100, 1, '10.20.0.1/24', '10.20.0.100', '10.20.0.200',
		        '1.1.1.1', 3, 'ndn1', 1000, 2000)`); err != nil {
		t.Fatalf("insert legacy network: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 7`); err != nil {
		t.Fatalf("set schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	st, err = Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	vnfs, err := st.ListVNFs(ctx, nil)
	if err != nil {
		t.Fatalf("list migrated vnfs: %v", err)
	}
	if len(vnfs) != 1 {
		t.Fatalf("migrated vnfs = %+v, want one", vnfs)
	}
	vnf := vnfs[0]
	if vnf.Name != "dhcpv4-1" || vnf.Type != VNFTypeDHCPv4 || !vnf.Enabled ||
		vnf.Config.Gateway != "10.20.0.1/24" || vnf.Config.RangeStart != "10.20.0.100" ||
		vnf.Config.RangeEnd != "10.20.0.200" || vnf.Config.DNS != "1.1.1.1" ||
		vnf.Config.LeaseTime != "12h" || vnf.TapSwIfIndex != 0 || vnf.TapHostName != "" {
		t.Fatalf("migrated vnf = %+v", vnf)
	}
}

func TestMigrationBackfillsVMDisks(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "legacy-vm-disk.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("create current database: %v", err)
	}
	disk, err := st.CreateDisk(ctx, Disk{
		Name:     "legacy-root",
		SizeGB:   20,
		VG:       "vg0",
		ThinPool: "rookery",
	})
	if err != nil {
		t.Fatalf("create legacy disk: %v", err)
	}
	vm, err := st.CreateVM(ctx, VM{
		Name:     "legacy",
		Status:   StatusStopped,
		VCPUs:    1,
		MemoryMB: 512,
		DiskID:   &disk.ID,
	})
	if err != nil {
		t.Fatalf("create legacy vm: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close current database: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE vm_disks`); err != nil {
		t.Fatalf("drop vm_disks table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE interface_acls`); err != nil {
		t.Fatalf("drop interface_acls table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE routes`); err != nil {
		t.Fatalf("drop routes table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE route_tables`); err != nil {
		t.Fatalf("drop route_tables table: %v", err)
	}
	for _, column := range []string{"ipv4_table_id", "ipv6_table_id"} {
		if _, err := db.Exec(`ALTER TABLE virtual_interfaces DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop virtual_interfaces.%s: %v", column, err)
		}
	}
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_disks_template_disk_id`); err != nil {
		t.Fatalf("drop disk template index: %v", err)
	}
	for _, column := range []string{"is_template", "template_disk_id", "prepared_by_script", "provisioning_script", "provisioning_timeout"} {
		if _, err := db.Exec(`ALTER TABLE disks DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop disks.%s: %v", column, err)
		}
	}
	for _, column := range []string{"memif_sw_if_index", "memif_socket", "memif_host_name"} {
		if _, err := db.Exec(`ALTER TABLE vnfs DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop vnfs.%s: %v", column, err)
		}
	}
	for _, column := range []string{
		"vnf_transport", "vnf_memif_socket", "vnf_memif_sw_if_index", "vnf_memif_host_name",
		"ipv4_cidr", "ipv6_cidr", "route_table_id",
	} {
		if _, err := db.Exec(`ALTER TABLE networks DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop networks.%s: %v", column, err)
		}
	}
	if _, err := db.Exec(`DROP TABLE startup_script`); err != nil {
		t.Fatalf("drop startup_script table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE vpp_interface_addresses`); err != nil {
		t.Fatalf("drop vpp_interface_addresses table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS network_pipes`); err != nil {
		t.Fatalf("drop network_pipes table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS vpp_interface_route_tables`); err != nil {
		t.Fatalf("drop vpp_interface_route_tables table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS vpp_interface_dhcp_clients`); err != nil {
		t.Fatalf("drop vpp_interface_dhcp_clients table: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE vms DROP COLUMN desired_state`); err != nil {
		t.Fatalf("drop vms.desired_state: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 11`); err != nil {
		t.Fatalf("set schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	st, err = Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer st.Close()

	got, err := st.GetVM(ctx, vm.ID)
	if err != nil {
		t.Fatalf("get migrated vm: %v", err)
	}
	if len(got.Disks) != 1 || got.Disks[0].ID != disk.ID || len(got.DiskIDs) != 1 || got.DiskIDs[0] != disk.ID {
		t.Fatalf("migrated vm disks = %+v, ids = %v", got.Disks, got.DiskIDs)
	}
	gotDisk, err := st.GetDisk(ctx, disk.ID)
	if err != nil {
		t.Fatalf("get migrated disk: %v", err)
	}
	if gotDisk.AttachedVMID == nil || *gotDisk.AttachedVMID != vm.ID {
		t.Fatalf("migrated disk attached to %v, want %s", gotDisk.AttachedVMID, vm.ID)
	}
}

func TestMigrationPreservesLegacyVMsWithoutDisks(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}

	initSQL, err := migrations.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatalf("read initial migration: %v", err)
	}
	if _, err := db.Exec(string(initSQL)); err != nil {
		t.Fatalf("apply initial schema: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatalf("set schema version: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO vms (name, description, status, vcpus, memory_mb, disk_gb, created_at, updated_at)
		VALUES ('legacy', 'old vm', 'stopped', 2, 1024, 42, 1000, 2000)`); err != nil {
		t.Fatalf("insert legacy vm: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	var vmID string
	if err := st.db.QueryRowContext(ctx, `SELECT id FROM vms WHERE name = 'legacy'`).Scan(&vmID); err != nil {
		t.Fatalf("read migrated vm id: %v", err)
	}
	if _, err := uuid.Parse(vmID); err != nil {
		t.Fatalf("migrated vm id = %q, want UUID: %v", vmID, err)
	}
	vm, err := st.GetVM(ctx, vmID)
	if err != nil {
		t.Fatalf("get migrated vm: %v", err)
	}
	if vm.Name != "legacy" || vm.DiskID != nil || vm.Disk != nil || vm.BootMediaID != nil || vm.BootMedia != nil {
		t.Fatalf("migrated vm = %+v", vm)
	}
	if !vm.ConsoleEnabled {
		t.Fatal("existing VM lost console access during migration")
	}
	if media, err := st.ListMedia(ctx); err != nil || len(media) != 0 {
		t.Fatalf("migrated media = %+v, err = %v", media, err)
	}
	if networks, err := st.ListNetworks(ctx); err != nil || len(networks) != 0 {
		t.Fatalf("migrated networks = %+v, err = %v", networks, err)
	}
	if interfaces, err := st.ListInterfaces(ctx); err != nil || len(interfaces) != 0 {
		t.Fatalf("migrated interfaces = %+v, err = %v", interfaces, err)
	}
	if vnfs, err := st.ListVNFs(ctx, nil); err != nil || len(vnfs) != 0 {
		t.Fatalf("migrated vnfs = %+v, err = %v", vnfs, err)
	}

	var version int
	if err := st.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 38 {
		t.Fatalf("schema version = %d, want 37", version)
	}
}

func TestEnsureStartupScriptSchemaAfterTableDrop(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "version-27.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("create current database: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close current database: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE startup_script`); err != nil {
		t.Fatalf("drop startup_script table: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE vms DROP COLUMN desired_state`); err != nil {
		t.Fatalf("drop vms.desired_state: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 37`); err != nil {
		t.Fatalf("set schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	st, err = Open(dbPath)
	if err != nil {
		t.Fatalf("repair database without startup_script: %v", err)
	}
	defer st.Close()
	script, err := st.GetStartupScript(context.Background())
	if err != nil {
		t.Fatalf("get startup script after migration: %v", err)
	}
	if script.Enabled || script.Status != "disabled" || strings.TrimSpace(script.Script) != "" {
		t.Fatalf("rookery startup script = %+v, want empty/disabled", script)
	}
	vppScript, err := st.GetVPPStartupScript(context.Background())
	if err != nil {
		t.Fatalf("get VPP startup script after migration: %v", err)
	}
	if !vppScript.Enabled || vppScript.Status != "pending" {
		t.Fatalf("VPP startup script = %+v, want enabled/pending", vppScript)
	}
	if strings.TrimSpace(vppScript.Script) != "# Assign NAT44 workers\nvppctl set nat workers 0" {
		t.Fatalf("VPP startup script = %q, want default NAT44 command", vppScript.Script)
	}
}

func TestEnsureVPPInterfaceAddressesSchemaAfterTableDrop(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "addresses-version-30.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("create current database: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close current database: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE vpp_interface_addresses`); err != nil {
		t.Fatalf("drop vpp_interface_addresses table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS network_pipes`); err != nil {
		t.Fatalf("drop network_pipes table: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS vpp_interface_route_tables`); err != nil {
		t.Fatalf("drop vpp_interface_route_tables table: %v", err)
	}
	for _, column := range []string{
		"route_count", "next_hop_table", "weight", "udp_encap",
		"ipv4_lookup_table", "ipv6_lookup_table", "mpls_lookup_table",
		"resolve_via_host", "resolve_via_connected",
		"rx_ipv4_interface", "rx_ipv6_interface", "out_labels", "is_drop",
		"description", "preference_set", "route_count_set", "next_hop_table_set",
		"weight_set", "udp_encap_set", "ipv4_lookup_table_set",
		"ipv6_lookup_table_set", "mpls_lookup_table_set",
		"next_hop_table_name", "ipv4_lookup_table_name", "ipv6_lookup_table_name",
	} {
		if _, err := db.Exec(`ALTER TABLE routes DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop routes.%s: %v", column, err)
		}
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS vpp_interface_dhcp_clients`); err != nil {
		t.Fatalf("drop vpp_interface_dhcp_clients table: %v", err)
	}
	for _, column := range []string{"provisioning_script", "provisioning_timeout"} {
		if _, err := db.Exec(`ALTER TABLE disks DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop disks.%s: %v", column, err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE vms DROP COLUMN desired_state`); err != nil {
		t.Fatalf("drop vms.desired_state: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 30`); err != nil {
		t.Fatalf("set schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	st, err = Open(dbPath)
	if err != nil {
		t.Fatalf("repair database without vpp_interface_addresses: %v", err)
	}
	defer st.Close()
	if err := st.ReplaceVPPInterfaceAddresses(context.Background(), "vhost-test", []string{"10.20.0.10/24"}); err != nil {
		t.Fatalf("use repaired address table: %v", err)
	}
	addresses, err := st.ListVPPInterfaceAddresses(context.Background(), "vhost-test")
	if err != nil {
		t.Fatalf("list repaired addresses: %v", err)
	}
	if len(addresses) != 1 || addresses[0] != "10.20.0.10/24" {
		t.Fatalf("addresses = %v", addresses)
	}
}

func TestMigrationBackfillsVMPowerState(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "legacy-power.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("create current database: %v", err)
	}
	runningDisk, err := st.CreateDisk(ctx, Disk{Name: "legacy-running", SizeGB: 20, VG: "vg0", ThinPool: "rookery"})
	if err != nil {
		t.Fatalf("create running disk: %v", err)
	}
	stoppedDisk, err := st.CreateDisk(ctx, Disk{Name: "legacy-stopped", SizeGB: 20, VG: "vg0", ThinPool: "rookery"})
	if err != nil {
		t.Fatalf("create stopped disk: %v", err)
	}
	running, err := st.CreateVM(ctx, VM{Name: "was-running", Status: StatusRunning, VCPUs: 1, MemoryMB: 512, DiskID: &runningDisk.ID})
	if err != nil {
		t.Fatalf("create running vm: %v", err)
	}
	stopped, err := st.CreateVM(ctx, VM{Name: "was-stopped", Status: StatusStopped, VCPUs: 1, MemoryMB: 512, DiskID: &stoppedDisk.ID})
	if err != nil {
		t.Fatalf("create stopped vm: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close current database: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE vms DROP COLUMN desired_state`); err != nil {
		t.Fatalf("drop vms.desired_state: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 37`); err != nil {
		t.Fatalf("set schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	st, err = Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer st.Close()

	gotRunning, err := st.GetVM(ctx, running.ID)
	if err != nil {
		t.Fatalf("get migrated running vm: %v", err)
	}
	if gotRunning.DesiredState != DesiredRunning {
		t.Fatalf("running vm desired state = %q, want running", gotRunning.DesiredState)
	}
	gotStopped, err := st.GetVM(ctx, stopped.ID)
	if err != nil {
		t.Fatalf("get migrated stopped vm: %v", err)
	}
	if gotStopped.DesiredState != DesiredStopped {
		t.Fatalf("stopped vm desired state = %q, want stopped", gotStopped.DesiredState)
	}
}
