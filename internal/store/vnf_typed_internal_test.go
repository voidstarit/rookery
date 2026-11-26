package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// typedTestStore opens a store with two networks for typed parent tests.
func typedTestStore(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "vnf-typed.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	network, err := st.CreateNetwork(ctx, Network{Name: "lab", BridgeDomainID: 300})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	other, err := st.CreateNetwork(ctx, Network{Name: "other", BridgeDomainID: 301})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	return st, network.ID, other.ID
}

// execInternalSQL runs a maintenance statement against the store's database.
func execInternalSQL(t *testing.T, st *Store, query string) {
	t.Helper()
	if _, err := st.db.ExecContext(context.Background(), query); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// TestTypedParentUpdatesAllKinds covers the typed update path for every type.
func TestTypedParentUpdatesAllKinds(t *testing.T) {
	st, networkID, _ := typedTestStore(t)
	ctx := context.Background()

	dns := createChildDNS(t, st, networkID)
	updatedDNS, err := st.UpdateDNSVNF(ctx, dns, DNSVNFConfig{Forward: false})
	if err != nil {
		t.Fatalf("update dns: %v", err)
	}
	if updatedDNS.Config.DNSForward {
		t.Fatalf("dns update did not persist the forward flag: %+v", updatedDNS.Config)
	}
	if read, err := st.GetDNSVNF(ctx, dns.ID); err != nil || read.Config.Forward {
		t.Fatalf("get dns after update = %+v, err = %v", read.Config, err)
	}

	dhcp6 := createChildDHCPv6(t, st, networkID)
	dhcp6.Name = "lab-dhcpv6-renamed"
	dhcp6.Enabled = true
	dhcp6.TapSwIfIndex = 7
	updatedDHCP, err := st.UpdateDHCPVNF(ctx, dhcp6, DHCPVNFConfig{
		Gateway:    "fd00:1::1/64",
		RangeStart: "fd00:1::100",
		RangeEnd:   "fd00:1::200",
		LeaseTime:  "6h",
		DNS:        "fd00:1::2",
		BindIP:     "fd00:1::2/64",
		Transport:  "tap",
		DNSDomain:  "lab.test",
		DNSTTL:     600,
	})
	if err != nil {
		t.Fatalf("update dhcpv6: %v", err)
	}
	if updatedDHCP.Name != "lab-dhcpv6-renamed" || !updatedDHCP.Enabled ||
		updatedDHCP.Config.Gateway != "fd00:1::1/64" || updatedDHCP.Config.LeaseTime != "6h" ||
		updatedDHCP.Config.DNSDomain != "lab.test" || updatedDHCP.Config.DNSTTL != 600 {
		t.Fatalf("updated dhcpv6 = %+v", updatedDHCP)
	}
	if read, err := st.GetDHCPVNF(ctx, dhcp6.ID); err != nil || read.Family != VNFTypeDHCPv6 {
		t.Fatalf("get dhcpv6 after update = %+v, err = %v", read, err)
	}

	nat44 := createChildNAT44(t, st, networkID)
	nat44.Enabled = true
	updatedNAT44, err := st.UpdateNAT44VNF(ctx, nat44, NAT44VNFConfig{
		Gateway:          "10.31.0.1/24",
		OutsideInterface: "eth1",
		OutsideAddress:   "192.0.2.20/24",
		OutsideGateway:   "192.0.2.1",
		NAT44Pool:        "192.0.2.100-192.0.2.200",
	})
	if err != nil {
		t.Fatalf("update nat44: %v", err)
	}
	if !updatedNAT44.Enabled || updatedNAT44.Config.Gateway != "10.31.0.1/24" ||
		updatedNAT44.Config.OutsideInterface != "eth1" ||
		updatedNAT44.Config.NAT44Pool != "192.0.2.100-192.0.2.200" {
		t.Fatalf("updated nat44 = %+v", updatedNAT44)
	}
	if read, err := st.GetNAT44VNF(ctx, nat44.ID); err != nil || read.Config.OutsideInterface != "eth1" {
		t.Fatalf("get nat44 after update = %+v, err = %v", read, err)
	}

	bgp, err := st.CreateBGPVNF(ctx, NewBGPVNF{
		Name: "lab-bgp", NetworkID: networkID,
		Config: BGPVNFConfig{
			Transport: "tap", LocalAS: 65000, RouterID: "10.40.0.1",
			PeerAS: 65001, PeerIP: "10.40.0.2", LocalIP: "10.40.0.1/24",
			Password: "s3cret",
		},
	})
	if err != nil {
		t.Fatalf("create bgp: %v", err)
	}
	bgp.Name = "lab-bgp-renamed"
	updatedBGP, err := st.UpdateBGPVNF(ctx, bgp, BGPVNFConfig{
		Transport: "memif", LocalAS: 65010, RouterID: "10.40.0.9",
		PeerAS: 65011, PeerIP: "10.40.0.10", LocalIP: "10.40.0.9/24",
		PeerGateway: "10.40.0.1", MultihopTTL: 10, Password: "s3cret",
		Passive: true, TargetTableID: 200, NextHop: "10.40.0.9",
	})
	if err != nil {
		t.Fatalf("update bgp: %v", err)
	}
	if updatedBGP.Name != "lab-bgp-renamed" || updatedBGP.Config.LocalAS != 65010 ||
		updatedBGP.Config.PeerGateway != "10.40.0.1" || !updatedBGP.Config.Passive ||
		updatedBGP.Config.TargetTableID != 200 || updatedBGP.Config.NextHop != "10.40.0.9" {
		t.Fatalf("updated bgp = %+v", updatedBGP)
	}
	if hydrated, err := st.GetVNF(ctx, bgp.ID); err != nil || hydrated.Config.Password != "s3cret" {
		t.Fatalf("bgp update lost the password: %+v, err = %v", hydrated.Config, err)
	}
	// The read model never exposes the password.
	if read, err := st.GetBGPVNF(ctx, bgp.ID); err != nil || read.Config.Password != "" {
		t.Fatalf("bgp read model = %+v, err = %v", read.Config, err)
	}
	// An empty password clears the stored one.
	if _, err := st.UpdateBGPVNF(ctx, updatedBGP, BGPVNFConfig{
		Transport: "tap", LocalAS: 65010, RouterID: "10.40.0.9",
		PeerAS: 65011, PeerIP: "10.40.0.10", LocalIP: "10.40.0.9/24",
	}); err != nil {
		t.Fatalf("clear bgp password: %v", err)
	}
	if hydrated, err := st.GetVNF(ctx, bgp.ID); err != nil || hydrated.Config.Password != "" {
		t.Fatalf("bgp password was not cleared: %+v, err = %v", hydrated.Config, err)
	}
}

// TestBGPStorageDefaults proves the typed table defaults are applied by the
// store when a direct write omits them.
func TestBGPStorageDefaults(t *testing.T) {
	st, networkID, _ := typedTestStore(t)
	ctx := context.Background()
	bgp, err := st.CreateBGPVNF(ctx, NewBGPVNF{
		Name: "lab-bgp", NetworkID: networkID,
		Config: BGPVNFConfig{LocalAS: 65000, RouterID: "10.40.0.1", PeerAS: 65001,
			PeerIP: "10.40.0.2", LocalIP: "10.40.0.1/24"},
	})
	if err != nil {
		t.Fatalf("create bgp: %v", err)
	}
	if bgp.Config.Transport != "memif" || bgp.Config.MultihopTTL != 64 {
		t.Fatalf("created bgp defaults = %+v", bgp.Config)
	}
	updated, err := st.UpdateBGPVNF(ctx, bgp, BGPVNFConfig{
		LocalAS: 65000, RouterID: "10.40.0.1", PeerAS: 65001,
		PeerIP: "10.40.0.2", LocalIP: "10.40.0.1/24",
	})
	if err != nil {
		t.Fatalf("update bgp: %v", err)
	}
	if updated.Config.Transport != "memif" || updated.Config.MultihopTTL != 64 {
		t.Fatalf("updated bgp defaults = %+v", updated.Config)
	}
}

// TestTypedParentCreateErrors covers the validation and transaction error paths
// of the typed create methods.
func TestTypedParentCreateErrors(t *testing.T) {
	ctx := context.Background()
	dhcp := NewDHCPVNF{
		Name: "lab-dhcp", Family: VNFTypeDHCPv4,
		Config: DHCPVNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
	}

	t.Run("unsupported dhcp family", func(t *testing.T) {
		st, networkID, _ := typedTestStore(t)
		in := dhcp
		in.NetworkID = networkID
		in.Family = "dhcpv5"
		if _, err := st.CreateDHCPVNF(ctx, in); err == nil || !strings.Contains(err.Error(), "unsupported dhcp family") {
			t.Fatalf("error = %v, want unsupported family", err)
		}
	})

	t.Run("missing network", func(t *testing.T) {
		st, _, _ := typedTestStore(t)
		in := dhcp
		in.NetworkID = 9999
		if _, err := st.CreateDHCPVNF(ctx, in); !errors.Is(err, ErrNetworkNotFound) {
			t.Fatalf("dhcp error = %v, want ErrNetworkNotFound", err)
		}
		if _, err := st.CreateDNSVNF(ctx, NewDNSVNF{Name: "dns", NetworkID: 9999}); !errors.Is(err, ErrNetworkNotFound) {
			t.Fatalf("dns error = %v, want ErrNetworkNotFound", err)
		}
		if _, err := st.CreateNAT44VNF(ctx, NewNAT44VNF{Name: "nat", NetworkID: 9999}); !errors.Is(err, ErrNetworkNotFound) {
			t.Fatalf("nat44 error = %v, want ErrNetworkNotFound", err)
		}
		if _, err := st.CreateBGPVNF(ctx, NewBGPVNF{Name: "bgp", NetworkID: 9999}); !errors.Is(err, ErrNetworkNotFound) {
			t.Fatalf("bgp error = %v, want ErrNetworkNotFound", err)
		}
	})

	t.Run("dhcp parent insert constraint", func(t *testing.T) {
		st, networkID, _ := typedTestStore(t)
		in := dhcp
		in.NetworkID = networkID
		in.Config.Transport = "bogus"
		if _, err := st.CreateDHCPVNF(ctx, in); err == nil || !strings.Contains(err.Error(), "insert dhcp vnf") {
			t.Fatalf("error = %v, want insert dhcp vnf", err)
		}
		// The identity row rolls back with the failed parent insert.
		if vnfs, err := st.ListVNFs(ctx, nil); err != nil || len(vnfs) != 0 {
			t.Fatalf("rolled-back vnfs = %+v, err = %v", vnfs, err)
		}
	})

	t.Run("dhcpv6 transport constraint", func(t *testing.T) {
		st, networkID, _ := typedTestStore(t)
		in := dhcp
		in.NetworkID = networkID
		in.Family = VNFTypeDHCPv6
		in.Config.Transport = "shared"
		if _, err := st.CreateDHCPVNF(ctx, in); err == nil || !strings.Contains(err.Error(), "insert dhcp vnf") {
			t.Fatalf("error = %v, want insert dhcp vnf", err)
		}
	})

	t.Run("dns reference foreign key", func(t *testing.T) {
		st, networkID, _ := typedTestStore(t)
		in := dhcp
		in.NetworkID = networkID
		in.Config.DNSVNFID = 4242
		if _, err := st.CreateDHCPVNF(ctx, in); err == nil || !strings.Contains(err.Error(), "insert dhcp vnf") {
			t.Fatalf("error = %v, want insert dhcp vnf", err)
		}
	})

	t.Run("typed parent insert failures", func(t *testing.T) {
		cases := []struct {
			name    string
			trigger string
			table   string
			create  func(st *Store, networkID int64) error
		}{
			{"dns", "fail_dns_parent", "dns_vnfs", func(st *Store, networkID int64) error {
				_, err := st.CreateDNSVNF(ctx, NewDNSVNF{Name: "dns", NetworkID: networkID})
				return err
			}},
			{"nat44", "fail_nat44_parent", "nat44_vnfs", func(st *Store, networkID int64) error {
				_, err := st.CreateNAT44VNF(ctx, NewNAT44VNF{Name: "nat", NetworkID: networkID})
				return err
			}},
			{"bgp check", "", "", func(st *Store, networkID int64) error {
				_, err := st.CreateBGPVNF(ctx, NewBGPVNF{Name: "bgp", NetworkID: networkID,
					Config: BGPVNFConfig{RouterID: "10.40.0.1", PeerIP: "10.40.0.2", LocalIP: "10.40.0.1/24"}})
				return err
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				st, networkID, _ := typedTestStore(t)
				if tc.trigger != "" {
					execInternalSQL(t, st, `CREATE TRIGGER `+tc.trigger+` BEFORE INSERT ON `+tc.table+`
						BEGIN
							SELECT RAISE(ABORT, 'insert blocked');
						END`)
				}
				err := tc.create(st, networkID)
				if err == nil {
					t.Fatal("create succeeded, want a parent insert failure")
				}
				if tc.trigger != "" && !strings.Contains(err.Error(), "insert") {
					t.Fatalf("error = %v, want an insert error", err)
				}
				if vnfs, listErr := st.ListVNFs(ctx, nil); listErr != nil || len(vnfs) != 0 {
					t.Fatalf("rolled-back vnfs = %+v, err = %v", vnfs, listErr)
				}
			})
		}
	})

	t.Run("identity insert failure", func(t *testing.T) {
		st, networkID, _ := typedTestStore(t)
		execInternalSQL(t, st, `CREATE TRIGGER fail_vnf_insert BEFORE INSERT ON vnfs
			BEGIN
				SELECT RAISE(ABORT, 'insert blocked');
			END`)
		in := dhcp
		in.NetworkID = networkID
		if _, err := st.CreateDHCPVNF(ctx, in); err == nil || !strings.Contains(err.Error(), "insert vnf") {
			t.Fatalf("error = %v, want a plain insert error", err)
		}
	})

	t.Run("duplicate name and type", func(t *testing.T) {
		st, networkID, otherID := typedTestStore(t)
		createChildDHCPv4(t, st, networkID)
		if _, err := st.CreateDNSVNF(ctx, NewDNSVNF{Name: "lab-dhcpv4", NetworkID: otherID}); !errors.Is(err, ErrVNFNameTaken) {
			t.Fatalf("duplicate name error = %v, want ErrVNFNameTaken", err)
		}
		createChildDNS(t, st, networkID)
		if _, err := st.CreateDNSVNF(ctx, NewDNSVNF{Name: "lab-dns-2", NetworkID: networkID}); !errors.Is(err, ErrVNFTypeTaken) {
			t.Fatalf("duplicate type error = %v, want ErrVNFTypeTaken", err)
		}
	})
}

// TestTypedParentUpdateErrors covers update validation, conflicts, missing rows,
// and typed parent write failures.
func TestTypedParentUpdateErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("wrong type and unknown id", func(t *testing.T) {
		st, networkID, _ := typedTestStore(t)
		dhcp := createChildDHCPv4(t, st, networkID)
		dhcp.Type = VNFTypeDNS
		if _, err := st.UpdateDHCPVNF(ctx, dhcp, DHCPVNFConfig{}); err == nil || !strings.Contains(err.Error(), "unsupported dhcp family") {
			t.Fatalf("dhcp family error = %v", err)
		}
		dhcp.Type = VNFTypeDHCPv4
		dhcp.ID = 9999
		if _, err := st.UpdateDHCPVNF(ctx, dhcp, DHCPVNFConfig{Gateway: "10.0.0.1/24", RangeStart: "10.0.0.2", RangeEnd: "10.0.0.3"}); !errors.Is(err, ErrVNFNotFound) {
			t.Fatalf("dhcp missing error = %v, want ErrVNFNotFound", err)
		}
		dns := createChildDNS(t, st, networkID)
		if _, err := st.UpdateDNSVNF(ctx, dhcp, DNSVNFConfig{}); !errors.Is(err, ErrVNFTypeMismatch) {
			t.Fatalf("dns wrong type error = %v, want ErrVNFTypeMismatch", err)
		}
		if _, err := st.UpdateNAT44VNF(ctx, dhcp, NAT44VNFConfig{}); !errors.Is(err, ErrVNFTypeMismatch) {
			t.Fatalf("nat44 wrong type error = %v, want ErrVNFTypeMismatch", err)
		}
		if _, err := st.UpdateBGPVNF(ctx, dhcp, BGPVNFConfig{}); !errors.Is(err, ErrVNFTypeMismatch) {
			t.Fatalf("bgp wrong type error = %v, want ErrVNFTypeMismatch", err)
		}
		_ = dns
	})

	t.Run("duplicate rename", func(t *testing.T) {
		st, networkID, _ := typedTestStore(t)
		dhcp := createChildDHCPv4(t, st, networkID)
		dns := createChildDNS(t, st, networkID)
		dns.Name = dhcp.Name
		if _, err := st.UpdateDNSVNF(ctx, dns, DNSVNFConfig{}); !errors.Is(err, ErrVNFNameTaken) {
			t.Fatalf("dns rename error = %v, want ErrVNFNameTaken", err)
		}
		nat44 := createChildNAT44(t, st, networkID)
		nat44.Name = dhcp.Name
		if _, err := st.UpdateNAT44VNF(ctx, nat44, NAT44VNFConfig{}); !errors.Is(err, ErrVNFNameTaken) {
			t.Fatalf("nat44 rename error = %v, want ErrVNFNameTaken", err)
		}
		bgp := createChildBGP(t, st, networkID)
		bgp.Name = dhcp.Name
		if _, err := st.UpdateBGPVNF(ctx, bgp, BGPVNFConfig{LocalAS: 65000}); !errors.Is(err, ErrVNFNameTaken) {
			t.Fatalf("bgp rename error = %v, want ErrVNFNameTaken", err)
		}
	})

	t.Run("missing typed parent row", func(t *testing.T) {
		st, networkID, _ := typedTestStore(t)
		type parentCase struct {
			name   string
			parent func() VNF
			table  string
			update func(vnf VNF) error
		}
		cases := []parentCase{
			{"dhcpv4", func() VNF { return createChildDHCPv4(t, st, networkID) }, "dhcpv4_vnfs",
				func(vnf VNF) error {
					_, err := st.UpdateDHCPVNF(ctx, vnf, DHCPVNFConfig{Gateway: "10.0.0.1/24", RangeStart: "10.0.0.2", RangeEnd: "10.0.0.3"})
					return err
				}},
			{"dns", func() VNF { return createChildDNS(t, st, networkID) }, "dns_vnfs",
				func(vnf VNF) error {
					_, err := st.UpdateDNSVNF(ctx, vnf, DNSVNFConfig{})
					return err
				}},
			{"nat44", func() VNF { return createChildNAT44(t, st, networkID) }, "nat44_vnfs",
				func(vnf VNF) error {
					_, err := st.UpdateNAT44VNF(ctx, vnf, NAT44VNFConfig{})
					return err
				}},
			{"bgp", func() VNF { return createChildBGP(t, st, networkID) }, "bgp_vnfs",
				func(vnf VNF) error {
					_, err := st.UpdateBGPVNF(ctx, vnf, BGPVNFConfig{LocalAS: 65000})
					return err
				}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				vnf := tc.parent()
				execInternalSQL(t, st, `DELETE FROM `+tc.table+` WHERE vnf_id = `+itoa(vnf.ID))
				if err := tc.update(vnf); !errors.Is(err, ErrVNFNotFound) {
					t.Fatalf("update without parent row = %v, want ErrVNFNotFound", err)
				}
			})
		}
	})

	t.Run("typed parent write failures", func(t *testing.T) {
		st, networkID, _ := typedTestStore(t)
		dhcp := createChildDHCPv4(t, st, networkID)
		dns := createChildDNS(t, st, networkID)
		nat44 := createChildNAT44(t, st, networkID)
		bgp := createChildBGP(t, st, networkID)
		for _, table := range []string{"dhcpv4_vnfs", "dns_vnfs", "nat44_vnfs", "bgp_vnfs"} {
			execInternalSQL(t, st, `CREATE TRIGGER fail_update_`+table+` BEFORE UPDATE ON `+table+`
				BEGIN
					SELECT RAISE(ABORT, 'update blocked');
				END`)
		}
		if _, err := st.UpdateDHCPVNF(ctx, dhcp, DHCPVNFConfig{Gateway: "10.0.0.1/24", RangeStart: "10.0.0.2", RangeEnd: "10.0.0.3"}); err == nil || !strings.Contains(err.Error(), "update dhcp vnf") {
			t.Fatalf("dhcp update error = %v", err)
		}
		if _, err := st.UpdateDNSVNF(ctx, dns, DNSVNFConfig{}); err == nil || !strings.Contains(err.Error(), "update dns vnf") {
			t.Fatalf("dns update error = %v", err)
		}
		if _, err := st.UpdateNAT44VNF(ctx, nat44, NAT44VNFConfig{}); err == nil || !strings.Contains(err.Error(), "update nat44 vnf") {
			t.Fatalf("nat44 update error = %v", err)
		}
		if _, err := st.UpdateBGPVNF(ctx, bgp, BGPVNFConfig{LocalAS: 65000}); err == nil || !strings.Contains(err.Error(), "update bgp vnf") {
			t.Fatalf("bgp update error = %v", err)
		}
	})

	t.Run("bgp check constraint", func(t *testing.T) {
		st, networkID, _ := typedTestStore(t)
		bgp := createChildBGP(t, st, networkID)
		if _, err := st.UpdateBGPVNF(ctx, bgp, BGPVNFConfig{RouterID: "10.40.0.1", PeerIP: "10.40.0.2", LocalIP: "10.40.0.1/24"}); err == nil || !strings.Contains(err.Error(), "update bgp vnf") {
			t.Fatalf("bgp constraint error = %v", err)
		}
	})
}

