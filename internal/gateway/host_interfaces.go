package gateway

import (
	"context"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
)

// VPPInterfaceSource lists the interfaces currently present in VPP.
type VPPInterfaceSource interface {
	VPPInterfaces(ctx context.Context) ([]host.VPPInterface, error)
}

func (s *hostService) listInterfaces(ctx context.Context) (*rookeryv1.ListHostInterfacesResponse, error) {
	interfaces, err := host.ListHostInterfaces()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]host.VPPInterface)
	vppChecked := false
	if s.source != nil {
		if vppInterfaces, err := s.source.VPPInterfaces(ctx); err == nil {
			vppChecked = true
			for _, iface := range vppInterfaces {
				byName[iface.Name] = iface
			}
		}
	}
	response := &rookeryv1.ListHostInterfacesResponse{Interfaces: make([]*rookeryv1.HostInterface, 0, len(interfaces))}
	for _, iface := range interfaces {
		item := &rookeryv1.HostInterface{
			Name: iface.Name, Up: iface.Up, Addresses: iface.Addresses,
			DefaultRoute: iface.DefaultRoute, Physical: iface.Physical, Wireless: iface.Wireless,
			VppChecked: vppChecked,
		}
		for _, name := range []string{"host-" + iface.Name, iface.Name} {
			if vppIface, ok := byName[name]; ok {
				item.InVpp = true
				item.VppName = vppIface.Name
				item.VppUp = vppIface.Up
				item.VppType = vppIface.Type
				item.VppSwIfIndex = &vppIface.SwIfIndex
				break
			}
		}
		response.Interfaces = append(response.Interfaces, item)
	}
	return response, nil
}
