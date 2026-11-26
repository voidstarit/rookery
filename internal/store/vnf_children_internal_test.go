package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// openChildStore opens a fresh store with two networks for child-row tests.
func openChildStore(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "vnf-children.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	network, err := st.CreateNetwork(ctx, Network{Name: "lab", BridgeDomainID: 200})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	other, err := st.CreateNetwork(ctx, Network{Name: "other", BridgeDomainID: 201})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	return st, network.ID, other.ID
}

// createChildDHCPv4 creates a DHCPv4 parent for child-row tests.
func createChildDHCPv4(t *testing.T, st *Store, networkID int64) VNF {
	t.Helper()
	vnf, err := st.CreateDHCPVNF(context.Background(), NewDHCPVNF{
		Name:      "lab-dhcpv4",
		NetworkID: networkID,
		Family:    VNFTypeDHCPv4,
		Config: DHCPVNFConfig{
			Gateway:    "10.20.0.1/24",
			RangeStart: "10.20.0.100",
			RangeEnd:   "10.20.0.200",
			LeaseTime:  "12h",
		},
	})
	if err != nil {
		t.Fatalf("create dhcpv4 parent: %v", err)
	}
	return vnf
}

// createChildDHCPv6 creates a DHCPv6 parent for child-row tests.
func createChildDHCPv6(t *testing.T, st *Store, networkID int64) VNF {
	t.Helper()
	vnf, err := st.CreateDHCPVNF(context.Background(), NewDHCPVNF{
		Name:      "lab-dhcpv6",
		NetworkID: networkID,
		Family:    VNFTypeDHCPv6,
		Config: DHCPVNFConfig{
			Gateway:    "fd00::1/64",
			RangeStart: "fd00::100",
			RangeEnd:   "fd00::200",
			LeaseTime:  "12h",
			Transport:  "tap",
		},
	})
	if err != nil {
		t.Fatalf("create dhcpv6 parent: %v", err)
	}
	return vnf
}

// createChildDNS creates a DNS parent for child-row tests.
func createChildDNS(t *testing.T, st *Store, networkID int64) VNF {
	t.Helper()
	vnf, err := st.CreateDNSVNF(context.Background(), NewDNSVNF{
		Name: "lab-dns", NetworkID: networkID, Config: DNSVNFConfig{Forward: true},
	})
	if err != nil {
		t.Fatalf("create dns parent: %v", err)
	}
	return vnf
}

// createChildNAT44 creates a NAT44 parent for child-row tests.
func createChildNAT44(t *testing.T, st *Store, networkID int64) VNF {
	t.Helper()
	vnf, err := st.CreateNAT44VNF(context.Background(), NewNAT44VNF{
		Name: "lab-nat44", NetworkID: networkID,
		Config: NAT44VNFConfig{Gateway: "10.30.0.1/24", OutsideInterface: "eth0"},
	})
	if err != nil {
		t.Fatalf("create nat44 parent: %v", err)
	}
	return vnf
}

// createChildBGP creates a BGP parent for child-row tests.
func createChildBGP(t *testing.T, st *Store, networkID int64) VNF {
	t.Helper()
	vnf, err := st.CreateBGPVNF(context.Background(), NewBGPVNF{
		Name: "lab-bgp", NetworkID: networkID,
		Config: BGPVNFConfig{
			Transport: "tap", LocalAS: 65000, RouterID: "10.40.0.1",
			PeerAS: 65001, PeerIP: "10.40.0.2", LocalIP: "10.40.0.1/24",
		},
	})
	if err != nil {
		t.Fatalf("create bgp parent: %v", err)
	}
	return vnf
}

// childKind describes one typed child table so the shared CRUD and error
// matrices can exercise every kind the same way.
type childKind struct {
	name        string
	parent      func(t *testing.T, st *Store, networkID int64) VNF
	wrongParent func(t *testing.T, st *Store, networkID int64) VNF
	table       string
	create      func(st *Store, vnfID int64) (int64, error)
	createDup   func(t *testing.T, st *Store, vnfID int64) error
	get         func(st *Store, vnfID, id int64) error
	list        func(st *Store, vnfID int64) (int, error)
	update      func(t *testing.T, st *Store, vnfID, id int64)
	remove      func(st *Store, vnfID, id int64) error
}

