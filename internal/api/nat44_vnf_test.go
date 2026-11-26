package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

// nat44VNFView is the flat NAT44 parent representation served by the API.
type nat44VNFView struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	NetworkID        int64  `json:"network_id"`
	Enabled          bool   `json:"enabled"`
	Gateway          string `json:"gateway"`
	OutsideInterface string `json:"outside_interface"`
	OutsideAddress   string `json:"outside_address"`
	OutsideGateway   string `json:"outside_gateway"`
	NAT44Pool        string `json:"nat44_pool"`
	DNATRuleCount    int    `json:"dnat_rule_count"`
	Status           string `json:"status"`
	StatusDetail     string `json:"status_detail"`
	TapSwIfIndex     uint32 `json:"tap_sw_if_index"`
	TapHostName      string `json:"tap_host_name"`
}

// dnatRuleView is one child DNAT rule.
type dnatRuleView struct {
	ID           int64  `json:"id"`
	VNFID        int64  `json:"vnf_id"`
	Protocol     string `json:"protocol"`
	ExternalPort int32  `json:"external_port"`
	LocalIP      string `json:"local_ip"`
	LocalPort    int32  `json:"local_port"`
}

// nat44VNFBody builds a NAT44 parent create request.
func nat44VNFBody(name string, networkID int64, enabled bool) map[string]any {
	return map[string]any{
		"name":              name,
		"network_id":        networkID,
		"enabled":           enabled,
		"gateway":           "10.20.0.1/24",
		"outside_interface": "enp9s0",
		"outside_address":   "198.51.100.10/24",
		"outside_gateway":   "198.51.100.1",
	}
}

// createNAT44VNFAPI creates one typed NAT44 parent and returns its flat view.
func createNAT44VNFAPI(t *testing.T, handler http.Handler, name string, networkID int64, enabled bool) nat44VNFView {
	t.Helper()
	recorder := do(t, handler, http.MethodPost, "/api/nat44-vnfs", nat44VNFBody(name, networkID, enabled))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create NAT44 VNF %q status = %d, body = %s", name, recorder.Code, recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); location == "" {
		t.Fatalf("create NAT44 VNF %q has no Location header", name)
	}
	return decode[nat44VNFView](t, recorder)
}

