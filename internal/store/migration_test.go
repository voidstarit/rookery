package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMigrationConvertsNetworkDHCPToVNF(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-dhcp.db")
	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 7)
	execSQL(t, db, `
		INSERT INTO networks
			(name, bridge_domain_id, dhcp_enabled, dhcp_gateway,
			 dhcp_range_start, dhcp_range_end, dhcp_dns,
			 dhcp_tap_sw_if_index, dhcp_tap_host_name, created_at, updated_at)
		VALUES ('legacy-net', 100, 1, '10.20.0.1/24', '10.20.0.100', '10.20.0.200',
		        '1.1.1.1', 3, 'ndn1', 1000, 2000)`)
	closeRawDB(t, db)

	st, err := Open(dbPath)
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
	const (
		diskID = int64(1)
		vmID   = "11111111-1111-4111-8111-111111111111"
	)

	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 11)
	execSQL(t, db, `
		INSERT INTO disks
			(id, name, size_gb, vg, thin_pool, created_at, updated_at,
			 storage_backend_id)
		VALUES (1, 'legacy-root', 20, 'vg0', 'rookery', 1000, 1000, NULL)`)
	execSQL(t, db, `
		INSERT INTO vms
			(id, name, description, status, vcpus, memory_mb, disk_id, boot_media_id,
			 created_at, updated_at, console_enabled)
		VALUES ('11111111-1111-4111-8111-111111111111', 'legacy', '', 'stopped',
		        1, 512, 1, NULL, 1000, 1000, 1)`)
	closeRawDB(t, db)

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer st.Close()

	got, err := st.GetVM(ctx, vmID)
	if err != nil {
		t.Fatalf("get migrated vm: %v", err)
	}
	if len(got.Disks) != 1 || got.Disks[0].ID != diskID || len(got.DiskIDs) != 1 || got.DiskIDs[0] != diskID {
		t.Fatalf("migrated vm disks = %+v, ids = %v", got.Disks, got.DiskIDs)
	}
	gotDisk, err := st.GetDisk(ctx, diskID)
	if err != nil {
		t.Fatalf("get migrated disk: %v", err)
	}
	if gotDisk.AttachedVMID == nil || *gotDisk.AttachedVMID != vmID {
		t.Fatalf("migrated disk attached to %v, want %s", gotDisk.AttachedVMID, vmID)
	}
}