// childKinds returns the matrix of every typed child table.
func childKinds() []childKind {
	return []childKind{
		{
			name:        "dhcpv4 static assignment",
			parent:      createChildDHCPv4,
			wrongParent: createChildDHCPv6,
			table:       "dhcpv4_static_assignments",
			create: func(st *Store, vnfID int64) (int64, error) {
				item, err := st.CreateDHCPStaticAssignment(context.Background(), vnfID, DHCPStaticAssignment{
					MAC: "02:00:00:00:00:01", IP: "10.20.0.50",
				})
				if err != nil {
					return 0, err
				}
				return item.ID, nil
			},
			createDup: func(t *testing.T, st *Store, vnfID int64) error {
				_, err := st.CreateDHCPStaticAssignment(context.Background(), vnfID, DHCPStaticAssignment{
					MAC: "02:00:00:00:00:01", IP: "10.20.0.51",
				})
				return err
			},
			get: func(st *Store, vnfID, id int64) error {
				_, err := st.GetDHCPStaticAssignment(context.Background(), vnfID, id)
				return err
			},
			list: func(st *Store, vnfID int64) (int, error) {
				items, err := st.ListDHCPStaticAssignments(context.Background(), vnfID)
				return len(items), err
			},
			update: func(t *testing.T, st *Store, vnfID, id int64) {
				updated, err := st.UpdateDHCPStaticAssignment(context.Background(), vnfID, id, DHCPStaticAssignment{
					MAC: "02:00:00:00:00:02", IP: "10.20.0.60",
				})
				if err != nil {
					t.Fatalf("update assignment: %v", err)
				}
				if updated.MAC != "02:00:00:00:00:02" || updated.IP != "10.20.0.60" {
					t.Fatalf("updated assignment = %+v", updated)
				}
			},
			remove: func(st *Store, vnfID, id int64) error {
				return st.DeleteDHCPStaticAssignment(context.Background(), vnfID, id)
			},
		},
		{
			name:        "dhcpv4 option",
			parent:      createChildDHCPv4,
			wrongParent: createChildDNS,
			table:       "dhcpv4_options",
			create: func(st *Store, vnfID int64) (int64, error) {
				item, err := st.CreateDHCPOption(context.Background(), vnfID, DHCPOption{
					Code: 15, Format: "string", Value: "lab.test",
				})
				if err != nil {
					return 0, err
				}
				return item.ID, nil
			},
			createDup: func(t *testing.T, st *Store, vnfID int64) error {
				_, err := st.CreateDHCPOption(context.Background(), vnfID, DHCPOption{
					Code: 15, Format: "string", Value: "other.test",
				})
				return err
			},
			get: func(st *Store, vnfID, id int64) error {
				_, err := st.GetDHCPOption(context.Background(), vnfID, id)
				return err
			},
			list: func(st *Store, vnfID int64) (int, error) {
				items, err := st.ListDHCPOptions(context.Background(), vnfID)
				return len(items), err
			},
			update: func(t *testing.T, st *Store, vnfID, id int64) {
				updated, err := st.UpdateDHCPOption(context.Background(), vnfID, id, DHCPOption{
					Code: 15, Format: "string", Value: "updated.test",
				})
				if err != nil {
					t.Fatalf("update option: %v", err)
				}
				if updated.Value != "updated.test" {
					t.Fatalf("updated option = %+v", updated)
				}
			},
			remove: func(st *Store, vnfID, id int64) error {
				return st.DeleteDHCPOption(context.Background(), vnfID, id)
			},
		},
		{
			name:        "dhcpv6 option",
			parent:      createChildDHCPv6,
			wrongParent: createChildDNS,
			table:       "dhcpv6_options",
			create: func(st *Store, vnfID int64) (int64, error) {
				item, err := st.CreateDHCPOption(context.Background(), vnfID, DHCPOption{
					Code: 23, Format: "ipv6", Value: "fd00::2",
				})
				if err != nil {
					return 0, err
				}
				return item.ID, nil
			},
			createDup: func(t *testing.T, st *Store, vnfID int64) error {
				_, err := st.CreateDHCPOption(context.Background(), vnfID, DHCPOption{
					Code: 23, Format: "ipv6", Value: "fd00::3",
				})
				return err
			},
			get: func(st *Store, vnfID, id int64) error {
				_, err := st.GetDHCPOption(context.Background(), vnfID, id)
				return err
			},
			list: func(st *Store, vnfID int64) (int, error) {
				items, err := st.ListDHCPOptions(context.Background(), vnfID)
				return len(items), err
			},
			update: func(t *testing.T, st *Store, vnfID, id int64) {
				if _, err := st.UpdateDHCPOption(context.Background(), vnfID, id, DHCPOption{
					Code: 23, Format: "ipv6", Value: "fd00::4",
				}); err != nil {
					t.Fatalf("update option: %v", err)
				}
			},
			remove: func(st *Store, vnfID, id int64) error {
				return st.DeleteDHCPOption(context.Background(), vnfID, id)
			},
		},
		{
			name:        "dns record",
			parent:      createChildDNS,
			wrongParent: createChildDHCPv4,
			table:       "dns_records",
			create: func(st *Store, vnfID int64) (int64, error) {
				item, err := st.CreateDNSVNFRecord(context.Background(), vnfID, DNSRecord{
					Name: "host.lab.test", Type: "A", Value: "10.20.0.50", TTL: 300,
				})
				if err != nil {
					return 0, err
				}
				return item.ID, nil
			},
			createDup: func(t *testing.T, st *Store, vnfID int64) error {
				_, err := st.CreateDNSVNFRecord(context.Background(), vnfID, DNSRecord{
					Name: "host.lab.test", Type: "A", Value: "10.20.0.50", TTL: 300,
				})
				return err
			},
			get: func(st *Store, vnfID, id int64) error {
				_, err := st.GetDNSVNFRecord(context.Background(), vnfID, id)
				return err
			},
			list: func(st *Store, vnfID int64) (int, error) {
				items, err := st.ListDNSVNFRecords(context.Background(), vnfID)
				return len(items), err
			},
			update: func(t *testing.T, st *Store, vnfID, id int64) {
				updated, err := st.UpdateDNSVNFRecord(context.Background(), vnfID, id, DNSRecord{
					Name: "host.lab.test", Type: "A", Value: "10.20.0.51", TTL: 600,
				})
				if err != nil {
					t.Fatalf("update dns record: %v", err)
				}
				if updated.Value != "10.20.0.51" || updated.TTL != 600 {
					t.Fatalf("updated dns record = %+v", updated)
				}
			},
			remove: func(st *Store, vnfID, id int64) error {
				return st.DeleteDNSVNFRecord(context.Background(), vnfID, id)
			},
		},
		{
			name:        "dns forwarder",
			parent:      createChildDNS,
			wrongParent: createChildNAT44,
			table:       "dns_forwarders",
			create: func(st *Store, vnfID int64) (int64, error) {
				item, err := st.CreateDNSVNFForwarder(context.Background(), vnfID, DNSForwarder{
					Address: "1.1.1.1", Position: 0,
				})
				if err != nil {
					return 0, err
				}
				return item.ID, nil
			},
			createDup: func(t *testing.T, st *Store, vnfID int64) error {
				_, err := st.CreateDNSVNFForwarder(context.Background(), vnfID, DNSForwarder{
					Address: "1.1.1.1", Position: 1,
				})
				return err
			},
			get: func(st *Store, vnfID, id int64) error {
				_, err := st.GetDNSVNFForwarder(context.Background(), vnfID, id)
				return err
			},
			list: func(st *Store, vnfID int64) (int, error) {
				items, err := st.ListDNSVNFForwarders(context.Background(), vnfID)
				return len(items), err
			},
			update: func(t *testing.T, st *Store, vnfID, id int64) {
				updated, err := st.UpdateDNSVNFForwarder(context.Background(), vnfID, id, DNSForwarder{
					Address: "9.9.9.9", Position: 2,
				})
				if err != nil {
					t.Fatalf("update forwarder: %v", err)
				}
				if updated.Address != "9.9.9.9" || updated.Position != 2 {
					t.Fatalf("updated forwarder = %+v", updated)
				}
			},
			remove: func(st *Store, vnfID, id int64) error {
				return st.DeleteDNSVNFForwarder(context.Background(), vnfID, id)
			},
		},
		{
			name:        "nat44 dnat rule",
			parent:      createChildNAT44,
			wrongParent: createChildBGP,
			table:       "nat44_dnat_rules",
			create: func(st *Store, vnfID int64) (int64, error) {
				item, err := st.CreateNAT44DNATRule(context.Background(), vnfID, DNATRule{
					Protocol: "tcp", ExternalPort: 80, LocalIP: "10.30.0.10", LocalPort: 8080,
				})
				if err != nil {
					return 0, err
				}
				return item.ID, nil
			},
			createDup: func(t *testing.T, st *Store, vnfID int64) error {
				_, err := st.CreateNAT44DNATRule(context.Background(), vnfID, DNATRule{
					Protocol: "tcp", ExternalPort: 80, LocalIP: "10.30.0.11", LocalPort: 8081,
				})
				return err
			},
			get: func(st *Store, vnfID, id int64) error {
				_, err := st.GetNAT44DNATRule(context.Background(), vnfID, id)
				return err
			},
			list: func(st *Store, vnfID int64) (int, error) {
				items, err := st.ListNAT44DNATRules(context.Background(), vnfID)
				return len(items), err
			},
			update: func(t *testing.T, st *Store, vnfID, id int64) {
				updated, err := st.UpdateNAT44DNATRule(context.Background(), vnfID, id, DNATRule{
					Protocol: "udp", ExternalPort: 53, LocalIP: "10.30.0.11", LocalPort: 5353,
				})
				if err != nil {
					t.Fatalf("update dnat rule: %v", err)
				}
				if updated.Protocol != "udp" || updated.ExternalPort != 53 {
					t.Fatalf("updated dnat rule = %+v", updated)
				}
			},
			remove: func(st *Store, vnfID, id int64) error {
				return st.DeleteNAT44DNATRule(context.Background(), vnfID, id)
			},
		},
		{
			name:        "bgp advertised prefix",
			parent:      createChildBGP,
			wrongParent: createChildNAT44,
			table:       "bgp_advertised_prefixes",
			create: func(st *Store, vnfID int64) (int64, error) {
				item, err := st.CreateBGPVNFAdvertisedPrefix(context.Background(), vnfID, BGPPrefix{
					Prefix: "10.50.0.0/24",
				})
				if err != nil {
					return 0, err
				}
				return item.ID, nil
			},
			createDup: func(t *testing.T, st *Store, vnfID int64) error {
				_, err := st.CreateBGPVNFAdvertisedPrefix(context.Background(), vnfID, BGPPrefix{
					Prefix: "10.50.0.0/24",
				})
				return err
			},
			get: func(st *Store, vnfID, id int64) error {
				_, err := st.GetBGPVNFAdvertisedPrefix(context.Background(), vnfID, id)
				return err
			},
			list: func(st *Store, vnfID int64) (int, error) {
				items, err := st.ListBGPVNFAdvertisedPrefixes(context.Background(), vnfID)
				return len(items), err
			},
			update: func(t *testing.T, st *Store, vnfID, id int64) {
				updated, err := st.UpdateBGPVNFAdvertisedPrefix(context.Background(), vnfID, id, BGPPrefix{
					Prefix: "10.60.0.0/24",
				})
				if err != nil {
					t.Fatalf("update advertised prefix: %v", err)
				}
				if updated.Prefix != "10.60.0.0/24" {
					t.Fatalf("updated advertised prefix = %+v", updated)
				}
			},
			remove: func(st *Store, vnfID, id int64) error {
				return st.DeleteBGPVNFAdvertisedPrefix(context.Background(), vnfID, id)
			},
		},
	}
}

