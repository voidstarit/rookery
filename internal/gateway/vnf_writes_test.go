package gateway_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeVNFWriter struct {
	calls []string
}

func (f *fakeVNFWriter) CreateVNF(_ context.Context, request *rookeryv1.CreateVNFRequest) (store.VNF, error) {
	f.calls = append(f.calls, "create "+request.GetName())
	return store.VNF{ID: 9, Name: request.GetName(), NetworkID: request.GetNetworkId(), Type: request.GetType()}, nil
}
func (f *fakeVNFWriter) UpdateVNF(_ context.Context, request *rookeryv1.UpdateVNFRequest) (store.VNF, error) {
	f.calls = append(f.calls, fmt.Sprintf("update %d", request.GetId()))
	return store.VNF{ID: request.GetId(), Name: request.GetName(), Type: store.VNFTypeDHCPv4, Enabled: true}, nil
}
func (f *fakeVNFWriter) DeleteVNF(_ context.Context, request *rookeryv1.DeleteVNFRequest) error {
	f.calls = append(f.calls, fmt.Sprintf("delete %d", request.GetId()))
	return nil
}
func (f *fakeVNFWriter) RestartVNF(_ context.Context, request *rookeryv1.RestartVNFRequest) (store.VNF, error) {
	f.calls = append(f.calls, fmt.Sprintf("restart %d", request.GetId()))
	return store.VNF{ID: request.GetId(), Name: "dhcp", Type: store.VNFTypeDHCPv4, Enabled: true}, nil
}

func TestVNFWriteRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "vnf-writes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	writer := &fakeVNFWriter{}
	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithVNFWriter(writer), gateway.WithVNFStatusProvider(fakeVNFStatusProvider{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	created, err := http.Post(server.URL+"/api/vnfs", "application/json", strings.NewReader(`{"name":"dhcp","network_id":3,"type":"dhcpv4","config":{"gateway":"10.0.0.1/24","range_start":"10.0.0.10","range_end":"10.0.0.20"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var createdVNF map[string]any
	decodeBody(t, created, &createdVNF)
	if jsonID(createdVNF["id"]) != "9" || createdVNF["name"] != "dhcp" {
		t.Fatalf("created vnf = %+v", createdVNF)
	}

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/vnfs/9", strings.NewReader(`{"name":"dhcp2","enabled":true}`))
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
	if updatedVNF["name"] != "dhcp2" || updatedVNF["status"] != "running" {
		t.Fatalf("updated vnf = %+v", updatedVNF)
	}

	restarted, err := http.Post(server.URL+"/api/vnfs/9/restart", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.StatusCode != http.StatusOK {
		t.Fatalf("restart status = %d, body = %s", restarted.StatusCode, readBody(t, restarted))
	}
	_ = restarted.Body.Close()

	request, err = http.NewRequest(http.MethodDelete, server.URL+"/api/vnfs/9", nil)
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

	want := []string{"create dhcp", "update 9", "restart 9", "delete 9"}
	if fmt.Sprint(writer.calls) != fmt.Sprint(want) {
		t.Fatalf("writer calls = %v", writer.calls)
	}
}
