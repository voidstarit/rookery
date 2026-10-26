package host

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	ipapi "go.fd.io/govpp/binapi/ip"
	ip_types "go.fd.io/govpp/binapi/ip_types"
)

// RouteSpec is the full VPP route command description used by Rookery.
type RouteSpec struct {
	TableID             uint32
	IPv6                bool
	Prefix              string
	NextHop             string
	EgressInterface     string
	Preference          *uint32
	Count               *uint32
	NextHopTable        *uint32
	Weight              *uint32
	UDPEncap            *uint32
	IPv4LookupTable     *uint32
	IPv6LookupTable     *uint32
	MPLSLookupTable     *uint32
	ResolveViaHost      bool
	ResolveViaConnected bool
	RxIPv4Interface     string
	RxIPv6Interface     string
	OutLabels           []uint32
	Drop                bool
}

// AddRouteSpec installs a route using the full VPP `ip route` CLI syntax.
// The egress interface must be a VPP interface name.
func (v *VPPNetwork) AddRouteSpec(ctx context.Context, spec RouteSpec) error {
	prefix := strings.TrimSpace(spec.Prefix)
	if prefix == "" {
		return errors.New("route prefix is required")
	}
	if spec.RxIPv4Interface != "" && spec.RxIPv6Interface != "" {
		return errors.New("rx-ip4 and rx-ip6 are mutually exclusive")
	}
	if spec.NextHop == "" && spec.EgressInterface == "" && !spec.Drop &&
		spec.RxIPv4Interface == "" && spec.RxIPv6Interface == "" &&
		spec.NextHopTable == nil && spec.UDPEncap == nil &&
		spec.IPv4LookupTable == nil && spec.IPv6LookupTable == nil && spec.MPLSLookupTable == nil {
		return errors.New("route requires a next hop, egress interface, rx interface, lookup option, or drop")
	}

	args := []string{"ip", "route", "add"}
	if spec.Count != nil {
		args = append(args, "count", strconv.FormatUint(uint64(*spec.Count), 10))
	}
	args = append(args, prefix)
	if spec.TableID != 0 {
		args = append(args, "table", strconv.FormatUint(uint64(spec.TableID), 10))
	}
	args = append(args, "via")
	if spec.NextHop != "" {
		args = append(args, spec.NextHop)
	}
	if spec.EgressInterface != "" {
		args = append(args, spec.EgressInterface)
	}
	if spec.NextHopTable != nil {
		args = append(args, "next-hop-table", strconv.FormatUint(uint64(*spec.NextHopTable), 10))
	}
	if spec.Weight != nil {
		args = append(args, "weight", strconv.FormatUint(uint64(*spec.Weight), 10))
	}
	if spec.Preference != nil {
		args = append(args, "preference", strconv.FormatUint(uint64(*spec.Preference), 10))
	}
	if spec.UDPEncap != nil {
		args = append(args, "udp-encap", strconv.FormatUint(uint64(*spec.UDPEncap), 10))
	}
	if spec.IPv4LookupTable != nil {
		args = append(args, "ip4-lookup-in-table", strconv.FormatUint(uint64(*spec.IPv4LookupTable), 10))
	}
	if spec.IPv6LookupTable != nil {
		args = append(args, "ip6-lookup-in-table", strconv.FormatUint(uint64(*spec.IPv6LookupTable), 10))
	}
	if spec.MPLSLookupTable != nil {
		args = append(args, "mpls-lookup-in-table", strconv.FormatUint(uint64(*spec.MPLSLookupTable), 10))
	}
	if spec.ResolveViaHost {
		args = append(args, "resolve-via-host")
	}
	if spec.ResolveViaConnected {
		args = append(args, "resolve-via-connected")
	}
	if spec.RxIPv4Interface != "" {
		args = append(args, "rx-ip4", spec.RxIPv4Interface)
	}
	if spec.RxIPv6Interface != "" {
		args = append(args, "rx-ip6", spec.RxIPv6Interface)
	}
	if len(spec.OutLabels) > 0 {
		if len(spec.OutLabels) > 16 {
			return errors.New("at most 16 out-labels are allowed")
		}
		args = append(args, "out-labels")
		for _, label := range spec.OutLabels {
			args = append(args, strconv.FormatUint(uint64(label), 10))
		}
	}
	if spec.Drop {
		args = append(args, "drop")
	}
	out, err := v.runCLI(ctx, args...)
	if err != nil {
		return fmt.Errorf("add route %s: %w: %s", prefix, err, out)
	}
	return nil
}

// deleteRouteAPI removes the route from the binary-API source used by older
// Rookery versions.
func (v *VPPNetwork) deleteRouteAPI(ctx context.Context, tableID uint32, ipv6 bool, prefix string) error {
	parsedPrefix, err := ip_types.ParsePrefix(strings.TrimSpace(prefix))
	if err != nil {
		return fmt.Errorf("parse route prefix: %w", err)
	}
	if (parsedPrefix.Address.Af == ip_types.ADDRESS_IP6) != ipv6 {
		return errors.New("route prefix family does not match the table")
	}
	return v.changeRoute(ctx, false, ipapi.IPRoute{TableID: tableID, Prefix: parsedPrefix})
}

// deleteRouteCLI removes the route from the VPP CLI source.
func (v *VPPNetwork) deleteRouteCLI(ctx context.Context, tableID uint32, prefix string) error {
	args := []string{"ip", "route", "del", strings.TrimSpace(prefix)}
	if tableID != 0 {
		args = append(args, "table", strconv.FormatUint(uint64(tableID), 10))
	}
	out, err := v.runCLI(ctx, args...)
	if err != nil && !strings.Contains(strings.ToLower(out), "not found") {
		return fmt.Errorf("delete route %s: %w: %s", prefix, err, out)
	}
	return nil
}