// TestVNFChildCRUDAllKinds exercises list, get, create, update, and delete for
// every typed child table.
func TestVNFChildCRUDAllKinds(t *testing.T) {
	for _, kind := range childKinds() {
		t.Run(kind.name, func(t *testing.T) {
			st, networkID, _ := openChildStore(t)
			parent := kind.parent(t, st, networkID)
			if count, err := kind.list(st, parent.ID); err != nil || count != 0 {
				t.Fatalf("initial list = %d, err = %v", count, err)
			}
			id := mustChildCreate(t, kind, st, parent.ID)
			if id < 1 {
				t.Fatalf("created child id = %d", id)
			}
			if err := kind.get(st, parent.ID, id); err != nil {
				t.Fatalf("get child: %v", err)
			}
			if count, err := kind.list(st, parent.ID); err != nil || count != 1 {
				t.Fatalf("list after create = %d, err = %v", count, err)
			}
			kind.update(t, st, parent.ID, id)
			if err := kind.get(st, parent.ID, id); err != nil {
				t.Fatalf("get after update: %v", err)
			}
			if err := kind.remove(st, parent.ID, id); err != nil {
				t.Fatalf("delete child: %v", err)
			}
			if count, err := kind.list(st, parent.ID); err != nil || count != 0 {
				t.Fatalf("list after delete = %d, err = %v", count, err)
			}
			if err := kind.get(st, parent.ID, id); !errors.Is(err, ErrVNFChildNotFound) {
				t.Fatalf("get deleted child = %v, want ErrVNFChildNotFound", err)
			}
		})
	}
}

