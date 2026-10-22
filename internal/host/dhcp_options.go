package host

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/miekg/dns"

	"github.com/voidstarit/rookery/internal/store"
)

const maxDHCPOptions = 64

var dhcpv4ServerOptions = map[uint16]string{
	1:  "subnet mask",
	3:  "router",
	6:  "DNS servers (use config.dns)",
	28: "broadcast address",
	51: "lease time",
	53: "message type",
	54: "server identifier",
	58: "renewal time",
	59: "rebinding time",
}

var dhcpv6ServerOptions = map[uint16]string{
	1:  "client identifier",
	2:  "server identifier",
	3:  "IA_NA",
	5:  "IA address",
	6:  "option request",
	7:  "preference",
	8:  "elapsed time",
	9:  "relay message",
	13: "status code",
	14: "rapid commit",
	23: "DNS servers (use config.dns)",
	25: "IA_PD",
	26: "IA prefix",
}

type parsedDHCPOption struct {
	code  uint16
	value []byte
}

// NormalizeDHCPOptions validates and canonicalizes extra DHCP options for a
// DHCPv4 or DHCPv6 VNF.
func NormalizeDHCPOptions(options []store.DHCPOption, ipv6 bool) ([]store.DHCPOption, error) {
	if len(options) > maxDHCPOptions {
		return nil, fmt.Errorf("at most %d DHCP options are supported", maxDHCPOptions)
	}
	normalized := make([]store.DHCPOption, 0, len(options))
	seen := make(map[uint16]struct{}, len(options))
	for index, option := range options {
		if _, exists := seen[option.Code]; exists {
			return nil, fmt.Errorf("dhcp_options[%d]: option %d is configured more than once", index, option.Code)
		}
		if err := validateDHCPOptionCode(option.Code, ipv6); err != nil {
			return nil, fmt.Errorf("dhcp_options[%d]: %w", index, err)
		}
		format := strings.ToLower(strings.TrimSpace(option.Format))
		if format == "" {
			format = "string"
		}
		value, err := normalizeDHCPOptionValue(format, option.Value, ipv6)
		if err != nil {
			return nil, fmt.Errorf("dhcp_options[%d]: %w", index, err)
		}
		canonical := store.DHCPOption{Code: option.Code, Format: format, Value: value}
		data, err := dhcpOptionData(canonical, ipv6)
		if err != nil {
			return nil, fmt.Errorf("dhcp_options[%d]: %w", index, err)
		}
		limit := 255
		if ipv6 {
			limit = 1024
		}
		if len(data) > limit {
			return nil, fmt.Errorf("dhcp_options[%d]: encoded value exceeds %d bytes", index, limit)
		}
		seen[option.Code] = struct{}{}
		normalized = append(normalized, canonical)
	}
	return normalized, nil
}

func validateDHCPOptionCode(code uint16, ipv6 bool) error {
	if code == 0 {
		return errors.New("option code must be greater than zero")
	}
	if ipv6 {
		if name, reserved := dhcpv6ServerOptions[code]; reserved {
			return fmt.Errorf("option %d (%s) is managed by the server", code, name)
		}
		return nil
	}
	if code > 254 {
		return fmt.Errorf("DHCPv4 option code must be between 1 and 254, got %d", code)
	}
	if name, reserved := dhcpv4ServerOptions[code]; reserved {
		return fmt.Errorf("option %d (%s) is managed by the server", code, name)
	}
	return nil
}

