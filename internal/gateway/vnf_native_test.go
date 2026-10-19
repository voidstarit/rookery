package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/api"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

func TestNativeVNFWrites(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "vnf-bridge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	network, err := st.CreateNetwork(context.Background(), store.Network{Name: "lab", BridgeDomainID: 400})
	if err != nil {
		t.Fatal(err)
	}

	ready := fakeReadiness{}
	legacy := api.New(st, ready, nil, nil, nil, nil, nil, nil)
	gw, err := gateway.New(st, ready, gateway.WithVNFWriter(legacy), gateway.WithVNFStatusProvider(legacy))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	created, err := http.Post(server.URL+"/api/vnfs", "application/json", strings.NewReader(`{"name":"dhcp","network_id":`+itoa(network.ID)+`,"type":"dhcpv4","config":{"gateway":"10.0.0.1/24","range_start":"10.0.0.10","range_end":"10.0.0.20"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var vnf map[string]any
	decodeBody(t, created, &vnf)
	id := jsonID(vnf["id"])
	if vnf["name"] != "dhcp" || vnf["status"] != "disabled" {
		t.Fatalf("created vnf = %+v", vnf)
	}

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/vnfs/"+id, strings.NewReader(`{"name":"dhcp2"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	updated, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updated.StatusCode, readBody(t, updated))
	}
	var updatedVNF map[string]any
	decodeBody(t, updated, &updatedVNF)
	if updatedVNF["name"] != "dhcp2" {
		t.Fatalf("updated vnf = %+v", updatedVNF)
	}

	request, err = http.NewRequest(http.MethodDelete, server.URL+"/api/vnfs/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	_ = deleted.Body.Close()
}