// TestNAT44VNFAndDHCPIntegration covers the NAT44 create path, its BVI and
// DNAT setup, the DHCP gateway/bind rewrite, sessions, disable, delete, and the
// shared network BVI lifetime.
func TestNAT44VNFAndDHCPIntegration(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "nat-lab")
	dhcp := createDHCPVNFAPI(t, handler, "dhcpv4", network.ID, store.VNFTypeDHCPv4, true)

	nat := createNAT44VNFAPI(t, handler, "nat44", network.ID, true)
	if !nat.Enabled || nat.Gateway != "10.20.0.1/24" || nat.OutsideInterface != "enp9s0" || nat.Status != "running" {
		t.Fatalf("created nat44 = %+v", nat)
	}
	if len(env.nat.bvis) != 1 || len(env.nat.hostIfs) != 1 || len(env.nat.applied) != 1 {
		t.Fatalf("NAT calls: bvis=%v host=%v applied=%v", env.nat.bvis, env.nat.hostIfs, env.nat.applied)
	}
	if len(env.nat.staticRules) != 0 {
		t.Fatalf("NAT44 without DNAT children applied rules: %+v", env.nat.staticRules)
	}
	gotNetwork, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if gotNetwork.BVIHostName != fmt.Sprintf("bvi%d", network.ID) || gotNetwork.IPv4Gateway != "10.20.0.1/24" {
		t.Fatalf("network BVI = %+v", gotNetwork)
	}
	gotDHCP, err := env.store.GetDHCPVNF(context.Background(), dhcp.ID)
	if err != nil {
		t.Fatalf("get DHCP VNF: %v", err)
	}
	if gotDHCP.Config.BindIP != "10.20.0.2/24" || gotDHCP.Config.Gateway != "10.20.0.1/24" {
		t.Fatalf("DHCP VNF after NAT = %+v", gotDHCP.Config)
	}
	// The derived DHCP gateway/bind change reloads the runtime; it does not
	// restart it.
	if len(env.vnfs.stopped) != 0 || len(env.vnfs.started) != 1 {
		t.Fatalf("DHCP VNF restart state: stopped=%v started=%v", env.vnfs.stopped, env.vnfs.started)
	}
	// The network-create path already provisioned the BVI and IPv4 gateway, so
	// the DHCP config already matched the NAT-derived values and no reload was
	// needed. The derived setter plus reload path is covered in the DHCP tests.
	if len(env.vnfs.reloaded) != 0 {
		t.Fatalf("unchanged derived bindings reloaded DHCP: %v", env.vnfs.reloaded)
	}

	env.nat.sessions = []string{"udp 10.20.0.100:1234 -> 198.51.100.10:1234 -> 1.1.1.1:53"}
	recorder := do(t, handler, http.MethodGet, fmt.Sprintf("/api/nat44-vnfs/%d/sessions", nat.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("sessions status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	sessions := decode[struct {
		VNFID    int64    `json:"vnf_id"`
		Sessions []string `json:"sessions"`
	}](t, recorder)
	if sessions.VNFID != nat.ID || len(sessions.Sessions) != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}

	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/nat44-vnfs/%d/restart", nat.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restart nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if restarted := decode[nat44VNFView](t, recorder); !restarted.Enabled || restarted.Status != "running" {
		t.Fatalf("restarted nat44 = %+v", restarted)
	}

	removedBefore := len(env.nat.removed)
	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/nat44-vnfs/%d", nat.ID), map[string]any{"enabled": false})
	if recorder.Code != http.StatusOK {
		t.Fatalf("disable nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	disabled := decode[nat44VNFView](t, recorder)
	if disabled.Enabled || len(env.nat.removed) != removedBefore+1 {
		t.Fatalf("disabled nat44 = %+v, removed = %v", disabled, env.nat.removed)
	}

	// The DHCP VNF still needs the network BVI, so deleting NAT44 keeps it.
	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/nat44-vnfs/%d", nat.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.deletedBVI) != 0 {
		t.Fatalf("NAT44 delete removed the network BVI used by DHCP: %v", env.nat.deletedBVI)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/networks/%d", network.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete network status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.deletedBVI) != 1 || env.nat.deletedBVI[0] != fmt.Sprintf("bvi%d", network.ID) {
		t.Fatalf("network BVI was not removed: %v", env.nat.deletedBVI)
	}
}

