package api

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/netutil"
	"github.com/voidstarit/rookery/internal/store"
)

const (
	maxVNFNameLength = 64
	maxVNFRangeSize  = 65536
)

type vnfPayload struct {
	Name      *string           `json:"name"`
	NetworkID *int64            `json:"network_id"`
	Type      *string           `json:"type"`
	Enabled   *bool             `json:"enabled"`
	Config    *vnfConfigPayload `json:"config"`
}

type vnfUpdatePayload struct {
	Name    *string           `json:"name"`
	Enabled *bool             `json:"enabled"`
	Config  *vnfConfigPayload `json:"config"`
}

type vnfConfigPayload struct {
	Gateway           string                        `json:"gateway"`
	RangeStart        string                        `json:"range_start"`
	RangeEnd          string                        `json:"range_end"`
	DNS               string                        `json:"dns"`
	LeaseTime         string                        `json:"lease_time"`
	BindIP            string                        `json:"bind_ip"`
	StaticAssignments []dhcpStaticAssignmentPayload `json:"static_assignments"`
	OutsideInterface  string                        `json:"outside_interface"`
	OutsideAddress    string                        `json:"outside_address"`
	OutsideGateway    string                        `json:"outside_gateway"`
	NAT44Pool         string                        `json:"nat44_pool"`
	DNATRules         []dnatRulePayload             `json:"dnat_rules"`

	Transport         string   `json:"transport"`
	LocalAS           uint32   `json:"local_as"`
	RouterID          string   `json:"router_id"`
	PeerAS            uint32   `json:"peer_as"`
	PeerIP            string   `json:"peer_ip"`
	LocalIP           string   `json:"local_ip"`
	PeerGateway       string   `json:"peer_gateway"`
	MultihopTTL       uint32   `json:"multihop_ttl"`
	Password          string   `json:"password"`
	Passive           bool     `json:"passive"`
	TargetTableID     uint32   `json:"target_table_id"`
	AdvertisePrefixes []string `json:"advertise_prefixes"`
	NextHop           string   `json:"next_hop"`

	DNSRecords    []dnsRecordPayload `json:"dns_records"`
	DNSForward    bool               `json:"dns_forward"`
	DNSForwarders []string           `json:"dns_forwarders"`

	DNSVNFID  int64  `json:"dns_vnf_id"`
	DNSDomain string `json:"dns_domain"`
	DNSTTL    uint32 `json:"dns_ttl"`

	DHCPOptions []dhcpOptionPayload `json:"dhcp_options"`
}

type dhcpOptionPayload struct {
	Code   uint16 `json:"code"`
	Format string `json:"format"`
	Value  string `json:"value"`
}

type dnsRecordPayload struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   uint32 `json:"ttl"`
}

type dhcpStaticAssignmentPayload struct {
	MAC string `json:"mac"`
	IP  string `json:"ip"`
}

type dnatRulePayload struct {
	Protocol     string `json:"protocol"`
	ExternalPort int    `json:"external_port"`
	LocalIP      string `json:"local_ip"`
	LocalPort    int    `json:"local_port"`
}

type vnfView struct {
	store.VNF
	Status       string `json:"status"`
	StatusDetail string `json:"status_detail,omitempty"`
	LeaseCount   int    `json:"lease_count"`
}

