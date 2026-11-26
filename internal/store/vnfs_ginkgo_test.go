package store_test

import (
	"context"
	"errors"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/voidstarit/rookery/internal/store"
)

var _ = Describe("Typed VNF store", func() {
	var (
		st       *store.Store
		ctx      context.Context
		network  store.Network
		other    store.Network
		nextBDID uint32
	)

	openStore := func() *store.Store {
		st, err := store.Open(filepath.Join(GinkgoT().TempDir(), "vnfs.db"))
		Expect(err).NotTo(HaveOccurred())
		return st
	}
	createNetwork := func(name string) store.Network {
		nextBDID++
		created, err := st.CreateNetwork(ctx, store.Network{Name: name, BridgeDomainID: nextBDID})
		Expect(err).NotTo(HaveOccurred())
		return created
	}
	createDHCPv4 := func(name string, networkID int64) store.VNF {
		vnf, err := st.CreateDHCPVNF(ctx, store.NewDHCPVNF{
			Name:      name,
			NetworkID: networkID,
			Family:    store.VNFTypeDHCPv4,
			Config: store.DHCPVNFConfig{
				Gateway:    "10.20.0.1/24",
				RangeStart: "10.20.0.100",
				RangeEnd:   "10.20.0.200",
				LeaseTime:  "12h",
				Transport:  "shared",
			},
		})
		Expect(err).NotTo(HaveOccurred())
		return vnf
	}

	BeforeEach(func() {
		st = openStore()
		ctx = context.Background()
		nextBDID = 100
		network = createNetwork("lab")
		other = createNetwork("other")
	})

	AfterEach(func() {
		Expect(st.Close()).To(Succeed())
	})

	Context("parent CRUD", func() {
		It("creates, reads, lists, updates, and deletes each type", func() {
			dhcp, err := st.CreateDHCPVNF(ctx, store.NewDHCPVNF{
				Name:      "lab-dhcpv4",
				NetworkID: network.ID,
				Family:    store.VNFTypeDHCPv4,
				Enabled:   true,
				Config: store.DHCPVNFConfig{
					Gateway:    "10.20.0.1/24",
					RangeStart: "10.20.0.100",
					RangeEnd:   "10.20.0.200",
					LeaseTime:  "12h",
					Transport:  "shared",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(dhcp.NetworkName).To(Equal("lab"))
			Expect(dhcp.Config.Gateway).To(Equal("10.20.0.1/24"))

			dns, err := st.CreateDNSVNF(ctx, store.NewDNSVNF{
				Name:      "lab-dns",
				NetworkID: network.ID,
				Config:    store.DNSVNFConfig{Forward: true},
			})
			Expect(err).NotTo(HaveOccurred())
			nat44, err := st.CreateNAT44VNF(ctx, store.NewNAT44VNF{
				Name:      "lab-nat44",
				NetworkID: network.ID,
				Config: store.NAT44VNFConfig{
					Gateway:          "10.30.0.1/24",
					OutsideInterface: "eth0",
					OutsideAddress:   "192.0.2.10/24",
					OutsideGateway:   "192.0.2.1",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			bgp, err := st.CreateBGPVNF(ctx, store.NewBGPVNF{
				Name:      "lab-bgp",
				NetworkID: network.ID,
				Config: store.BGPVNFConfig{
					Transport:   "tap",
					LocalAS:     65000,
					RouterID:    "10.40.0.1",
					PeerAS:      65001,
					PeerIP:      "10.40.0.2",
					LocalIP:     "10.40.0.1/24",
					MultihopTTL: 64,
					Password:    "s3cret",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			dhcpList, err := st.ListDHCPVNFs(ctx, &network.ID, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(dhcpList).To(HaveLen(1))
			Expect(dhcpList[0].Family).To(Equal(store.VNFTypeDHCPv4))
			dnsList, err := st.ListDNSVNFs(ctx, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(dnsList).To(HaveLen(1))
			natList, err := st.ListNAT44VNFs(ctx, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(natList).To(HaveLen(1))
			bgpList, err := st.ListBGPVNFs(ctx, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(bgpList).To(HaveLen(1))
			Expect(bgpList[0].Config.Password).To(BeEmpty(), "read model must not expose the BGP password")

			updated, err := st.UpdateDHCPVNF(ctx, dhcp, store.DHCPVNFConfig{
				Gateway:    "10.20.0.1/24",
				RangeStart: "10.20.0.100",
				RangeEnd:   "10.20.0.250",
				LeaseTime:  "6h",
				Transport:  "tap",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Config.RangeEnd).To(Equal("10.20.0.250"))
			Expect(updated.Config.LeaseTime).To(Equal("6h"))

			Expect(st.DeleteVNF(ctx, dns.ID)).To(Succeed())
			Expect(st.DeleteVNF(ctx, nat44.ID)).To(Succeed())
			Expect(st.DeleteVNF(ctx, bgp.ID)).To(Succeed())
			_, err = st.GetDNSVNF(ctx, dns.ID)
			Expect(errors.Is(err, store.ErrVNFNotFound)).To(BeTrue())
		})

		It("returns a type mismatch for a typed get of another type", func() {
			dhcp := createDHCPv4("lab-dhcpv4", network.ID)
			_, err := st.GetDNSVNF(ctx, dhcp.ID)
			Expect(errors.Is(err, store.ErrVNFTypeMismatch)).To(BeTrue())
			_, err = st.GetDHCPVNF(ctx, dhcp.ID)
			Expect(err).NotTo(HaveOccurred())
			_, err = st.GetDHCPVNF(ctx, 9999)
			Expect(errors.Is(err, store.ErrVNFNotFound)).To(BeTrue())
		})

		It("enforces one VNF of each type per network", func() {
			createDHCPv4("lab-dhcpv4", network.ID)
			_, err := st.CreateDHCPVNF(ctx, store.NewDHCPVNF{
				Name:      "lab-dhcpv4-second",
				NetworkID: network.ID,
				Family:    store.VNFTypeDHCPv4,
				Config: store.DHCPVNFConfig{
					Gateway:    "10.20.0.1/24",
					RangeStart: "10.20.0.100",
					RangeEnd:   "10.20.0.200",
				},
			})
			Expect(errors.Is(err, store.ErrVNFTypeTaken)).To(BeTrue())

			// A second network may have the same type, and one network may have
			// a different type.
			createDHCPv4("other-dhcpv4", other.ID)
			_, err = st.CreateDHCPVNF(ctx, store.NewDHCPVNF{
				Name:      "lab-dhcpv6",
				NetworkID: network.ID,
				Family:    store.VNFTypeDHCPv6,
				Config: store.DHCPVNFConfig{
					Gateway:    "fd00::1/64",
					RangeStart: "fd00::100",
					RangeEnd:   "fd00::200",
					Transport:  "tap",
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("reports duplicate names as a name conflict", func() {
			createDHCPv4("lab-dhcpv4", network.ID)
			_, err := st.CreateDNSVNF(ctx, store.NewDNSVNF{Name: "lab-dhcpv4", NetworkID: other.ID})
			Expect(errors.Is(err, store.ErrVNFNameTaken)).To(BeTrue())
		})
	})

	Context("child rows", func() {
		var dhcp store.VNF

		BeforeEach(func() {
			dhcp = createDHCPv4("lab-dhcpv4", network.ID)
		})

		It("creates, updates, lists, and deletes static assignments", func() {
			first, err := st.CreateDHCPStaticAssignment(ctx, dhcp.ID, store.DHCPStaticAssignment{
				MAC: "02:00:00:00:00:01", IP: "10.20.0.50",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(first.ID).To(BeNumerically(">", 0))

			_, err = st.CreateDHCPStaticAssignment(ctx, dhcp.ID, store.DHCPStaticAssignment{
				MAC: "02:00:00:00:00:01", IP: "10.20.0.51",
			})
			Expect(errors.Is(err, store.ErrVNFChildTaken)).To(BeTrue(), "duplicate MAC must conflict")

			// The storage collation makes the MAC key case-insensitive.
			_, err = st.CreateDHCPStaticAssignment(ctx, dhcp.ID, store.DHCPStaticAssignment{
				MAC: "02:00:00:00:00:01", IP: "10.20.0.52",
			})
			Expect(errors.Is(err, store.ErrVNFChildTaken)).To(BeTrue())

			updated, err := st.UpdateDHCPStaticAssignment(ctx, dhcp.ID, first.ID, store.DHCPStaticAssignment{
				MAC: "02:00:00:00:00:02", IP: "10.20.0.50",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.MAC).To(Equal("02:00:00:00:00:02"))

			items, err := st.ListDHCPStaticAssignments(ctx, dhcp.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(items).To(HaveLen(1))

			Expect(st.DeleteDHCPStaticAssignment(ctx, dhcp.ID, first.ID)).To(Succeed())
			items, err = st.ListDHCPStaticAssignments(ctx, dhcp.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(items).To(BeEmpty())
			_, err = st.GetDHCPStaticAssignment(ctx, dhcp.ID, first.ID)
			Expect(errors.Is(err, store.ErrVNFChildNotFound)).To(BeTrue())
		})

		It("bumps the parent timestamp on every child write", func() {
			before, err := st.GetVNF(ctx, dhcp.ID)
			Expect(err).NotTo(HaveOccurred())
			assignment, err := st.CreateDHCPStaticAssignment(ctx, dhcp.ID, store.DHCPStaticAssignment{
				MAC: "02:00:00:00:00:01", IP: "10.20.0.50",
			})
			Expect(err).NotTo(HaveOccurred())
			after, err := st.GetVNF(ctx, dhcp.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(after.UpdatedAt.After(before.UpdatedAt) || after.UpdatedAt.Equal(before.UpdatedAt)).To(BeTrue())
			Expect(after.Config.StaticAssignments).To(HaveLen(1))
			Expect(after.Config.StaticAssignments[0].ID).To(Equal(assignment.ID))
		})

		It("refuses DHCPv4 children on another family or type", func() {
			dhcp6, err := st.CreateDHCPVNF(ctx, store.NewDHCPVNF{
				Name:      "lab-dhcpv6",
				NetworkID: network.ID,
				Family:    store.VNFTypeDHCPv6,
				Config: store.DHCPVNFConfig{
					Gateway:    "fd00::1/64",
					RangeStart: "fd00::100",
					RangeEnd:   "fd00::200",
					Transport:  "tap",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			_, err = st.CreateDHCPStaticAssignment(ctx, dhcp6.ID, store.DHCPStaticAssignment{
				MAC: "02:00:00:00:00:01", IP: "fd00::50",
			})
			Expect(errors.Is(err, store.ErrVNFTypeMismatch)).To(BeTrue())

			// DHCP options are valid on both DHCP families.
			_, err = st.CreateDHCPOption(ctx, dhcp6.ID, store.DHCPOption{Code: 23, Format: "ipv6", Value: "fd00::2"})
			Expect(err).NotTo(HaveOccurred())
			_, err = st.CreateDHCPOption(ctx, dhcp.ID, store.DHCPOption{Code: 15, Format: "string", Value: "lab.test"})
			Expect(err).NotTo(HaveOccurred())
		})

		It("stores DNS records and ordered forwarders", func() {
			dns, err := st.CreateDNSVNF(ctx, store.NewDNSVNF{Name: "lab-dns", NetworkID: network.ID})
			Expect(err).NotTo(HaveOccurred())
			_, err = st.CreateDNSVNFRecord(ctx, dns.ID, store.DNSRecord{
				Name: "host.lab.test", Type: "A", Value: "10.20.0.50", TTL: 300,
			})
			Expect(err).NotTo(HaveOccurred())
			_, err = st.CreateDNSVNFRecord(ctx, dns.ID, store.DNSRecord{
				Name: "host.lab.test", Type: "A", Value: "10.20.0.50", TTL: 300,
			})
			Expect(errors.Is(err, store.ErrVNFChildTaken)).To(BeTrue(), "duplicate record must conflict")
			_, err = st.CreateDNSVNFForwarder(ctx, dns.ID, store.DNSForwarder{Address: "1.1.1.1", Position: 0})
			Expect(err).NotTo(HaveOccurred())
			_, err = st.CreateDNSVNFForwarder(ctx, dns.ID, store.DNSForwarder{Address: "9.9.9.9", Position: 1})
			Expect(err).NotTo(HaveOccurred())
			forwarders, err := st.ListDNSVNFForwarders(ctx, dns.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(forwarders).To(HaveLen(2))
			Expect(forwarders[0].Address).To(Equal("1.1.1.1"))

			hydrated, err := st.GetVNF(ctx, dns.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(hydrated.Config.DNSRecords).To(HaveLen(1))
			Expect(hydrated.Config.DNSForwarders).To(Equal([]string{"1.1.1.1", "9.9.9.9"}))
		})

		It("stores DNAT rules and advertised prefixes with unique keys", func() {
			nat44, err := st.CreateNAT44VNF(ctx, store.NewNAT44VNF{
				Name: "lab-nat44", NetworkID: network.ID,
				Config: store.NAT44VNFConfig{Gateway: "10.30.0.1/24", OutsideInterface: "eth0"},
			})
			Expect(err).NotTo(HaveOccurred())
			rule, err := st.CreateNAT44DNATRule(ctx, nat44.ID, store.DNATRule{
				Protocol: "tcp", ExternalPort: 80, LocalIP: "10.30.0.10", LocalPort: 8080,
			})
			Expect(err).NotTo(HaveOccurred())
			_, err = st.CreateNAT44DNATRule(ctx, nat44.ID, store.DNATRule{
				Protocol: "tcp", ExternalPort: 80, LocalIP: "10.30.0.11", LocalPort: 8081,
			})
			Expect(errors.Is(err, store.ErrVNFChildTaken)).To(BeTrue())
			// The same external port on the other protocol is allowed.
			_, err = st.CreateNAT44DNATRule(ctx, nat44.ID, store.DNATRule{
				Protocol: "udp", ExternalPort: 80, LocalIP: "10.30.0.11", LocalPort: 8081,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(st.DeleteNAT44DNATRule(ctx, nat44.ID, rule.ID)).To(Succeed())

			bgp, err := st.CreateBGPVNF(ctx, store.NewBGPVNF{
				Name: "lab-bgp", NetworkID: network.ID,
				Config: store.BGPVNFConfig{
					Transport: "tap", LocalAS: 65000, RouterID: "10.40.0.1",
					PeerAS: 65001, PeerIP: "10.40.0.2", LocalIP: "10.40.0.1/24",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			_, err = st.CreateBGPVNFAdvertisedPrefix(ctx, bgp.ID, store.BGPPrefix{Prefix: "10.50.0.0/24"})
			Expect(err).NotTo(HaveOccurred())
			_, err = st.CreateBGPVNFAdvertisedPrefix(ctx, bgp.ID, store.BGPPrefix{Prefix: "10.50.0.0/24"})
			Expect(errors.Is(err, store.ErrVNFChildTaken)).To(BeTrue())
			prefixes, err := st.ListBGPVNFAdvertisedPrefixes(ctx, bgp.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(prefixes).To(HaveLen(1))
		})

		It("keeps child rows out of the read model counts until they exist", func() {
			nat44, err := st.CreateNAT44VNF(ctx, store.NewNAT44VNF{
				Name: "lab-nat44", NetworkID: network.ID,
				Config: store.NAT44VNFConfig{Gateway: "10.30.0.1/24", OutsideInterface: "eth0"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(nat44.Config.Gateway).To(Equal("10.30.0.1/24"))
			read, err := st.GetNAT44VNF(ctx, nat44.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(read.DNATRuleCount).To(Equal(0))
		})
	})
})