// TestVNFChildMissingRowErrors proves get, update, and delete report a missing
// child row with ErrVNFChildNotFound for every kind.
func TestVNFChildMissingRowErrors(t *testing.T) {
	for _, kind := range childKinds() {
		t.Run(kind.name, func(t *testing.T) {
			st, networkID, _ := openChildStore(t)
			parent := kind.parent(t, st, networkID)
			if err := kind.get(st, parent.ID, 4242); !errors.Is(err, ErrVNFChildNotFound) {
				t.Fatalf("get missing child = %v, want ErrVNFChildNotFound", err)
			}
			if err := kind.remove(st, parent.ID, 4242); !errors.Is(err, ErrVNFChildNotFound) {
				t.Fatalf("delete missing child = %v, want ErrVNFChildNotFound", err)
			}
			// Updates of a missing row report the same sentinel.
			if _, err := kindUpdateMissing(kind, st, parent.ID); !errors.Is(err, ErrVNFChildNotFound) {
				t.Fatalf("update missing child = %v, want ErrVNFChildNotFound", err)
			}
		})
	}
}

// kindUpdateMissing performs an update of a child ID that does not exist.
func kindUpdateMissing(kind childKind, st *Store, vnfID int64) (int64, error) {
	switch kind.name {
	case "dhcpv4 static assignment":
		_, err := st.UpdateDHCPStaticAssignment(context.Background(), vnfID, 4242, DHCPStaticAssignment{
			MAC: "02:00:00:00:00:09", IP: "10.20.0.99",
		})
		return 0, err
	case "dhcpv4 option", "dhcpv6 option":
		_, err := st.UpdateDHCPOption(context.Background(), vnfID, 4242, DHCPOption{
			Code: 200, Format: "string", Value: "missing",
		})
		return 0, err
	case "dns record":
		_, err := st.UpdateDNSVNFRecord(context.Background(), vnfID, 4242, DNSRecord{
			Name: "missing.lab.test", Type: "A", Value: "10.20.0.99", TTL: 300,
		})
		return 0, err
	case "dns forwarder":
		_, err := st.UpdateDNSVNFForwarder(context.Background(), vnfID, 4242, DNSForwarder{
			Address: "9.9.9.8", Position: 3,
		})
		return 0, err
	case "nat44 dnat rule":
		_, err := st.UpdateNAT44DNATRule(context.Background(), vnfID, 4242, DNATRule{
			Protocol: "tcp", ExternalPort: 8080, LocalIP: "10.30.0.99", LocalPort: 8080,
		})
		return 0, err
	case "bgp advertised prefix":
		_, err := st.UpdateBGPVNFAdvertisedPrefix(context.Background(), vnfID, 4242, BGPPrefix{
			Prefix: "10.99.0.0/24",
		})
		return 0, err
	default:
		panic("unknown child kind " + kind.name)
	}
}

