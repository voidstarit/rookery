package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// dnsVNFView is the flat DNS parent representation served by the API.
type dnsVNFView struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	NetworkID      int64  `json:"network_id"`
	Enabled        bool   `json:"enabled"`
	Forward        bool   `json:"forward"`
	RecordCount    int    `json:"record_count"`
	ForwarderCount int    `json:"forwarder_count"`
	Status         string `json:"status"`
	StatusDetail   string `json:"status_detail"`
	TapSwIfIndex   uint32 `json:"tap_sw_if_index"`
	TapHostName    string `json:"tap_host_name"`
}

// dnsRecordView is one static DNS record.
type dnsRecordView struct {
	ID    int64  `json:"id"`
	VNFID int64  `json:"vnf_id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   uint32 `json:"ttl"`
}

// dnsForwarderView is one DNS forwarder.
type dnsForwarderView struct {
	ID       int64  `json:"id"`
	VNFID    int64  `json:"vnf_id"`
	Address  string `json:"address"`
	Position int32  `json:"position"`
}

// mergedDNSRecordView is one entry of the merged static and dynamic view.
type mergedDNSRecordView struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Value       string `json:"value"`
	TTL         uint32 `json:"ttl"`
	Source      string `json:"source"`
	SourceVNFID int64  `json:"source_vnf_id"`
}

// dnsVNFBody builds a DNS parent create request.
func dnsVNFBody(name string, networkID int64, enabled, forward bool) map[string]any {
	return map[string]any{
		"name":       name,
		"network_id": networkID,
		"enabled":    enabled,
		"forward":    forward,
	}
}

// createDNSVNFAPIWith creates a DNS parent and returns its flat view.
func createDNSVNFAPIWith(t *testing.T, handler http.Handler, name string, networkID int64, enabled, forward bool) dnsVNFView {
	t.Helper()
	recorder := do(t, handler, http.MethodPost, "/api/dns-vnfs", dnsVNFBody(name, networkID, enabled, forward))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create DNS VNF %q status = %d, body = %s", name, recorder.Code, recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); location == "" {
		t.Fatalf("create DNS VNF %q has no Location header", name)
	}
	return decode[dnsVNFView](t, recorder)
}

// createDNSVNFAPI creates a DNS parent without forwarding.
func createDNSVNFAPI(t *testing.T, handler http.Handler, name string, networkID int64, enabled bool) dnsVNFView {
	t.Helper()
	return createDNSVNFAPIWith(t, handler, name, networkID, enabled, false)
}

// createDNSRecordAPI creates one static record and returns it.
func createDNSRecordAPI(t *testing.T, handler http.Handler, vnfID int64, body map[string]any) dnsRecordView {
	t.Helper()
	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/dns-vnfs/%d/records", vnfID), body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create DNS record status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); location == "" {
		t.Fatalf("create DNS record has no Location header")
	}
	return decode[dnsRecordView](t, recorder)
}

// TestDNSVNFLifecycle covers create, records, forwarders, counts, restart,
// update, and delete for a DNS VNF on the shared memif.
func TestDNSVNFLifecycle(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")

	created := createDNSVNFAPIWith(t, handler, "lab-dns", network.ID, true, true)
	if !created.Enabled || !created.Forward || created.Status != "running" ||
		created.RecordCount != 0 || created.ForwarderCount != 0 {
		t.Fatalf("created DNS VNF = %+v", created)
	}
	stored, err := env.store.GetVNF(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get stored DNS VNF: %v", err)
	}
	storedNetwork, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if stored.MemifSocket != storedNetwork.VNFMemifSocket || stored.MemifSwIfIndex == 0 ||
		stored.TapSwIfIndex != 0 || stored.TapHostName != "" {
		t.Fatalf("DNS VNF runtime = %+v, want shared memif %+v", stored, storedNetwork)
	}
	if len(env.vnfs.started) != 1 || env.vnfs.started[0] != created.ID {
		t.Fatalf("DNS runtime starts = %+v", env.vnfs.started)
	}

	records := []map[string]any{
		{"name": "vm1.lab1.test", "type": "A", "value": "10.20.0.50"},
		{"name": "*.lab1.test", "type": "A", "value": "10.20.0.51", "ttl": 60},
		{"name": "alias.lab1.test", "type": "CNAME", "value": "vm1.lab1.test"},
		{"name": "vm1.lab1.test", "type": "TXT", "value": "hello"},
	}
	var storedRecords []dnsRecordView
	for _, body := range records {
		storedRecords = append(storedRecords, createDNSRecordAPI(t, handler, created.ID, body))
	}
	if storedRecords[0].Name != "vm1.lab1.test." || storedRecords[0].TTL != 300 {
		t.Fatalf("normalized A record = %+v", storedRecords[0])
	}
	if storedRecords[1].Name != "*.lab1.test." || storedRecords[1].TTL != 60 {
		t.Fatalf("normalized wildcard record = %+v", storedRecords[1])
	}
	if storedRecords[2].Value != "vm1.lab1.test." {
		t.Fatalf("normalized CNAME value = %+v", storedRecords[2])
	}
	if storedRecords[3].Value != "hello" {
		t.Fatalf("normalized TXT value = %+v", storedRecords[3])
	}

	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/dns-vnfs/%d/forwarders", created.ID), map[string]any{
		"address": "1.1.1.1", "position": 0,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create forwarder status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if forwarder := decode[dnsForwarderView](t, recorder); forwarder.Address != "1.1.1.1:53" || forwarder.VNFID != created.ID {
		t.Fatalf("normalized forwarder = %+v", forwarder)
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dns-vnfs/%d/forwarders", created.ID), map[string]any{
		"address": "8.8.8.8:53", "position": 1,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create second forwarder status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if forwarder := decode[dnsForwarderView](t, recorder); forwarder.Address != "8.8.8.8:53" {
		t.Fatalf("second forwarder = %+v", forwarder)
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dns-vnfs/%d/records", created.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list records status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if list := decode[[]dnsRecordView](t, recorder); len(list) != 4 {
		t.Fatalf("listed records = %+v", list)
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dns-vnfs/%d/forwarders", created.ID), nil)
	if list := decode[[]dnsForwarderView](t, recorder); len(list) != 2 {
		t.Fatalf("listed forwarders = %+v", list)
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dns-vnfs/%d", created.ID), nil)
	if parent := decode[dnsVNFView](t, recorder); parent.RecordCount != 4 || parent.ForwarderCount != 2 {
		t.Fatalf("DNS counts = %+v", parent)
	}

	// Invalid records and forwarders are rejected before they reach the store.
	for index, body := range []map[string]any{
		{"name": "x.test", "type": "MX", "value": "mail.test"},
		{"name": "x.test", "type": "A", "value": "fd00::1"},
		{"name": "vm1.lab1.test", "type": "CNAME", "value": "other.test"},
	} {
		recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/dns-vnfs/%d/records", created.ID), body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid DNS record %d status = %d, body = %s", index, recorder.Code, recorder.Body.String())
		}
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dns-vnfs/%d/records", created.ID), map[string]any{
		"name": "vm1.lab1.test", "type": "A", "value": "10.20.0.50",
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate DNS record status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dns-vnfs/%d/forwarders", created.ID), map[string]any{
		"address": "not-an-ip", "position": 2,
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid forwarder status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	// Update one record and delete one forwarder.
	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/dns-vnfs/%d/records/%d", created.ID, storedRecords[0].ID), map[string]any{
		"name": "vm1.lab1.test", "type": "A", "value": "10.20.0.60", "ttl": 120,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update record status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if updated := decode[dnsRecordView](t, recorder); updated.Value != "10.20.0.60" || updated.TTL != 120 {
		t.Fatalf("updated record = %+v", updated)
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dns-vnfs/%d/forwarders", created.ID), nil)
	forwarders := decode[[]dnsForwarderView](t, recorder)
	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/dns-vnfs/%d/forwarders/%d", created.ID, forwarders[1].ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete forwarder status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	// Restart and update the parent; the runtime is re-created each time.
	stoppedBefore := len(env.vnfs.stopped)
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dns-vnfs/%d/restart", created.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restart DNS status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if restarted := decode[dnsVNFView](t, recorder); !restarted.Enabled || restarted.Status != "running" {
		t.Fatalf("restarted DNS VNF = %+v", restarted)
	}
	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/dns-vnfs/%d", created.ID), map[string]any{"forward": false})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update DNS status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if updated := decode[dnsVNFView](t, recorder); updated.Forward || !updated.Enabled {
		t.Fatalf("updated DNS VNF = %+v", updated)
	}
	if len(env.vnfs.stopped) <= stoppedBefore {
		t.Fatalf("DNS updates did not stop the runtime: %v", env.vnfs.stopped)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/dns-vnfs/%d", created.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete DNS status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dns-vnfs/%d", created.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted DNS status = %d", recorder.Code)
	}
}

// TestDNSVNFRecordViews merges static records with the DHCP-derived records
// reported by the runtime.
func TestDNSVNFRecordViews(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	dns := createDNSVNFAPIWith(t, handler, "lab-dns", network.ID, true, true)

	createDNSRecordAPI(t, handler, dns.ID, map[string]any{
		"name": "vm1.lab1.test", "type": "A", "value": "10.20.0.50",
	})
	env.vnfs.dnsRecords[dns.ID] = []host.DNSRecordView{{
		Name: "vm2.lab1.test", Type: "A", Value: "10.20.0.55", TTL: 300,
		Source: "dhcp", SourceVNFID: 42,
	}}

	recorder := do(t, handler, http.MethodGet, fmt.Sprintf("/api/dns-vnfs/%d/record-views", dns.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("record views status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	views := decode[struct {
		VNFID        int64                 `json:"vnf_id"`
		Records      []mergedDNSRecordView `json:"records"`
		DynamicError string                `json:"dynamic_error"`
	}](t, recorder)
	if views.VNFID != dns.ID || len(views.Records) != 2 {
		t.Fatalf("record views = %+v", views)
	}
	if views.Records[0].Source != "static" || views.Records[0].Name != "vm1.lab1.test." {
		t.Fatalf("static record view = %+v", views.Records[0])
	}
	if views.Records[1].Source != "dhcp" || views.Records[1].SourceVNFID != 42 || views.Records[1].Value != "10.20.0.55" {
		t.Fatalf("dynamic record view = %+v", views.Records[1])
	}
}

// TestDNSVNFChildReloadRollback covers both reload failure paths on a DNS
// child write.
func TestDNSVNFChildReloadRollback(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	dns := createDNSVNFAPIWith(t, handler, "lab-dns", network.ID, true, false)

	createDNSRecordAPI(t, handler, dns.ID, map[string]any{
		"name": "first.lab.test", "type": "A", "value": "10.20.0.50",
	})

	env.vnfs.reloadFailures = 1
	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/dns-vnfs/%d/records", dns.ID), map[string]any{
		"name": "second.lab.test", "type": "A", "value": "10.20.0.51",
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("failed reload status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dns-vnfs/%d/records", dns.ID), nil)
	if list := decode[[]dnsRecordView](t, recorder); len(list) != 1 {
		t.Fatalf("failed DNS child write was not rolled back: %+v", list)
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dns-vnfs/%d", dns.ID), nil)
	if parent := decode[dnsVNFView](t, recorder); !parent.Enabled {
		t.Fatalf("recoverable DNS reload failure disabled the parent: %+v", parent)
	}

	env.vnfs.reloadFailures = 2
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dns-vnfs/%d/records", dns.ID), map[string]any{
		"name": "third.lab.test", "type": "A", "value": "10.20.0.52",
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("double reload failure status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dns-vnfs/%d", dns.ID), nil)
	if parent := decode[dnsVNFView](t, recorder); parent.Enabled {
		t.Fatalf("DNS parent stayed enabled after its restore reload failed: %+v", parent)
	}
	env.vnfs.reloadFailures = 0
}

// TestDNSVNFWrongTypeRoutes verifies that DNS routes return 404 for another VNF
// type and that child writes do not leak across types.
func TestDNSVNFWrongTypeRoutes(t *testing.T) {
	handler := newTestHandler(t)
	network := createNetworkAPI(t, handler, "lab")
	dhcp := createDHCPVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, false)

	for _, target := range []string{
		fmt.Sprintf("/api/dns-vnfs/%d", dhcp.ID),
		fmt.Sprintf("/api/dns-vnfs/%d/records", dhcp.ID),
		fmt.Sprintf("/api/dns-vnfs/%d/forwarders", dhcp.ID),
		fmt.Sprintf("/api/dns-vnfs/%d/record-views", dhcp.ID),
	} {
		recorder := do(t, handler, http.MethodGet, target, nil)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, body = %s", target, recorder.Code, recorder.Body.String())
		}
	}
	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/dns-vnfs/%d/records", dhcp.ID), map[string]any{
		"name": "x.test", "type": "A", "value": "10.20.0.50",
	})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("record on DHCP parent status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dns-vnfs/%d/restart", dhcp.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("DNS restart on DHCP parent status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