func TestMigrationWorkloadDisks(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "workload-disks.db")
	const (
		vm1 = "11111111-1111-4111-8111-111111111111"
		vm2 = "22222222-2222-4222-8222-222222222222"
		vm3 = "33333333-3333-4333-8333-333333333333"
		vm4 = "44444444-4444-4444-8444-444444444444"
		vm5 = "55555555-5555-4555-8555-555555555555"
		vm6 = "66666666-6666-4666-8666-666666666666"
		vm7 = "77777777-7777-4777-8777-777777777777"
	)

	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 52)
	execSQL(t, db, `
		INSERT INTO disks (id, name, size_gb, vg, thin_pool, created_at, updated_at)
		VALUES (1, 'disk-a', 10, 'vg0', 'pool', 1, 1),
		       (2, 'disk-b', 10, 'vg0', 'pool', 1, 1),
		       (3, 'disk-c', 10, 'vg0', 'pool', 1, 1),
		       (4, 'disk-d', 10, 'vg0', 'pool', 1, 1),
		       (5, 'disk-e', 10, 'vg0', 'pool', 1, 1),
		       (6, 'disk-f', 10, 'vg0', 'pool', 1, 1),
		       (7, 'disk-h', 10, 'vg0', 'pool', 1, 1),
		       (8, 'disk-i', 10, 'vg0', 'pool', 1, 1)`)
	execSQL(t, db, `PRAGMA foreign_keys = OFF`)
	execSQL(t, db, `
		INSERT INTO vms (id, name, status, vcpus, memory_mb, disk_id, created_at, updated_at)
		VALUES ('`+vm1+`', 'vm1', 'stopped', 1, 512, 6, 1, 1),
		       ('`+vm2+`', 'vm2', 'stopped', 1, 512, NULL, 1, 1),
		       ('`+vm3+`', 'vm3', 'stopped', 1, 512, NULL, 1, 1),
		       ('`+vm4+`', 'vm4', 'stopped', 1, 512, 2, 1, 1),
		       ('`+vm5+`', 'vm5', 'stopped', 1, 512, 5, 1, 1),
		       ('`+vm6+`', 'vm6', 'stopped', 1, 512, 9, 1, 1),
		       ('`+vm7+`', 'vm7', 'stopped', 1, 512, NULL, 1, 1)`)
	execSQL(t, db, `PRAGMA foreign_keys = ON`)
	execSQL(t, db, `
		INSERT INTO vm_disks (vm_id, disk_id, position)
		VALUES ('`+vm1+`', 1, 5),
		       ('`+vm2+`', 2, 0),
		       ('`+vm3+`', 3, -1),
		       ('`+vm3+`', 4, 0),
		       ('`+vm7+`', 8, 0),
		       ('`+vm7+`', 7, 0)`)
	closeRawDB(t, db)

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer st.Close()

	assertAttachments := func(vmID string, want ...int64) {
		t.Helper()
		vm, err := st.GetVM(ctx, vmID)
		if err != nil {
			t.Fatalf("get %s: %v", vmID, err)
		}
		if len(vm.DiskIDs) != len(want) {
			t.Fatalf("%s disk ids = %v, want %v", vmID, vm.DiskIDs, want)
		}
		for i, diskID := range want {
			if vm.DiskIDs[i] != diskID {
				t.Fatalf("%s disk ids = %v, want %v", vmID, vm.DiskIDs, want)
			}
		}
	}
	assertAttachments(vm1, 1)
	assertAttachments(vm2, 2)
	assertAttachments(vm3, 3, 4)
	assertAttachments(vm4)
	assertAttachments(vm5, 5)
	assertAttachments(vm6)
	assertAttachments(vm7, 7, 8)
	disk2, err := st.GetDisk(ctx, 2)
	if err != nil {
		t.Fatalf("get disk b: %v", err)
	}
	if disk2.AttachedVMID == nil || *disk2.AttachedVMID != vm2 || disk2.AttachedVMName != "vm2" {
		t.Fatalf("disk b owner = %v/%q, want vm2", disk2.AttachedVMID, disk2.AttachedVMName)
	}
	if err := st.DeleteDisk(ctx, 2); !errors.Is(err, ErrDiskInUse) {
		t.Fatalf("delete attached disk error = %v, want ErrDiskInUse", err)
	}

	disk6, err := st.GetDisk(ctx, 6)
	if err != nil {
		t.Fatalf("get disk f: %v", err)
	}
	if disk6.AttachedVMID != nil || disk6.AttachedLightVMID != nil {
		t.Fatalf("disk f owner = %v/%v, want unattached", disk6.AttachedVMID, disk6.AttachedLightVMID)
	}
	if tableExists(t, st.db, "vm_disks") {
		t.Fatal("vm_disks still exists")
	}
	var nonNull int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vms WHERE disk_id IS NOT NULL`).Scan(&nonNull); err != nil {
		t.Fatalf("count legacy disk pointers: %v", err)
	}
	if nonNull != 0 {
		t.Fatalf("legacy disk pointers = %d, want 0", nonNull)
	}
	var generation int64
	if err := st.db.QueryRowContext(ctx, `SELECT lifecycle_generation FROM lightvms LIMIT 1`).Scan(&generation); err != nil && err != sql.ErrNoRows {
		t.Fatalf("read lifecycle_generation: %v", err)
	}

	if err := st.DeleteVM(ctx, vm1); err != nil {
		t.Fatalf("delete vm1: %v", err)
	}
	var rows int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workload_disks WHERE disk_id = 1`).Scan(&rows); err != nil {
		t.Fatalf("count vm1 attachment: %v", err)
	}
	if rows != 0 {
		t.Fatalf("vm1 attachment rows = %d, want 0", rows)
	}
	if _, err := st.GetDisk(ctx, 1); err != nil {
		t.Fatalf("disk a was deleted with vm1: %v", err)
	}

	if _, err := st.db.ExecContext(ctx, `INSERT INTO workload_disks (vm_id, lightvm_id, disk_id, position) VALUES (?, NULL, 3, 2)`, vm2); err == nil {
		t.Fatal("both-owner workload row succeeded")
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO workload_disks (vm_id, lightvm_id, disk_id, position) VALUES (NULL, NULL, 3, 2)`); err == nil {
		t.Fatal("neither-owner workload row succeeded")
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workload_disks WHERE disk_id = 2`).Scan(&rows); err != nil {
		t.Fatalf("count disk b attachment: %v", err)
	}
	if rows != 1 {
		t.Fatalf("disk b attachment rows = %d, want 1", rows)
	}

	execSQL(t, st.db, `
		INSERT INTO lightvm_kernels
			(id, name, description, path, file_name, size_bytes, managed, created_at, updated_at)
		VALUES (100, 'k', '', '/tmp/k', 'k', 1, 0, 1, 1)`)
	execSQL(t, st.db, `
		INSERT INTO lightvm_initramfs
			(id, name, description, path, file_name, size_bytes, managed, created_at, updated_at)
		VALUES (100, 'i', '', '/tmp/i', 'i', 1, 0, 1, 1)`)
	execSQL(t, st.db, `
		INSERT INTO lightvms
			(id, name, description, vcpus, memory_mb, console_enabled, cgroup_class,
			 kernel_id, initramfs_id, image, env, executable_path, executable_args,
			 append, status, desired_state, created_at, updated_at)
		VALUES ('99999999-9999-4999-8999-999999999999', 'lv', '', 1, 128, 0, '',
		        100, 100, 'img', '[]', '/bin/sh', '[]', 'console=hvc0', 'stopped', 'stopped', 1, 1)`)
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO workload_disks (vm_id, lightvm_id, disk_id, position)
		VALUES (NULL, '99999999-9999-4999-8999-999999999999', 6, 1)`); err != nil {
		t.Fatalf("attach lightvm disk: %v", err)
	}
	if err := st.DeleteLightVM(ctx, "99999999-9999-4999-8999-999999999999"); err != nil {
		t.Fatalf("delete lightvm: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workload_disks WHERE disk_id = 6`).Scan(&rows); err != nil {
		t.Fatalf("count lightvm attachment: %v", err)
	}
	if rows != 0 {
		t.Fatalf("lightvm attachment rows = %d, want 0", rows)
	}

	execSQL(t, st.db, `UPDATE vms SET disk_id = 6 WHERE id = '`+vm2+`'`)
	assertAttachments(vm2, 2)
	disk6, err = st.GetDisk(ctx, 6)
	if err != nil {
		t.Fatalf("get disk f after inert pointer: %v", err)
	}
	if disk6.AttachedVMID != nil {
		t.Fatalf("inert vms.disk_id changed disk f owner: %v", disk6.AttachedVMID)
	}
}

