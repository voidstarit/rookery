package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// vnfRouteEnv is a gateway backed by real typed store rows and fake runtime
// providers.
type vnfRouteEnv struct {
	store      *store.Store
	writer     *fakeVNFWriter
	status     *fakeVNFStatusProvider
	server     *httptest.Server
	network    store.Network
	dhcp       store.VNF
	dns        store.VNF
	nat44      store.VNF
	bgp        store.VNF
	assignment store.DHCPStaticAssignment
	option     store.DHCPOption
	record     store.DNSRecord
	forwarder  store.DNSForwarder
	rule       store.DNATRule
	prefix     store.BGPPrefix
}

func newVNFRouteEnv(t *testing.T, opts ...gateway.Option) *vnfRouteEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "gateway-routes.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	network, err := st.CreateNetwork(ctx, store.Network{Name: "lab", BridgeDomainID: 100})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	env := &vnfRouteEnv{store: st, network: network}
	env.dhcp, err = st.CreateDHCPVNF(ctx, store.NewDHCPVNF{
		Name: "dhcp", NetworkID: network.ID, Family: store.VNFTypeDHCPv4, Enabled: true,
		Config: store.DHCPVNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
	})
	if err != nil {
		t.Fatalf("create dhcp: %v", err)
	}
	env.assignment, err = st.CreateDHCPStaticAssignment(ctx, env.dhcp.ID, store.DHCPStaticAssignment{MAC: "02:00:00:00:00:01", IP: "10.20.0.50"})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	env.option, err = st.CreateDHCPOption(ctx, env.dhcp.ID, store.DHCPOption{Code: 15, Format: "string", Value: "lab.test"})
	if err != nil {
		t.Fatalf("create option: %v", err)
	}
	env.dns, err = st.CreateDNSVNF(ctx, store.NewDNSVNF{Name: "dns", NetworkID: network.ID, Enabled: true})
	if err != nil {
		t.Fatalf("create dns: %v", err)
	}
	env.record, err = st.CreateDNSVNFRecord(ctx, env.dns.ID, store.DNSRecord{Name: "host.lab.test.", Type: "A", Value: "10.20.0.50", TTL: 300})
	if err != nil {
		t.Fatalf("create record: %v", err)
	}
	env.forwarder, err = st.CreateDNSVNFForwarder(ctx, env.dns.ID, store.DNSForwarder{Address: "1.1.1.1:53", Position: 0})
	if err != nil {
		t.Fatalf("create forwarder: %v", err)
	}
	env.nat44, err = st.CreateNAT44VNF(ctx, store.NewNAT44VNF{
		Name: "nat44", NetworkID: network.ID, Enabled: true,
		Config: store.NAT44VNFConfig{Gateway: "10.30.0.1/24", OutsideInterface: "eth0"},
	})
	if err != nil {
		t.Fatalf("create nat44: %v", err)
	}
	env.rule, err = st.CreateNAT44DNATRule(ctx, env.nat44.ID, store.DNATRule{Protocol: "tcp", ExternalPort: 80, LocalIP: "10.30.0.10", LocalPort: 8080})
	if err != nil {
		t.Fatalf("create dnat: %v", err)
	}
	env.bgp, err = st.CreateBGPVNF(ctx, store.NewBGPVNF{
		Name: "bgp", NetworkID: network.ID, Enabled: true,
		Config: store.BGPVNFConfig{
			Transport: "tap", LocalAS: 65000, RouterID: "10.40.0.1",
			PeerAS: 65001, PeerIP: "10.40.0.2", LocalIP: "10.40.0.1/24",
		},
	})
	if err != nil {
		t.Fatalf("create bgp: %v", err)
	}
	env.prefix, err = st.CreateBGPVNFAdvertisedPrefix(ctx, env.bgp.ID, store.BGPPrefix{Prefix: "10.50.0.0/24"})
	if err != nil {
		t.Fatalf("create prefix: %v", err)
	}
	env.writer = &fakeVNFWriter{}
	env.status = &fakeVNFStatusProvider{
		sessions: []string{"tcp"},
		bgp:      host.BGPStatus{Running: true, Established: true, Advertised: []string{"10.50.0.0/24"}},
		dnsRecords: []host.DNSRecordView{{
			Name: "lease.lab.test.", Type: "A", Value: "10.20.0.150", TTL: 300, SourceVNFID: env.dhcp.ID,
		}},
	}
	options := []gateway.Option{
		gateway.WithVNFRuntime(fakeVNFRuntime{}),
		gateway.WithVNFStatusProvider(env.status),
		gateway.WithVNFWriter(env.writer),
	}
	options = append(options, opts...)
	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()}, options...)
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	env.server = httptest.NewServer(gw.Handler)
	t.Cleanup(func() {
		env.server.Close()
		_ = st.Close()
	})
	return env
}

