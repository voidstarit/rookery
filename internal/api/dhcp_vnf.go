package api

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"strings"

	"github.com/miekg/dns"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/netutil"
	"github.com/voidstarit/rookery/internal/store"
)

const maxVNFRangeSize = 65536

// NormalizeDHCPVNFConfig validates and normalizes the scalar configuration of a
// DHCPv4 or DHCPv6 VNF. Static assignments and options are child rows and are
// validated on their own write path.
func (s *Server) NormalizeDHCPVNFConfig(family string, config store.DHCPVNFConfig) (store.DHCPVNFConfig, error) {
	if family != store.VNFTypeDHCPv4 && family != store.VNFTypeDHCPv6 {
		return store.DHCPVNFConfig{}, fmt.Errorf("unsupported DHCP family %q", family)
	}
	gateway := strings.TrimSpace(config.Gateway)
	rangeStart := strings.TrimSpace(config.RangeStart)
	rangeEnd := strings.TrimSpace(config.RangeEnd)
	if gateway == "" {
		return store.DHCPVNFConfig{}, errors.New("config.gateway is required")
	}
	if rangeStart == "" || rangeEnd == "" {
		return store.DHCPVNFConfig{}, errors.New("config.range_start and config.range_end are required")
	}
	transport := strings.ToLower(strings.TrimSpace(config.Transport))
	switch transport {
	case "", vnfTransportShared, vnfTransportDedicated, vnfTransportTap:
	case "memif":
		transport = vnfTransportDedicated
	default:
		return store.DHCPVNFConfig{}, fmt.Errorf("config.transport must be shared, dedicated, or tap, got %q", config.Transport)
	}
	if family == store.VNFTypeDHCPv6 {
		// DHCPv6 has no memif transport yet; keep it on the tap path.
		transport = vnfTransportTap
	}
	leaseTime, err := normalizeLeaseTime(config.LeaseTime)
	if err != nil {
		return store.DHCPVNFConfig{}, err
	}
	var normalizedGateway, normalizedStart, normalizedEnd, dns string
	if family == store.VNFTypeDHCPv4 {
		normalizedGateway, normalizedStart, normalizedEnd, dns, err = normalizeDHCPv4Config(gateway, rangeStart, rangeEnd, config.DNS)
	} else {
		normalizedGateway, normalizedStart, normalizedEnd, dns, err = normalizeDHCPv6Config(gateway, rangeStart, rangeEnd, config.DNS)
	}
	if err != nil {
		return store.DHCPVNFConfig{}, err
	}
	bindIP, err := normalizeDHCPBindIP(config.BindIP, normalizedGateway, family == store.VNFTypeDHCPv6)
	if err != nil {
		return store.DHCPVNFConfig{}, err
	}
	if bindIP != "" && family == store.VNFTypeDHCPv4 {
		bind, _, _ := net.ParseCIDR(bindIP)
		start := net.ParseIP(normalizedStart).To4()
		end := net.ParseIP(normalizedEnd).To4()
		if start != nil && end != nil && bytesCompareIPv4(start, bind) <= 0 && bytesCompareIPv4(end, bind) >= 0 {
			return store.DHCPVNFConfig{}, errors.New("bind_ip cannot be inside the DHCP range")
		}
	}
	dnsDomain, dnsTTL, err := normalizeDNSIntegration(config.DNSVNFID, config.DNSDomain, config.DNSTTL)
	if err != nil {
		return store.DHCPVNFConfig{}, err
	}
	return store.DHCPVNFConfig{
		Gateway:    normalizedGateway,
		RangeStart: normalizedStart,
		RangeEnd:   normalizedEnd,
		LeaseTime:  leaseTime,
		DNS:        dns,
		BindIP:     bindIP,
		Transport:  transport,
		DNSVNFID:   config.DNSVNFID,
		DNSDomain:  dnsDomain,
		DNSTTL:     dnsTTL,
	}, nil
}

// NormalizeDHCPStaticAssignment validates one DHCPv4 assignment against the
// parent's gateway, service bind address, and network CIDR.
func (s *Server) NormalizeDHCPStaticAssignment(ctx context.Context, vnf store.VNF, item store.DHCPStaticAssignment) (store.DHCPStaticAssignment, error) {
	if vnf.Type != store.VNFTypeDHCPv4 {
		return store.DHCPStaticAssignment{}, errors.New("static assignments are supported for DHCPv4 VNFs only")
	}
	gatewayIP, subnet, err := net.ParseCIDR(vnf.Config.Gateway)
	if err != nil {
		return store.DHCPStaticAssignment{}, fmt.Errorf("invalid DHCP gateway: %w", err)
	}
	normalized, err := normalizeDHCPv4StaticAssignments([]store.DHCPStaticAssignment{item}, gatewayIP.To4(), subnet, vnf.Config.BindIP)
	if err != nil {
		return store.DHCPStaticAssignment{}, err
	}
	network, err := s.store.GetNetwork(ctx, vnf.NetworkID)
	if err != nil {
		return store.DHCPStaticAssignment{}, fmt.Errorf("read VNF network: %w", err)
	}
	if network.IPv4CIDR != "" {
		if !netutil.Contains(network.IPv4CIDR, normalized[0].IP) || netutil.IsReserved(network.IPv4CIDR, normalized[0].IP) {
			return store.DHCPStaticAssignment{}, fmt.Errorf(
				"static assignment %s (%s) must stay inside %s and avoid the first %d reserved addresses",
				normalized[0].MAC, normalized[0].IP, network.IPv4CIDR, netutil.ReservedHosts)
		}
	}
	return normalized[0], nil
}