func TestMigrationPreservesLegacyVMsWithoutDisks(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 1)
	execSQL(t, db, `
		INSERT INTO vms (name, description, status, vcpus, memory_mb, disk_gb, created_at, updated_at)
		VALUES ('legacy', 'old vm', 'stopped', 2, 1024, 42, 1000, 2000)`)
	closeRawDB(t, db)

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
	if want := latestMigrationVersion(t); version != want {
		t.Fatalf("schema version = %d, want %d", version, want)
	}
}

func TestMigrationAddsDefaultLightVMConsole(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lightvm-consoles.db")
	const vmID = "44444444-4444-4444-8444-444444444444"

	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 46)
	execSQL(t, db, `
		INSERT INTO lightvm_kernels
			(id, name, description, path, file_name, size_bytes, managed, created_at, updated_at)
		VALUES (1, 'vmlinuz', '', '/tmp/vmlinuz', 'vmlinuz', 1, 0, 1000, 1000)`)
	execSQL(t, db, `
		INSERT INTO lightvm_initramfs
			(id, name, description, path, file_name, size_bytes, managed, created_at, updated_at)
		VALUES (1, 'initramfs', '', '/tmp/initramfs', 'initramfs', 1, 0, 1000, 1000)`)
	execSQL(t, db, `
		INSERT INTO lightvms
			(id, name, description, vcpus, memory_mb, console_enabled, cgroup_class,
			 kernel_id, initramfs_id, image, env, executable_path, executable_args,
			 append, status, desired_state, created_at, updated_at)
		VALUES ('44444444-4444-4444-8444-444444444444', 'app-01', '', 1, 128, 1, '',
		        1, 1, 'alpine', '[]', '/bin/sh', '[]', 'console=hvc0 rdinit=/init',
		        'stopped', 'stopped', 1000, 1000)`)
	closeRawDB(t, db)

	migrated, err := Open(dbPath)
	if err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	defer migrated.Close()
	updated, err := migrated.GetLightVM(context.Background(), vmID)
	if err != nil {
		t.Fatalf("get migrated lightvm: %v", err)
	}
	if len(updated.Consoles) != 1 || updated.Consoles[0].Name != "console" ||
		updated.Consoles[0].PortName != "org.example.console" || updated.Consoles[0].Position != 0 {
		t.Fatalf("migrated lightvm consoles = %+v", updated.Consoles)
	}
}