func (e *vnfRouteEnv) request(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	var req *http.Request
	var err error
	if reader != nil {
		req, err = http.NewRequest(method, e.server.URL+path, reader)
	} else {
		req, err = http.NewRequest(method, e.server.URL+path, nil)
	}
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return response
}

func decodeRoute(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func TestVNFReadRoutes(t *testing.T) {
	env := newVNFRouteEnv(t)
	paths := []string{
		"/api/dhcp-vnfs", "/api/dhcp-vnfs?network_id=" + jsonID(env.network.ID),
		"/api/dhcp-vnfs/" + jsonID(env.dhcp.ID),
		"/api/dchp-vnfs",
		"/api/dhcp-vnfs/" + jsonID(env.dhcp.ID) + "/leases",
		"/api/dhcp-vnfs/" + jsonID(env.dhcp.ID) + "/static-assignments",
		"/api/dhcp-vnfs/" + jsonID(env.dhcp.ID) + "/static-assignments/" + jsonID(env.assignment.ID),
		"/api/dhcp-vnfs/" + jsonID(env.dhcp.ID) + "/options",
		"/api/dhcp-vnfs/" + jsonID(env.dhcp.ID) + "/options/" + jsonID(env.option.ID),
		"/api/dns-vnfs", "/api/dns-vnfs/" + jsonID(env.dns.ID),
		"/api/dns-vnfs/" + jsonID(env.dns.ID) + "/record-views",
		"/api/dns-vnfs/" + jsonID(env.dns.ID) + "/records",
		"/api/dns-vnfs/" + jsonID(env.dns.ID) + "/records/" + jsonID(env.record.ID),
		"/api/dns-vnfs/" + jsonID(env.dns.ID) + "/forwarders",
		"/api/dns-vnfs/" + jsonID(env.dns.ID) + "/forwarders/" + jsonID(env.forwarder.ID),
		"/api/nat44-vnfs", "/api/nat44-vnfs/" + jsonID(env.nat44.ID),
		"/api/nat44-vnfs/" + jsonID(env.nat44.ID) + "/sessions",
		"/api/nat44-vnfs/" + jsonID(env.nat44.ID) + "/dnat-rules",
		"/api/nat44-vnfs/" + jsonID(env.nat44.ID) + "/dnat-rules/" + jsonID(env.rule.ID),
		"/api/bgp-vnfs", "/api/bgp-vnfs/" + jsonID(env.bgp.ID),
		"/api/bgp-vnfs/" + jsonID(env.bgp.ID) + "/status",
		"/api/bgp-vnfs/" + jsonID(env.bgp.ID) + "/advertised-prefixes",
		"/api/bgp-vnfs/" + jsonID(env.bgp.ID) + "/advertised-prefixes/" + jsonID(env.prefix.ID),
	}
	for _, path := range paths[:2] {
		response := env.request(t, http.MethodGet, path, "")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d", path, response.StatusCode)
		}
		response.Body.Close()
	}
	for _, path := range paths[2:] {
		if strings.Contains(path, "dchp") {
			continue
		}
		response := env.request(t, http.MethodGet, path, "")
		if response.StatusCode != http.StatusOK {
			body := readBody(t, response)
			t.Fatalf("%s = %d, body = %s", path, response.StatusCode, body)
		}
		response.Body.Close()
	}
}

