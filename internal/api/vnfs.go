package api

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"strings"
	"time"

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
		return "", fmt.Errorf("type must be %q or %q", store.VNFTypeDHCPv4, store.VNFTypeDHCPv6)
	}
	return vnfType, nil
}

func (p vnfConfigPayload) toConfig(vnfType string) (store.VNFConfig, error) {
	gateway := strings.TrimSpace(p.Gateway)
	rangeStart := strings.TrimSpace(p.RangeStart)
	rangeEnd := strings.TrimSpace(p.RangeEnd)
	if gateway == "" {
		return store.VNFConfig{}, errors.New("config.gateway is required")
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
		return store.VNFConfig{
			Gateway:           normalizedGateway,
			RangeStart:        normalizedStart,
			RangeEnd:          normalizedEnd,
			DNS:               dns,
			LeaseTime:         leaseTime,
			BindIP:            bindIP,
			StaticAssignments: staticAssignments,
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
		if s.vnfRunning(vnf) {
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

func (s *Server) vnfRunning(vnf store.VNF) bool {
	if vnf.Type == store.VNFTypeNAT44 {
		s.natMu.Lock()
		defer s.natMu.Unlock()
		return s.natRunning[vnf.ID]
	}
	return s.vnfs != nil && s.vnfs.Running(vnf.ID)
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
	if err := s.cleanupLegacyNetworkVNF(ctx, network); err != nil {
		return vnf, err
	}

	if s.memifDHCP && vnf.Type == store.VNFTypeDHCPv4 {
		return s.activateMemifVNF(ctx, vnf, network)
	}

	hostIf := fmt.Sprintf("vnf%d", vnf.ID)
	tapSwIfIndex, err := s.networks.CreateTap(ctx, hostIf, vnfTapCIDR(vnf), network.BridgeDomainID)
	if err != nil {
		return vnf, fmt.Errorf("create VNF tap: %w", err)
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
		if vnf.MemifSwIfIndex > 0 || vnf.MemifSocket != "" {
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