// NormalizeDHCPOption validates one extra DHCP option for the parent's family.
func (s *Server) NormalizeDHCPOption(_ context.Context, vnf store.VNF, item store.DHCPOption) (store.DHCPOption, error) {
	ipv6 := vnf.Type == store.VNFTypeDHCPv6
	normalized, err := host.NormalizeDHCPOptions([]store.DHCPOption{item}, ipv6)
	if err != nil {
		return store.DHCPOption{}, err
	}
	return normalized[0], nil
}

// normalizeDHCPv4StaticAssignments validates up to 256 MAC-to-IP reservations,
// rejecting addresses outside the subnet, network/broadcast, the gateway, or the
// DHCP bind address, as well as duplicate MACs or IPs, and returns normalized
// entries.
func normalizeDHCPv4StaticAssignments(payload []store.DHCPStaticAssignment, gateway net.IP, subnet *net.IPNet, bindIP string) ([]store.DHCPStaticAssignment, error) {
	const maxStaticAssignments = 256
	if len(payload) > maxStaticAssignments {
		return nil, fmt.Errorf("config.static_assignments can contain at most %d entries", maxStaticAssignments)
	}
	if len(payload) == 0 {
		return nil, nil
	}
	network := subnet.IP.To4()
	broadcast := broadcastIPv4(subnet)
	var bind net.IP
	if strings.TrimSpace(bindIP) != "" {
		bind, _, _ = net.ParseCIDR(strings.TrimSpace(bindIP))
	}
	seenMAC := make(map[string]int, len(payload))
	seenIP := make(map[string]int, len(payload))
	assignments := make([]store.DHCPStaticAssignment, 0, len(payload))
	for i, assignment := range payload {
		prefix := fmt.Sprintf("config.static_assignments[%d]", i)
		mac, err := net.ParseMAC(strings.TrimSpace(assignment.MAC))
		if err != nil || len(mac) != 6 {
			return nil, fmt.Errorf("%s.mac must be a valid Ethernet MAC address", prefix)
		}
		ip := net.ParseIP(strings.TrimSpace(assignment.IP)).To4()
		if ip == nil {
			return nil, fmt.Errorf("%s.ip must be a valid IPv4 address", prefix)
		}
		if !subnet.Contains(ip) {
			return nil, fmt.Errorf("%s.ip must be inside the DHCP subnet %s", prefix, subnet)
		}
		if ip.Equal(network) || ip.Equal(broadcast) {
			return nil, fmt.Errorf("%s.ip must be a usable host address", prefix)
		}
		if ip.Equal(gateway) {
			return nil, fmt.Errorf("%s.ip cannot be the DHCP gateway", prefix)
		}
		if bind != nil && ip.Equal(bind) {
			return nil, fmt.Errorf("%s.ip cannot be the DHCP service address", prefix)
		}
		macKey := strings.ToLower(mac.String())
		if previous, exists := seenMAC[macKey]; exists {
			return nil, fmt.Errorf("%s.mac duplicates config.static_assignments[%d]", prefix, previous)
		}
		ipKey := ip.String()
		if previous, exists := seenIP[ipKey]; exists {
			return nil, fmt.Errorf("%s.ip duplicates config.static_assignments[%d]", prefix, previous)
		}
		seenMAC[macKey] = i
		seenIP[ipKey] = i
		assignments = append(assignments, store.DHCPStaticAssignment{
			MAC: mac.String(),
			IP:  ip.String(),
		})
	}
	return assignments, nil
}

// normalizeLeaseTime decodes a lease duration with the shared legacy decoder and
// returns the canonical Go duration string new writes store.
func normalizeLeaseTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "12h", nil
	}
	duration, err := host.DecodeLeaseTime(value)
	if err != nil || duration <= 0 {
		return "", fmt.Errorf("invalid lease_time %q", value)
	}
	return duration.String(), nil
}