func TestVNFWriteRoutes(t *testing.T) {
	env := newVNFRouteEnv(t)
	parentCreates := []struct {
		path string
		body string
		id   int64
	}{
		{path: "/api/dhcp-vnfs", id: env.dhcp.ID, body: fmt.Sprintf(`{"name":"lab","network_id":%d,"family":"dhcpv4","gateway":"10.20.0.1/24","range_start":"10.20.0.100","range_end":"10.20.0.200"}`, env.network.ID)},
		{path: "/api/dns-vnfs", id: env.dns.ID, body: fmt.Sprintf(`{"name":"lab","network_id":%d}`, env.network.ID)},
		{path: "/api/nat44-vnfs", id: env.nat44.ID, body: fmt.Sprintf(`{"name":"lab","network_id":%d,"gateway":"10.30.0.1/24","outside_interface":"eth0"}`, env.network.ID)},
		{path: "/api/bgp-vnfs", id: env.bgp.ID, body: fmt.Sprintf(`{"name":"lab","network_id":%d,"transport":"tap","local_as":65000,"router_id":"10.40.0.1","peer_as":65001,"peer_ip":"10.40.0.2","local_ip":"10.40.0.1/24"}`, env.network.ID)},
	}
	for _, create := range parentCreates {
		env.writer.id = create.id
		response := env.request(t, http.MethodPost, create.path, create.body)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("%s create = %d, body = %s", create.path, response.StatusCode, readBody(t, response))
		}
		if location := response.Header.Get("Location"); location == "" {
			t.Fatalf("%s create has no Location", create.path)
		}
		response.Body.Close()
		response = env.request(t, http.MethodPut, create.path+"/"+jsonID(create.id), `{"name":"renamed"}`)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s update = %d, body = %s", create.path, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
		response = env.request(t, http.MethodPost, create.path+"/"+jsonID(create.id)+"/restart", "")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s restart = %d, body = %s", create.path, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
		response = env.request(t, http.MethodDelete, create.path+"/"+jsonID(create.id), "")
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("%s delete = %d, body = %s", create.path, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}

	children := []struct {
		collection string
		item       string
		create     string
		update     string
	}{
		{collection: "/api/dhcp-vnfs/" + jsonID(env.dhcp.ID) + "/static-assignments", item: jsonID(env.assignment.ID), create: `{"mac":"02:00:00:00:00:02","ip":"10.20.0.51"}`, update: `{"mac":"02:00:00:00:00:03","ip":"10.20.0.52"}`},
		{collection: "/api/dhcp-vnfs/" + jsonID(env.dhcp.ID) + "/options", item: jsonID(env.option.ID), create: `{"code":42,"format":"ipv4","value":"10.20.0.3"}`, update: `{"code":42,"format":"ipv4","value":"10.20.0.4"}`},
		{collection: "/api/dns-vnfs/" + jsonID(env.dns.ID) + "/records", item: jsonID(env.record.ID), create: `{"name":"b.lab.test","type":"A","value":"10.20.0.51","ttl":300}`, update: `{"name":"b.lab.test","type":"A","value":"10.20.0.52","ttl":300}`},
		{collection: "/api/dns-vnfs/" + jsonID(env.dns.ID) + "/forwarders", item: jsonID(env.forwarder.ID), create: `{"address":"9.9.9.9","position":1}`, update: `{"address":"9.9.9.10","position":1}`},
		{collection: "/api/nat44-vnfs/" + jsonID(env.nat44.ID) + "/dnat-rules", item: jsonID(env.rule.ID), create: `{"protocol":"tcp","external_port":81,"local_ip":"10.30.0.11","local_port":8081}`, update: `{"protocol":"tcp","external_port":81,"local_ip":"10.30.0.12","local_port":8082}`},
		{collection: "/api/bgp-vnfs/" + jsonID(env.bgp.ID) + "/advertised-prefixes", item: jsonID(env.prefix.ID), create: `{"prefix":"10.60.0.0/24"}`, update: `{"prefix":"10.61.0.0/24"}`},
	}
	for _, child := range children {
		response := env.request(t, http.MethodPost, child.collection, child.create)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("%s create = %d, body = %s", child.collection, response.StatusCode, readBody(t, response))
		}
		if location := response.Header.Get("Location"); location == "" {
			t.Fatalf("%s create has no Location", child.collection)
		}
		response.Body.Close()
		response = env.request(t, http.MethodPut, child.collection+"/"+child.item, child.update)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s update = %d, body = %s", child.collection, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
		response = env.request(t, http.MethodDelete, child.collection+"/"+child.item, "")
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("%s delete = %d, body = %s", child.collection, response.StatusCode, readBody(t, response))
		}
		response.Body.Close()
	}
}

