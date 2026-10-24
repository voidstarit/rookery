package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/voidstarit/rookery/internal/host"
)

type fakeVPPInterfaceSource struct {
	interfaces []host.VPPInterface
	err        error
}

func (f fakeVPPInterfaceSource) VPPInterfaces(context.Context) ([]host.VPPInterface, error) {
	return f.interfaces, f.err
}

func TestHostInterfacesCorrelateVPP(t *testing.T) {
	interfaces, err := host.ListHostInterfaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(interfaces) == 0 {
		t.Skip("no host interfaces")
	}
	name := interfaces[0].Name
	service := &hostService{source: fakeVPPInterfaceSource{interfaces: []host.VPPInterface{{
		Name: "host-" + name, SwIfIndex: 42, Type: "af_packet", Up: true,
	}}}}
	response, err := service.listInterfaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Interfaces) == 0 {
		t.Fatal("no host interfaces returned")
	}
	found := false
	for _, iface := range response.Interfaces {
		if iface.GetName() != name {
			continue
		}
		found = true
		if !iface.GetVppChecked() || !iface.GetInVpp() || iface.GetVppSwIfIndex() != 42 {
			t.Fatalf("interface = %+v", iface)
		}
	}
	if !found {
		t.Fatalf("host interface %q missing", name)
	}
}

func TestHostInterfacesUnknownWhenVPPUnavailable(t *testing.T) {
	interfaces, err := host.ListHostInterfaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(interfaces) == 0 {
		t.Skip("no host interfaces")
	}
	service := &hostService{source: fakeVPPInterfaceSource{err: errors.New("VPP unavailable")}}
	response, err := service.listInterfaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range response.Interfaces {
		if iface.GetVppChecked() {
			t.Fatalf("interface %s incorrectly marked checked", iface.GetName())
		}
	}
}
