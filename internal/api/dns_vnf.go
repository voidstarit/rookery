package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/voidstarit/rookery/internal/dnsrecord"
	"github.com/voidstarit/rookery/internal/store"
)

// NormalizeDNSRecord validates one static DNS record against the parent and the
// sibling records that remain. CNAME exclusivity spans rows and stays in Go.
func (s *Server) NormalizeDNSRecord(_ context.Context, vnf store.VNF, siblings []store.DNSRecord, item store.DNSRecord) (store.DNSRecord, error) {
	if vnf.Type != store.VNFTypeDNS {
		return store.DNSRecord{}, errors.New("DNS records are supported for DNS VNFs only")
	}
	name, err := dnsrecord.NormalizeName(item.Name)
	if err != nil {
		return store.DNSRecord{}, fmt.Errorf("name: %w", err)
	}
	recordType, err := dnsrecord.NormalizeType(item.Type)
	if err != nil {
		return store.DNSRecord{}, fmt.Errorf("type: %w", err)
	}
	value, err := dnsrecord.NormalizeValue(recordType, item.Value)
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