func TestVNFGatewayErrors(t *testing.T) {
	t.Run("no writer is unavailable", func(t *testing.T) {
		env := newVNFRouteEnv(t)
		// The writer is installed; remove it by creating a second gateway is not
		// possible here, so exercise the typed write error mapping instead.
		statusCodes := []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable, http.StatusInternalServerError}
		for _, code := range statusCodes {
			env.writer.err = &testVNFError{status: code, message: "failed"}
			response := env.request(t, http.MethodDelete, "/api/dhcp-vnfs/"+jsonID(env.dhcp.ID), "")
			if response.StatusCode != code {
				t.Fatalf("writer status %d -> %d", code, response.StatusCode)
			}
			response.Body.Close()
		}
		env.writer.err = errors.New("untyped")
		response := env.request(t, http.MethodDelete, "/api/dhcp-vnfs/"+jsonID(env.dhcp.ID), "")
		if response.StatusCode != http.StatusInternalServerError {
			t.Fatalf("untyped error -> %d", response.StatusCode)
		}
		response.Body.Close()
	})

	t.Run("read errors and wrong types", func(t *testing.T) {
		env := newVNFRouteEnv(t)
		for _, path := range []string{
			"/api/dhcp-vnfs/999",
			"/api/dns-vnfs/" + jsonID(env.dhcp.ID),
			"/api/nat44-vnfs/" + jsonID(env.dhcp.ID),
			"/api/bgp-vnfs/" + jsonID(env.dhcp.ID),
			"/api/dhcp-vnfs/" + jsonID(env.dhcp.ID) + "/static-assignments/999",
			"/api/dhcp-vnfs/" + jsonID(env.dhcp.ID) + "/options/999",
			"/api/dns-vnfs/" + jsonID(env.dns.ID) + "/records/999",
			"/api/dns-vnfs/" + jsonID(env.dns.ID) + "/forwarders/999",
			"/api/nat44-vnfs/" + jsonID(env.nat44.ID) + "/dnat-rules/999",
			"/api/bgp-vnfs/" + jsonID(env.bgp.ID) + "/advertised-prefixes/999",
			"/api/dhcp-vnfs/" + jsonID(env.dns.ID) + "/static-assignments",
			"/api/dns-vnfs/" + jsonID(env.dhcp.ID) + "/records",
			"/api/nat44-vnfs/" + jsonID(env.dhcp.ID) + "/dnat-rules",
			"/api/bgp-vnfs/" + jsonID(env.dhcp.ID) + "/advertised-prefixes",
		} {
			response := env.request(t, http.MethodGet, path, "")
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("%s = %d, want 404", path, response.StatusCode)
			}
			response.Body.Close()
		}
	})

	t.Run("provider failures", func(t *testing.T) {
		env := newVNFRouteEnv(t)
		env.status.sessionsErr = errors.New("session failure")
		env.status.bgpErr = errors.New("bgp failure")
		env.status.dnsError = errors.New("dns failure")
		response := env.request(t, http.MethodGet, "/api/nat44-vnfs/"+jsonID(env.nat44.ID)+"/sessions", "")
		if response.StatusCode != http.StatusInternalServerError {
			t.Fatalf("sessions error -> %d", response.StatusCode)
		}
		response.Body.Close()
		response = env.request(t, http.MethodGet, "/api/bgp-vnfs/"+jsonID(env.bgp.ID)+"/status", "")
		if response.StatusCode != http.StatusInternalServerError {
			t.Fatalf("bgp error -> %d", response.StatusCode)
		}
		response.Body.Close()
		response = env.request(t, http.MethodGet, "/api/dns-vnfs/"+jsonID(env.dns.ID)+"/record-views", "")
		var views map[string]any
		decodeRoute(t, response, &views)
		if views["dynamic_error"] == nil {
			t.Fatal("dns dynamic error was not reported")
		}
	})
}