// TestTypedVNFReadErrors covers each typed getter's wrong-type and missing-row
// behavior and the list filters.
func TestTypedVNFReadErrors(t *testing.T) {
	st, networkID, otherID := typedTestStore(t)
	ctx := context.Background()
	dhcp := createChildDHCPv4(t, st, networkID)
	dns := createChildDNS(t, st, networkID)
	nat44 := createChildNAT44(t, st, networkID)
	bgp := createChildBGP(t, st, networkID)

	if _, err := st.GetDHCPVNF(ctx, dns.ID); !errors.Is(err, ErrVNFTypeMismatch) {
		t.Fatalf("GetDHCPVNF(dns) = %v", err)
	}
	if _, err := st.GetDNSVNF(ctx, nat44.ID); !errors.Is(err, ErrVNFTypeMismatch) {
		t.Fatalf("GetDNSVNF(nat44) = %v", err)
	}
	if _, err := st.GetNAT44VNF(ctx, bgp.ID); !errors.Is(err, ErrVNFTypeMismatch) {
		t.Fatalf("GetNAT44VNF(bgp) = %v", err)
	}
	if _, err := st.GetBGPVNF(ctx, dhcp.ID); !errors.Is(err, ErrVNFTypeMismatch) {
		t.Fatalf("GetBGPVNF(dhcp) = %v", err)
	}
	for _, get := range []func() error{
		func() error { _, err := st.GetDHCPVNF(ctx, 9999); return err },
		func() error { _, err := st.GetDNSVNF(ctx, 9999); return err },
		func() error { _, err := st.GetNAT44VNF(ctx, 9999); return err },
		func() error { _, err := st.GetBGPVNF(ctx, 9999); return err },
	} {
		if err := get(); !errors.Is(err, ErrVNFNotFound) {
			t.Fatalf("missing typed get = %v, want ErrVNFNotFound", err)
		}
	}

	// A typed VNF whose parent row is gone is not found by the typed getter.
	for _, tc := range []struct {
		table string
		id    int64
		get   func(int64) error
	}{
		{"dns_vnfs", dns.ID, func(id int64) error { _, err := st.GetDNSVNF(ctx, id); return err }},
		{"nat44_vnfs", nat44.ID, func(id int64) error { _, err := st.GetNAT44VNF(ctx, id); return err }},
		{"bgp_vnfs", bgp.ID, func(id int64) error { _, err := st.GetBGPVNF(ctx, id); return err }},
	} {
		execInternalSQL(t, st, `DELETE FROM `+tc.table+` WHERE vnf_id = `+itoa(tc.id))
		if err := tc.get(tc.id); !errors.Is(err, ErrVNFNotFound) {
			t.Fatalf("%s without parent row = %v, want ErrVNFNotFound", tc.table, err)
		}
	}

	if _, err := st.ListDHCPVNFs(ctx, nil, "dhcpv5"); err == nil {
		t.Fatal("ListDHCPVNFs accepted an unsupported family")
	}
	dhcpList, err := st.ListDHCPVNFs(ctx, &networkID, VNFTypeDHCPv4)
	if err != nil || len(dhcpList) != 1 {
		t.Fatalf("ListDHCPVNFs filter = %+v, err = %v", dhcpList, err)
	}
	emptyList, err := st.ListDHCPVNFs(ctx, &otherID, "")
	if err != nil || len(emptyList) != 0 {
		t.Fatalf("ListDHCPVNFs other network = %+v, err = %v", emptyList, err)
	}
	if list, err := st.ListDNSVNFs(ctx, &otherID); err != nil || len(list) != 0 {
		t.Fatalf("ListDNSVNFs other network = %+v, err = %v", list, err)
	}
	if list, err := st.ListNAT44VNFs(ctx, &networkID); err != nil || len(list) != 0 {
		t.Fatalf("ListNAT44VNFs = %+v, err = %v", list, err)
	}
	if list, err := st.ListBGPVNFs(ctx, &networkID); err != nil || len(list) != 0 {
		t.Fatalf("ListBGPVNFs = %+v, err = %v", list, err)
	}
}

