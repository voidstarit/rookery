// Package dnsrecord normalizes the name, type, value, and TTL of a DNS record.
// The API validates writes with it, and the Terraform provider sends the same
// canonical form when it writes a record, so a non-canonical configuration does
// not produce an inconsistent result after apply.
package dnsrecord

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/miekg/dns"
)

// DefaultTTL is the TTL the API stores for a record that configures none.
const DefaultTTL uint32 = 300

// MaxTTL is the largest record TTL the API accepts, in seconds.
const MaxTTL uint32 = 86400

// NormalizeName validates a record owner name or a CNAME/PTR target, allowing
// one leading wildcard label (*.example.com), and returns the canonical
// trailing-dot form the API stores. The DNS root and names with a non-leading
// wildcard are rejected.
func NormalizeName(value string) (string, error) {
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

// NormalizeType trims and upper-cases a record type, rejecting an unsupported
// one.
func NormalizeType(value string) (string, error) {
	recordType := strings.ToUpper(strings.TrimSpace(value))
	switch recordType {
	case "A", "AAAA", "CNAME", "TXT", "PTR":
		return recordType, nil
	default:
		return "", errors.New("type must be A, AAAA, CNAME, TXT, or PTR")
	}
}

// NormalizeValue validates and canonicalizes a record value for its type: A and
// AAAA addresses, CNAME and PTR names without wildcards, or TXT up to 255 bytes.
// The record type is normalized first, so a caller may pass it in any case.
func NormalizeValue(recordType, value string) (string, error) {
	recordType, err := NormalizeType(recordType)
	if err != nil {
		return "", err
	}
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
		name, err := NormalizeName(value)
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
		name, err := NormalizeName(value)
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

// NormalizeTTL validates a record TTL and substitutes DefaultTTL for zero, the
// value the API receives when a write does not set one.
func NormalizeTTL(ttl uint32) (uint32, error) {
	if ttl > MaxTTL {
		return 0, fmt.Errorf("ttl must be at most %d", MaxTTL)
	}
	if ttl == 0 {
		return DefaultTTL, nil
	}
	return ttl, nil
}