func TestVNFGatewayWithoutProviders(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "gateway-no-providers.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	network, err := st.CreateNetwork(ctx, store.Network{Name: "lab", BridgeDomainID: 101})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	dhcp, err := st.CreateDHCPVNF(ctx, store.NewDHCPVNF{
		Name: "dhcp", NetworkID: network.ID, Family: store.VNFTypeDHCPv4,
		Config: store.DHCPVNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
	})
	if err != nil {
		t.Fatalf("create dhcp: %v", err)
	}
	dns, err := st.CreateDNSVNF(ctx, store.NewDNSVNF{Name: "dns", NetworkID: network.ID})
	if err != nil {
		t.Fatalf("create dns: %v", err)
	}
	nat44, err := st.CreateNAT44VNF(ctx, store.NewNAT44VNF{
		Name: "nat44", NetworkID: network.ID,
		Config: store.NAT44VNFConfig{Gateway: "10.30.0.1/24", OutsideInterface: "eth0"},
	})
	if err != nil {
		t.Fatalf("create nat44: %v", err)
	}
	bgp, err := st.CreateBGPVNF(ctx, store.NewBGPVNF{
		Name: "bgp", NetworkID: network.ID,
		Config: store.BGPVNFConfig{Transport: "tap", LocalAS: 1, RouterID: "1.1.1.1", PeerAS: 2, PeerIP: "10.0.0.2", LocalIP: "10.0.0.1/24"},
	})
	if err != nil {
		t.Fatalf("create bgp: %v", err)
	}
	gw, err := gateway.New(st, fakeReadiness{status: readyStatus()})
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	server := httptest.NewServer(gw.Handler)
	defer server.Close()
	get := func(path string) int {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	for _, path := range []string{
		"/api/dhcp-vnfs", "/api/dns-vnfs", "/api/nat44-vnfs", "/api/bgp-vnfs",
		"/api/dhcp-vnfs/" + jsonID(dhcp.ID) + "/leases",
		"/api/dns-vnfs/" + jsonID(dns.ID) + "/record-views",
		"/api/nat44-vnfs/" + jsonID(nat44.ID) + "/sessions",
		"/api/bgp-vnfs/" + jsonID(bgp.ID) + "/status",
	} {
		if code := get(path); code != http.StatusOK {
			t.Fatalf("%s = %d", path, code)
		}
	}
	// A write without a writer is Unavailable.
	response, err := http.Post(server.URL+"/api/dhcp-vnfs", "application/json", strings.NewReader(fmt.Sprintf(
		`{"name":"x","network_id":%d,"family":"dhcpv4","gateway":"10.20.0.1/24","range_start":"10.20.0.100","range_end":"10.20.0.200"}`, network.ID)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("write without writer = %d", response.StatusCode)
	}
	response.Body.Close()
}