// normalizeDHCPv4Config validates an IPv4 gateway CIDR and range, rejecting
// addresses outside the subnet or overlapping the gateway, network, or broadcast
// address and ranges over 65536 addresses, and returns canonical strings.
func normalizeDHCPv4Config(gatewayCIDR, rangeStart, rangeEnd, dns string) (string, string, string, string, error) {
	gateway, ipnet, err := net.ParseCIDR(gatewayCIDR)
	if err != nil {
		return "", "", "", "", fmt.Errorf("invalid gateway CIDR: %v", err)
	}
	gateway4 := gateway.To4()
	network4 := ipnet.IP.To4()
	if gateway4 == nil || network4 == nil {
		return "", "", "", "", errors.New("DHCPv4 gateway must be an IPv4 CIDR")
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones > 30 {
		return "", "", "", "", errors.New("DHCPv4 gateway prefix must be /30 or smaller")
	}
	broadcast := broadcastIPv4(ipnet)
	if gateway4.Equal(network4) || gateway4.Equal(broadcast) {
		return "", "", "", "", errors.New("DHCPv4 gateway must be a usable host address")
	}
	start := net.ParseIP(rangeStart).To4()
	end := net.ParseIP(rangeEnd).To4()
	if start == nil || end == nil {
		return "", "", "", "", errors.New("DHCPv4 range addresses must be valid IPv4 addresses")
	}
	for _, addr := range []net.IP{start, end} {
		if !ipnet.Contains(addr) {
			return "", "", "", "", errors.New("DHCPv4 range must be inside the gateway subnet")
		}
		if addr.Equal(network4) || addr.Equal(broadcast) {
			return "", "", "", "", errors.New("DHCPv4 range cannot include the network or broadcast address")
		}
		if addr.Equal(gateway4) {
			return "", "", "", "", errors.New("DHCPv4 range cannot include the gateway address")
		}
	}
	if bytesCompareIPv4(start, end) > 0 {
		return "", "", "", "", errors.New("DHCPv4 range start must not be after range end")
	}
	if bytesCompareIPv4(start, gateway4) <= 0 && bytesCompareIPv4(end, gateway4) >= 0 {
		return "", "", "", "", errors.New("DHCPv4 range cannot include the gateway address")
	}
	size := uint64(binaryBigEndianUint32(end)) - uint64(binaryBigEndianUint32(start)) + 1
	if size > maxVNFRangeSize {
		return "", "", "", "", fmt.Errorf("DHCPv4 range must contain at most %d addresses", maxVNFRangeSize)
	}
	normalizedDNS, err := normalizeVNFDNS(dns, false)
	if err != nil {
		return "", "", "", "", err
	}
	return fmt.Sprintf("%s/%d", gateway4.String(), ones), start.String(), end.String(), normalizedDNS, nil
}

// normalizeDHCPv6Config validates an IPv6 gateway prefix and range, rejecting
// ranges outside the prefix, overlapping the gateway or subnet-router anycast
// address, or exceeding 65536 addresses.
func normalizeDHCPv6Config(gatewayCIDR, rangeStart, rangeEnd, dns string) (string, string, string, string, error) {
	prefix, err := netip.ParsePrefix(gatewayCIDR)
	if err != nil || !prefix.Addr().Is6() {
		return "", "", "", "", errors.New("DHCPv6 gateway must be an IPv6 CIDR")
	}
	gateway := prefix.Addr()
	if gateway.IsUnspecified() {
		return "", "", "", "", errors.New("DHCPv6 gateway must be a usable address")
	}
	start, err := netip.ParseAddr(rangeStart)
	if err != nil || !start.Is6() {
		return "", "", "", "", errors.New("DHCPv6 range start must be a valid IPv6 address")
	}
	end, err := netip.ParseAddr(rangeEnd)
	if err != nil || !end.Is6() {
		return "", "", "", "", errors.New("DHCPv6 range end must be a valid IPv6 address")
	}
	if !prefix.Contains(start) || !prefix.Contains(end) {
		return "", "", "", "", errors.New("DHCPv6 range must be inside the gateway prefix")
	}
	if start.Compare(end) > 0 {
		return "", "", "", "", errors.New("DHCPv6 range start must not be after range end")
	}
	if start.Compare(gateway) <= 0 && end.Compare(gateway) >= 0 {
		return "", "", "", "", errors.New("DHCPv6 range cannot include the gateway address")
	}
	anycast := prefix.Masked().Addr()
	if start.Compare(anycast) <= 0 && end.Compare(anycast) >= 0 {
		return "", "", "", "", errors.New("DHCPv6 range cannot include the subnet-router anycast address")
	}
	startInt := new(big.Int).SetBytes(start.AsSlice())
	endInt := new(big.Int).SetBytes(end.AsSlice())
	size := new(big.Int).Sub(endInt, startInt)
	size.Add(size, big.NewInt(1))
	if size.Cmp(big.NewInt(maxVNFRangeSize)) > 0 {
		return "", "", "", "", fmt.Errorf("DHCPv6 range must contain at most %d addresses", maxVNFRangeSize)
	}
	normalizedDNS, err := normalizeVNFDNS(dns, true)
	if err != nil {
		return "", "", "", "", err
	}
	return prefix.String(), start.String(), end.String(), normalizedDNS, nil
}

// normalizeVNFDNS splits a comma-separated DNS list, trims and validates each
// address for the requested family, and returns the joined result; empty input
// yields an empty string.
func normalizeVNFDNS(value string, ipv6 bool) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ip := net.ParseIP(part)
		if ip == nil {
			return "", fmt.Errorf("invalid DNS address %q", part)
		}
		if ipv6 {
			if ip.To4() != nil {
				return "", fmt.Errorf("DNS address %q is not IPv6", part)
			}
			out = append(out, ip.To16().String())
		} else {
			if ip.To4() == nil {
				return "", fmt.Errorf("DNS address %q is not IPv4", part)
			}
			out = append(out, ip.To4().String())
		}
	}
	return strings.Join(out, ","), nil
}

