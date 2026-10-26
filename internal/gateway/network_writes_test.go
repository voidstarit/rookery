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

type fakeNetworkRuntime struct {
	deleted     []uint32
	pipeCreated [][2]uint32
	pipeDeleted []uint32
}

func (f *fakeNetworkRuntime) CreateBridgeDomain(_ context.Context, _ string, reserved []uint32) (uint32, error) {
	return 200 + uint32(len(reserved)), nil
}
func (f *fakeNetworkRuntime) DeleteBridgeDomain(_ context.Context, bdID uint32) error {
	f.deleted = append(f.deleted, bdID)
	return nil
}

func (f *fakeNetworkRuntime) CreatePipe(_ context.Context, bdA, bdB uint32) (host.PipeRuntime, error) {
	f.pipeCreated = append(f.pipeCreated, [2]uint32{bdA, bdB})
	return host.PipeRuntime{Instance: 5, ParentSwIfIndex: 90, EndpointASwIfIndex: 91, EndpointBSwIfIndex: 92}, nil
}

func (f *fakeNetworkRuntime) EnsurePipe(_ context.Context, instance, bdA, bdB uint32) (host.PipeRuntime, error) {
	return host.PipeRuntime{Instance: instance, EndpointASwIfIndex: bdA, EndpointBSwIfIndex: bdB}, nil
}

func (f *fakeNetworkRuntime) DeletePipe(_ context.Context, _ uint32, parent uint32) error {
	f.pipeDeleted = append(f.pipeDeleted, parent)
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

	created, err := http.Post(server.URL+"/api/networks", "application/json", strings.NewReader(`{"name":"lab-1","ipv4_cidr":"10.20.0.0/24","ipv6_cidr":"fd00:20::/64"}`))
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
	deleted     []int64
	deletedPipe []int64
}

func (f *fakeNetworkWriter) CreateNetwork(_ context.Context, name, ipv4CIDR, ipv6CIDR string) (store.Network, error) {
	return store.Network{ID: 1, Name: name, BridgeDomainID: 601, IPv4CIDR: ipv4CIDR, IPv6CIDR: ipv6CIDR}, nil
}
func (f *fakeNetworkWriter) DeleteNetwork(_ context.Context, id int64) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeNetworkWriter) CreateNetworkPipe(_ context.Context, a, b int64) (store.NetworkPipe, error) {
	return store.NetworkPipe{ID: 7, NetworkAID: a, NetworkBID: b, Instance: 2}, nil
}
func (f *fakeNetworkWriter) DeleteNetworkPipe(_ context.Context, id int64) error {
	f.deletedPipe = append(f.deletedPipe, id)
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

func TestNetworkPipeRoutes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "network-pipes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	networkA, err := st.CreateNetwork(ctx, store.Network{Name: "lab-a", BridgeDomainID: 300})
	if err != nil {
		t.Fatal(err)
	}
	networkB, err := st.CreateNetwork(ctx, store.Network{Name: "lab-b", BridgeDomainID: 301})
	if err != nil {
		t.Fatal(err)
	}
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

	body := `{"network_a_id":` + jsonID(networkB.ID) + `,"network_b_id":` + jsonID(networkA.ID) + `}`
	created, err := http.Post(server.URL+"/api/network-pipes", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create pipe status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var pipe map[string]any
	decodeBody(t, created, &pipe)
	if pipe["network_a_name"] != "lab-a" || pipe["network_b_name"] != "lab-b" {
		t.Fatalf("pipe = %+v", pipe)
	}
	if pipe["instance"].(float64) != 5 || pipe["endpoint_a_sw_if_index"].(float64) != 91 || pipe["endpoint_b_sw_if_index"].(float64) != 92 {
		t.Fatalf("pipe runtime = %+v", pipe)
	}
	if len(runtime.pipeCreated) != 1 || runtime.pipeCreated[0] != [2]uint32{300, 301} {
		t.Fatalf("created pipes = %+v", runtime.pipeCreated)
	}

	listed, err := http.Get(server.URL + "/api/network-pipes")
	if err != nil {
		t.Fatal(err)
	}
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list pipes status = %d, body = %s", listed.StatusCode, readBody(t, listed))
	}
	var pipes []map[string]any
	decodeBody(t, listed, &pipes)
	if len(pipes) != 1 || pipes[0]["id"].(float64) != pipe["id"].(float64) {
		t.Fatalf("pipes = %+v", pipes)
	}

	blockedRequest, err := http.NewRequest(http.MethodDelete, server.URL+"/api/networks/"+jsonID(networkA.ID), nil)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := http.DefaultClient.Do(blockedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.StatusCode != http.StatusConflict {
		t.Fatalf("delete network with pipe status = %d, body = %s", blocked.StatusCode, readBody(t, blocked))
	}
	_ = blocked.Body.Close()

	duplicate, err := http.Post(server.URL+"/api/network-pipes", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate pipe status = %d, body = %s", duplicate.StatusCode, readBody(t, duplicate))
	}
	_ = duplicate.Body.Close()
	if len(runtime.pipeDeleted) != 1 || runtime.pipeDeleted[0] != 90 {
		t.Fatalf("rolled back pipes = %+v", runtime.pipeDeleted)
	}

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/network-pipes/"+jsonID(pipe["id"]), nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete pipe status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	_ = deleted.Body.Close()
	if len(runtime.pipeDeleted) != 2 || runtime.pipeDeleted[1] != 90 {
		t.Fatalf("deleted pipes = %+v", runtime.pipeDeleted)
	}
}
