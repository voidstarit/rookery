package gateway_test

import (
	"context"
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

func TestVPPInterfaceRouteTables(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "vpp-interface-tables.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	network, err := st.CreateNetwork(ctx, store.Network{Name: "lab", BridgeDomainID: 760})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetNetworkRouteTable(ctx, network.ID, 600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRouteTable(ctx, store.RouteTable{Name: "network-lab-v4", TableID: 600, NetworkID: network.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRouteTable(ctx, store.RouteTable{Name: "network-lab-v6", TableID: 600, IPv6: true, NetworkID: network.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateNetworkHostTap(ctx, store.NetworkHostTap{
		NetworkID: network.ID, Name: "tap-lab", SwIfIndex: 30, IPv4Address: "10.20.0.10/24",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateInterface(ctx, store.VirtualInterface{
		Name: "vhost-lab", NetworkID: network.ID, SwIfIndex: 31,
	}); err != nil {
		t.Fatal(err)
	}
	ipv4, err := st.CreateRouteTable(ctx, store.RouteTable{Name: "lab-v4", TableID: 500})
	if err != nil {
		t.Fatal(err)
	}
	ipv6, err := st.CreateRouteTable(ctx, store.RouteTable{Name: "lab-v6", TableID: 501, IPv6: true})
	if err != nil {
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
		ifaces: []host.VPPInterface{
			{Name: "tap-lab", SwIfIndex: 30},
			{Name: "vhost-lab", SwIfIndex: 31},
		},
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
	response, body := get("/api/vpp/interfaces/tap-lab/route-tables")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initial tables = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	if body["ipv4_table_id"].(float64) != 600 || body["ipv6_table_id"].(float64) != 600 {
		t.Fatalf("initial tables = %+v", body)
	}
	if body["ipv4_table_name"] != "network-lab-v4" || body["ipv6_table_name"] != "network-lab-v6" {
		t.Fatalf("initial table names = %+v", body)
	}

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/vpp/interfaces/tap-lab/route-tables",
		strings.NewReader(`{"ipv4_table_id":`+itoa(int64(ipv4.TableID))+`,"ipv6_table_id":`+itoa(int64(ipv6.TableID))+`}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update tables = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var updated map[string]any
	decodeBody(t, response, &updated)
	if updated["ipv4_table_name"] != "lab-v4" || updated["ipv6_table_name"] != "lab-v6" {
		t.Fatalf("updated tables = %+v", updated)
	}
	if len(runtime.tables) != 2 || runtime.tables[0] != "30/false/500" || runtime.tables[1] != "30/true/501" {
		t.Fatalf("runtime table calls = %+v", runtime.tables)
	}

	response, body = get("/api/vpp/interfaces/tap-lab/route-tables")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get tables = %d", response.StatusCode)
	}
	if body["ipv4_table_id"].(float64) != 500 || body["ipv6_table_id"].(float64) != 501 {
		t.Fatalf("stored tables = %+v", body)
	}

	response, body = get("/api/vpp/interfaces/vhost-lab/route-tables")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("vhost tables = %d, body = %+v", response.StatusCode, body)
	}
	_ = response.Body.Close()

	response, body = get("/api/vpp/interfaces/unknown/route-tables")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown interface = %d, body = %+v", response.StatusCode, body)
	}
	_ = response.Body.Close()

	// Table IDs must reference tables of the matching family.
	request, err = http.NewRequest(http.MethodPut, server.URL+"/api/vpp/interfaces/tap-lab/route-tables",
		strings.NewReader(`{"ipv4_table_id":`+itoa(int64(ipv4.TableID))+`,"ipv6_table_id":`+itoa(int64(ipv4.TableID))+`}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("mismatched table family = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	_ = response.Body.Close()

}
