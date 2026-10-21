package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"

	"go.fd.io/govpp/binapi/fib_types"
	ipapi "go.fd.io/govpp/binapi/ip"
	"go.fd.io/govpp/binapi/ip_types"
)

// FIBPath is one forwarding path of a VPP FIB entry.
type FIBPath struct {
	SwIfIndex     uint32 `json:"sw_if_index"`
	InterfaceName string `json:"interface_name,omitempty"`
	NextHop       string `json:"next_hop,omitempty"`
	Type          string `json:"type"`
	Weight        uint8  `json:"weight"`
	Preference    uint8  `json:"preference"`
	Proto         string `json:"proto,omitempty"`
}

// FIBRoute is a live VPP IP FIB entry, including connected and dynamic routes.
type FIBRoute struct {
	Prefix     string    `json:"prefix"`
	TableID    uint32    `json:"table_id"`
	IPv6       bool      `json:"ipv6"`
	Source     uint8     `json:"source"`
	SourceName string    `json:"source_name"`
	Dynamic    bool      `json:"dynamic"`
	StatsIndex uint32    `json:"stats_index"`
	Paths      []FIBPath `json:"paths"`
}

var fibSourceNames = map[uint8]string{
	0:  "invalid",
	1:  "special",
	2:  "classify",
	3:  "proxy",
	4:  "interface",
	5:  "SR",
	6:  "BIER",
	7:  "6RD",
	8:  "API",
	9:  "CLI",
	10: "LISP",
	11: "MAP",
	12: "DHCP",
	13: "IPv6 proxy ND",
	14: "IPv6 ND",
	15: "adjacency",
	16: "MPLS",
	17: "attached export",
	18: "recursive resolution",
	19: "uRPF exempt",
	20: "default route",
	21: "interpose",
}

// fibSourceName returns the VPP fib_source_t name for a source value.
func fibSourceName(source uint8) string {
	if name, ok := fibSourceNames[source]; ok {
		return name
	}
	return fmt.Sprintf("source-%d", source)
}

// dynamicFIBRoute reports whether a route is not one of Rookery's API-installed
// static routes. Connected, DHCP, ND, and adjacency entries are dynamic for
// troubleshooting purposes.
func dynamicFIBRoute(source uint8) bool {
	return source != 8 // FIB_SOURCE_API
}

// DumpFIBRoutes lists all live FIB entries in one table, including connected
// and dynamic routes. It uses ip_route_v2_dump so the source is available.
func (v *VPPNetwork) DumpFIBRoutes(ctx context.Context, tableID uint32, ipv6 bool) ([]FIBRoute, error) {
	interfaceNames := map[uint32]string{}
	if interfaces, err := v.VPPInterfaces(ctx); err == nil {
		for _, iface := range interfaces {
			interfaceNames[iface.SwIfIndex] = iface.Name
		}
	}

	conn, err := v.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Disconnect()

	stream, err := ipapi.NewServiceClient(conn).IPRouteV2Dump(ctx, &ipapi.IPRouteV2Dump{
		Src:   0,
		Table: ipapi.IPTable{TableID: tableID, IsIP6: ipv6},
	})
	if err != nil {
		return nil, fmt.Errorf("dump VPP routes: %w", err)
	}

	routes := make([]FIBRoute, 0)
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read VPP route dump: %w", err)
		}
		route := details.Route
		entry := FIBRoute{
			Prefix:     route.Prefix.String(),
			TableID:    route.TableID,
			IPv6:       ipv6,
			Source:     route.Src,
			SourceName: fibSourceName(route.Src),
			Dynamic:    dynamicFIBRoute(route.Src),
			StatsIndex: route.StatsIndex,
			Paths:      make([]FIBPath, 0, len(route.Paths)),
		}
		for _, path := range route.Paths {
			entry.Paths = append(entry.Paths, FIBPath{
				SwIfIndex:     path.SwIfIndex,
				InterfaceName: interfaceNames[path.SwIfIndex],
				NextHop:       fibPathNextHop(path.Proto, path.Nh.Address),
				Type:          cleanFIBPathType(path.Type.String()),
				Weight:        path.Weight,
				Preference:    path.Preference,
				Proto:         path.Proto.String(),
			})
		}
		routes = append(routes, entry)
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Prefix < routes[j].Prefix
	})
	return routes, nil
}

func fibPathNextHop(proto fib_types.FibPathNhProto, address ip_types.AddressUnion) string {
	switch proto {
	case fib_types.FIB_API_PATH_NH_PROTO_IP4:
		ip := address.GetIP4()
		if ip == [4]byte{} {
			return ""
		}
		return ip.String()
	case fib_types.FIB_API_PATH_NH_PROTO_IP6:
		ip := address.GetIP6()
		if ip == [16]byte{} {
			return ""
		}
		return ip.String()
	default:
		return ""
	}
}

