package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// hydrateVNF fills vnf.Config from the VNF's typed parent and child tables. The
// result is the read-only runtime shape; it is never written back as a union.
func (s *Store) hydrateVNF(ctx context.Context, vnf *VNF) error {
	switch vnf.Type {
	case VNFTypeDHCPv4:
		return s.hydrateDHCPv4(ctx, vnf)
	case VNFTypeDHCPv6:
		return s.hydrateDHCPv6(ctx, vnf)
	case VNFTypeDNS:
		return s.hydrateDNS(ctx, vnf)
	case VNFTypeNAT44:
		return s.hydrateNAT44(ctx, vnf)
	case VNFTypeBGP:
		return s.hydrateBGP(ctx, vnf)
	default:
		return fmt.Errorf("unsupported vnf type %q", vnf.Type)
	}
}

// hydrateDHCPv4 loads the DHCPv4 scalar config, static assignments, and options.
func (s *Store) hydrateDHCPv4(ctx context.Context, vnf *VNF) error {
	var cfg DHCPVNFConfig
	err := s.db.QueryRowContext(ctx, `
		SELECT gateway, range_start, range_end, lease_time, dns, bind_ip,
		       transport, COALESCE(dns_vnf_id, 0), dns_domain, dns_ttl
		FROM dhcpv4_vnfs WHERE vnf_id = ?`, vnf.ID,
	).Scan(&cfg.Gateway, &cfg.RangeStart, &cfg.RangeEnd, &cfg.LeaseTime,
		&cfg.DNS, &cfg.BindIP, &cfg.Transport, &cfg.DNSVNFID, &cfg.DNSDomain,
		&cfg.DNSTTL)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("dhcpv4 vnf %d has no parent row", vnf.ID)
	}
	if err != nil {
		return fmt.Errorf("read dhcpv4 vnf %d: %w", vnf.ID, err)
	}

	vnf.Config = VNFConfig{
		Gateway:    cfg.Gateway,
		RangeStart: cfg.RangeStart,
		RangeEnd:   cfg.RangeEnd,
		LeaseTime:  cfg.LeaseTime,
		DNS:        cfg.DNS,
		BindIP:     cfg.BindIP,
		Transport:  cfg.Transport,
		DNSVNFID:   cfg.DNSVNFID,
		DNSDomain:  cfg.DNSDomain,
		DNSTTL:     cfg.DNSTTL,
	}

	assignments, err := s.ListDHCPStaticAssignments(ctx, vnf.ID)
	if err != nil {
		return err
	}
	options, err := s.ListDHCPOptions(ctx, vnf.ID)
	if err != nil {
		return err
	}
	vnf.Config.StaticAssignments = assignments
	vnf.Config.DHCPOptions = options
	return nil
}

// hydrateDHCPv6 loads the DHCPv6 scalar config and options.
func (s *Store) hydrateDHCPv6(ctx context.Context, vnf *VNF) error {
	var cfg DHCPVNFConfig
	err := s.db.QueryRowContext(ctx, `
		SELECT gateway, range_start, range_end, lease_time, dns, bind_ip,
		       transport, COALESCE(dns_vnf_id, 0), dns_domain, dns_ttl
		FROM dhcpv6_vnfs WHERE vnf_id = ?`, vnf.ID,
	).Scan(&cfg.Gateway, &cfg.RangeStart, &cfg.RangeEnd, &cfg.LeaseTime,
		&cfg.DNS, &cfg.BindIP, &cfg.Transport, &cfg.DNSVNFID, &cfg.DNSDomain,
		&cfg.DNSTTL)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("dhcpv6 vnf %d has no parent row", vnf.ID)
	}
	if err != nil {
		return fmt.Errorf("read dhcpv6 vnf %d: %w", vnf.ID, err)
	}

	vnf.Config = VNFConfig{
		Gateway:    cfg.Gateway,
		RangeStart: cfg.RangeStart,
		RangeEnd:   cfg.RangeEnd,
		LeaseTime:  cfg.LeaseTime,
		DNS:        cfg.DNS,
		BindIP:     cfg.BindIP,
		Transport:  cfg.Transport,
		DNSVNFID:   cfg.DNSVNFID,
		DNSDomain:  cfg.DNSDomain,
		DNSTTL:     cfg.DNSTTL,
	}

	options, err := s.ListDHCPOptions(ctx, vnf.ID)
	if err != nil {
		return err
	}
	vnf.Config.DHCPOptions = options
	return nil
}