func normalizeDHCPOptionValue(format, value string, ipv6 bool) (string, error) {
	value = strings.TrimSpace(value)
	switch format {
	case "string":
		if value == "" {
			return "", errors.New("value is required")
		}
		if (ipv6 && len(value) > 1024) || (!ipv6 && len(value) > 255) {
			return "", errors.New("value is too long")
		}
		return value, nil
	case "ipv4":
		ips, err := normalizeDHCPIPList(value, false)
		if err != nil {
			return "", err
		}
		return strings.Join(ips, ","), nil
	case "ipv6":
		ips, err := normalizeDHCPIPList(value, true)
		if err != nil {
			return "", err
		}
		return strings.Join(ips, ","), nil
	case "hex":
		cleaned := strings.NewReplacer(":", "", "-", "", " ", "", "0x", "", "0X", "").Replace(value)
		if cleaned == "" {
			return "", errors.New("value is required")
		}
		if len(cleaned)%2 != 0 {
			return "", errors.New("hex value must contain an even number of digits")
		}
		if _, err := hex.DecodeString(cleaned); err != nil {
			return "", errors.New("value must be valid hexadecimal")
		}
		return strings.ToLower(cleaned), nil
	case "uint8", "uint16", "uint32":
		bits := map[string]int{"uint8": 8, "uint16": 16, "uint32": 32}[format]
		number, err := strconv.ParseUint(value, 0, bits)
		if err != nil {
			return "", fmt.Errorf("value must be an unsigned %d-bit integer", bits)
		}
		return strconv.FormatUint(number, 10), nil
	case "domain-list":
		parts := strings.Split(value, ",")
		names := make([]string, 0, len(parts))
		for _, part := range parts {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			canonical := dns.CanonicalName(name)
			if canonical == "." {
				return "", errors.New("domain name must not be the DNS root")
			}
			if _, ok := dns.IsDomainName(canonical); !ok {
				return "", fmt.Errorf("invalid domain name %q", name)
			}
			names = append(names, canonical)
		}
		if len(names) == 0 {
			return "", errors.New("at least one domain name is required")
		}
		return strings.Join(names, ","), nil
	default:
		return "", fmt.Errorf("format must be string, ipv4, ipv6, hex, uint8, uint16, uint32, or domain-list, got %q", format)
	}
}

func normalizeDHCPIPList(value string, ipv6 bool) ([]string, error) {
	parts := strings.Split(value, ",")
	ips := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ip := net.ParseIP(part)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP address %q", part)
		}
		if ipv6 {
			if ip.To4() != nil {
				return nil, fmt.Errorf("%q is not an IPv6 address", part)
			}
			ips = append(ips, ip.To16().String())
		} else {
			if ip.To4() == nil {
				return nil, fmt.Errorf("%q is not an IPv4 address", part)
			}
			ips = append(ips, ip.To4().String())
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("at least one IP address is required")
	}
	return ips, nil
}

func dhcpOptionData(option store.DHCPOption, ipv6 bool) ([]byte, error) {
	switch option.Format {
	case "string":
		return []byte(option.Value), nil
	case "ipv4":
		ips, err := normalizeDHCPIPList(option.Value, false)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(ips)*4)
		for _, value := range ips {
			out = append(out, net.ParseIP(value).To4()...)
		}
		return out, nil
	case "ipv6":
		ips, err := normalizeDHCPIPList(option.Value, true)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(ips)*16)
		for _, value := range ips {
			out = append(out, net.ParseIP(value).To16()...)
		}
		return out, nil
	case "hex":
		value := strings.NewReplacer(":", "", "-", "", " ", "", "0x", "", "0X", "").Replace(option.Value)
		return hex.DecodeString(value)
	case "uint8":
		number, err := strconv.ParseUint(option.Value, 10, 8)
		if err != nil {
			return nil, err
		}
		return []byte{byte(number)}, nil
	case "uint16":
		number, err := strconv.ParseUint(option.Value, 10, 16)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 2)
		binary.BigEndian.PutUint16(out, uint16(number))
		return out, nil
	case "uint32":
		number, err := strconv.ParseUint(option.Value, 10, 32)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, uint32(number))
		return out, nil
	case "domain-list":
		var out []byte
		for _, name := range strings.Split(option.Value, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			buf := make([]byte, 255)
			offset, err := dns.PackDomainName(dns.CanonicalName(name), buf, 0, nil, false)
			if err != nil {
				return nil, err
			}
			out = append(out, buf[:offset]...)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported DHCP option format %q", option.Format)
	}
}

func parseDHCPOptionsJSON(raw string, ipv6 bool) ([]parsedDHCPOption, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var options []store.DHCPOption
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return nil, fmt.Errorf("parse DHCP options: %w", err)
	}
	normalized, err := NormalizeDHCPOptions(options, ipv6)
	if err != nil {
		return nil, err
	}
	parsed := make([]parsedDHCPOption, 0, len(normalized))
	for _, option := range normalized {
		value, err := dhcpOptionData(option, ipv6)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, parsedDHCPOption{code: option.Code, value: value})
	}
	return parsed, nil
}