func cleanFIBPathType(value string) string {
	return strings.TrimPrefix(value, "FIB_API_PATH_TYPE_")
}

// bgpFIBRouteSource is the VPP FIB source used for BGP-learned routes.
// Value 9 is FIB_SOURCE_CLI in VPP's fib_source_t enum, chosen deliberately:
// its priority (0x81) sits above the API source (0x80), so a BGP route wins
// over API/interface-installed routes for the same prefix, stays distinct
// from static API routes, and `show ip fib` labels it "CLI" (like
// `vppctl ip route add`).
const bgpFIBRouteSource uint8 = 9

// AddBGPRoute installs an IPv4 or IPv6 route learned from BGP into a VPP
// table. VPP decodes sw_if_index 0 as interface 0 (local0); a recursive path
// must send the ~0 sentinel instead and resolve its next hop in tableID.
func (v *VPPNetwork) AddBGPRoute(ctx context.Context, tableID uint32, prefix, nextHop string) error {
	prefix = strings.TrimSpace(prefix)
	if !strings.Contains(prefix, "/") {
		return errors.New("BGP route prefix must include a mask length")
	}
	parsedPrefix, err := ip_types.ParsePrefix(prefix)
	if err != nil {
		return fmt.Errorf("parse BGP route prefix: %w", err)
	}
	nh := net.ParseIP(strings.TrimSpace(nextHop))
	var (
		address ip_types.AddressUnion
		proto   fib_types.FibPathNhProto
	)
	switch parsedPrefix.Address.Af {
	case ip_types.ADDRESS_IP4:
		nh4 := nh.To4()
		if nh4 == nil {
			return errors.New("BGP route next hop family does not match the prefix")
		}
		address = ip_types.AddressUnionIP4(ip_types.IP4Address{nh4[0], nh4[1], nh4[2], nh4[3]})
		proto = fib_types.FIB_API_PATH_NH_PROTO_IP4
	case ip_types.ADDRESS_IP6:
		nh16 := nh.To16()
		if nh16 == nil {
			return errors.New("BGP route next hop family does not match the prefix")
		}
		var addr6 ip_types.IP6Address
		copy(addr6[:], nh16)
		address = ip_types.AddressUnionIP6(addr6)
		proto = fib_types.FIB_API_PATH_NH_PROTO_IP6
	default:
		return errors.New("only IPv4 and IPv6 BGP routes are supported")
	}
	route := ipapi.IPRouteV2{
		TableID: tableID,
		Prefix:  parsedPrefix,
		Src:     bgpFIBRouteSource,
		Paths: []fib_types.FibPath{{
			SwIfIndex: ^uint32(0),
			TableID:   tableID,
			Type:      fib_types.FIB_API_PATH_TYPE_NORMAL,
			Proto:     proto,
			Nh:        fib_types.FibPathNh{Address: address},
		}},
	}
	return v.changeRouteV2(ctx, true, route)
}

// DeleteBGPRoute removes an IPv4 or IPv6 route previously learned from BGP.
// A prefix without a mask length is treated as a host route so that routes
// tracked by older runtimes (which stored bare IPs) are still cleaned up on
// shutdown.
func (v *VPPNetwork) DeleteBGPRoute(ctx context.Context, tableID uint32, prefix string) error {
	prefix = strings.TrimSpace(prefix)
	if !strings.Contains(prefix, "/") {
		if ip := net.ParseIP(prefix); ip != nil && ip.To4() == nil {
			prefix += "/128"
		} else {
			prefix += "/32"
		}
	}
	parsedPrefix, err := ip_types.ParsePrefix(prefix)
	if err != nil {
		return fmt.Errorf("parse BGP route prefix: %w", err)
	}
	if parsedPrefix.Address.Af != ip_types.ADDRESS_IP4 && parsedPrefix.Address.Af != ip_types.ADDRESS_IP6 {
		return errors.New("only IPv4 and IPv6 BGP routes are supported")
	}
	return v.changeRouteV2(ctx, false, ipapi.IPRouteV2{
		TableID: tableID,
		Prefix:  parsedPrefix,
		Src:     bgpFIBRouteSource,
	})
}

func (v *VPPNetwork) changeRouteV2(ctx context.Context, isAdd bool, route ipapi.IPRouteV2) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()
	reply, err := ipapi.NewServiceClient(conn).IPRouteAddDelV2(ctx, &ipapi.IPRouteAddDelV2{
		IsAdd: isAdd,
		Route: route,
	})
	if err != nil {
		if !isAdd && isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("change BGP route: %w", err)
	}
	if err := checkVPPRetval("change BGP route", reply.Retval); err != nil {
		if !isAdd && isVPPNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}
