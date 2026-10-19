package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeNetworkRuntime struct {
	deleted []uint32
}

func (f *fakeNetworkRuntime) CreateBridgeDomain(_ context.Context, _ string, reserved []uint32) (uint32, error) {
	return 200 + uint32(len(reserved)), nil
}
func (f *fakeNetworkRuntime) DeleteBridgeDomain(_ context.Context, bdID uint32) error {
	f.deleted = append(f.deleted, bdID)
	return nil
}

func TestNetworkWriteRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "network-writes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	runtime := &fakeNetworkRuntime{}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{
		Ready: true, VPP: readiness.VPPStatus{ComponentStatus: readiness.ComponentStatus{Ready: true}},
	}}, gateway.WithNetworkRuntime(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	created, err := http.Post(server.URL+"/api/networks", "application/json", strings.NewReader(`{"name":"lab-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create network status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var network map[string]any
	decodeBody(t, created, &network)
	id := jsonID(network["id"])
	if network["name"] != "lab-1" || network["bridge_domain_id"].(float64) != 200 {
		t.Fatalf("network = %+v", network)
	}

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/networks/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete network status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	_ = deleted.Body.Close()
	if len(runtime.deleted) != 1 || runtime.deleted[0] != 200 {
		t.Fatalf("deleted bridge domains = %+v", runtime.deleted)
	}
}

type fakeNetworkWriter struct {
	deleted []int64
}

func (f *fakeNetworkWriter) DeleteNetwork(_ context.Context, id int64) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func TestNetworkDeleteWithWriter(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "network-writer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	network, err := st.CreateNetwork(context.Background(), store.Network{Name: "lab", BridgeDomainID: 600})
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeNetworkWriter{}
	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithNetworkWriter(writer))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/networks/"+jsonID(network.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete network status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	_ = response.Body.Close()
	if len(writer.deleted) != 1 || writer.deleted[0] != network.ID {
		t.Fatalf("deleted networks = %+v", writer.deleted)
	}
}
