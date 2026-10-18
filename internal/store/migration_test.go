package store

import (
	"context"
	"database/sql"
	"path/filepath"
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
	for _, column := range []string{"bvi_host_name", "bvi_sw_if_index", "ipv4_gateway"} {
		if _, err := db.Exec(`ALTER TABLE networks DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop networks.%s: %v", column, err)
		}
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
	for _, column := range []string{"ingress_acl_id", "egress_acl_id"} {
		if _, err := db.Exec(`ALTER TABLE virtual_interfaces DROP COLUMN ` + column); err != nil {
			t.Fatalf("drop virtual_interfaces.%s: %v", column, err)
		}
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
		ThinPool: "nerds-vms",
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
	if version != 12 {
		t.Fatalf("schema version = %d, want 12", version)
	}
}
