package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// bgpVNFView is the flat BGP parent representation served by the API.
type bgpVNFView struct {
	ID                    int64  `json:"id"`
	Name                  string `json:"name"`
	NetworkID             int64  `json:"network_id"`
	Enabled               bool   `json:"enabled"`
	Transport             string `json:"transport"`
	LocalAS               uint32 `json:"local_as"`
	RouterID              string `json:"router_id"`
	PeerAS                uint32 `json:"peer_as"`
	PeerIP                string `json:"peer_ip"`
	LocalIP               string `json:"local_ip"`
	PeerGateway           string `json:"peer_gateway"`
	MultihopTTL           uint32 `json:"multihop_ttl"`
	Passive               bool   `json:"passive"`
	TargetTableID         uint32 `json:"target_table_id"`
	NextHop               string `json:"next_hop"`
	AdvertisedPrefixCount int    `json:"advertised_prefix_count"`
	Status                string `json:"status"`
	StatusDetail          string `json:"status_detail"`
	TapSwIfIndex          uint32 `json:"tap_sw_if_index"`
	TapHostName           string `json:"tap_host_name"`
}

// bgpPrefixView is one advertised prefix.
type bgpPrefixView struct {
	ID     int64  `json:"id"`
	VNFID  int64  `json:"vnf_id"`
	Prefix string `json:"prefix"`
}

// bgpVNFBody builds a BGP parent create request using the memif transport.
func bgpVNFBody(name string, networkID int64, enabled bool) map[string]any {
	return map[string]any{
		"name":            name,
		"network_id":      networkID,
		"enabled":         enabled,
		"transport":       "memif",
		"local_as":        64512,
		"router_id":       "10.20.0.1",
		"peer_as":         64513,
		"peer_ip":         "169.254.0.2",
		"local_ip":        "169.254.0.1/30",
		"multihop_ttl":    1,
		"target_table_id": 0,
	}
}

// createBGPVNFAPI creates one typed BGP parent and returns its flat view.
func createBGPVNFAPI(t *testing.T, handler http.Handler, name string, networkID int64, enabled bool) bgpVNFView {
	t.Helper()
	recorder := do(t, handler, http.MethodPost, "/api/bgp-vnfs", bgpVNFBody(name, networkID, enabled))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create BGP VNF %q status = %d, body = %s", name, recorder.Code, recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); location == "" {
		t.Fatalf("create BGP VNF %q has no Location header", name)
	}
	return decode[bgpVNFView](t, recorder)
}