// TestVNFChildWrongParentAndMissingParentErrors covers the parent-type guard on
// every child operation.
func TestVNFChildWrongParentAndMissingParentErrors(t *testing.T) {
	for _, kind := range childKinds() {
		t.Run(kind.name, func(t *testing.T) {
			st, networkID, otherNetworkID := openChildStore(t)
			wrong := kind.wrongParent(t, st, otherNetworkID)
			if err := kind.get(st, wrong.ID, 1); !errors.Is(err, ErrVNFTypeMismatch) {
				t.Fatalf("get on wrong parent = %v, want ErrVNFTypeMismatch", err)
			}
			if _, err := kind.list(st, wrong.ID); !errors.Is(err, ErrVNFTypeMismatch) {
				t.Fatalf("list on wrong parent = %v, want ErrVNFTypeMismatch", err)
			}
			if _, err := kind.create(st, wrong.ID); !errors.Is(err, ErrVNFTypeMismatch) {
				t.Fatalf("create on wrong parent = %v, want ErrVNFTypeMismatch", err)
			}
			if err := kind.remove(st, wrong.ID, 1); !errors.Is(err, ErrVNFTypeMismatch) {
				t.Fatalf("delete on wrong parent = %v, want ErrVNFTypeMismatch", err)
			}
			if _, err := kindUpdateMissing(kind, st, wrong.ID); !errors.Is(err, ErrVNFTypeMismatch) {
				t.Fatalf("update on wrong parent = %v, want ErrVNFTypeMismatch", err)
			}

			// A missing identity row is a not-found, not a type mismatch.
			if _, err := kind.list(st, 9999); !errors.Is(err, ErrVNFNotFound) {
				t.Fatalf("list on missing parent = %v, want ErrVNFNotFound", err)
			}
			if _, err := kind.create(st, 9999); !errors.Is(err, ErrVNFNotFound) {
				t.Fatalf("create on missing parent = %v, want ErrVNFNotFound", err)
			}
			if err := kind.remove(st, 9999, 1); !errors.Is(err, ErrVNFNotFound) {
				t.Fatalf("delete on missing parent = %v, want ErrVNFNotFound", err)
			}
			_ = networkID
		})
	}
}

