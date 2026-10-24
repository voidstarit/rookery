package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeVNFRuntime struct {
	leases []store.VNFLease
}

func (f *fakeVNFRuntime) Leases(context.Context, int64) ([]store.VNFLease, error) {
	return f.leases, nil
}

type fakeVNFStatusProvider struct{}

func (fakeVNFStatusProvider) VNFStatus(context.Context, store.VNF) (string, string, int) {
	return "running", "", 1
}
func (fakeVNFStatusProvider) VNFSessions(context.Context, store.VNF) ([]string, error) {
	return []string{"10.0.0.2:1234 -> 203.0.113.5:80"}, nil
}
func (fakeVNFStatusProvider) VNFBGP(context.Context, store.VNF) (host.BGPStatus, error) {
	return host.BGPStatus{}, nil
}

func TestVNFViews(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "vnf-views.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	network, err := st.CreateNetwork(context.Background(), store.Network{Name: "lab", BridgeDomainID: 300})
	if err != nil {
		t.Fatal(err)
	}
	vnf, err := st.CreateVNF(context.Background(), store.VNF{
		Name: "dhcp", NetworkID: network.ID, Type: store.VNFTypeDHCPv4, Enabled: true,
		Config: store.VNFConfig{Gateway: "10.0.0.1/24", RangeStart: "10.0.0.10", RangeEnd: "10.0.0.20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	natNetwork, err := st.CreateNetwork(context.Background(), store.Network{Name: "nat", BridgeDomainID: 301})
	if err != nil {
		t.Fatal(err)
	}
	natVNF, err := st.CreateVNF(context.Background(), store.VNF{
		Name: "nat", NetworkID: natNetwork.ID, Type: store.VNFTypeNAT44, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	runtime := &fakeVNFRuntime{leases: []store.VNFLease{
		{ClientID: "aa:bb", Address: "10.0.0.15", Hostname: "guest", Active: true, ExpiresAt: time.Now().Add(time.Hour)},
		{ClientID: "cc:dd", Address: "192.0.2.15", Active: true},
	}}
	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithVNFRuntime(runtime), gateway.WithVNFStatusProvider(fakeVNFStatusProvider{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	list, err := http.Get(server.URL + "/api/vnfs")
	if err != nil {
		t.Fatal(err)
	}
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", list.StatusCode, readBody(t, list))
	}
	var vnfs []map[string]any
	decodeBody(t, list, &vnfs)
	if len(vnfs) != 2 || vnfs[0]["status"] != "running" || jsonInteger(vnfs[0]["lease_count"]) != 1 {
		t.Fatalf("vnfs = %+v", vnfs)
	}

	leases, err := http.Get(server.URL + "/api/vnfs/" + jsonID(vnf.ID) + "/leases")
	if err != nil {
		t.Fatal(err)
	}
	if leases.StatusCode != http.StatusOK {
		t.Fatalf("leases status = %d, body = %s", leases.StatusCode, readBody(t, leases))
	}
	var leaseBody map[string]any
	decodeBody(t, leases, &leaseBody)
	items := leaseBody["leases"].([]any)
	if len(items) != 1 {
		t.Fatalf("leases = %+v", leaseBody)
	}

	sessions, err := http.Get(server.URL + "/api/vnfs/" + jsonID(natVNF.ID) + "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	if sessions.StatusCode != http.StatusOK {
		t.Fatalf("sessions status = %d, body = %s", sessions.StatusCode, readBody(t, sessions))
	}
	var sessionBody map[string]any
	decodeBody(t, sessions, &sessionBody)
	if len(sessionBody["sessions"].([]any)) != 1 {
		t.Fatalf("sessions = %+v", sessionBody)
	}

	invalid, err := http.Get(server.URL + "/api/vnfs/" + jsonID(vnf.ID) + "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-NAT sessions status = %d, body = %s", invalid.StatusCode, readBody(t, invalid))
	}
	_ = invalid.Body.Close()
}

type fakeBGPProvider struct {
	status host.BGPStatus
}

func (f *fakeBGPProvider) VNFStatus(context.Context, store.VNF) (string, string, int) {
	return "running", "", 0
}
func (f *fakeBGPProvider) VNFSessions(context.Context, store.VNF) ([]string, error) {
	return nil, nil
}
func (f *fakeBGPProvider) VNFBGP(context.Context, store.VNF) (host.BGPStatus, error) {
	return f.status, nil
}

func TestVNFBGPView(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "vnf-bgp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	network, err := st.CreateNetwork(context.Background(), store.Network{Name: "lab", BridgeDomainID: 310})
	if err != nil {
		t.Fatal(err)
	}
	vnf, err := st.CreateVNF(context.Background(), store.VNF{
		Name: "bgp1", NetworkID: network.ID, Type: store.VNFTypeBGP, Enabled: true,
		Config: store.VNFConfig{Transport: "memif", LocalAS: 65020, PeerAS: 65040, PeerIP: "10.40.0.100", LocalIP: "10.20.0.113/24"},
	})
	if err != nil {
		t.Fatal(err)
	}

	provider := &fakeBGPProvider{status: host.BGPStatus{
		Running: true, Established: true, SessionState: "SESSION_STATE_ESTABLISHED",
		PeerAddress: "10.40.0.100", PeerAS: 65040, LocalAS: 65020,
		Advertised: []string{"10.20.0.0/24"},
		Received: []host.BGPReceivedPrefix{{
			Prefix: "10.99.0.0/24", NextHop: "10.40.0.100", ASPath: []uint32{65040, 65100},
		}},
	}}
	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithVNFStatusProvider(provider))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/vnfs/" + jsonID(vnf.ID) + "/bgp")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("bgp status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var body map[string]any
	decodeBody(t, response, &body)
	if body["running"] != true || body["established"] != true ||
		body["session_state"] != "SESSION_STATE_ESTABLISHED" ||
		body["peer_address"] != "10.40.0.100" ||
		jsonInteger(body["peer_as"]) != 65040 || jsonInteger(body["local_as"]) != 65020 {
		t.Fatalf("bgp body = %+v", body)
	}
	advertised := body["advertised"].([]any)
	if len(advertised) != 1 || advertised[0] != "10.20.0.0/24" {
		t.Fatalf("advertised = %+v", advertised)
	}
	received := body["received"].([]any)
	if len(received) != 1 {
		t.Fatalf("received = %+v", received)
	}
	item := received[0].(map[string]any)
	if item["prefix"] != "10.99.0.0/24" || item["next_hop"] != "10.40.0.100" {
		t.Fatalf("received item = %+v", item)
	}
	asPath := item["as_path"].([]any)
	if len(asPath) != 2 || jsonInteger(asPath[0]) != 65040 || jsonInteger(asPath[1]) != 65100 {
		t.Fatalf("as_path = %+v", asPath)
	}
}