func TestEnsureStartupScriptSchemaAfterTableDrop(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "version-37.db")
	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 37)
	execSQL(t, db, `DROP TABLE startup_script`)
	closeRawDB(t, db)

	st, err := Open(dbPath)
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
	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 30)
	execSQL(t, db, `DROP TABLE vpp_interface_addresses`)
	closeRawDB(t, db)

	st, err := Open(dbPath)
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
	const (
		runningVMID = "22222222-2222-4222-8222-222222222222"
		stoppedVMID = "33333333-3333-4333-8333-333333333333"
	)

	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 37)
	execSQL(t, db, `
		INSERT INTO disks
			(id, name, size_gb, vg, thin_pool, created_at, updated_at,
			 storage_backend_id, is_template, template_disk_id, prepared_by_script,
			 provisioning_script, provisioning_timeout)
		VALUES (1, 'legacy-running', 20, 'vg0', 'rookery', 1000, 1000, NULL, 0, NULL,
		        0, '', ''),
		       (2, 'legacy-stopped', 20, 'vg0', 'rookery', 1000, 1000, NULL, 0, NULL,
		        0, '', '')`)
	execSQL(t, db, `
		INSERT INTO vms
			(id, name, description, status, vcpus, memory_mb, disk_id, boot_media_id,
			 created_at, updated_at, console_enabled)
		VALUES ('22222222-2222-4222-8222-222222222222', 'was-running', '', 'running',
		        1, 512, 1, NULL, 1000, 1000, 1),
		       ('33333333-3333-4333-8333-333333333333', 'was-stopped', '', 'stopped',
		        1, 512, 2, NULL, 1000, 1000, 1)`)
	closeRawDB(t, db)

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer st.Close()

	gotRunning, err := st.GetVM(ctx, runningVMID)
	if err != nil {
		t.Fatalf("get migrated running vm: %v", err)
	}
	if gotRunning.DesiredState != DesiredRunning {
		t.Fatalf("running vm desired state = %q, want running", gotRunning.DesiredState)
	}
	gotStopped, err := st.GetVM(ctx, stoppedVMID)
	if err != nil {
		t.Fatalf("get migrated stopped vm: %v", err)
	}
	if gotStopped.DesiredState != DesiredStopped {
		t.Fatalf("stopped vm desired state = %q, want stopped", gotStopped.DesiredState)
	}
}

