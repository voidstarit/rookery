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

func TestNetworkPipeEndpointAddresses(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "pipe-addresses.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	networkA, err := st.CreateNetwork(ctx, store.Network{Name: "lab-a", BridgeDomainID: 770})
	if err != nil {
		t.Fatal(err)
	}
	networkB, err := st.CreateNetwork(ctx, store.Network{Name: "lab-b", BridgeDomainID: 771})
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := st.CreateNetworkPipe(ctx, store.NetworkPipe{
		NetworkAID: networkA.ID, NetworkBID: networkB.ID, Instance: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoints := store.NetworkPipeEndpointNames(pipe.Instance)
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
			{Name: endpoints[0], SwIfIndex: 40},
			{Name: endpoints[1], SwIfIndex: 41},
		},
	}
	gw, err := gateway.New(st, ready, gateway.WithInterfaceAddressRuntime(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/vpp/interfaces/"+endpoints[0]+"/addresses",
		strings.NewReader(`{"addresses":["10.99.0.1/24"],"dhcp_client_enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("set pipe endpoint address = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var body map[string]any
	decodeBody(t, response, &body)
	if addresses := body["addresses"].([]any); len(addresses) != 1 || addresses[0] != "10.99.0.1/24" {
		t.Fatalf("pipe endpoint addresses = %+v", body)
	}
	if len(runtime.added) != 1 || runtime.added[0] != endpoints[0]+" 10.99.0.1/24" {
		t.Fatalf("runtime added = %+v", runtime.added)
	}
}