// hydrateDNS loads the DNS forwarding flag, records, and ordered forwarders.
func (s *Store) hydrateDNS(ctx context.Context, vnf *VNF) error {
	var forward int64
	err := s.db.QueryRowContext(ctx, `SELECT forward FROM dns_vnfs WHERE vnf_id = ?`, vnf.ID).Scan(&forward)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("dns vnf %d has no parent row", vnf.ID)
	}
	if err != nil {
		return fmt.Errorf("read dns vnf %d: %w", vnf.ID, err)
	}
	records, err := s.ListDNSVNFRecords(ctx, vnf.ID)
	if err != nil {
		return err
	}
	forwarders, err := s.ListDNSVNFForwarders(ctx, vnf.ID)
	if err != nil {
		return err
	}
	addresses := make([]string, 0, len(forwarders))
	for _, forwarder := range forwarders {
		addresses = append(addresses, forwarder.Address)
	}
	vnf.Config.DNSForward = forward != 0
	vnf.Config.DNSRecords = records
	vnf.Config.DNSForwarders = addresses
	return nil
}

// hydrateNAT44 loads the NAT44 scalar config and DNAT rules.
func (s *Store) hydrateNAT44(ctx context.Context, vnf *VNF) error {
	var cfg NAT44VNFConfig
	err := s.db.QueryRowContext(ctx, `
		SELECT gateway, outside_interface, outside_address, outside_gateway, nat44_pool
		FROM nat44_vnfs WHERE vnf_id = ?`, vnf.ID,
	).Scan(&cfg.Gateway, &cfg.OutsideInterface, &cfg.OutsideAddress,
		&cfg.OutsideGateway, &cfg.NAT44Pool)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("nat44 vnf %d has no parent row", vnf.ID)
	}
	if err != nil {
		return fmt.Errorf("read nat44 vnf %d: %w", vnf.ID, err)
	}
	rules, err := s.ListNAT44DNATRules(ctx, vnf.ID)
	if err != nil {
		return err
	}
	vnf.Config = VNFConfig{
		Gateway:          cfg.Gateway,
		OutsideInterface: cfg.OutsideInterface,
		OutsideAddress:   cfg.OutsideAddress,
		OutsideGateway:   cfg.OutsideGateway,
		NAT44Pool:        cfg.NAT44Pool,
		DNATRules:        rules,
	}
	return nil
}

// hydrateBGP loads the BGP scalar config (including the write-only password)
// and advertised prefixes.
func (s *Store) hydrateBGP(ctx context.Context, vnf *VNF) error {
	var (
		cfg     BGPVNFConfig
		localAS int64
		peerAS  int64
		ttl     int64
		passive int64
		tableID int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT transport, local_as, router_id, peer_as, peer_ip, local_ip,
		       peer_gateway, multihop_ttl, password, passive, target_table_id, next_hop
		FROM bgp_vnfs WHERE vnf_id = ?`, vnf.ID,
	).Scan(&cfg.Transport, &localAS, &cfg.RouterID, &peerAS, &cfg.PeerIP,
		&cfg.LocalIP, &cfg.PeerGateway, &ttl, &cfg.Password, &passive,
		&tableID, &cfg.NextHop)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("bgp vnf %d has no parent row", vnf.ID)
	}
	if err != nil {
		return fmt.Errorf("read bgp vnf %d: %w", vnf.ID, err)
	}
	prefixes, err := s.ListBGPVNFAdvertisedPrefixes(ctx, vnf.ID)
	if err != nil {
		return err
	}
	advertised := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		advertised = append(advertised, prefix.Prefix)
	}
	cfg.LocalAS = uint32(localAS)
	cfg.PeerAS = uint32(peerAS)
	cfg.MultihopTTL = uint32(ttl)
	cfg.Passive = passive != 0
	cfg.TargetTableID = uint32(tableID)
	vnf.Config = VNFConfig{
		Transport:         cfg.Transport,
		LocalAS:           cfg.LocalAS,
		RouterID:          cfg.RouterID,
		PeerAS:            cfg.PeerAS,
		PeerIP:            cfg.PeerIP,
		LocalIP:           cfg.LocalIP,
		PeerGateway:       cfg.PeerGateway,
		MultihopTTL:       cfg.MultihopTTL,
		Password:          cfg.Password,
		Passive:           cfg.Passive,
		TargetTableID:     cfg.TargetTableID,
		NextHop:           cfg.NextHop,
		AdvertisePrefixes: advertised,
	}
	return nil
}