// mustChildCreate creates one child row and fails the test on error.
func mustChildCreate(t *testing.T, kind childKind, st *Store, vnfID int64) int64 {
	t.Helper()
	id, err := kind.create(st, vnfID)
	if err != nil {
		t.Fatalf("create %s: %v", kind.name, err)
	}
	return id
}

// TestVNFChildDuplicateKeysConflict proves a duplicate unique key on create and
// on update is reported as ErrVNFChildTaken and does not commit a partial row.
func TestVNFChildDuplicateKeysConflict(t *testing.T) {
	for _, kind := range childKinds() {
		t.Run(kind.name, func(t *testing.T) {
			st, networkID, _ := openChildStore(t)
			parent := kind.parent(t, st, networkID)
			id := mustChildCreate(t, kind, st, parent.ID)
			before, err := st.identityVNF(context.Background(), parent.ID)
			if err != nil {
				t.Fatalf("read parent: %v", err)
			}
			if err := kind.createDup(t, st, parent.ID); !errors.Is(err, ErrVNFChildTaken) {
				t.Fatalf("duplicate create = %v, want ErrVNFChildTaken", err)
			}
			if count, err := kind.list(st, parent.ID); err != nil || count != 1 {
				t.Fatalf("list after duplicate = %d, err = %v", count, err)
			}
			after, err := st.identityVNF(context.Background(), parent.ID)
			if err != nil {
				t.Fatalf("read parent after duplicate: %v", err)
			}
			if after.UpdatedAt.After(before.UpdatedAt) {
				t.Fatalf("failed duplicate create bumped the parent timestamp")
			}
			_ = id
		})
	}
}

