package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"go.fd.io/govpp/binapi/fib_types"
	"go.fd.io/govpp/binapi/ip"
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

	stream, err := ip.NewServiceClient(conn).IPRouteV2Dump(ctx, &ip.IPRouteV2Dump{
		Src:   0,
		Table: ip.IPTable{TableID: tableID, IsIP6: ipv6},
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