func (p vnfPayload) toName() (string, error) {
	if p.Name == nil {
		return "", errors.New("name is required")
	}
	name := strings.TrimSpace(*p.Name)
	switch {
	case name == "":
		return "", errors.New("name is required")
	case len(name) > maxVNFNameLength:
		return "", fmt.Errorf("name must be at most %d characters", maxVNFNameLength)
	case !vmNamePattern.MatchString(name):
		return "", errors.New("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}
	return name, nil
}

func (p vnfPayload) toType() (string, error) {
	if p.Type == nil {
		return "", errors.New("type is required")
	}
	vnfType := strings.TrimSpace(*p.Type)
	if !store.ValidVNFType(vnfType) {
		return "", fmt.Errorf("unsupported VNF type %q", vnfType)
	}
	return vnfType, nil
}

func (p vnfConfigPayload) toConfig(vnfType string) (store.VNFConfig, error) {
	if vnfType != store.VNFTypeDHCPv4 && vnfType != store.VNFTypeDHCPv6 && len(p.DHCPOptions) > 0 {
		return store.VNFConfig{}, errors.New("config.dhcp_options is supported for DHCP VNFs only")
	}
	if vnfType != store.VNFTypeDHCPv4 && vnfType != store.VNFTypeDHCPv6 &&
		(p.DNSVNFID != 0 || strings.TrimSpace(p.DNSDomain) != "" || p.DNSTTL != 0) {
		return store.VNFConfig{}, errors.New("config.dns_vnf_id, dns_domain, and dns_ttl are supported for DHCP VNFs only")
	}
	if vnfType == store.VNFTypeBGP {
		return normalizeBGPConfig(p)
	}
	if vnfType == store.VNFTypeDNS {
		return normalizeDNSConfig(p)
	}
	gateway := strings.TrimSpace(p.Gateway)
	rangeStart := strings.TrimSpace(p.RangeStart)
	rangeEnd := strings.TrimSpace(p.RangeEnd)
	if gateway == "" {
		return store.VNFConfig{}, errors.New("config.gateway is required")
	}
	transport := strings.ToLower(strings.TrimSpace(p.Transport))
	switch transport {
	case "", vnfTransportShared, vnfTransportDedicated, vnfTransportTap:
	case "memif":
		transport = vnfTransportDedicated
	default:
		return store.VNFConfig{}, fmt.Errorf("config.transport must be shared, dedicated, or tap, got %q", p.Transport)
	}
	if vnfType == store.VNFTypeDHCPv6 {
		// DHCPv6 has no memif transport yet; keep it on the tap path.
		transport = vnfTransportTap
	}
	if vnfType != store.VNFTypeDHCPv4 && len(p.StaticAssignments) > 0 {
		return store.VNFConfig{}, errors.New("config.static_assignments is supported for DHCPv4 only")
	}

	switch vnfType {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
		if rangeStart == "" || rangeEnd == "" {
			return store.VNFConfig{}, errors.New("config.range_start and config.range_end are required")
		}
		leaseTime, err := normalizeLeaseTime(p.LeaseTime)
		if err != nil {
			return store.VNFConfig{}, err
		}
		var normalizedGateway, normalizedStart, normalizedEnd, dns string
		if vnfType == store.VNFTypeDHCPv4 {
			normalizedGateway, normalizedStart, normalizedEnd, dns, err = normalizeDHCPv4Config(gateway, rangeStart, rangeEnd, p.DNS)
		} else {
			normalizedGateway, normalizedStart, normalizedEnd, dns, err = normalizeDHCPv6Config(gateway, rangeStart, rangeEnd, p.DNS)
		}
		if err != nil {
			return store.VNFConfig{}, err
		}
		bindIP, err := normalizeDHCPBindIP(p.BindIP, normalizedGateway, vnfType == store.VNFTypeDHCPv6)
		if err != nil {
			return store.VNFConfig{}, err
		}
		if bindIP != "" && vnfType == store.VNFTypeDHCPv4 {
			bind, _, _ := net.ParseCIDR(bindIP)
			start := net.ParseIP(normalizedStart).To4()
			end := net.ParseIP(normalizedEnd).To4()
			if start != nil && end != nil && bytesCompareIPv4(start, bind) <= 0 && bytesCompareIPv4(end, bind) >= 0 {
				return store.VNFConfig{}, errors.New("bind_ip cannot be inside the DHCP range")
			}
		}
		var staticAssignments []store.DHCPStaticAssignment
		if vnfType == store.VNFTypeDHCPv4 {
			gatewayIP, subnet, _ := net.ParseCIDR(normalizedGateway)
			staticAssignments, err = normalizeDHCPv4StaticAssignments(p.StaticAssignments, gatewayIP.To4(), subnet, bindIP)
			if err != nil {
				return store.VNFConfig{}, err
			}
		}
		options, err := normalizeDHCPOptionsPayload(p.DHCPOptions, vnfType == store.VNFTypeDHCPv6)
		if err != nil {
			return store.VNFConfig{}, err
		}
		dnsDomain, dnsTTL, err := normalizeDNSIntegration(p.DNSVNFID, p.DNSDomain, p.DNSTTL)
		if err != nil {
			return store.VNFConfig{}, err
		}
		return store.VNFConfig{
			Gateway:           normalizedGateway,
			RangeStart:        normalizedStart,
			RangeEnd:          normalizedEnd,
			DNS:               dns,
			LeaseTime:         leaseTime,
			BindIP:            bindIP,
			StaticAssignments: staticAssignments,
			Transport:         transport,
			DHCPOptions:       options,
			DNSVNFID:          p.DNSVNFID,
			DNSDomain:         dnsDomain,
			DNSTTL:            dnsTTL,
		}, nil
	case store.VNFTypeNAT44:
		return normalizeNAT44Config(p)
	default:
		return store.VNFConfig{}, fmt.Errorf("unsupported VNF type %q", vnfType)
	}
}

func normalizeNAT44Config(p vnfConfigPayload) (store.VNFConfig, error) {
	gateway := strings.TrimSpace(p.Gateway)
	gatewayIP, ipnet, err := net.ParseCIDR(gateway)
	if err != nil {
		return store.VNFConfig{}, fmt.Errorf("invalid NAT44 gateway CIDR: %v", err)
	}
	gateway4 := gatewayIP.To4()
	network4 := ipnet.IP.To4()
	if gateway4 == nil || network4 == nil {
		return store.VNFConfig{}, errors.New("NAT44 gateway must be an IPv4 CIDR")
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones > 30 {
		return store.VNFConfig{}, errors.New("NAT44 gateway prefix must be /30 or smaller")
	}
	broadcast := broadcastIPv4(ipnet)
	if gateway4.Equal(network4) || gateway4.Equal(broadcast) {
		return store.VNFConfig{}, errors.New("NAT44 gateway must be a usable host address")
	}
	outsideInterface := strings.TrimSpace(p.OutsideInterface)
	if outsideInterface == "" {
		return store.VNFConfig{}, errors.New("config.outside_interface is required for NAT44")
	}
	outsideAddress := strings.TrimSpace(p.OutsideAddress)
	outsideGateway := strings.TrimSpace(p.OutsideGateway)
	if outsideAddress != "" {
		ip, outsideNet, err := net.ParseCIDR(outsideAddress)
		if err != nil || ip.To4() == nil {
			return store.VNFConfig{}, errors.New("config.outside_address must be an IPv4 CIDR")
		}
		ones, bits := outsideNet.Mask.Size()
		if bits != 32 || ones == 0 {
			return store.VNFConfig{}, errors.New("config.outside_address must have a nonzero IPv4 prefix")
		}
	} else {
		outsideAddress = ""
		if outsideGateway != "" {
			return store.VNFConfig{}, errors.New("config.outside_gateway requires config.outside_address")
		}
	}
	if outsideGateway != "" {
		if ip := net.ParseIP(outsideGateway).To4(); ip == nil {
			return store.VNFConfig{}, errors.New("config.outside_gateway must be an IPv4 address")
		}
		if outsideAddress != "" {
			_, outsideNet, _ := net.ParseCIDR(outsideAddress)
			if !outsideNet.Contains(net.ParseIP(outsideGateway).To4()) {
				return store.VNFConfig{}, errors.New("config.outside_gateway must be inside the outside subnet")
			}
		}
	}
	pool := strings.TrimSpace(p.NAT44Pool)
	if pool != "" && !validNAT44Pool(pool) {
		return store.VNFConfig{}, errors.New("config.nat44_pool must be a single IPv4 address or start-end range")
	}
	dnatRules, err := normalizeDNATRules(p.DNATRules, ipnet)
	if err != nil {
		return store.VNFConfig{}, err
	}
	return store.VNFConfig{
		Gateway:          fmt.Sprintf("%s/%d", gateway4.String(), ones),
		OutsideInterface: outsideInterface,
		OutsideAddress:   outsideAddress,
		OutsideGateway:   outsideGateway,
		NAT44Pool:        pool,
		DNATRules:        dnatRules,
	}, nil
}

const maxDNATRules = 256

func normalizeDNATRules(payload []dnatRulePayload, subnet *net.IPNet) ([]store.DNATRule, error) {
	if len(payload) > maxDNATRules {
		return nil, fmt.Errorf("config.dnat_rules can contain at most %d rules", maxDNATRules)
	}
	if len(payload) == 0 {
		return nil, nil
	}
	network4 := subnet.IP.To4()
	broadcast := broadcastIPv4(subnet)
	seenExternal := make(map[string]int, len(payload))
	rules := make([]store.DNATRule, 0, len(payload))
	for i, rule := range payload {
		prefix := fmt.Sprintf("config.dnat_rules[%d]", i)
		protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
		if protocol != "tcp" && protocol != "udp" {
			return nil, fmt.Errorf("%s.protocol must be tcp or udp", prefix)
		}
		if rule.ExternalPort < 1 || rule.ExternalPort > 65535 {
			return nil, fmt.Errorf("%s.external_port must be between 1 and 65535", prefix)
		}
		if rule.LocalPort < 1 || rule.LocalPort > 65535 {
			return nil, fmt.Errorf("%s.local_port must be between 1 and 65535", prefix)
		}
		localIP := net.ParseIP(strings.TrimSpace(rule.LocalIP)).To4()
		if localIP == nil {
			return nil, fmt.Errorf("%s.local_ip must be a valid IPv4 address", prefix)
		}
		if !subnet.Contains(localIP) {
			return nil, fmt.Errorf("%s.local_ip must be inside the NAT44 subnet %s", prefix, subnet)
		}
		if localIP.Equal(network4) || localIP.Equal(broadcast) {
			return nil, fmt.Errorf("%s.local_ip must be a usable host address", prefix)
		}
		externalKey := fmt.Sprintf("%s/%d", protocol, rule.ExternalPort)
		if previous, exists := seenExternal[externalKey]; exists {
			return nil, fmt.Errorf("%s duplicates the %s external port %d from config.dnat_rules[%d]", prefix, protocol, rule.ExternalPort, previous)
		}
		seenExternal[externalKey] = i
		rules = append(rules, store.DNATRule{
			Protocol:     protocol,
			ExternalPort: rule.ExternalPort,
			LocalIP:      localIP.String(),
			LocalPort:    rule.LocalPort,
		})
	}
	return rules, nil
}

func normalizeDHCPOptionsPayload(payload []dhcpOptionPayload, ipv6 bool) ([]store.DHCPOption, error) {
	options := make([]store.DHCPOption, 0, len(payload))
	for _, option := range payload {
		options = append(options, store.DHCPOption{Code: option.Code, Format: option.Format, Value: option.Value})
	}
	return host.NormalizeDHCPOptions(options, ipv6)
}

func normalizeDHCPv4StaticAssignments(payload []dhcpStaticAssignmentPayload, gateway net.IP, subnet *net.IPNet, bindIP string) ([]store.DHCPStaticAssignment, error) {
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

func validNAT44Pool(value string) bool {
	if strings.Contains(value, "-") {
		parts := strings.SplitN(value, "-", 2)
		start := net.ParseIP(strings.TrimSpace(parts[0])).To4()
		end := net.ParseIP(strings.TrimSpace(parts[1])).To4()
		return start != nil && end != nil && bytesCompareIPv4(start, end) <= 0
	}
	return net.ParseIP(strings.TrimSpace(value)).To4() != nil
}

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

func normalizeLeaseTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "12h", nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return "", fmt.Errorf("invalid lease_time %q", value)
	}
	return compactDuration(duration), nil
}

func compactDuration(duration time.Duration) string {
	value := duration.String()
	value = strings.TrimSuffix(value, "0s")
	value = strings.TrimSuffix(value, "0m")
	return value
}

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

func binaryBigEndianUint32(ip net.IP) uint32 {
	ip4 := ip.To4()
	return uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
}

func bytesCompareIPv4(a, b net.IP) int {
	av := binaryBigEndianUint32(a)
	bv := binaryBigEndianUint32(b)
	switch {
	case av < bv:
		return -1
	case av > bv:
		return 1
	default:
		return 0
	}
}

func vnfUpdateName(value *string) (string, error) {
	if value == nil {
		return "", errors.New("name is required")
	}
	name := strings.TrimSpace(*value)
	switch {
	case name == "":
		return "", errors.New("name is required")
	case len(name) > maxVNFNameLength:
		return "", fmt.Errorf("name must be at most %d characters", maxVNFNameLength)
	case !vmNamePattern.MatchString(name):
		return "", errors.New("name must start with a letter or digit and contain only letters, digits, dots, dashes, and underscores")
	}
	return name, nil
}

func leaseBelongsToVNF(vnf store.VNF, address string) bool {
	switch vnf.Type {
	case store.VNFTypeDHCPv4:
		_, ipnet, err := net.ParseCIDR(vnf.Config.Gateway)
		if err != nil {
			return false
		}
		ip := net.ParseIP(address).To4()
		start := net.ParseIP(vnf.Config.RangeStart).To4()
		end := net.ParseIP(vnf.Config.RangeEnd).To4()
		if ip == nil || start == nil || end == nil || !ipnet.Contains(ip) {
			return false
		}
		value := binaryBigEndianUint32(ip)
		return value >= binaryBigEndianUint32(start) && value <= binaryBigEndianUint32(end)
	case store.VNFTypeDHCPv6:
		prefix, err := netip.ParsePrefix(vnf.Config.Gateway)
		if err != nil {
			return false
		}
		addr, err := netip.ParseAddr(address)
		if err != nil || !addr.Is6() || !prefix.Contains(addr) {
			return false
		}
		start, err := netip.ParseAddr(vnf.Config.RangeStart)
		if err != nil {
			return false
		}
		end, err := netip.ParseAddr(vnf.Config.RangeEnd)
		if err != nil {
			return false
		}
		return addr.Compare(start) >= 0 && addr.Compare(end) <= 0
	default:
		return false
	}
}

func (s *Server) viewVNF(ctx context.Context, vnf store.VNF) vnfView {
	view := vnfView{VNF: vnf, Status: "disabled"}
	if vnf.Enabled {
		if s.vnfRunning(ctx, vnf) {
			view.Status = "running"
		} else {
			view.Status = "degraded"
			view.StatusDetail = "runtime is not running; use Restart (recreate the network first if VPP was restarted)"
		}
	}
	if s.vnfs != nil {
		if leases, err := s.vnfs.Leases(ctx, vnf.ID); err == nil {
			for _, lease := range leases {
				if lease.Active && leaseBelongsToVNF(vnf, lease.Address) {
					view.LeaseCount++
				}
			}
		}
	}
	return view
}

func (s *Server) vnfRunning(ctx context.Context, vnf store.VNF) bool {
	if vnf.Type == store.VNFTypeNAT44 {
		s.natMu.Lock()
		defer s.natMu.Unlock()
		return s.natRunning[vnf.ID]
	}
	return s.vnfs != nil && s.vnfs.Running(ctx, vnf.ID)
}

func (s *Server) setNAT44Running(vnfID int64, running bool) {
	s.natMu.Lock()
	defer s.natMu.Unlock()
	if s.natRunning == nil {
		s.natRunning = make(map[int64]bool)
	}
	if running {
		s.natRunning[vnfID] = true
	} else {
		delete(s.natRunning, vnfID)
	}
}

func applyNATGatewayToDHCPConfig(network store.Network, vnfType string, config *store.VNFConfig) error {
	if vnfType != store.VNFTypeDHCPv4 || network.IPv4Gateway == "" {
		return nil
	}
	natIP, natSubnet, err := net.ParseCIDR(network.IPv4Gateway)
	if err != nil {
		return fmt.Errorf("parse network IPv4 gateway: %w", err)
	}
	if natIP.To4() == nil {
		return errors.New("network IPv4 gateway is invalid")
	}
	start := net.ParseIP(config.RangeStart).To4()
	end := net.ParseIP(config.RangeEnd).To4()
	if start == nil || end == nil || !natSubnet.Contains(start) || !natSubnet.Contains(end) {
		return fmt.Errorf("DHCPv4 range must be inside the network NAT subnet %s", natSubnet)
	}
	if network.IPv4CIDR != "" {
		if !netutil.InRange(network.IPv4CIDR, config.RangeStart, config.RangeEnd) {
			return fmt.Errorf("DHCPv4 range %s-%s must stay inside %s and avoid the first %d reserved addresses", config.RangeStart, config.RangeEnd, network.IPv4CIDR, netutil.ReservedHosts)
		}
		for _, assignment := range config.StaticAssignments {
			if !netutil.Contains(network.IPv4CIDR, assignment.IP) || netutil.IsReserved(network.IPv4CIDR, assignment.IP) {
				return fmt.Errorf("DHCPv4 static assignment %s (%s) must stay inside %s and avoid the first %d reserved addresses", assignment.MAC, assignment.IP, network.IPv4CIDR, netutil.ReservedHosts)
			}
		}
	}
	bindCIDR, err := nextIPv4CIDR(network.IPv4Gateway)
	if err != nil {
		return err
	}
	bindIP, _, _ := net.ParseCIDR(bindCIDR)
	if bytesCompareIPv4(start, bindIP) <= 0 && bytesCompareIPv4(end, bindIP) >= 0 {
		return fmt.Errorf("DHCPv4 range cannot include the NAT service address %s", bindIP)
	}
	config.Gateway = network.IPv4Gateway
	config.BindIP = bindCIDR
	return nil
}

func (s *Server) activateVNF(ctx context.Context, vnf store.VNF) (store.VNF, error) {
	if s.vnfs == nil || s.networks == nil {
		return vnf, errors.New("VNF runtime is not configured")
	}
	if blocked, message := s.vppBlocked(); blocked {
		return vnf, errors.New(message)
	}
	network, err := s.store.GetNetwork(ctx, vnf.NetworkID)
	if err != nil {
		return vnf, fmt.Errorf("get VNF network: %w", err)
	}
	network, err = s.ensureNetworkCIDRs(ctx, network)
	if err != nil {
		return vnf, err
	}
	if err := s.validateVNFNetwork(vnf, network); err != nil {
		return vnf, err
	}
	if err := s.cleanupLegacyNetworkVNF(ctx, network); err != nil {
		return vnf, err
	}
	if vnf.Type == store.VNFTypeDNS {
		return s.activateSharedDNS(ctx, vnf, network)
	}

	transport := s.effectiveVNFTransport(network, vnf)
	if s.memifDHCP && vnf.Type == store.VNFTypeDHCPv4 {
		switch transport {
		case vnfTransportShared:
			return s.activateSharedVNF(ctx, vnf, network)
		case vnfTransportDedicated:
			return s.activateMemifVNF(ctx, vnf, network)
		}
	}
	if vnf.Type == store.VNFTypeBGP && vnf.Config.Transport == "memif" {
		return s.activateMemifVNF(ctx, vnf, network)
	}

	hostIf := fmt.Sprintf("vnf%d", vnf.ID)
	tapSwIfIndex, err := s.networks.CreateTap(ctx, hostIf, vnfTapCIDR(vnf), network.BridgeDomainID, network.RouteTableID)
	if err != nil {
		return vnf, fmt.Errorf("create VNF tap: %w", err)
	}
	if err := s.bindNetworkInterface(ctx, network, tapSwIfIndex); err != nil {
		_ = s.networks.DeleteTap(ctx, tapSwIfIndex)
		return vnf, err
	}
	if err := s.vnfs.Start(ctx, vnf, hostIf); err != nil {
		_ = s.networks.DeleteTap(ctx, tapSwIfIndex)
		return vnf, err
	}

	vnf.Enabled = true
	vnf.TapSwIfIndex = tapSwIfIndex
	vnf.TapHostName = hostIf
	updated, err := s.store.UpdateVNF(ctx, vnf.ID, vnf)
	if err != nil {
		_ = s.vnfs.Stop(ctx, vnf.ID, false)
		_ = s.networks.DeleteTap(ctx, tapSwIfIndex)
		return vnf, err
	}
	return updated, nil
}

func (s *Server) stopVNF(ctx context.Context, vnf store.VNF, removeLeases bool) error {
	var errs []error
	if s.vnfs != nil {
		if err := s.vnfs.Stop(ctx, vnf.ID, removeLeases); err != nil {
			errs = append(errs, fmt.Errorf("stop VNF runtime: %w", err))
		}
	}
	if s.networks != nil {
		sharedMemif := strings.TrimSpace(vnf.Config.Transport) == vnfTransportShared
		if network, err := s.store.GetNetwork(ctx, vnf.NetworkID); err == nil &&
			network.VNFMemifSocket != "" && vnf.MemifSocket == network.VNFMemifSocket {
			sharedMemif = true
		}
		if !sharedMemif && (vnf.MemifSwIfIndex > 0 || vnf.MemifSocket != "") {
			if err := s.deleteMemif(ctx, vnf.MemifSwIfIndex, vnfMemifDeleteName(vnf)); err != nil {
				errs = append(errs, fmt.Errorf("delete VNF memif: %w", err))
			}
		}
		if err := s.deleteTap(ctx, vnf.TapSwIfIndex, vnf.TapHostName); err != nil {
			errs = append(errs, fmt.Errorf("delete VNF tap: %w", err))
		}
	}
	return errors.Join(errs...)
}

type tapDeleterByName interface {
	DeleteTapByName(ctx context.Context, hostName string) error
}

func (s *Server) deleteTap(ctx context.Context, swIfIndex uint32, hostName string) error {
	if swIfIndex == 0 && strings.TrimSpace(hostName) == "" {
		return nil
	}
	if hostName != "" {
		if deleter, ok := s.networks.(tapDeleterByName); ok {
			return deleter.DeleteTapByName(ctx, hostName)
		}
	}
	if swIfIndex == 0 {
		return nil
	}
	return s.networks.DeleteTap(ctx, swIfIndex)
}

func (s *Server) cleanupLegacyNetworkVNF(ctx context.Context, network store.Network) error {
	if network.DHCPTapSwIfIndex > 0 || network.DHCPTapHostName != "" {
		if err := s.deleteTap(ctx, network.DHCPTapSwIfIndex, network.DHCPTapHostName); err != nil {
			return fmt.Errorf("delete legacy network DHCP tap: %w", err)
		}
	}
	if network.DHCPEnabled || network.DHCPTapSwIfIndex > 0 {
		if _, err := s.store.SetNetworkDHCP(ctx, network.ID, store.Network{}); err != nil {
			return fmt.Errorf("clear legacy network DHCP: %w", err)
		}
	}
	if s.vnfs != nil {
		s.vnfs.CleanupLegacyNetwork(network.ID)
	}
	return nil
}

// VNFStatus returns the status fields and active lease count used by the
// HTTP VNF views. The grpc-gateway reuses it so both listeners agree.
func (s *Server) VNFStatus(ctx context.Context, vnf store.VNF) (status, detail string, leaseCount int) {
	view := s.viewVNF(ctx, vnf)
	return view.Status, view.StatusDetail, view.LeaseCount
}

// VNFDNSRecords returns the dynamic DNS records registered for a DNS VNF.
func (s *Server) VNFDNSRecords(ctx context.Context, vnf store.VNF) ([]host.DNSRecordView, error) {
	if vnf.Type != store.VNFTypeDNS {
		return nil, errors.New("DNS records are available for DNS VNFs only")
	}
	if s.vnfs == nil {
		return []host.DNSRecordView{}, nil
	}
	return s.vnfs.DNSRecords(ctx, vnf.ID)
}

// VNFBGP returns the BGP session and RIB snapshot for a BGP VNF.
func (s *Server) VNFBGP(ctx context.Context, vnf store.VNF) (host.BGPStatus, error) {
	if s.vnfs == nil {
		return host.BGPStatus{}, errors.New("VNF runtime is not configured")
	}
	if vnf.Type != store.VNFTypeBGP {
		return host.BGPStatus{}, errors.New("BGP status is available for BGP VNFs only")
	}
	return s.vnfs.BGPStatus(ctx, vnf.ID)
}

// VNFSessions returns NAT44 sessions for a VNF, or an empty list when the NAT
// runtime is unavailable.
func (s *Server) VNFSessions(ctx context.Context, vnf store.VNF) ([]string, error) {
	if vnf.Type != store.VNFTypeNAT44 {
		return nil, errors.New("sessions are available for NAT44 VNFs only")
	}
	if s.nat == nil {
		return []string{}, nil
	}
	sessions, err := s.nat.NAT44Sessions(ctx)
	if err != nil {
		return nil, err
	}
	if sessions == nil {
		sessions = []string{}
	}
	return sessions, nil
}

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

func normalizeDNSConfig(p vnfConfigPayload) (store.VNFConfig, error) {
	if len(p.DNSRecords) > 1024 {
		return store.VNFConfig{}, errors.New("config.dns_records supports at most 1024 records")
	}
	records := make([]store.DNSRecord, 0, len(p.DNSRecords))
	seen := make(map[string]struct{}, len(p.DNSRecords))
	cnameNames := make(map[string]struct{})
	otherNames := make(map[string]struct{})
	for index, raw := range p.DNSRecords {
		name, err := normalizeDNSRecordName(raw.Name)
		if err != nil {
			return store.VNFConfig{}, fmt.Errorf("config.dns_records[%d].name: %w", index, err)
		}
		recordType := strings.ToUpper(strings.TrimSpace(raw.Type))
		value, err := normalizeDNSRecordValue(recordType, raw.Value)
		if err != nil {
			return store.VNFConfig{}, fmt.Errorf("config.dns_records[%d].value: %w", index, err)
		}
		ttl := raw.TTL
		if ttl == 0 {
			ttl = 300
		}
		if ttl > 86400 {
			return store.VNFConfig{}, fmt.Errorf("config.dns_records[%d].ttl must be at most 86400", index)
		}
		key := name + "\x00" + recordType + "\x00" + value
		if _, exists := seen[key]; exists {
			return store.VNFConfig{}, fmt.Errorf("config.dns_records[%d] duplicates another record", index)
		}
		seen[key] = struct{}{}
		switch {
		case recordType == "CNAME":
			if _, exists := otherNames[name]; exists {
				return store.VNFConfig{}, fmt.Errorf("config.dns_records[%d]: %s already has non-CNAME records", index, name)
			}
			if _, exists := cnameNames[name]; exists {
				return store.VNFConfig{}, fmt.Errorf("config.dns_records[%d]: %s already has a CNAME record", index, name)
			}
			cnameNames[name] = struct{}{}
		default:
			if _, exists := cnameNames[name]; exists {
				return store.VNFConfig{}, fmt.Errorf("config.dns_records[%d]: %s already has a CNAME record", index, name)
			}
			otherNames[name] = struct{}{}
		}
		records = append(records, store.DNSRecord{Name: name, Type: recordType, Value: value, TTL: ttl})
	}
	if len(p.DNSForwarders) > 16 {
		return store.VNFConfig{}, errors.New("config.dns_forwarders supports at most 16 servers")
	}
	forwarders := make([]string, 0, len(p.DNSForwarders))
	seenForwarders := make(map[string]struct{}, len(p.DNSForwarders))
	for index, raw := range p.DNSForwarders {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		forwarder, err := normalizeDNSForwarder(raw)
		if err != nil {
			return store.VNFConfig{}, fmt.Errorf("config.dns_forwarders[%d]: %w", index, err)
		}
		if _, exists := seenForwarders[forwarder]; exists {
			continue
		}
		seenForwarders[forwarder] = struct{}{}
		forwarders = append(forwarders, forwarder)
	}
	return store.VNFConfig{
		DNSRecords:    records,
		DNSForward:    p.DNSForward,
		DNSForwarders: forwarders,
	}, nil
}

func normalizeDNSRecordName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		return "", errors.New("name is required")
	}
	wildcard := strings.HasPrefix(name, "*.")
	if strings.Contains(name, "*") && !wildcard {
		return "", errors.New("only a leading *. wildcard is supported")
	}
	if wildcard {
		name = strings.TrimPrefix(name, "*.")
		if name == "" {
			return "", errors.New("wildcard name requires a domain suffix")
		}
	}
	if strings.ContainsAny(name, " \t\r\n") {
		return "", errors.New("name must not contain whitespace")
	}
	canonical := dns.CanonicalName(name)
	if canonical == "." {
		return "", errors.New("name must not be the DNS root")
	}
	if _, ok := dns.IsDomainName(canonical); !ok {
		return "", errors.New("invalid DNS name")
	}
	if wildcard {
		return "*." + canonical, nil
	}
	return canonical, nil
}

