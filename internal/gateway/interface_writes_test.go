package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeInterfaceRuntime struct {
	created []string
	deleted []uint32
}

func (f *fakeInterfaceRuntime) CreateVhostUser(_ context.Context, name, _ string, _ uint32) (string, uint32, error) {
	f.created = append(f.created, name)
	return "/run/vpp/" + name + ".sock", 7, nil
}
func (f *fakeInterfaceRuntime) SetInterfaceRouteTable(context.Context, uint32, bool, uint32) error {
	return nil
}

func (f *fakeInterfaceRuntime) DeleteVhostUser(_ context.Context, swIfIndex uint32) error {
	f.deleted = append(f.deleted, swIfIndex)
	return nil
}

func TestInterfaceWriteRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "interface-writes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	network, err := st.CreateNetwork(context.Background(), store.Network{Name: "lab", BridgeDomainID: 101})
	if err != nil {
		t.Fatal(err)
	}

	runtime := &fakeInterfaceRuntime{}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{
		Ready: true, VPP: readiness.VPPStatus{ComponentStatus: readiness.ComponentStatus{Ready: true}},
	}}, gateway.WithInterfaceRuntime(runtime), gateway.WithVMController(&fakeVMController{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	created, err := http.Post(server.URL+"/api/interfaces", "application/json", strings.NewReader(`{"name":"tap-1","network_id":`+itoa(network.ID)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create interface status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var iface map[string]any
	decodeBody(t, created, &iface)
	id := jsonID(iface["id"])
	if iface["name"] != "tap-1" || iface["network_name"] != "lab" || iface["sw_if_index"].(float64) != 7 {
		t.Fatalf("interface = %+v", iface)
	}

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/interfaces/"+id, strings.NewReader(`{"attached_vm_id":""}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	updated, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("update interface status = %d, body = %s", updated.StatusCode, readBody(t, updated))
	}
	_ = updated.Body.Close()

	request, err = http.NewRequest(http.MethodDelete, server.URL+"/api/interfaces/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete interface status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	_ = deleted.Body.Close()
	if len(runtime.deleted) != 1 || runtime.deleted[0] != 7 {
		t.Fatalf("deleted interfaces = %+v", runtime.deleted)
	}
}

func itoa(value int64) string {
	// Avoid importing strconv just for the test body.
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}

type fakeInterfaceCounterSource struct {
	counters   map[string]host.InterfaceCounters
	err        error
	resetCalls int
	resetErr   error
}

func (f *fakeInterfaceCounterSource) InterfaceCounters(_ context.Context, names []string) (map[string]host.InterfaceCounters, error) {
	if f.err != nil {
		return nil, f.err
	}
	result := make(map[string]host.InterfaceCounters, len(names))
	for _, name := range names {
		if counter, ok := f.counters[name]; ok {
			result[name] = counter
		}
	}
	return result, nil
}

func (f *fakeInterfaceCounterSource) ResetInterfaceCounters(_ context.Context) error {
	f.resetCalls++
	return f.resetErr
}

func TestInterfaceCounters(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "interface-counters.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	network, err := st.CreateNetwork(context.Background(), store.Network{Name: "lab", BridgeDomainID: 101})
	if err != nil {
		t.Fatal(err)
	}
	iface, err := st.CreateInterface(context.Background(), store.VirtualInterface{
		Name: "vm0", NetworkID: network.ID, SocketPath: "/run/vpp/vm0.sock",
		MAC: "02:00:00:00:00:01", SwIfIndex: 7,
	})
	if err != nil {
		t.Fatal(err)
	}

	counters := &fakeInterfaceCounterSource{counters: map[string]host.InterfaceCounters{
		"vm0": {RXPackets: 10, TXPackets: 20, RXBytes: 1000, TXBytes: 2000},
	}}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{
		Ready: true, VPP: readiness.VPPStatus{ComponentStatus: readiness.ComponentStatus{Ready: true}},
	}}, gateway.WithInterfaceCounters(counters), gateway.WithInterfaceRuntime(&fakeInterfaceRuntime{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/interfaces/counters")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("counters status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var items []map[string]any
	decodeBody(t, response, &items)
	if len(items) != 1 {
		t.Fatalf("counters = %+v", items)
	}
	if items[0]["name"] != "vm0" || jsonInteger(items[0]["sw_if_index"]) != 7 ||
		jsonInteger(items[0]["rx_packets"]) != 10 || jsonInteger(items[0]["tx_packets"]) != 20 ||
		jsonInteger(items[0]["rx_bytes"]) != 1000 || jsonInteger(items[0]["tx_bytes"]) != 2000 {
		t.Fatalf("counters = %+v", items)
	}
	if _, ok := items[0]["sampled_at"].(string); !ok {
		t.Fatalf("sampled_at = %+v", items[0]["sampled_at"])
	}

	// The literal counters path must not shadow the {id} route.
	get, err := http.Get(server.URL + "/api/interfaces/" + itoa(iface.ID))
	if err != nil {
		t.Fatal(err)
	}
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get interface status = %d, body = %s", get.StatusCode, readBody(t, get))
	}
	var fetched map[string]any
	decodeBody(t, get, &fetched)
	if fetched["name"] != "vm0" {
		t.Fatalf("interface = %+v", fetched)
	}

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/interfaces/counters", nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("reset counters status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	_ = deleted.Body.Close()
	if counters.resetCalls != 1 {
		t.Fatalf("reset calls = %d", counters.resetCalls)
	}
}