// normalizeDHCPBindIP validates an optional bind address as a CIDR inside the
// gateway subnet with the same prefix and address family, returning it in
// canonical form or an empty string when unset.
func normalizeDHCPBindIP(bind, gatewayCIDR string, ipv6 bool) (string, error) {
	bind = strings.TrimSpace(bind)
	if bind == "" {
		return "", nil
	}
	ip, ipnet, err := net.ParseCIDR(bind)
	if err != nil {
		return "", fmt.Errorf("invalid bind_ip CIDR: %v", err)
	}
	if ipv6 {
		if ip.To4() != nil {
			return "", errors.New("bind_ip must be IPv6 for DHCPv6")
		}
	} else if ip.To4() == nil {
		return "", errors.New("bind_ip must be IPv4 for DHCPv4")
	}
	_, gatewayNet, err := net.ParseCIDR(gatewayCIDR)
	if err != nil {
		return "", err
	}
	gatewayIP, _, _ := net.ParseCIDR(gatewayCIDR)
	if !gatewayNet.Contains(ip) {
		return "", errors.New("bind_ip must be inside the gateway subnet")
	}
	if ip.Equal(gatewayIP) {
		return "", errors.New("bind_ip cannot equal the gateway address")
	}
	ones, bits := gatewayNet.Mask.Size()
	bindOnes, bindBits := ipnet.Mask.Size()
	if bits != bindBits || ones != bindOnes {
		return "", errors.New("bind_ip must use the gateway prefix length")
	}
	if !ipv6 {
		ip4 := ip.To4()
		if ip4.Equal(ipnet.IP.To4()) || ip4.Equal(broadcastIPv4(ipnet)) {
			return "", errors.New("bind_ip must be a usable host address")
		}
	}
	return fmt.Sprintf("%s/%d", ip.String(), bindOnes), nil
}

// normalizeDNSIntegration validates the DHCP-to-DNS integration fields,
// defaulting the TTL to 300 and requiring a domain when a DNS VNF is set. It
// returns zero values when integration is disabled.
func normalizeDNSIntegration(vnfID int64, domain string, ttl uint32) (string, uint32, error) {
	domain = strings.TrimSpace(domain)
	if vnfID < 0 {
		return "", 0, errors.New("config.dns_vnf_id must be positive")
	}
	if vnfID == 0 {
		if domain != "" || ttl != 0 {
			return "", 0, errors.New("config.dns_vnf_id is required when dns_domain or dns_ttl is set")
		}
		return "", 0, nil
	}
	normalized, err := normalizeDNSDomain(domain)
	if err != nil {
		return "", 0, err
	}
	if normalized == "" {
		return "", 0, errors.New("config.dns_domain is required when dns_vnf_id is set")
	}
	if ttl == 0 {
		ttl = 300
	}
	if ttl > 86400 {
		return "", 0, errors.New("config.dns_ttl must be at most 86400")
	}
	return normalized, ttl, nil
}

// normalizeDNSDomain trims a trailing dot, rejects wildcards, whitespace, and
// the DNS root, and returns the canonical domain or an empty string for empty
// input.
func normalizeDNSDomain(value string) (string, error) {
	value = strings.TrimSpace(strings.TrimSuffix(value, "."))
	if value == "" {
		return "", nil
	}
	if strings.ContainsAny(value, " \t\r\n*") {
		return "", errors.New("config.dns_domain must be a domain name without wildcards or whitespace")
	}
	canonical := dns.CanonicalName(value)
	if canonical == "." {
		return "", errors.New("config.dns_domain must not be the DNS root")
	}
	if _, ok := dns.IsDomainName(canonical); !ok {
		return "", errors.New("config.dns_domain is not a valid domain name")
	}
	return strings.TrimSuffix(canonical, "."), nil
}