// TestHydrateAllTypesAndMissingParents proves every type hydrates through
// GetVNF/ListVNFs and a missing typed parent row is reported.
func TestHydrateAllTypesAndMissingParents(t *testing.T) {
	st, networkID, _ := typedTestStore(t)
	ctx := context.Background()
	dhcp4 := createChildDHCPv4(t, st, networkID)
	dhcp6 := createChildDHCPv6(t, st, networkID)
	dns := createChildDNS(t, st, networkID)
	nat44 := createChildNAT44(t, st, networkID)
	bgp := createChildBGP(t, st, networkID)
	if _, err := st.CreateDHCPStaticAssignment(ctx, dhcp4.ID, DHCPStaticAssignment{MAC: "02:00:00:00:00:01", IP: "10.20.0.50"}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if _, err := st.CreateDHCPOption(ctx, dhcp6.ID, DHCPOption{Code: 23, Format: "ipv6", Value: "fd00::2"}); err != nil {
		t.Fatalf("create option: %v", err)
	}
	if _, err := st.CreateDNSVNFRecord(ctx, dns.ID, DNSRecord{Name: "host.lab.test", Type: "A", Value: "10.20.0.50", TTL: 100}); err != nil {
		t.Fatalf("create dns record: %v", err)
	}
	if _, err := st.CreateDNSVNFForwarder(ctx, dns.ID, DNSForwarder{Address: "1.1.1.1"}); err != nil {
		t.Fatalf("create forwarder: %v", err)
	}
	if _, err := st.CreateNAT44DNATRule(ctx, nat44.ID, DNATRule{Protocol: "tcp", ExternalPort: 80, LocalIP: "10.30.0.10", LocalPort: 8080}); err != nil {
		t.Fatalf("create dnat rule: %v", err)
	}
	if _, err := st.CreateBGPVNFAdvertisedPrefix(ctx, bgp.ID, BGPPrefix{Prefix: "10.50.0.0/24"}); err != nil {
		t.Fatalf("create advertised prefix: %v", err)
	}

	for _, id := range []int64{dhcp4.ID, dhcp6.ID, dns.ID, nat44.ID, bgp.ID} {
		if _, err := st.GetVNF(ctx, id); err != nil {
			t.Fatalf("hydrate vnf %d: %v", id, err)
		}
	}
	all, err := st.ListVNFs(ctx, nil)
	if err != nil || len(all) != 5 {
		t.Fatalf("ListVNFs = %d, err = %v", len(all), err)
	}

	// A missing typed parent row is a hard hydration error.
	for _, tc := range []struct {
		table string
		id    int64
	}{
		{"dhcpv4_vnfs", dhcp4.ID},
		{"dhcpv6_vnfs", dhcp6.ID},
		{"dns_vnfs", dns.ID},
		{"nat44_vnfs", nat44.ID},
		{"bgp_vnfs", bgp.ID},
	} {
		t.Run(tc.table, func(t *testing.T) {
			execInternalSQL(t, st, `DELETE FROM `+tc.table+` WHERE vnf_id = `+itoa(tc.id))
			if _, err := st.GetVNF(ctx, tc.id); err == nil || !strings.Contains(err.Error(), "has no parent row") {
				t.Fatalf("hydration error = %v, want a missing parent row message", err)
			}
		})
	}
	if _, err := st.ListVNFs(ctx, nil); err == nil {
		t.Fatal("ListVNFs ignored a missing typed parent row")
	}

	// An identity row of an unknown type cannot hydrate.
	var unknown VNF
	unknown.Type = "wireguard"
	if err := st.hydrateVNF(ctx, &unknown); err == nil || !strings.Contains(err.Error(), "unsupported vnf type") {
		t.Fatalf("unknown-type hydration error = %v", err)
	}
}

// TestTypedStoreErrorsAfterClose covers the query and write error branches that
// need a broken database handle.
func TestTypedStoreErrorsAfterClose(t *testing.T) {
	st, vnf := openInternalVNFStore(t)
	ctx := context.Background()
	if err := st.db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	if _, err := st.GetVNF(ctx, vnf.ID); err == nil {
		t.Fatal("GetVNF succeeded with a closed database")
	}
	if _, err := st.ListVNFs(ctx, nil); err == nil {
		t.Fatal("ListVNFs succeeded with a closed database")
	}
	if _, err := st.ListDHCPVNFs(ctx, nil, ""); err == nil {
		t.Fatal("ListDHCPVNFs succeeded with a closed database")
	}
	if _, err := st.ListDNSVNFs(ctx, nil); err == nil {
		t.Fatal("ListDNSVNFs succeeded with a closed database")
	}
	if _, err := st.ListNAT44VNFs(ctx, nil); err == nil {
		t.Fatal("ListNAT44VNFs succeeded with a closed database")
	}
	if _, err := st.ListBGPVNFs(ctx, nil); err == nil {
		t.Fatal("ListBGPVNFs succeeded with a closed database")
	}
	if _, err := st.GetDHCPVNF(ctx, vnf.ID); err == nil {
		t.Fatal("GetDHCPVNF succeeded with a closed database")
	}
	if _, err := st.GetDNSVNF(ctx, vnf.ID); err == nil {
		t.Fatal("GetDNSVNF succeeded with a closed database")
	}
	if _, err := st.GetNAT44VNF(ctx, vnf.ID); err == nil {
		t.Fatal("GetNAT44VNF succeeded with a closed database")
	}
	if _, err := st.GetBGPVNF(ctx, vnf.ID); err == nil {
		t.Fatal("GetBGPVNF succeeded with a closed database")
	}
	if _, err := st.UpdateVNFIdentity(ctx, vnf); err == nil {
		t.Fatal("UpdateVNFIdentity succeeded with a closed database")
	}
	if err := st.DeleteVNF(ctx, vnf.ID); err == nil {
		t.Fatal("DeleteVNF succeeded with a closed database")
	}
	if err := st.SetDHCPVNFNetworkBindings(ctx, vnf.ID, "10.0.0.1/24", "10.0.0.2/24"); err == nil {
		t.Fatal("SetDHCPVNFNetworkBindings succeeded with a closed database")
	}
	if _, err := st.CreateDHCPVNF(ctx, NewDHCPVNF{
		Name: "new", NetworkID: vnf.NetworkID, Family: VNFTypeDHCPv4,
		Config: DHCPVNFConfig{Gateway: "10.0.0.1/24", RangeStart: "10.0.0.2", RangeEnd: "10.0.0.3"},
	}); err == nil {
		t.Fatal("CreateDHCPVNF succeeded with a closed database")
	}
}

// TestUpdateVNFIdentityConflicts proves the identity write reports a name
// conflict and a missing row.
func TestUpdateVNFIdentityConflicts(t *testing.T) {
	st, networkID, _ := typedTestStore(t)
	ctx := context.Background()
	dhcp := createChildDHCPv4(t, st, networkID)
	dns := createChildDNS(t, st, networkID)
	renamed := dns
	renamed.Name = dhcp.Name
	if _, err := st.UpdateVNFIdentity(ctx, renamed); !errors.Is(err, ErrVNFNameTaken) {
		t.Fatalf("identity rename error = %v, want ErrVNFNameTaken", err)
	}
	if _, err := st.UpdateVNFIdentity(ctx, VNF{ID: 9999, Name: "missing"}); !errors.Is(err, ErrVNFNotFound) {
		t.Fatalf("identity update missing = %v, want ErrVNFNotFound", err)
	}
	if err := st.DeleteVNF(ctx, 9999); !errors.Is(err, ErrVNFNotFound) {
		t.Fatalf("delete missing vnf = %v, want ErrVNFNotFound", err)
	}
}

// TestSetDHCPVNFNetworkBindingsWriteErrors covers the two write failure branches
// of the derived binding setter.
func TestSetDHCPVNFNetworkBindingsWriteErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("parent update", func(t *testing.T) {
		st, vnf := openInternalVNFStore(t)
		execInternalSQL(t, st, `CREATE TRIGGER fail_binding_update BEFORE UPDATE ON dhcpv4_vnfs
			BEGIN
				SELECT RAISE(ABORT, 'update blocked');
			END`)
		if err := st.SetDHCPVNFNetworkBindings(ctx, vnf.ID, "10.0.0.1/24", "10.0.0.2/24"); err == nil ||
			!strings.Contains(err.Error(), "update dhcp vnf bindings") {
			t.Fatalf("binding update error = %v", err)
		}
	})
	t.Run("parent touch", func(t *testing.T) {
		st, vnf := openInternalVNFStore(t)
		execInternalSQL(t, st, `CREATE TRIGGER fail_binding_touch BEFORE UPDATE ON vnfs
			BEGIN
				SELECT RAISE(ABORT, 'update blocked');
			END`)
		if err := st.SetDHCPVNFNetworkBindings(ctx, vnf.ID, "10.0.0.1/24", "10.0.0.2/24"); err == nil ||
			!strings.Contains(err.Error(), "touch vnf") {
			t.Fatalf("binding touch error = %v", err)
		}
	})
}

// itoa formats an int64 for the small SQL snippets in these tests.
func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for value > 0 {
		pos--
		buf[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[pos:])
}