// TestBGPVNFCreate covers flat BGP parent creation, the password transport
// rule, and advertised-prefix CRUD.
func TestBGPVNFCreate(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "bgp-net")

	vnf := createBGPVNFAPI(t, handler, "lab-bgp", network.ID, false)
	if vnf.Transport != "memif" || vnf.LocalAS != 64512 || vnf.TargetTableID != 0 ||
		vnf.NextHop != "" || vnf.AdvertisedPrefixCount != 0 {
		t.Fatalf("created BGP VNF = %+v", vnf)
	}

	// A TCP MD5 password requires tap transport.
	passwordBody := bgpVNFBody("bgp-memif-password", network.ID, false)
	passwordBody["password"] = "secret"
	recorder := do(t, handler, http.MethodPost, "/api/bgp-vnfs", passwordBody)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("memif BGP password status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	tapNetwork := createNetworkAPICIDR(t, handler, "bgp-tap-net", "10.99.0.0/24", "fd00:99::/64")
	tapBody := bgpVNFBody("bgp-tap-password", tapNetwork.ID, false)
	tapBody["transport"] = "tap"
	tapBody["password"] = "secret"
	recorder = do(t, handler, http.MethodPost, "/api/bgp-vnfs", tapBody)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("tap BGP password status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	createPrefix := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		return do(t, handler, http.MethodPost, fmt.Sprintf("/api/bgp-vnfs/%d/advertised-prefixes", vnf.ID), body)
	}
	first := createPrefix(map[string]any{"prefix": "10.10.0.0/16"})
	if first.Code != http.StatusCreated {
		t.Fatalf("create prefix status = %d, body = %s", first.Code, first.Body.String())
	}
	prefix := decode[bgpPrefixView](t, first)
	if prefix.ID == 0 || prefix.VNFID != vnf.ID || prefix.Prefix != "10.10.0.0/16" {
		t.Fatalf("created prefix = %+v", prefix)
	}
	if location := first.Header().Get("Location"); location != fmt.Sprintf("/api/bgp-vnfs/%d/advertised-prefixes/%d", vnf.ID, prefix.ID) {
		t.Fatalf("prefix Location = %q", location)
	}
	if recorder := createPrefix(map[string]any{"prefix": "10.20.0.0/24"}); recorder.Code != http.StatusCreated {
		t.Fatalf("create second prefix status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/bgp-vnfs/%d", vnf.ID), nil)
	if parent := decode[bgpVNFView](t, recorder); parent.AdvertisedPrefixCount != 2 {
		t.Fatalf("advertised prefix count = %+v", parent)
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/bgp-vnfs/%d/advertised-prefixes", vnf.ID), nil)
	if list := decode[[]bgpPrefixView](t, recorder); len(list) != 2 {
		t.Fatalf("listed prefixes = %+v", list)
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/bgp-vnfs/%d/advertised-prefixes/%d", vnf.ID, prefix.ID), map[string]any{
		"prefix": "10.30.0.0/16",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update prefix status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if updated := decode[bgpPrefixView](t, recorder); updated.Prefix != "10.30.0.0/16" {
		t.Fatalf("updated prefix = %+v", updated)
	}
	recorder = createPrefix(map[string]any{"prefix": "10.30.0.0/16"})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate prefix status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = createPrefix(map[string]any{"prefix": "fd00:99::/64"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("wrong-family prefix status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/bgp-vnfs/%d/advertised-prefixes/%d", vnf.ID, prefix.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete prefix status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/bgp-vnfs/%d/advertised-prefixes/%d", vnf.ID, prefix.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted prefix status = %d", recorder.Code)
	}
}

// TestBGPVNFMemifActivation covers an enabled BGP VNF on a dedicated memif.
func TestBGPVNFMemifActivation(t *testing.T) {
	env := newTestAPI(t)
	env.server.ConfigureMemifDHCP(true, t.TempDir())
	network := createNetworkAPI(t, env.handler, "bgp-memif-net")

	vnf := createBGPVNFAPI(t, env.handler, "bgp-memif", network.ID, true)
	if !vnf.Enabled || vnf.Status != "running" {
		t.Fatalf("activated BGP VNF = %+v", vnf)
	}
	stored, err := env.store.GetVNF(context.Background(), vnf.ID)
	if err != nil {
		t.Fatalf("get activated BGP VNF: %v", err)
	}
	if stored.MemifHostName != "bgp-memif" || stored.MemifSwIfIndex == 0 || stored.TapHostName != "" {
		t.Fatalf("stored BGP VNF = %+v", stored)
	}
	if !slices.Contains(env.networks.createdMemifs, "bgp-memif") {
		t.Fatalf("created memifs = %+v", env.networks.createdMemifs)
	}
}

// TestBGPVNFTapActivation covers an enabled BGP VNF on a tap and proves the
// write-only password is never returned by a read.
func TestBGPVNFTapActivation(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "bgp-tap-activate-net")
	body := bgpVNFBody("bgp-tap", network.ID, true)
	body["transport"] = "tap"
	body["password"] = "secret"
	recorder := do(t, handler, http.MethodPost, "/api/bgp-vnfs", body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("activate BGP tap status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	created := decode[bgpVNFView](t, recorder)
	if created.TapHostName == "" || created.TapSwIfIndex == 0 || created.Status != "running" {
		t.Fatalf("created BGP tap VNF = %+v", created)
	}
	stored, err := env.store.GetVNF(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get BGP tap VNF: %v", err)
	}
	if stored.TapHostName == "" || stored.TapSwIfIndex == 0 {
		t.Fatalf("stored BGP tap VNF = %+v", stored)
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/bgp-vnfs/%d", created.ID), nil)
	var raw map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode BGP response: %v", err)
	}
	if _, present := raw["password"]; present {
		t.Fatalf("BGP read returned the write-only password: %s", recorder.Body.String())
	}
}

// TestBGPVNFStatus covers the live status view.
func TestBGPVNFStatus(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "bgp-status-net")
	vnf := createBGPVNFAPI(t, handler, "bgp-status", network.ID, true)
	env.vnfs.bgpStatus[vnf.ID] = host.BGPStatus{
		Running:      true,
		Established:  true,
		SessionState: "established",
		PeerAddress:  "169.254.0.2",
		PeerAS:       64513,
		LocalAS:      64512,
		Advertised:   []string{"10.10.0.0/16"},
		Received: []host.BGPReceivedPrefix{{
			Prefix: "10.99.0.0/16", NextHop: "169.254.0.2", ASPath: []uint32{64513},
		}},
	}

	recorder := do(t, handler, http.MethodGet, fmt.Sprintf("/api/bgp-vnfs/%d/status", vnf.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("BGP status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	status := decode[struct {
		VNFID        int64    `json:"vnf_id"`
		Running      bool     `json:"running"`
		Established  bool     `json:"established"`
		SessionState string   `json:"session_state"`
		PeerAddress  string   `json:"peer_address"`
		PeerAS       uint32   `json:"peer_as"`
		LocalAS      uint32   `json:"local_as"`
		Advertised   []string `json:"advertised"`
		Received     []struct {
			Prefix  string   `json:"prefix"`
			NextHop string   `json:"next_hop"`
			ASPath  []uint32 `json:"as_path"`
		} `json:"received"`
	}](t, recorder)
	if status.VNFID != vnf.ID || !status.Running || !status.Established ||
		status.SessionState != "established" || status.PeerAS != 64513 || status.LocalAS != 64512 {
		t.Fatalf("BGP status = %+v", status)
	}
	if len(status.Advertised) != 1 || len(status.Received) != 1 ||
		status.Received[0].Prefix != "10.99.0.0/16" || status.Received[0].ASPath[0] != 64513 {
		t.Fatalf("BGP status prefixes = %+v", status)
	}
}

// TestBGPVNFChildWritesReload covers the reload-on-child-write contract and
// parent update/restart/delete.
func TestBGPVNFChildWritesReload(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "bgp-reload-net")
	body := bgpVNFBody("bgp-reload", network.ID, true)
	body["transport"] = "tap"
	recorder := do(t, handler, http.MethodPost, "/api/bgp-vnfs", body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create BGP status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	vnf := decode[bgpVNFView](t, recorder)

	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/bgp-vnfs/%d/advertised-prefixes", vnf.ID), map[string]any{
		"prefix": "10.10.0.0/16",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create prefix status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !slices.Contains(env.vnfs.reloaded, vnf.ID) {
		t.Fatalf("advertised prefix write did not reload BGP: %v", env.vnfs.reloaded)
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/bgp-vnfs/%d", vnf.ID), map[string]any{
		"multihop_ttl": 2,
		"passive":      true,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update BGP status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if updated := decode[bgpVNFView](t, recorder); updated.MultihopTTL != 2 || !updated.Passive {
		t.Fatalf("updated BGP VNF = %+v", updated)
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/bgp-vnfs/%d/restart", vnf.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restart BGP status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/bgp-vnfs/%d", vnf.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete BGP status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/bgp-vnfs/%d", vnf.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted BGP status = %d", recorder.Code)
	}
}

// TestBGPVNFWrongTypeRoutes verifies that BGP parent and child routes return
// 404 for a different VNF type.
func TestBGPVNFWrongTypeRoutes(t *testing.T) {
	handler := newTestHandler(t)
	network := createNetworkAPI(t, handler, "lab")
	dhcp := createDHCPVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, false)

	for _, target := range []string{
		fmt.Sprintf("/api/bgp-vnfs/%d", dhcp.ID),
		fmt.Sprintf("/api/bgp-vnfs/%d/status", dhcp.ID),
		fmt.Sprintf("/api/bgp-vnfs/%d/advertised-prefixes", dhcp.ID),
	} {
		recorder := do(t, handler, http.MethodGet, target, nil)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, body = %s", target, recorder.Code, recorder.Body.String())
		}
	}
	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/bgp-vnfs/%d/advertised-prefixes", dhcp.ID), map[string]any{
		"prefix": "10.10.0.0/16",
	})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("prefix on DHCP parent status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/bgp-vnfs/%d/restart", dhcp.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("BGP restart on DHCP parent status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
