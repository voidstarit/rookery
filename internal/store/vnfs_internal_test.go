package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// openInternalVNFStore opens a fresh store with one network and one DHCPv4 VNF.
func openInternalVNFStore(t *testing.T) (*Store, VNF) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "internal-vnfs.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	network, err := st.CreateNetwork(ctx, Network{Name: "lab", BridgeDomainID: 100})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	vnf, err := st.CreateDHCPVNF(ctx, NewDHCPVNF{
		Name:      "lab-dhcpv4",
		NetworkID: network.ID,
		Family:    VNFTypeDHCPv4,
		Config: DHCPVNFConfig{
			Gateway:    "10.20.0.1/24",
			RangeStart: "10.20.0.100",
			RangeEnd:   "10.20.0.200",
			LeaseTime:  "12h",
		},
	})
	if err != nil {
		t.Fatalf("create vnf: %v", err)
	}
	return st, vnf
}

// TestVNFTypeTriggersRejectMismatchedParent proves a direct SQL write cannot
// create a typed parent row for a VNF of another type.
func TestVNFTypeTriggersRejectMismatchedParent(t *testing.T) {
	st, vnf := openInternalVNFStore(t)
	ctx := context.Background()
	if _, err := st.db.ExecContext(ctx, `INSERT INTO dns_vnfs (vnf_id, forward) VALUES (?, 0)`, vnf.ID); err == nil {
		t.Fatal("dns_vnfs accepted a DHCP VNF")
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO bgp_vnfs (vnf_id, local_as, router_id, peer_as, peer_ip, local_ip) VALUES (?, 1, '1.1.1.1', 1, '2.2.2.2', '1.1.1.1/24')`, vnf.ID); err == nil {
		t.Fatal("bgp_vnfs accepted a DHCP VNF")
	}
}

// TestVNFTypeTriggersRejectUnknownAndChangedTypes proves the identity table
// rejects unknown types and type changes.
func TestVNFTypeTriggersRejectUnknownAndChangedTypes(t *testing.T) {
	st, vnf := openInternalVNFStore(t)
	ctx := context.Background()
	network, err := st.GetNetwork(ctx, vnf.NetworkID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO vnfs (name, network_id, type, enabled, created_at, updated_at)
		VALUES ('bad', ?, 'wireguard', 0, 1, 1)`, network.ID); err == nil {
		t.Fatal("vnfs accepted an unknown type")
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE vnfs SET type = 'dns' WHERE id = ?`, vnf.ID); err == nil {
		t.Fatal("vnfs allowed a type change")
	}
}

// TestVNFChildCascadeOnDelete proves typed parent and child rows disappear with
// the identity row.
func TestVNFChildCascadeOnDelete(t *testing.T) {
	st, vnf := openInternalVNFStore(t)
	ctx := context.Background()
	if _, err := st.CreateDHCPStaticAssignment(ctx, vnf.ID, DHCPStaticAssignment{
		MAC: "02:00:00:00:00:01", IP: "10.20.0.50",
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if _, err := st.CreateDHCPOption(ctx, vnf.ID, DHCPOption{Code: 15, Format: "string", Value: "lab.test"}); err != nil {
		t.Fatalf("create option: %v", err)
	}
	if err := st.DeleteVNF(ctx, vnf.ID); err != nil {
		t.Fatalf("delete vnf: %v", err)
	}
	for _, table := range []string{"dhcpv4_vnfs", "dhcpv4_static_assignments", "dhcpv4_options"} {
		var count int
		if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s kept %d rows after vnf delete", table, count)
		}
	}
}

// TestSetDHCPVNFNetworkBindingsWritesOnlyDerivedFields proves the derived setter
// updates gateway and bind_ip without touching the rest of the parent row.
func TestSetDHCPVNFNetworkBindingsWritesOnlyDerivedFields(t *testing.T) {
	st, vnf := openInternalVNFStore(t)
	ctx := context.Background()
	if err := st.SetDHCPVNFNetworkBindings(ctx, vnf.ID, "10.20.0.1/24", "10.20.0.2/24"); err != nil {
		t.Fatalf("set bindings: %v", err)
	}
	updated, err := st.GetVNF(ctx, vnf.ID)
	if err != nil {
		t.Fatalf("get vnf: %v", err)
	}
	if updated.Config.Gateway != "10.20.0.1/24" || updated.Config.BindIP != "10.20.0.2/24" {
		t.Fatalf("derived bindings = %+v", updated.Config)
	}
	if updated.Config.RangeStart != "10.20.0.100" || updated.Config.RangeEnd != "10.20.0.200" ||
		updated.Config.LeaseTime != "12h" {
		t.Fatalf("derived setter changed unrelated config: %+v", updated.Config)
	}
	if err := st.SetDHCPVNFNetworkBindings(ctx, 9999, "10.0.0.1/24", "10.0.0.2/24"); !errors.Is(err, ErrVNFNotFound) {
		t.Fatalf("missing vnf binding error = %v, want ErrVNFNotFound", err)
	}
}

// TestUpdateVNFIdentityLeavesTypedConfig proves the identity write used by
// activation does not touch typed parent or child rows.
func TestUpdateVNFIdentityLeavesTypedConfig(t *testing.T) {
	st, vnf := openInternalVNFStore(t)
	ctx := context.Background()
	if _, err := st.CreateDHCPStaticAssignment(ctx, vnf.ID, DHCPStaticAssignment{
		MAC: "02:00:00:00:00:01", IP: "10.20.0.50",
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	vnf.Name = "renamed"
	vnf.Enabled = true
	vnf.TapSwIfIndex = 42
	vnf.TapHostName = "vnf-tap"
	updated, err := st.UpdateVNFIdentity(ctx, vnf)
	if err != nil {
		t.Fatalf("update identity: %v", err)
	}
	if updated.Name != "renamed" || !updated.Enabled || updated.TapSwIfIndex != 42 || updated.TapHostName != "vnf-tap" {
		t.Fatalf("updated identity = %+v", updated)
	}
	if updated.Config.RangeStart != "10.20.0.100" || len(updated.Config.StaticAssignments) != 1 {
		t.Fatalf("identity write changed typed config: %+v", updated.Config)
	}
}