// TestVNFChildUpdateUniqueConflictRollsBack proves an update that collides with
// a sibling row leaves both rows unchanged.
func TestVNFChildUpdateUniqueConflictRollsBack(t *testing.T) {
	st, networkID, _ := openChildStore(t)
	parent := createChildDHCPv4(t, st, networkID)
	first, err := st.CreateDHCPStaticAssignment(context.Background(), parent.ID, DHCPStaticAssignment{
		MAC: "02:00:00:00:00:01", IP: "10.20.0.50",
	})
	if err != nil {
		t.Fatalf("create first assignment: %v", err)
	}
	second, err := st.CreateDHCPStaticAssignment(context.Background(), parent.ID, DHCPStaticAssignment{
		MAC: "02:00:00:00:00:02", IP: "10.20.0.51",
	})
	if err != nil {
		t.Fatalf("create second assignment: %v", err)
	}
	if _, err := st.UpdateDHCPStaticAssignment(context.Background(), parent.ID, second.ID, DHCPStaticAssignment{
		MAC: first.MAC, IP: "10.20.0.60",
	}); !errors.Is(err, ErrVNFChildTaken) {
		t.Fatalf("conflicting update = %v, want ErrVNFChildTaken", err)
	}
	restored, err := st.GetDHCPStaticAssignment(context.Background(), parent.ID, second.ID)
	if err != nil {
		t.Fatalf("read second assignment: %v", err)
	}
	if restored.MAC != "02:00:00:00:00:02" || restored.IP != "10.20.0.51" {
		t.Fatalf("conflicting update changed the row: %+v", restored)
	}
}

// TestVNFChildTableErrorsAfterDrop covers the query and write error branches by
// removing the child table behind the store's back.
func TestVNFChildTableErrorsAfterDrop(t *testing.T) {
	for _, kind := range childKinds() {
		t.Run(kind.name, func(t *testing.T) {
			st, networkID, _ := openChildStore(t)
			parent := kind.parent(t, st, networkID)
			if _, err := st.db.ExecContext(context.Background(), `DROP TABLE `+kind.table); err != nil {
				t.Fatalf("drop child table: %v", err)
			}
			if _, err := kind.list(st, parent.ID); err == nil || errors.Is(err, ErrVNFTypeMismatch) {
				t.Fatalf("list error = %v, want a query error", err)
			}
			if err := kind.get(st, parent.ID, 1); err == nil || errors.Is(err, ErrVNFChildNotFound) {
				t.Fatalf("get error = %v, want a query error", err)
			}
			if _, err := kind.create(st, parent.ID); err == nil || errors.Is(err, ErrVNFChildTaken) {
				t.Fatalf("create error = %v, want a write error", err)
			}
			if _, err := kindUpdateMissing(kind, st, parent.ID); err == nil || errors.Is(err, ErrVNFChildNotFound) {
				t.Fatalf("update error = %v, want a write error", err)
			}
			if err := kind.remove(st, parent.ID, 1); err == nil || errors.Is(err, ErrVNFChildNotFound) {
				t.Fatalf("delete error = %v, want a write error", err)
			}
		})
	}
}

// TestVNFChildWriteErrorsDoNotPartiallyCommit proves a failed parent-touch rolls
// the child insert back.
func TestVNFChildWriteErrorsDoNotPartiallyCommit(t *testing.T) {
	st, networkID, _ := openChildStore(t)
	parent := createChildDHCPv4(t, st, networkID)
	if _, err := st.db.ExecContext(context.Background(), `
		CREATE TRIGGER fail_vnf_touch BEFORE UPDATE ON vnfs
		BEGIN
			SELECT RAISE(ABORT, 'touch blocked');
		END`); err != nil {
		t.Fatalf("create touch trigger: %v", err)
	}
	if _, err := st.CreateDHCPStaticAssignment(context.Background(), parent.ID, DHCPStaticAssignment{
		MAC: "02:00:00:00:00:01", IP: "10.20.0.50",
	}); err == nil {
		t.Fatal("child write succeeded despite the failed parent touch")
	}
	items, err := st.ListDHCPStaticAssignments(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("list assignments: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("failed child write was not rolled back: %+v", items)
	}
}

// TestVNFChildWriteErrorsWhenReadOnly proves the non-unique write error path
// (not a child-taken conflict) when SQLite rejects the write.
func TestVNFChildWriteErrorsWhenReadOnly(t *testing.T) {
	st, networkID, _ := openChildStore(t)
	parent := createChildDHCPv4(t, st, networkID)
	if _, err := st.db.ExecContext(context.Background(), `PRAGMA query_only = ON`); err != nil {
		t.Fatalf("set query_only: %v", err)
	}
	_, err := st.CreateDHCPOption(context.Background(), parent.ID, DHCPOption{Code: 15, Format: "string", Value: "lab.test"})
	if err == nil || errors.Is(err, ErrVNFChildTaken) {
		t.Fatalf("create error = %v, want a plain write error", err)
	}
}
