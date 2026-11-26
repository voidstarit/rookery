package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/voidstarit/rookery/internal/store"
)

// NormalizeNAT44VNFConfig validates the NAT44 gateway, outside interface,
// address/gateway, and pool, returning the canonical config. DNAT rules are
// child rows and are validated on their own write path.
func (s *Server) NormalizeNAT44VNFConfig(config store.NAT44VNFConfig) (store.NAT44VNFConfig, error) {
	gateway := strings.TrimSpace(config.Gateway)
	gatewayIP, ipnet, err := net.ParseCIDR(gateway)
	if err != nil {
		return store.NAT44VNFConfig{}, fmt.Errorf("invalid NAT44 gateway CIDR: %v", err)
	}
	gateway4 := gatewayIP.To4()
	network4 := ipnet.IP.To4()
	if gateway4 == nil || network4 == nil {
		return store.NAT44VNFConfig{}, errors.New("NAT44 gateway must be an IPv4 CIDR")
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones > 30 {
		return store.NAT44VNFConfig{}, errors.New("NAT44 gateway prefix must be /30 or smaller")
	}
	broadcast := broadcastIPv4(ipnet)
	if gateway4.Equal(network4) || gateway4.Equal(broadcast) {
		return store.NAT44VNFConfig{}, errors.New("NAT44 gateway must be a usable host address")
	}
	outsideInterface := strings.TrimSpace(config.OutsideInterface)
	if outsideInterface == "" {
		return store.NAT44VNFConfig{}, errors.New("config.outside_interface is required for NAT44")
	}
	outsideAddress := strings.TrimSpace(config.OutsideAddress)
	outsideGateway := strings.TrimSpace(config.OutsideGateway)
	if outsideAddress != "" {
		ip, outsideNet, err := net.ParseCIDR(outsideAddress)
		if err != nil || ip.To4() == nil {
			return store.NAT44VNFConfig{}, errors.New("config.outside_address must be an IPv4 CIDR")
		}
		ones, bits := outsideNet.Mask.Size()
		if bits != 32 || ones == 0 {
			return store.NAT44VNFConfig{}, errors.New("config.outside_address must have a nonzero IPv4 prefix")
		}
	} else {
		outsideAddress = ""
		if outsideGateway != "" {
			return store.NAT44VNFConfig{}, errors.New("config.outside_gateway requires config.outside_address")
		}
	}
	if outsideGateway != "" {
		if ip := net.ParseIP(outsideGateway).To4(); ip == nil {
			return store.NAT44VNFConfig{}, errors.New("config.outside_gateway must be an IPv4 address")
		}
		if outsideAddress != "" {
			_, outsideNet, _ := net.ParseCIDR(outsideAddress)
			if !outsideNet.Contains(net.ParseIP(outsideGateway).To4()) {
				return store.NAT44VNFConfig{}, errors.New("config.outside_gateway must be inside the outside subnet")
			}
		}
	}
	pool := strings.TrimSpace(config.NAT44Pool)
	if pool != "" && !validNAT44Pool(pool) {
		return store.NAT44VNFConfig{}, errors.New("config.nat44_pool must be a single IPv4 address or start-end range")
	}
	return store.NAT44VNFConfig{
		Gateway:          fmt.Sprintf("%s/%d", gateway4.String(), ones),
		OutsideInterface: outsideInterface,
		OutsideAddress:   outsideAddress,
		OutsideGateway:   outsideGateway,
		NAT44Pool:        pool,
	}, nil
}

// NormalizeDNATRule validates one DNAT rule against the parent's NAT44 subnet.
func (s *Server) NormalizeDNATRule(_ context.Context, vnf store.VNF, _ []store.DNATRule, item store.DNATRule) (store.DNATRule, error) {
	if vnf.Type != store.VNFTypeNAT44 {
		return store.DNATRule{}, errors.New("DNAT rules are supported for NAT44 VNFs only")
	}
	_, subnet, err := net.ParseCIDR(vnf.Config.Gateway)
	if err != nil {
		return store.DNATRule{}, fmt.Errorf("invalid NAT44 gateway: %w", err)
	}
	rules, err := normalizeDNATRules([]store.DNATRule{item}, subnet)
	if err != nil {
		return store.DNATRule{}, err
	}
	return rules[0], nil
}

// normalizeDNATRules validates DNAT rules, requiring tcp or udp, a usable local
// address inside subnet, and valid ports.
func normalizeDNATRules(payload []store.DNATRule, subnet *net.IPNet) ([]store.DNATRule, error) {
	const maxDNATRules = 256
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

// validNAT44Pool reports whether value is a single IPv4 address or an ascending
// start-end IPv4 range.
func validNAT44Pool(value string) bool {
	if strings.Contains(value, "-") {
		parts := strings.SplitN(value, "-", 2)
		start := net.ParseIP(strings.TrimSpace(parts[0])).To4()
		end := net.ParseIP(strings.TrimSpace(parts[1])).To4()
		return start != nil && end != nil && bytesCompareIPv4(start, end) <= 0
	}
	return net.ParseIP(strings.TrimSpace(value)).To4() != nil
}