func TestMigrationFromVersion50DropsRawBandwidthValues(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-raw.db")
	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 50)
	execSQL(t, db, `
		INSERT INTO disks
			(id, name, size_gb, vg, thin_pool, created_at, updated_at,
			 storage_backend_id, is_template, template_disk_id, prepared_by_script,
			 provisioning_script, provisioning_timeout, source, device, size_bytes,
			 backend_metadata, attachment_protocol, deletion_state, deletion_error,
			 deletion_attempts, deletion_next_attempt_at, deletion_claimed_at,
			 read_bps, write_bps)
		VALUES (1, 'legacy-raw', 20, 'vg0', 'rookery', 1000, 1000, NULL, 0, NULL, 0,
		        '', '', 'lvm-thin', '/dev/vg0/legacy-raw', 21474836480, '', '', '', '',
		        0, 0, 0, 1048576, 524288)`)
	closeRawDB(t, db)

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer st.Close()

	disk, err := st.GetDisk(context.Background(), 1)
	if err != nil {
		t.Fatalf("get migrated disk: %v", err)
	}
	if disk.ThrottleClassID != nil {
		t.Fatalf("migrated disk class = %v, want nil", disk.ThrottleClassID)
	}
	if !disksHasColumn(t, st.db, "throttle_class_id") {
		t.Fatal("disks.throttle_class_id is missing")
	}
	if !tableExists(t, st.db, "disk_throttle_classes") {
		t.Fatal("disk_throttle_classes is missing")
	}
	if disksHasColumn(t, st.db, "read_bps") || disksHasColumn(t, st.db, "write_bps") {
		t.Fatal("raw bandwidth columns should be dropped by 0052")
	}
}

func TestMigration0051KeepsRawColumnsUntil0052(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "version-51.db")
	db := openRawDB(t, dbPath)
	migrateToVersion(t, db, 51)
	if !disksHasColumn(t, db, "throttle_class_id") || !tableExists(t, db, "disk_throttle_classes") {
		t.Fatal("0051 did not add the class schema")
	}
	if !disksHasColumn(t, db, "read_bps") || !disksHasColumn(t, db, "write_bps") {
		t.Fatal("0051 unexpectedly dropped the raw columns")
	}
	closeRawDB(t, db)
}

func migrateToVersion(t *testing.T, db *sql.DB, target int) {
	t.Helper()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			t.Fatalf("apply %q: %v", pragma, err)
		}
	}
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			t.Fatalf("parse migration %s: %v", entry.Name(), err)
		}
		if version <= current || version > target {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatalf("read migration %s: %v", entry.Name(), err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatalf("begin migration %s: %v", entry.Name(), err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			t.Fatalf("apply migration %s: %v", entry.Name(), err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			tx.Rollback()
			t.Fatalf("record migration %s: %v", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit migration %s: %v", entry.Name(), err)
		}
		current = version
	}
}

func latestMigrationVersion(t *testing.T) int {
	t.Helper()
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	latest := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			t.Fatalf("parse migration %s: %v", entry.Name(), err)
		}
		if version > latest {
			latest = version
		}
	}
	return latest
}

func openRawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	return db
}

func closeRawDB(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}
}

func execSQL(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("exec SQL: %v", err)
	}
}

func disksHasColumn(t *testing.T, db *sql.DB, column string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info('disks')`)
	if err != nil {
		t.Fatalf("read disks schema: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notNull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan disks schema: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate disks schema: %v", err)
	}
	return false
}

// tableExists reports whether a table exists in the database.
func tableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("query table %s: %v", table, err)
	}
	return name == table
}
