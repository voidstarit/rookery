// Package netutil holds small IP-prefix helpers shared by the API, lifecycle,
// and gateway layers. Rookery reserves the first five host addresses of each
// network CIDR: .1 is the BVI, .2 is the shared VNF memif, and .3-.5 are
// reserved for future internal use.
package netutil

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// ReservedHosts is the number of addresses reserved at the start of a network
// prefix for Rookery's own infrastructure.
const ReservedHosts = 5

// NetworkRouteTableBase is the first VPP table ID reserved for per-network
// VRFs. User-defined route tables must use IDs below this value.
const NetworkRouteTableBase = 100000

// NormalizeNetworkCIDRs validates and canonicalizes the required IPv4 and IPv6
// network prefixes. Each prefix must be large enough for at least eight hosts
// (five reserved addresses plus usable space).
func NormalizeNetworkCIDRs(ipv4CIDR, ipv6CIDR string) (string, string, error) {
	v4, err := normalizePrefix(ipv4CIDR, false)
	if err != nil {
		return "", "", fmt.Errorf("ipv4_cidr: %w", err)
	}
	v6, err := normalizePrefix(ipv6CIDR, true)
	if err != nil {
		return "", "", fmt.Errorf("ipv6_cidr: %w", err)
	}
	return v4, v6, nil
}

func normalizePrefix(value string, ipv6 bool) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("must be a valid CIDR")
	}
	if prefix.Addr().Zone() != "" {
		return "", errors.New("must not include a zone")
	}
	if ipv6 {
		if !prefix.Addr().Is6() || prefix.Addr().Is4In6() {
			return "", errors.New("must be an IPv6 CIDR")
		}
		if prefix.Bits() > 125 {
			return "", fmt.Errorf("must have at least %d host addresses", ReservedHosts+3)
		}
	} else {
		if !prefix.Addr().Is4() {
			return "", errors.New("must be an IPv4 CIDR")
		}
		if prefix.Bits() > 29 {
			return "", fmt.Errorf("must have at least %d host addresses", ReservedHosts+3)
		}
	}
	return prefix.Masked().String(), nil
}

// HostCIDR returns the address at offset (1-based host offset) inside cidr,
// preserving the prefix length.
func HostCIDR(cidr string, offset uint64) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return "", fmt.Errorf("parse network CIDR %q: %w", cidr, err)
	}
	addr := prefix.Masked().Addr()
	for i := uint64(0); i < offset; i++ {
		addr = addr.Next()
		if !addr.IsValid() || !prefix.Contains(addr) {
			return "", fmt.Errorf("network CIDR %s has no address at offset %d", cidr, offset)
		}
	}
	return netip.PrefixFrom(addr, prefix.Bits()).String(), nil
}

// ParseHost returns the address portion of a host CIDR.
func ParseHost(cidr string) (netip.Addr, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return netip.Addr{}, err
	}
	return prefix.Addr(), nil
}

// Contains reports whether addr is inside cidr.
func Contains(cidr, addr string) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return false
	}
	parsed, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	return prefix.Contains(parsed)
}

// IsReserved reports whether addr is one of the first five host addresses of
// cidr.
func IsReserved(cidr, addr string) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return false
	}
	parsed, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil || !prefix.Contains(parsed) {
		return false
	}
	base := prefix.Masked().Addr()
	current := base
	for i := 0; i < ReservedHosts; i++ {
		current = current.Next()
		if current == parsed {
			return true
		}
	}
	return false
}

// InRange reports whether start and end are inside cidr, start <= end, and the
// range does not include any reserved address.
func InRange(cidr, start, end string) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
	if err != nil {
		return false
	}
	first, err := netip.ParseAddr(strings.TrimSpace(start))
	if err != nil {
		return false
	}
	last, err := netip.ParseAddr(strings.TrimSpace(end))
	if err != nil || first.Compare(last) > 0 {
		return false
	}
	if !prefix.Contains(first) || !prefix.Contains(last) {
		return false
	}
	reservedEnd := prefix.Masked().Addr()
	for i := 0; i < ReservedHosts; i++ {
		reservedEnd = reservedEnd.Next()
	}
	return first.Compare(reservedEnd) > 0
}