func normalizeDNSRecordValue(recordType, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("value is required")
	}
	switch recordType {
	case "A":
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			return "", errors.New("A records require an IPv4 address")
		}
		return ip.To4().String(), nil
	case "AAAA":
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() != nil {
			return "", errors.New("AAAA records require an IPv6 address")
		}
		return ip.String(), nil
	case "CNAME":
		name, err := normalizeDNSRecordName(value)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(name, "*.") {
			return "", errors.New("CNAME targets cannot be wildcards")
		}
		return name, nil
	case "TXT":
		if len(value) > 255 {
			return "", errors.New("TXT values must be at most 255 bytes")
		}
		return value, nil
	case "PTR":
		name, err := normalizeDNSRecordName(value)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(name, "*.") {
			return "", errors.New("PTR targets cannot be wildcards")
		}
		return name, nil
	default:
		return "", errors.New("type must be A, AAAA, CNAME, TXT, or PTR")
	}
}

func normalizeDNSForwarder(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("server is required")
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		addr, parseErr := netip.ParseAddr(strings.Trim(value, "[]"))
		if parseErr != nil {
			return "", errors.New("must be an IP address or IP:port")
		}
		return net.JoinHostPort(addr.String(), "53"), nil
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return "", errors.New("host must be an IP address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("port must be between 1 and 65535")
	}
	return net.JoinHostPort(addr.String(), strconv.Itoa(port)), nil
}