// TestNAT44DNATRulesCRUD covers per-item DNAT rule create, list, get, update,
// delete, uniqueness, and validation.
func TestNAT44DNATRulesCRUD(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "nat-lab")
	nat := createNAT44VNFAPI(t, handler, "nat44", network.ID, false)

	create := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		return do(t, handler, http.MethodPost, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules", nat.ID), body)
	}
	first := create(map[string]any{
		"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 80,
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("create rule status = %d, body = %s", first.Code, first.Body.String())
	}
	rule := decode[dnatRuleView](t, first)
	if rule.ID == 0 || rule.VNFID != nat.ID || rule.Protocol != "tcp" || rule.ExternalPort != 8080 {
		t.Fatalf("created rule = %+v", rule)
	}
	if location := first.Header().Get("Location"); location != fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules/%d", nat.ID, rule.ID) {
		t.Fatalf("rule Location = %q", location)
	}
	if len(env.nat.staticRules) != 0 {
		t.Fatalf("disabled NAT44 applied DNAT rules: %+v", env.nat.staticRules)
	}

	recorder := do(t, handler, http.MethodGet, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules", nat.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list rules status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if list := decode[[]dnatRuleView](t, recorder); len(list) != 1 || list[0].LocalIP != "10.20.0.100" {
		t.Fatalf("listed rules = %+v", list)
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules/%d", nat.ID, rule.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get rule status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules/%d", nat.ID, rule.ID), map[string]any{
		"protocol": "udp", "external_port": 8443, "local_ip": "10.20.0.101", "local_port": 443,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update rule status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if updated := decode[dnatRuleView](t, recorder); updated.Protocol != "udp" || updated.ExternalPort != 8443 ||
		updated.LocalIP != "10.20.0.101" || updated.LocalPort != 443 {
		t.Fatalf("updated rule = %+v", updated)
	}

	// The same external port is allowed once per protocol.
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules", nat.ID), map[string]any{
		"protocol": "tcp", "external_port": 8443, "local_ip": "10.20.0.102", "local_port": 8080,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("same port per protocol status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules", nat.ID), map[string]any{
		"protocol": "udp", "external_port": 8443, "local_ip": "10.20.0.103", "local_port": 8080,
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate external port status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	invalid := []map[string]any{
		{"protocol": "sctp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 80},
		{"protocol": "tcp", "external_port": 0, "local_ip": "10.20.0.100", "local_port": 80},
		{"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 70000},
		{"protocol": "tcp", "external_port": 8081, "local_ip": "10.21.0.100", "local_port": 80},
		{"protocol": "tcp", "external_port": 8081, "local_ip": "10.20.0.0", "local_port": 80},
	}
	for index, body := range invalid {
		recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules", nat.ID), body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid DNAT rule %d status = %d, body = %s", index, recorder.Code, recorder.Body.String())
		}
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules/%d", nat.ID, rule.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete rule status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules/%d", nat.ID, rule.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted rule status = %d", recorder.Code)
	}
}

// TestNAT44DNATChildWritesApplyMappings proves a DNAT child write replaces the
// VPP mappings in place on a running NAT44 VNF, without a daemon reload.
func TestNAT44DNATChildWritesApplyMappings(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "nat-lab")
	nat := createNAT44VNFAPI(t, handler, "nat44", network.ID, true)
	if len(env.nat.applied) != 1 || len(env.nat.bvis) != 1 {
		t.Fatalf("NAT44 activation calls: applied=%v bvis=%v", env.nat.applied, env.nat.bvis)
	}

	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules", nat.ID), map[string]any{
		"protocol": "tcp", "external_port": 8443, "local_ip": "10.20.0.50", "local_port": 443,
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create rule status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	rule := decode[dnatRuleView](t, recorder)
	if len(env.nat.staticRules) != 1 || env.nat.staticRules[0].ExternalPort != 8443 {
		t.Fatalf("DNAT rule was not applied: %+v", env.nat.staticRules)
	}
	if len(env.nat.bvis) != 1 {
		t.Fatalf("DNAT child write recreated the BVI: %v", env.nat.bvis)
	}
	if len(env.vnfs.reloaded) != 0 {
		t.Fatalf("DNAT child write used the daemon reload path: %v", env.vnfs.reloaded)
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules/%d", nat.ID, rule.ID), map[string]any{
		"protocol": "tcp", "external_port": 8444, "local_ip": "10.20.0.51", "local_port": 80,
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update rule status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.staticRules) != 1 || env.nat.staticRules[0].ExternalPort != 8444 ||
		env.nat.staticRules[0].LocalIP != "10.20.0.51" {
		t.Fatalf("updated DNAT mappings = %+v", env.nat.staticRules)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules/%d", nat.ID, rule.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete rule status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.staticRules) != 0 {
		t.Fatalf("deleted DNAT rule is still applied: %+v", env.nat.staticRules)
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/nat44-vnfs/%d", nat.ID), nil)
	if got := decode[nat44VNFView](t, recorder); got.DNATRuleCount != 0 || got.Status != "running" {
		t.Fatalf("NAT44 after child writes = %+v", got)
	}
}

// TestNAT44DNATChildFailureDisablesParent covers a failed in-place mapping
// update: the row is rolled back and, because the restore also fails, the
// parent is disabled.
func TestNAT44DNATChildFailureDisablesParent(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "nat-lab")
	nat := createNAT44VNFAPI(t, handler, "nat44", network.ID, true)

	env.nat.staticErr = errors.New("DNAT mapping rejected")
	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules", nat.ID), map[string]any{
		"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 80,
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("failed DNAT write status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	env.nat.staticErr = nil
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules", nat.ID), nil)
	if list := decode[[]dnatRuleView](t, recorder); len(list) != 0 {
		t.Fatalf("failed DNAT write was not rolled back: %+v", list)
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/nat44-vnfs/%d", nat.ID), nil)
	if parent := decode[nat44VNFView](t, recorder); parent.Enabled {
		t.Fatalf("NAT44 stayed enabled after its mapping restore failed: %+v", parent)
	}
}

// TestNAT44DeleteCleansUpFailedBVI covers the delete path removing a leftover
// BVI when no other VNF depends on it.
func TestNAT44DeleteCleansUpFailedBVI(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "nat-lab")
	nat := createNAT44VNFAPI(t, env.handler, "nat44", network.ID, false)
	if _, err := env.store.SetNetworkBVI(context.Background(), network.ID, "bvi-leftover", 42, "10.20.0.1/24"); err != nil {
		t.Fatalf("seed network BVI: %v", err)
	}
	recorder := do(t, env.handler, http.MethodDelete, fmt.Sprintf("/api/nat44-vnfs/%d", nat.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.deletedBVI) != 1 || env.nat.deletedBVI[0] != "bvi-leftover" {
		t.Fatalf("deleted BVIs = %v", env.nat.deletedBVI)
	}
	got, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if got.BVIHostName != "" || got.BVISwIfIndex != 0 || got.IPv4Gateway != "" {
		t.Fatalf("network BVI was not cleared: %+v", got)
	}
}

// TestNAT44OutsideFailureDoesNotLeaveBVI covers activation rollback preserving
// the network-owned BVI.
func TestNAT44OutsideFailureDoesNotLeaveBVI(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "nat-lab")
	env.nat.ensureErr = errors.New("outside NIC unavailable")
	recorder := do(t, env.handler, http.MethodPost, "/api/nat44-vnfs", nat44VNFBody("nat44", network.ID, true))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("create nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.bvis) != 1 {
		t.Fatalf("network-owned BVI was not preserved: %v", env.nat.bvis)
	}
	got, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if got.BVIHostName == "" || got.IPv4Gateway == "" {
		t.Fatalf("failed activation removed the network-owned BVI: %+v", got)
	}
}

// TestNAT44DNATFailureRollsBack covers a failed static-mapping step during
// activation rolling back NAT44 and leaving no VNF record.
func TestNAT44DNATFailureRollsBack(t *testing.T) {
	env := newTestAPI(t)
	env.nat.staticErr = errors.New("DNAT mapping rejected")
	network := createNetworkAPI(t, env.handler, "nat-lab")
	recorder := do(t, env.handler, http.MethodPost, "/api/nat44-vnfs", nat44VNFBody("nat44", network.ID, true))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("create nat44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.nat.applied) != 1 || len(env.nat.removed) != 1 {
		t.Fatalf("NAT44 was not rolled back: applied=%v removed=%v", env.nat.applied, env.nat.removed)
	}
	if len(env.nat.deletedBVI) != 0 {
		t.Fatalf("network-owned BVI was deleted: %v", env.nat.deletedBVI)
	}
	got, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if got.BVIHostName == "" || got.IPv4Gateway == "" {
		t.Fatalf("failed DNAT activation removed the network BVI: %+v", got)
	}
	vnfs, err := env.store.ListNAT44VNFs(context.Background(), nil)
	if err != nil {
		t.Fatalf("list nat44 vnfs: %v", err)
	}
	if len(vnfs) != 0 {
		t.Fatalf("failed DNAT activation left VNF records: %+v", vnfs)
	}
}

// TestNAT44Validation covers parent scalar validation.
func TestNAT44Validation(t *testing.T) {
	handler := newTestHandler(t)
	network := createNetworkAPI(t, handler, "nat-lab")
	base := nat44VNFBody("nat44", network.ID, false)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing name", mutate: func(m map[string]any) { delete(m, "name") }},
		{name: "missing network", mutate: func(m map[string]any) { delete(m, "network_id") }},
		{name: "missing outside interface", mutate: func(m map[string]any) { delete(m, "outside_interface") }},
		{name: "bad gateway", mutate: func(m map[string]any) { m["gateway"] = "nope" }},
		{name: "bad outside address", mutate: func(m map[string]any) { m["outside_address"] = "nope" }},
		{name: "bad outside gateway", mutate: func(m map[string]any) { m["outside_gateway"] = "nope" }},
		{name: "outside gateway without address", mutate: func(m map[string]any) { delete(m, "outside_address") }},
		{name: "outside gateway outside subnet", mutate: func(m map[string]any) { m["outside_gateway"] = "203.0.113.1" }},
		{name: "zero outside prefix", mutate: func(m map[string]any) { m["outside_address"] = "0.0.0.0/0" }},
		{name: "bad pool", mutate: func(m map[string]any) { m["nat44_pool"] = "nope" }},
		// DNAT rules are child resources now, so the union-era array is an
		// unknown parent field.
		{name: "child array on parent", mutate: func(m map[string]any) {
			m["dnat_rules"] = []map[string]any{{"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 80}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]any{}
			for key, value := range base {
				body[key] = value
			}
			test.mutate(body)
			recorder := do(t, handler, http.MethodPost, "/api/nat44-vnfs", body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s, want 400", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// TestNAT44WrongTypeRoutes verifies that NAT44 parent and child routes return
// 404 for a different VNF type.
func TestNAT44WrongTypeRoutes(t *testing.T) {
	handler := newTestHandler(t)
	network := createNetworkAPI(t, handler, "nat-lab")
	dhcp := createDHCPVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, false)

	for _, target := range []string{
		fmt.Sprintf("/api/nat44-vnfs/%d", dhcp.ID),
		fmt.Sprintf("/api/nat44-vnfs/%d/sessions", dhcp.ID),
		fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules", dhcp.ID),
	} {
		recorder := do(t, handler, http.MethodGet, target, nil)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, body = %s", target, recorder.Code, recorder.Body.String())
		}
	}
	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/nat44-vnfs/%d/dnat-rules", dhcp.ID), map[string]any{
		"protocol": "tcp", "external_port": 8080, "local_ip": "10.20.0.100", "local_port": 80,
	})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("DNAT rule on DHCP parent status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/nat44-vnfs/%d/restart", dhcp.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("NAT44 restart on DHCP parent status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

// TestNAT44Reconcile covers startup reconciliation of an enabled NAT44 VNF and
// its DNAT child rows.
func TestNAT44Reconcile(t *testing.T) {
	ctx := context.Background()
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "nat-lab")
	nat, err := env.store.CreateNAT44VNF(ctx, store.NewNAT44VNF{
		Name: "nat44", NetworkID: network.ID, Enabled: true,
		Config: store.NAT44VNFConfig{
			Gateway:          "10.20.0.1/24",
			OutsideInterface: "enp9s0",
			OutsideAddress:   "198.51.100.10/24",
			OutsideGateway:   "198.51.100.1",
		},
	})
	if err != nil {
		t.Fatalf("create nat44: %v", err)
	}
	if _, err := env.store.CreateNAT44DNATRule(ctx, nat.ID, store.DNATRule{
		Protocol: "tcp", ExternalPort: 8443, LocalIP: "10.20.0.50", LocalPort: 443,
	}); err != nil {
		t.Fatalf("create DNAT rule: %v", err)
	}

	if err := env.server.ReconcileNAT44s(ctx); err != nil {
		t.Fatalf("reconcile nat44: %v", err)
	}
	if !env.nat.bvisContain(network.ID) || len(env.nat.applied) == 0 {
		t.Fatalf("NAT44 was not reconciled: bvis=%v applied=%v", env.nat.bvis, env.nat.applied)
	}
	if len(env.nat.staticRules) != 1 || env.nat.staticRules[0].ExternalPort != 8443 {
		t.Fatalf("NAT44 DNAT rules were not reconciled: %+v", env.nat.staticRules)
	}
	recorder := do(t, env.handler, http.MethodGet, fmt.Sprintf("/api/nat44-vnfs/%d", nat.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get nat44 status = %d", recorder.Code)
	}
	if got := decode[nat44VNFView](t, recorder); got.Status != "running" || got.DNATRuleCount != 1 {
		t.Fatalf("NAT44 status = %+v", got)
	}
}
