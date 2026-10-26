package gateway_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeInterfaceAddressRuntime struct {
	added          []string
	removed        []string
	enabledDHCP    []string
	disabledDHCP   []string
	ifaces         []host.VPPInterface
	live           []string
	dhcpConfigured bool
	dhcpDetail     string
	tables         []string
}

func (f *fakeInterfaceAddressRuntime) AddInterfaceAddress(_ context.Context, name, cidr string) error {
	f.added = append(f.added, name+" "+cidr)
	return nil
}
func (f *fakeInterfaceAddressRuntime) DelInterfaceAddress(_ context.Context, name, cidr string) error {
	f.removed = append(f.removed, name+" "+cidr)
	return nil
}
func (f *fakeInterfaceAddressRuntime) EnableDHCPClient(_ context.Context, name string) error {
	f.enabledDHCP = append(f.enabledDHCP, name)
	return nil
}
func (f *fakeInterfaceAddressRuntime) DisableDHCPClient(_ context.Context, name string) error {
	f.disabledDHCP = append(f.disabledDHCP, name)
	return nil
}
func (f *fakeInterfaceAddressRuntime) InterfaceAddresses(context.Context, string) ([]string, error) {
	return f.live, nil
}
func (f *fakeInterfaceAddressRuntime) DHCPClientStatus(context.Context, string) (bool, string, error) {
	return f.dhcpConfigured, f.dhcpDetail, nil
}
func (f *fakeInterfaceAddressRuntime) VPPInterfaces(context.Context) ([]host.VPPInterface, error) {
	return f.ifaces, nil
}
func (f *fakeInterfaceAddressRuntime) SetInterfaceRouteTable(_ context.Context, swIfIndex uint32, ipv6 bool, tableID uint32) error {
	f.tables = append(f.tables, fmt.Sprintf("%d/%t/%d", swIfIndex, ipv6, tableID))
	return nil
}

func TestVPPInterfaceAddresses(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "vpp-addresses.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	network, err := st.CreateNetwork(context.Background(), store.Network{Name: "lab", BridgeDomainID: 720})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateInterface(context.Background(), store.VirtualInterface{
		Name: "vhost-lab", NetworkID: network.ID, SwIfIndex: 10,
	}); err != nil {
		t.Fatal(err)
	}
	checked := time.Now()
	ready := fakeReadiness{status: readiness.Status{
		Ready:     true,
		CheckedAt: &checked,
		VPP: readiness.VPPStatus{
			ComponentStatus: readiness.ComponentStatus{Ready: true, State: "ready"},
			Socket:          "/run/vpp/api.sock",
		},
	}}
	runtime := &fakeInterfaceAddressRuntime{
		ifaces:         []host.VPPInterface{{Name: "vhost-lab", SwIfIndex: 10}},
		live:           []string{"192.168.100.92/24"},
		dhcpConfigured: true,
		dhcpDetail:     "vhost-lab",
	}
	gw, err := gateway.New(st, ready, gateway.WithInterfaceAddressRuntime(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	get := func(path string) (*http.Response, map[string]any) {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if response.StatusCode == http.StatusOK {
			decodeBody(t, response, &body)
		}
		return response, body
	}
	response, body := get("/api/vpp/interfaces/vhost-lab/addresses")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initial addresses = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	if name := body["name"]; name != "vhost-lab" {
		t.Fatalf("address interface = %+v", body)
	}
	if live := body["live_addresses"].([]any); len(live) != 1 || live[0] != "192.168.100.92/24" {
		t.Fatalf("live addresses = %+v", body["live_addresses"])
	}
	if body["dhcp_client_configured"] != true {
		t.Fatalf("DHCP configured = %+v", body)
	}

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/vpp/interfaces/vhost-lab/addresses",
		strings.NewReader(`{"addresses":["10.20.0.10/24","fd00:20::10/64"],"dhcp_client_enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update addresses = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var updated map[string]any
	decodeBody(t, response, &updated)
	addresses, _ := updated["addresses"].([]any)
	if len(addresses) != 2 {
		t.Fatalf("updated addresses = %+v", updated)
	}
	if len(runtime.added) != 2 {
		t.Fatalf("runtime added = %+v", runtime.added)
	}
	if len(runtime.enabledDHCP) != 1 || runtime.enabledDHCP[0] != "vhost-lab" {
		t.Fatalf("runtime DHCP enabled = %+v", runtime.enabledDHCP)
	}
	if updated["dhcp_client_enabled"] != true {
		t.Fatalf("updated DHCP state = %+v", updated)
	}

	response, body = get("/api/vpp/interfaces/vhost-lab/addresses")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get addresses = %d", response.StatusCode)
	}
	if len(body["addresses"].([]any)) != 2 {
		t.Fatalf("stored addresses = %+v", body)
	}

	request, err = http.NewRequest(http.MethodPut, server.URL+"/api/vpp/interfaces/vhost-lab/addresses",
		strings.NewReader(`{"addresses":["10.20.0.10/24","fd00:20::10/64"],"dhcp_client_enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disable DHCP = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	if len(runtime.disabledDHCP) != 1 || runtime.disabledDHCP[0] != "vhost-lab" {
		t.Fatalf("runtime DHCP disabled = %+v", runtime.disabledDHCP)
	}

	response, body = get("/api/vpp/interfaces/unknown/addresses")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown interface = %d, body = %+v", response.StatusCode, body)
	}
}