func normalizeBGPConfig(p vnfConfigPayload) (store.VNFConfig, error) {
	transport := strings.ToLower(strings.TrimSpace(p.Transport))
	if transport == "" {
		transport = "memif"
	}
	if transport != "memif" && transport != "tap" {
		return store.VNFConfig{}, errors.New("config.transport must be memif or tap")
	}
	if p.LocalAS == 0 {
		return store.VNFConfig{}, errors.New("config.local_as is required")
	}
	if p.PeerAS == 0 {
		return store.VNFConfig{}, errors.New("config.peer_as is required")
	}
	routerID := net.ParseIP(strings.TrimSpace(p.RouterID)).To4()
	if routerID == nil {
		return store.VNFConfig{}, errors.New("config.router_id must be an IPv4 address")
	}
	localIP, localNet, err := net.ParseCIDR(strings.TrimSpace(p.LocalIP))
	if err != nil || localIP == nil {
		return store.VNFConfig{}, errors.New("config.local_ip must be an IPv4 or IPv6 CIDR")
	}
	ipv6 := localIP.To4() == nil
	ones, bits := localNet.Mask.Size()
	if (ipv6 && bits != 128) || (!ipv6 && bits != 32) || ones > bits {
		return store.VNFConfig{}, errors.New("config.local_ip must be an IPv4 or IPv6 CIDR")
	}
	local := localIP
	if !ipv6 {
		local = localIP.To4()
	}
	peerIP := net.ParseIP(strings.TrimSpace(p.PeerIP))
	if peerIP == nil || (peerIP.To4() == nil) != ipv6 {
		return store.VNFConfig{}, errors.New("config.peer_ip must use the same address family as config.local_ip")
	}
	if !ipv6 {
		peerIP = peerIP.To4()
	}
	if peerIP.Equal(local) {
		return store.VNFConfig{}, errors.New("config.peer_ip cannot equal config.local_ip")
	}
	peerGateway := strings.TrimSpace(p.PeerGateway)
	if peerGateway != "" {
		gw := net.ParseIP(peerGateway)
		if gw == nil || (gw.To4() == nil) != ipv6 {
			return store.VNFConfig{}, errors.New("config.peer_gateway must use the same address family as config.local_ip")
		}
		if !ipv6 {
			gw = gw.To4()
		}
		if !localNet.Contains(gw) {
			return store.VNFConfig{}, errors.New("config.peer_gateway must be inside config.local_ip subnet")
		}
		peerGateway = gw.String()
	} else if !localNet.Contains(peerIP) {
		return store.VNFConfig{}, errors.New("config.peer_gateway is required when peer_ip is outside the local subnet")
	}
	ttl := p.MultihopTTL
	if ttl == 0 {
		ttl = 64
	}
	if ttl > 255 {
		return store.VNFConfig{}, errors.New("config.multihop_ttl must be between 1 and 255")
	}
	password := strings.TrimSpace(p.Password)
	if password != "" && transport == "memif" {
		return store.VNFConfig{}, errors.New("BGP TCP MD5 password requires tap transport")
	}
	nextHop := ""
	if value := strings.TrimSpace(p.NextHop); value != "" {
		ip := net.ParseIP(value)
		if ip == nil || (ip.To4() == nil) != ipv6 {
			return store.VNFConfig{}, errors.New("config.next_hop must use the same address family as config.local_ip")
		}
		if !ipv6 {
			ip = ip.To4()
		}
		nextHop = ip.String()
	}
	if len(p.AdvertisePrefixes) > 1024 {
		return store.VNFConfig{}, errors.New("config.advertise_prefixes can contain at most 1024 prefixes")
	}
	prefixes := make([]string, 0, len(p.AdvertisePrefixes))
	seen := make(map[string]bool, len(p.AdvertisePrefixes))
	for i, value := range p.AdvertisePrefixes {
		ip, network, err := net.ParseCIDR(strings.TrimSpace(value))
		if err != nil || ip == nil || (ip.To4() == nil) != ipv6 {
			return store.VNFConfig{}, fmt.Errorf("config.advertise_prefixes[%d] must match the local_ip address family", i)
		}
		prefix := network.String()
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		prefixes = append(prefixes, prefix)
	}
	return store.VNFConfig{
		Transport:         transport,
		LocalAS:           p.LocalAS,
		RouterID:          routerID.String(),
		PeerAS:            p.PeerAS,
		PeerIP:            peerIP.String(),
		LocalIP:           fmt.Sprintf("%s/%d", local.String(), ones),
		PeerGateway:       peerGateway,
		MultihopTTL:       ttl,
		Password:          password,
		Passive:           p.Passive,
		TargetTableID:     p.TargetTableID,
		AdvertisePrefixes: prefixes,
		NextHop:           nextHop,
	}, nil
}
