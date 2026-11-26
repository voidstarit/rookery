package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/miekg/dns"

	"github.com/voidstarit/rookery/internal/store"
)

// NormalizeDNSRecord validates one static DNS record against the parent and the
// sibling records that remain. CNAME exclusivity spans rows and stays in Go.
func (s *Server) NormalizeDNSRecord(_ context.Context, vnf store.VNF, siblings []store.DNSRecord, item store.DNSRecord) (store.DNSRecord, error) {
	if vnf.Type != store.VNFTypeDNS {
		return store.DNSRecord{}, errors.New("DNS records are supported for DNS VNFs only")
	}
	name, err := normalizeDNSRecordName(item.Name)
	if err != nil {
		return store.DNSRecord{}, fmt.Errorf("name: %w", err)
	}
	recordType := strings.ToUpper(strings.TrimSpace(item.Type))
	value, err := normalizeDNSRecordValue(recordType, item.Value)
	if err != nil {
		return store.DNSRecord{}, fmt.Errorf("value: %w", err)
	}
	ttl := item.TTL
	if ttl == 0 {
		ttl = 300
	}
	if ttl > 86400 {
		return store.DNSRecord{}, errors.New("ttl must be at most 86400")
	}
	for _, sibling := range siblings {
		if sibling.Name != name {
			continue
		}
		if recordType == "CNAME" {
			return store.DNSRecord{}, fmt.Errorf("%s already has a record", name)
		}
		if sibling.Type == "CNAME" {
			return store.DNSRecord{}, fmt.Errorf("%s already has a CNAME record", name)
		}
	}
	return store.DNSRecord{Name: name, Type: recordType, Value: value, TTL: ttl}, nil
}

// NormalizeDNSForwarder validates one upstream resolver.
func (s *Server) NormalizeDNSForwarder(_ context.Context, vnf store.VNF, _ []store.DNSForwarder, item store.DNSForwarder) (store.DNSForwarder, error) {
	if vnf.Type != store.VNFTypeDNS {
		return store.DNSForwarder{}, errors.New("forwarders are supported for DNS VNFs only")
	}
	address, err := normalizeDNSForwarder(item.Address)
	if err != nil {
		return store.DNSForwarder{}, err
	}
	if item.Position < 0 {
		return store.DNSForwarder{}, errors.New("position must not be negative")
	}
	return store.DNSForwarder{Address: address, Position: item.Position}, nil
}

// normalizeDNSRecordName validates a DNS record owner name, allowing one
// leading wildcard label (*.example.com), and returns the canonical name.
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

// normalizeDNSRecordValue validates and canonicalizes a record value for its
// type: A and AAAA addresses, CNAME and PTR names without wildcards, or TXT up
// to 255 bytes.
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

// normalizeDNSForwarder accepts an IP address or IP:port, defaulting the port
// to 53, and returns the canonical host:port form.
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
