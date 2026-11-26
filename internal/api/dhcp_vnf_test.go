package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// dhcpVNFView is the flat DHCP parent representation served by the API.
type dhcpVNFView struct {
	ID                    int64  `json:"id"`
	Name                  string `json:"name"`
	NetworkID             int64  `json:"network_id"`
	Family                string `json:"family"`
	Enabled               bool   `json:"enabled"`
	Gateway               string `json:"gateway"`
	RangeStart            string `json:"range_start"`
	RangeEnd              string `json:"range_end"`
	LeaseTime             string `json:"lease_time"`
	DNS                   string `json:"dns"`
	BindIP                string `json:"bind_ip"`
	Transport             string `json:"transport"`
	DNSVNFID              int64  `json:"dns_vnf_id"`
	DNSDomain             string `json:"dns_domain"`
	DNSTTL                uint32 `json:"dns_ttl"`
	StaticAssignmentCount int    `json:"static_assignment_count"`
	OptionCount           int    `json:"option_count"`
	LeaseCount            int    `json:"lease_count"`
	Status                string `json:"status"`
	StatusDetail          string `json:"status_detail"`
	TapSwIfIndex          uint32 `json:"tap_sw_if_index"`
	TapHostName           string `json:"tap_host_name"`
}

// dhcpStaticAssignmentView is one child static assignment.
type dhcpStaticAssignmentView struct {
	ID    int64  `json:"id"`
	VNFID int64  `json:"vnf_id"`
	MAC   string `json:"mac"`
	IP    string `json:"ip"`
}

// dhcpOptionView is one child DHCP option.
type dhcpOptionView struct {
	ID     int64  `json:"id"`
	VNFID  int64  `json:"vnf_id"`
	Code   uint32 `json:"code"`
	Format string `json:"format"`
	Value  string `json:"value"`
}

// dhcpVNFBody builds a create request with a valid address plan for the family.
func dhcpVNFBody(name string, networkID int64, family string, enabled bool) map[string]any {
	body := map[string]any{
		"name":       name,
		"network_id": networkID,
		"family":     family,
		"enabled":    enabled,
		"lease_time": "12h",
	}
	if family == store.VNFTypeDHCPv6 {
		body["gateway"] = "fd00:20::1/64"
		body["range_start"] = "fd00:20::100"
		body["range_end"] = "fd00:20::200"
		body["dns"] = "2001:4860:4860::8888"
		return body
	}
	body["gateway"] = "10.20.0.1/24"
	body["range_start"] = "10.20.0.100"
	body["range_end"] = "10.20.0.200"
	body["dns"] = "1.1.1.1,8.8.8.8"
	return body
}

// createDHCPVNFAPI creates one typed DHCP parent and returns its flat view.
func createDHCPVNFAPI(t *testing.T, handler http.Handler, name string, networkID int64, family string, enabled bool) dhcpVNFView {
	t.Helper()
	recorder := do(t, handler, http.MethodPost, "/api/dhcp-vnfs", dhcpVNFBody(name, networkID, family, enabled))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create DHCP VNF %q status = %d, body = %s", name, recorder.Code, recorder.Body.String())
	}
	if location := recorder.Header().Get("Location"); location == "" {
		t.Fatalf("create DHCP VNF %q has no Location header", name)
	}
	return decode[dhcpVNFView](t, recorder)
}

// TestDHCPVNFSharedMemifLifecycle covers a DHCPv4 VNF activated on the network's
// shared memif: the daemon gets the .2 service address and stopping the VNF
// leaves the network-owned memif in place.
func TestDHCPVNFSharedMemifLifecycle(t *testing.T) {
	env := newTestAPI(t)
	env.server.ConfigureMemifDHCP(true, t.TempDir())
	handler := env.handler
	network := createNetworkAPI(t, handler, "shared-net")

	vnf := createDHCPVNFAPI(t, handler, "lab-dhcpv4-shared", network.ID, store.VNFTypeDHCPv4, true)
	if vnf.Family != store.VNFTypeDHCPv4 || !vnf.Enabled || vnf.BindIP != "10.20.0.2/24" {
		t.Fatalf("created shared DHCP VNF = %+v", vnf)
	}
	stored, err := env.store.GetVNF(context.Background(), vnf.ID)
	if err != nil {
		t.Fatalf("get stored vnf: %v", err)
	}
	if stored.Config.BindIP != "10.20.0.2/24" {
		t.Fatalf("bind address = %q, want 10.20.0.2/24", stored.Config.BindIP)
	}
	if stored.MemifHostName != fmt.Sprintf("net%d-vnf", network.ID) {
		t.Fatalf("memif host name = %q, want net%d-vnf", stored.MemifHostName, network.ID)
	}
	if stored.MemifSwIfIndex == 0 || stored.MemifSocket == "" {
		t.Fatalf("stored VNF has no shared memif: %+v", stored)
	}
	if stored.TapHostName != "" || stored.TapSwIfIndex != 0 {
		t.Fatalf("shared memif VNF should not create a tap: %+v", stored)
	}
	if len(env.networks.createdMemifs) != 1 || env.networks.createdMemifs[0] != stored.MemifHostName {
		t.Fatalf("created memifs = %v, want %s", env.networks.createdMemifs, stored.MemifHostName)
	}
	if len(env.nat.bvis) != 1 || env.nat.bvis[0] != network.ID {
		t.Fatalf("network BVIs = %v, want network %d", env.nat.bvis, network.ID)
	}
	if len(env.vnfs.startedHosts) != 1 || env.vnfs.startedHosts[0] != stored.MemifSocket+"@10.20.0.2/24" {
		t.Fatalf("started hosts = %v, want shared socket + address", env.vnfs.startedHosts)
	}
	if len(env.vnfs.sharedIfIDs) != 1 || env.vnfs.sharedIfIDs[0] != host.NetworkVNFMemifID(network.ID) {
		t.Fatalf("shared memif interface id = %v, want %d", env.vnfs.sharedIfIDs, host.NetworkVNFMemifID(network.ID))
	}
	current, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if current.VNFMemifSocket != stored.MemifSocket || current.VNFMemifSwIfIndex != stored.MemifSwIfIndex {
		t.Fatalf("network shared memif = %+v, want %s", current, stored.MemifSocket)
	}

	// Stopping the VNF must keep the network-owned shared memif.
	if err := env.server.StopVNF(context.Background(), stored, false); err != nil {
		t.Fatalf("stop VNF: %v", err)
	}
	if len(env.networks.deletedMemifs) != 0 {
		t.Fatalf("shared memif deleted with VNF: %v", env.networks.deletedMemifs)
	}
}

// TestDHCPVNFDedicatedMemifLifecycle covers a DHCPv4 VNF on a dedicated memif:
// the VNF owns the interface, and deleting it removes the memif.
func TestDHCPVNFDedicatedMemifLifecycle(t *testing.T) {
	env := newTestAPI(t)
	env.server.ConfigureMemifDHCP(true, t.TempDir())
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	if _, err := env.store.SetNetworkVNFTransport(context.Background(), network.ID, "dedicated"); err != nil {
		t.Fatalf("set network transport: %v", err)
	}

	vnf := createDHCPVNFAPI(t, handler, "lab-dhcpv4-memif", network.ID, store.VNFTypeDHCPv4, true)
	stored, err := env.store.GetVNF(context.Background(), vnf.ID)
	if err != nil {
		t.Fatalf("get stored vnf: %v", err)
	}
	if stored.MemifSwIfIndex == 0 || stored.MemifSocket == "" {
		t.Fatalf("stored vnf has no memif runtime: %+v", stored)
	}
	if stored.MemifHostName != stored.Name {
		t.Fatalf("memif interface name = %q, want %q", stored.MemifHostName, stored.Name)
	}
	if stored.TapHostName != "" || stored.TapSwIfIndex != 0 {
		t.Fatalf("memif VNF should not create a gateway tap: %+v", stored)
	}
	if !slices.Contains(env.networks.createdMemifs, stored.Name) {
		t.Fatalf("created memifs = %v, want %s", env.networks.createdMemifs, stored.Name)
	}
	if len(env.networks.createdTaps) != 0 {
		t.Fatalf("created taps = %v, want none", env.networks.createdTaps)
	}
	if len(env.vnfs.startedHosts) != 1 || env.vnfs.startedHosts[0] != stored.MemifSocket {
		t.Fatalf("started hosts = %v, want %s", env.vnfs.startedHosts, stored.MemifSocket)
	}

	recorder := do(t, handler, http.MethodDelete, fmt.Sprintf("/api/dhcp-vnfs/%d", stored.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete vnf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.networks.deletedMemifs) == 0 {
		t.Fatal("delete did not remove the memif interface")
	}
}

// TestDHCPVNFTapLifecycle covers create, list, leases, disable, restart, and
// delete for a DHCPv4 VNF on a tap.
func TestDHCPVNFTapLifecycle(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")

	vnf := createDHCPVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, true)
	if vnf.Family != store.VNFTypeDHCPv4 || !vnf.Enabled || vnf.TapSwIfIndex == 0 ||
		vnf.TapHostName != fmt.Sprintf("vnf%d", vnf.ID) || vnf.Status != "running" {
		t.Fatalf("created vnf = %+v", vnf)
	}
	if len(env.networks.createdTaps) != 1 || env.networks.createdTaps[0] != vnf.TapHostName {
		t.Fatalf("created taps = %v", env.networks.createdTaps)
	}
	if len(env.vnfs.started) != 1 || env.vnfs.started[0] != vnf.ID {
		t.Fatalf("started vnfs = %v", env.vnfs.started)
	}

	recorder := do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs?network_id=%d", network.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list vnfs status = %d", recorder.Code)
	}
	list := decode[[]dhcpVNFView](t, recorder)
	if len(list) != 1 || list[0].Status != "running" || list[0].ID != vnf.ID {
		t.Fatalf("listed vnfs = %+v", list)
	}

	env.vnfs.leases[vnf.ID] = []store.VNFLease{{
		ClientID:  "02:00:00:00:00:01",
		Address:   "10.20.0.100",
		ExpiresAt: time.Now().Add(time.Hour),
		Active:    true,
	}}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d/leases", vnf.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("leases status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	leases := decode[struct {
		VNFID  int64            `json:"vnf_id"`
		Leases []store.VNFLease `json:"leases"`
	}](t, recorder)
	if leases.VNFID != vnf.ID || len(leases.Leases) != 1 {
		t.Fatalf("leases = %+v", leases)
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/dhcp-vnfs/%d", vnf.ID), map[string]any{"enabled": false})
	if recorder.Code != http.StatusOK {
		t.Fatalf("disable vnf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	disabled := decode[dhcpVNFView](t, recorder)
	if disabled.Enabled || disabled.TapSwIfIndex != 0 || len(env.networks.deletedTaps) == 0 {
		t.Fatalf("disabled vnf = %+v, deleted taps = %v", disabled, env.networks.deletedTaps)
	}

	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/restart", vnf.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restart vnf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	restarted := decode[dhcpVNFView](t, recorder)
	if !restarted.Enabled || restarted.TapSwIfIndex == 0 || restarted.Status != "running" {
		t.Fatalf("restarted vnf = %+v", restarted)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/dhcp-vnfs/%d", vnf.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete vnf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d", vnf.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted vnf status = %d", recorder.Code)
	}
}

// TestDHCPVNFv6Lifecycle covers DHCPv6 create, update, restart, and delete.
func TestDHCPVNFv6Lifecycle(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")

	vnf := createDHCPVNFAPI(t, handler, "lab-dhcpv6", network.ID, store.VNFTypeDHCPv6, true)
	if !vnf.Enabled || vnf.Family != store.VNFTypeDHCPv6 || vnf.Gateway != "fd00:20::1/64" ||
		vnf.RangeStart != "fd00:20::100" || vnf.Status != "running" {
		t.Fatalf("created vnf = %+v", vnf)
	}
	if len(env.vnfs.started) != 1 {
		t.Fatalf("started vnfs = %v", env.vnfs.started)
	}

	recorder := do(t, handler, http.MethodPut, fmt.Sprintf("/api/dhcp-vnfs/%d", vnf.ID), map[string]any{
		"range_end":  "fd00:20::150",
		"lease_time": "6h",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update v6 vnf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	updated := decode[dhcpVNFView](t, recorder)
	if updated.RangeEnd != "fd00:20::150" || updated.LeaseTime != "6h0m0s" || !updated.Enabled {
		t.Fatalf("updated v6 vnf = %+v", updated)
	}

	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/restart", vnf.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restart v6 vnf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs?network_id=%d&family=%s", network.ID, store.VNFTypeDHCPv6), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list v6 vnfs status = %d", recorder.Code)
	}
	if list := decode[[]dhcpVNFView](t, recorder); len(list) != 1 || list[0].Family != store.VNFTypeDHCPv6 {
		t.Fatalf("listed v6 vnfs = %+v", list)
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs?network_id=%d&family=%s", network.ID, store.VNFTypeDHCPv4), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list v4 vnfs status = %d", recorder.Code)
	}
	if list := decode[[]dhcpVNFView](t, recorder); len(list) != 0 {
		t.Fatalf("family filter returned v4 vnfs: %+v", list)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/dhcp-vnfs/%d", vnf.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete v6 vnf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

// TestDHCPVNFStaticAssignmentsCRUD covers the per-item static assignment routes
// and their validation, uniqueness, and wrong-parent-type handling.
func TestDHCPVNFStaticAssignmentsCRUD(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	vnf := createDHCPVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, false)

	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", vnf.ID), map[string]any{
		"mac": "02:00:00:00:00:01",
		"ip":  "10.20.0.50",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create assignment status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	created := decode[dhcpStaticAssignmentView](t, recorder)
	if created.ID == 0 || created.VNFID != vnf.ID || created.MAC != "02:00:00:00:00:01" || created.IP != "10.20.0.50" {
		t.Fatalf("created assignment = %+v", created)
	}
	if location := recorder.Header().Get("Location"); location != fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments/%d", vnf.ID, created.ID) {
		t.Fatalf("assignment Location = %q", location)
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", vnf.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list assignments status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if list := decode[[]dhcpStaticAssignmentView](t, recorder); len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("listed assignments = %+v", list)
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments/%d", vnf.ID, created.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get assignment status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := decode[dhcpStaticAssignmentView](t, recorder); got.IP != "10.20.0.50" {
		t.Fatalf("got assignment = %+v", got)
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments/%d", vnf.ID, created.ID), map[string]any{
		"mac": "02:00:00:00:00:02",
		"ip":  "10.20.0.51",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update assignment status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if updated := decode[dhcpStaticAssignmentView](t, recorder); updated.MAC != "02:00:00:00:00:02" || updated.IP != "10.20.0.51" {
		t.Fatalf("updated assignment = %+v", updated)
	}

	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", vnf.ID), map[string]any{
		"mac": "02:00:00:00:00:02",
		"ip":  "10.20.0.60",
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate MAC status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", vnf.ID), map[string]any{
		"mac": "02:00:00:00:00:05",
		"ip":  "10.20.0.51",
	})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate IP status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", vnf.ID), map[string]any{
		"mac": "02:00:00:00:00:03",
		"ip":  "10.21.0.60",
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("outside-subnet assignment status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", vnf.ID), map[string]any{
		"mac": "not-a-mac",
		"ip":  "10.20.0.60",
	})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid MAC status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments/%d", vnf.ID, created.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete assignment status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments/%d", vnf.ID, created.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("get deleted assignment status = %d", recorder.Code)
	}

	// Static assignments are a DHCPv4-only child resource.
	v6Network := createNetworkAPI(t, handler, "lab-v6")
	v6 := createDHCPVNFAPI(t, handler, "lab-dhcpv6", v6Network.ID, store.VNFTypeDHCPv6, false)
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", v6.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("v6 static assignment list status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", v6.ID), map[string]any{
		"mac": "02:00:00:00:00:04",
		"ip":  "10.20.0.52",
	})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("v6 static assignment create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

// TestDHCPVNFOptionsCRUD covers DHCP option create, list, update, delete, and
// validation for both families.
func TestDHCPVNFOptionsCRUD(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	vnf := createDHCPVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, false)

	create := func(body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		return do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/options", vnf.ID), body)
	}
	first := create(map[string]any{"code": 15, "format": "string", "value": "lab.test"})
	if first.Code != http.StatusCreated {
		t.Fatalf("create option status = %d, body = %s", first.Code, first.Body.String())
	}
	option := decode[dhcpOptionView](t, first)
	if option.ID == 0 || option.VNFID != vnf.ID || option.Code != 15 || option.Value != "lab.test" {
		t.Fatalf("created option = %+v", option)
	}
	if location := first.Header().Get("Location"); location != fmt.Sprintf("/api/dhcp-vnfs/%d/options/%d", vnf.ID, option.ID) {
		t.Fatalf("option Location = %q", location)
	}

	second := create(map[string]any{"code": 42, "format": "ipv4", "value": "10.20.0.1, 10.20.0.2"})
	if second.Code != http.StatusCreated {
		t.Fatalf("create option 42 status = %d, body = %s", second.Code, second.Body.String())
	}
	if got := decode[dhcpOptionView](t, second); got.Value != "10.20.0.1,10.20.0.2" {
		t.Fatalf("normalized ipv4 option = %+v", got)
	}

	recorder := do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d/options", vnf.ID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list options status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if list := decode[[]dhcpOptionView](t, recorder); len(list) != 2 {
		t.Fatalf("listed options = %+v", list)
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/dhcp-vnfs/%d/options/%d", vnf.ID, option.ID), map[string]any{
		"code": 15, "format": "string", "value": "changed.test",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("update option status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if updated := decode[dhcpOptionView](t, recorder); updated.Value != "changed.test" {
		t.Fatalf("updated option = %+v", updated)
	}

	// Server-managed codes and bad values are rejected as input errors.
	recorder = create(map[string]any{"code": 1, "format": "ipv4", "value": "255.255.255.0"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("reserved option status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = create(map[string]any{"code": 42, "format": "ipv4", "value": "nope"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad option value status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = create(map[string]any{"code": 43, "format": "bogus", "value": "x"})
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad option format status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = create(map[string]any{"code": 15, "format": "string", "value": "duplicate.test"})
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate option code status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/dhcp-vnfs/%d/options/%d", vnf.ID, option.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete option status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d/options", vnf.ID), nil)
	if list := decode[[]dhcpOptionView](t, recorder); len(list) != 1 {
		t.Fatalf("options after delete = %+v", list)
	}

	// DHCPv6 parents use the same option routes.
	v6Network := createNetworkAPI(t, handler, "lab-v6")
	v6 := createDHCPVNFAPI(t, handler, "lab-dhcpv6", v6Network.ID, store.VNFTypeDHCPv6, false)
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/options", v6.ID), map[string]any{
		"code": 23, "format": "ipv6", "value": "2001:db8::1",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create v6 option status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	// Options are not available for non-DHCP parents.
	dns := createDNSVNFAPI(t, handler, "lab-dns", network.ID, false)
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d/options", dns.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("DNS options status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

// TestDHCPVNFChildWritesReload covers the enabled-parent reload contract and
// both reload failure paths: a restored row keeps the parent enabled, while a
// failed restore disables it.
func TestDHCPVNFChildWritesReload(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	vnf := createDHCPVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, true)
	if vnf.Status != "running" {
		t.Fatalf("created VNF is not running: %+v", vnf)
	}

	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/options", vnf.ID), map[string]any{
		"code": 15, "format": "string", "value": "lab.test",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create option status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.vnfs.reloaded) != 1 || env.vnfs.reloaded[0] != vnf.ID {
		t.Fatalf("reloads = %v, want one for %d", env.vnfs.reloaded, vnf.ID)
	}

	env.vnfs.reloadFailures = 1
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", vnf.ID), map[string]any{
		"mac": "02:00:00:00:00:01",
		"ip":  "10.20.0.50",
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("failed reload status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", vnf.ID), nil)
	if list := decode[[]dhcpStaticAssignmentView](t, recorder); len(list) != 0 {
		t.Fatalf("failed child write was not rolled back: %+v", list)
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d", vnf.ID), nil)
	if parent := decode[dhcpVNFView](t, recorder); !parent.Enabled || parent.Status != "running" {
		t.Fatalf("recoverable reload failure disabled the parent: %+v", parent)
	}

	env.vnfs.reloadFailures = 2
	recorder = do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", vnf.ID), map[string]any{
		"mac": "02:00:00:00:00:02",
		"ip":  "10.20.0.51",
	})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("double reload failure status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d", vnf.ID), nil)
	if parent := decode[dhcpVNFView](t, recorder); parent.Enabled {
		t.Fatalf("parent stayed enabled after its restore reload failed: %+v", parent)
	}
	env.vnfs.reloadFailures = 0
}

// TestDHCPVNFDisabledChildWritesDoNotReload covers the disabled-parent path.
func TestDHCPVNFDisabledChildWritesDoNotReload(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	vnf := createDHCPVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, false)

	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/options", vnf.ID), map[string]any{
		"code": 15, "format": "string", "value": "lab.test",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create option status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.vnfs.reloaded) != 0 {
		t.Fatalf("disabled parent reloaded the runtime: %v", env.vnfs.reloaded)
	}
}

// TestDHCPVNFValidation covers flat create validation and the two uniqueness
// rules: duplicate names and the one-per-network-type rule.
func TestDHCPVNFValidation(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	base := dhcpVNFBody("lab-dhcp", network.ID, store.VNFTypeDHCPv4, false)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing name", mutate: func(m map[string]any) { delete(m, "name") }},
		{name: "missing network", mutate: func(m map[string]any) { delete(m, "network_id") }},
		{name: "unknown network", mutate: func(m map[string]any) { m["network_id"] = 999 }},
		{name: "missing family", mutate: func(m map[string]any) { delete(m, "family") }},
		{name: "bad family", mutate: func(m map[string]any) { m["family"] = "bogus" }},
		{name: "bad gateway", mutate: func(m map[string]any) { m["gateway"] = "not-a-cidr" }},
		{name: "v6 gateway in v4", mutate: func(m map[string]any) { m["gateway"] = "fd00::1/64" }},
		{name: "range outside subnet", mutate: func(m map[string]any) { m["range_start"] = "10.21.0.10" }},
		{name: "range includes gateway", mutate: func(m map[string]any) { m["range_start"] = "10.20.0.1" }},
		{name: "bad dns", mutate: func(m map[string]any) { m["dns"] = "nope" }},
		{name: "bad lease time", mutate: func(m map[string]any) { m["lease_time"] = "forever" }},
		// Child arrays are not part of the parent create request; the flat
		// schema rejects them as unknown fields, which replaces the old
		// union-specific "field from another type" cases.
		{name: "child array on parent", mutate: func(m map[string]any) {
			m["static_assignments"] = []map[string]any{{"mac": "02:00:00:00:00:01", "ip": "10.20.0.50"}}
		}},
		{name: "v6 bad gateway", mutate: func(m map[string]any) {
			m["family"] = store.VNFTypeDHCPv6
			m["gateway"] = "10.20.0.1/24"
			m["range_start"] = "fd00:20::100"
			m["range_end"] = "fd00:20::200"
			m["dns"] = "2001:4860:4860::8888"
		}},
		{name: "v6 v4 dns", mutate: func(m map[string]any) {
			m["family"] = store.VNFTypeDHCPv6
			m["gateway"] = "fd00:20::1/64"
			m["range_start"] = "fd00:20::100"
			m["range_end"] = "fd00:20::200"
			m["dns"] = "1.1.1.1"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := map[string]any{}
			for key, value := range base {
				body[key] = value
			}
			test.mutate(body)
			recorder := do(t, handler, http.MethodPost, "/api/dhcp-vnfs", body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s, want 400", recorder.Code, recorder.Body.String())
			}
		})
	}

	first := createDHCPVNFAPI(t, handler, "duplicate", network.ID, store.VNFTypeDHCPv4, false)
	recorder := do(t, handler, http.MethodPost, "/api/dhcp-vnfs", dhcpVNFBody(first.Name, network.ID, store.VNFTypeDHCPv4, false))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate name status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodPost, "/api/dhcp-vnfs", dhcpVNFBody("second-dhcpv4", network.ID, store.VNFTypeDHCPv4, false))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("duplicate type status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	// A DHCPv6 VNF may share the network with a DHCPv4 VNF.
	createDHCPVNFAPI(t, handler, "lab-dhcpv6", network.ID, store.VNFTypeDHCPv6, false)
}

// TestDHCPVNFWrongTypeRoutes verifies that every DHCP route returns 404 for an
// ID that belongs to another VNF type.
func TestDHCPVNFWrongTypeRoutes(t *testing.T) {
	handler := newTestHandler(t)
	network := createNetworkAPI(t, handler, "lab")
	dns := createDNSVNFAPI(t, handler, "lab-dns", network.ID, false)

	for _, target := range []string{
		fmt.Sprintf("/api/dhcp-vnfs/%d", dns.ID),
		fmt.Sprintf("/api/dhcp-vnfs/%d/leases", dns.ID),
		fmt.Sprintf("/api/dhcp-vnfs/%d/static-assignments", dns.ID),
		fmt.Sprintf("/api/dhcp-vnfs/%d/options", dns.ID),
	} {
		recorder := do(t, handler, http.MethodGet, target, nil)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, body = %s", target, recorder.Code, recorder.Body.String())
		}
	}
	recorder := do(t, handler, http.MethodPost, fmt.Sprintf("/api/dhcp-vnfs/%d/restart", dns.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("restart wrong type status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/dhcp-vnfs/%d", dns.ID), nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("delete wrong type status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

// TestDHCPVNFDNSIntegration covers the typed DNS reference and the TTL default.
func TestDHCPVNFDNSIntegration(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	dns := createDNSVNFAPI(t, handler, "lab-dns", network.ID, true)

	body := dhcpVNFBody("lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, false)
	body["dns_vnf_id"] = dns.ID
	body["dns_domain"] = "Lab.Test."
	recorder := do(t, handler, http.MethodPost, "/api/dhcp-vnfs", body)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create integrated DHCP status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	created := decode[dhcpVNFView](t, recorder)
	if created.DNSVNFID != dns.ID || created.DNSDomain != "lab.test" || created.DNSTTL != 300 {
		t.Fatalf("integrated DHCP VNF = %+v", created)
	}

	recorder = do(t, handler, http.MethodDelete, fmt.Sprintf("/api/dns-vnfs/%d", dns.ID), nil)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("delete referenced DNS status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

// TestDHCPVNFFailures covers tap and runtime start rollback on create.
func TestDHCPVNFFailures(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	body := dhcpVNFBody("lab-dhcp", network.ID, store.VNFTypeDHCPv4, true)

	env.networks.createTapErr = errors.New("tap failed")
	recorder := do(t, handler, http.MethodPost, "/api/dhcp-vnfs", body)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("tap failure status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	env.networks.createTapErr = nil

	env.vnfs.startErr = errors.New("coredhcp failed")
	recorder = do(t, handler, http.MethodPost, "/api/dhcp-vnfs", body)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("vnf start failure status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if len(env.networks.deletedTaps) == 0 {
		t.Fatal("VNF start failure did not roll back the tap")
	}
	env.vnfs.startErr = nil

	recorder = do(t, handler, http.MethodGet, "/api/dhcp-vnfs", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d", recorder.Code)
	}
	if list := decode[[]dhcpVNFView](t, recorder); len(list) != 0 {
		t.Fatalf("failed VNFs left records: %+v", list)
	}
}

// TestDHCPVNFUpdateFailureAndRecovery covers the parent update restart and its
// recovery after the runtime starts again.
func TestDHCPVNFUpdateFailureAndRecovery(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	vnf := createDHCPVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, false)

	payload := map[string]any{
		"enabled":    true,
		"dns":        "1.1.1.1",
		"lease_time": "6h",
	}
	env.vnfs.startErr = errors.New("start failed")
	recorder := do(t, handler, http.MethodPut, fmt.Sprintf("/api/dhcp-vnfs/%d", vnf.ID), payload)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("failed update status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d", vnf.ID), nil)
	if failed := decode[dhcpVNFView](t, recorder); failed.Enabled || failed.TapSwIfIndex != 0 {
		t.Fatalf("failed VNF left enabled: %+v", failed)
	}
	env.vnfs.startErr = nil

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/dhcp-vnfs/%d", vnf.ID), payload)
	if recorder.Code != http.StatusOK {
		t.Fatalf("recovery status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recovered := decode[dhcpVNFView](t, recorder)
	if !recovered.Enabled || recovered.TapSwIfIndex == 0 || recovered.LeaseTime != "6h0m0s" {
		t.Fatalf("recovered vnf = %+v", recovered)
	}

	recorder = do(t, handler, http.MethodPut, fmt.Sprintf("/api/dhcp-vnfs/%d", vnf.ID), map[string]any{"enabled": false})
	if recorder.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if disabled := decode[dhcpVNFView](t, recorder); disabled.Enabled || disabled.TapSwIfIndex != 0 {
		t.Fatalf("disabled vnf = %+v", disabled)
	}
}

// TestDHCPVNFCleansLegacyNetworkDHCP covers the one-time migration of the old
// per-network DHCP tap and runtime state.
func TestDHCPVNFCleansLegacyNetworkDHCP(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")
	if _, err := env.store.SetNetworkDHCP(context.Background(), network.ID, store.Network{
		DHCPEnabled:      true,
		DHCPGateway:      "10.20.0.1/24",
		DHCPRangeStart:   "10.20.0.100",
		DHCPRangeEnd:     "10.20.0.200",
		DHCPDNS:          "1.1.1.1",
		DHCPTapSwIfIndex: 77,
		DHCPTapHostName:  "ndn1",
	}); err != nil {
		t.Fatalf("seed legacy DHCP: %v", err)
	}

	vnf := createDHCPVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, true)
	if vnf.TapHostName == "ndn1" || vnf.TapHostName != fmt.Sprintf("vnf%d", vnf.ID) {
		t.Fatalf("vnf tap = %+v", vnf)
	}
	if !slices.Contains(env.networks.deletedTaps, uint32(77)) {
		t.Fatalf("legacy tap was not deleted: %v", env.networks.deletedTaps)
	}
	if !slices.Contains(env.vnfs.cleaned, network.ID) {
		t.Fatalf("legacy DHCP cleanup was not requested: %v", env.vnfs.cleaned)
	}
	got, err := env.store.GetNetwork(context.Background(), network.ID)
	if err != nil {
		t.Fatalf("get network: %v", err)
	}
	if got.DHCPEnabled || got.DHCPTapSwIfIndex != 0 || got.DHCPGateway != "" {
		t.Fatalf("legacy network DHCP was not cleared: %+v", got)
	}
}

// TestDHCPVNFNATDerivedBindingsReload covers the NAT-adjusted gateway and bind
// address: the typed setter writes the derived values and reloads the running
// DHCP runtime without restarting it.
func TestDHCPVNFNATDerivedBindingsReload(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "nat-lab")
	dhcp := createDHCPVNFAPI(t, handler, "dhcpv4", network.ID, store.VNFTypeDHCPv4, true)
	started := len(env.vnfs.started)
	// Simulate a DHCP VNF whose stored derived bindings predate NAT setup, so
	// NAT44 activation must rewrite them through the typed setter and reload.
	if err := env.store.SetDHCPVNFNetworkBindings(context.Background(), dhcp.ID, "10.20.0.254/24", "10.20.0.253/24"); err != nil {
		t.Fatalf("seed pre-NAT bindings: %v", err)
	}

	recorder := do(t, handler, http.MethodPost, "/api/nat44-vnfs", map[string]any{
		"name":              "nat44",
		"network_id":        network.ID,
		"enabled":           true,
		"gateway":           "10.20.0.1/24",
		"outside_interface": "enp9s0",
		"outside_address":   "198.51.100.10/24",
		"outside_gateway":   "198.51.100.1",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create NAT44 status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if nat := decode[nat44VNFView](t, recorder); nat.Status != "running" {
		t.Fatalf("created NAT44 status = %+v", nat)
	}

	stored, err := env.store.GetDHCPVNF(context.Background(), dhcp.ID)
	if err != nil {
		t.Fatalf("get DHCP VNF: %v", err)
	}
	if stored.Config.Gateway != "10.20.0.1/24" || stored.Config.BindIP != "10.20.0.2/24" {
		t.Fatalf("DHCP typed config after NAT = %+v", stored.Config)
	}
	if !slices.Contains(env.vnfs.reloaded, dhcp.ID) {
		t.Fatalf("derived NAT binding did not reload the DHCP runtime: %v", env.vnfs.reloaded)
	}
	if len(env.vnfs.stopped) != 0 || len(env.vnfs.started) != started {
		t.Fatalf("derived NAT binding restarted the DHCP runtime: stopped=%v started=%v", env.vnfs.stopped, env.vnfs.started)
	}

	recorder = do(t, handler, http.MethodGet, fmt.Sprintf("/api/dhcp-vnfs/%d", dhcp.ID), nil)
	if got := decode[dhcpVNFView](t, recorder); got.BindIP != "10.20.0.2/24" {
		t.Fatalf("API bind address after NAT = %q", got.BindIP)
	}
}

// TestDHCPVNFNetworkDeleteStopsVNFs covers network deletion stopping the
// network's VNFs and relying on the identity cascade.
func TestDHCPVNFNetworkDeleteStopsVNFs(t *testing.T) {
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")

	createDHCPVNFAPI(t, handler, "lab-dhcpv4", network.ID, store.VNFTypeDHCPv4, false)
	v6 := createDHCPVNFAPI(t, handler, "lab-dhcpv6", network.ID, store.VNFTypeDHCPv6, true)

	recorder := do(t, handler, http.MethodDelete, fmt.Sprintf("/api/networks/%d", network.ID), nil)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete network status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !slices.Contains(env.vnfs.stopped, v6.ID) {
		t.Fatalf("network delete did not stop VNFs: %v", env.vnfs.stopped)
	}
	recorder = do(t, handler, http.MethodGet, "/api/dhcp-vnfs", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list vnfs status = %d", recorder.Code)
	}
	if list := decode[[]dhcpVNFView](t, recorder); len(list) != 0 {
		t.Fatalf("network delete left VNFs: %+v", list)
	}
}

// TestReconcileEnabledVNFs covers tap reuse, tap recreation, disabled skipping,
// and the no-op second pass.
func TestReconcileEnabledVNFs(t *testing.T) {
	ctx := context.Background()
	env := newTestAPI(t)
	handler := env.handler
	network := createNetworkAPI(t, handler, "lab")

	// Existing tap: reuse it and only start the CoreDHCP runtime.
	existing, err := env.store.CreateDHCPVNF(ctx, store.NewDHCPVNF{
		Name: "existing-v4", NetworkID: network.ID, Family: store.VNFTypeDHCPv4, Enabled: true,
		Config: store.DHCPVNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
	})
	if err != nil {
		t.Fatalf("create existing vnf: %v", err)
	}
	existing.TapSwIfIndex = 700
	existing.TapHostName = "vnf77"
	existing, err = env.store.UpdateVNFIdentity(ctx, existing)
	if err != nil {
		t.Fatalf("store existing tap: %v", err)
	}
	// No tap: recreate the tap, as for a migrated legacy network.
	migrated, err := env.store.CreateDHCPVNF(ctx, store.NewDHCPVNF{
		Name: "migrated-v6", NetworkID: network.ID, Family: store.VNFTypeDHCPv6, Enabled: true,
		Config: store.DHCPVNFConfig{Gateway: "fd00:20::1/64", RangeStart: "fd00:20::100", RangeEnd: "fd00:20::200"},
	})
	if err != nil {
		t.Fatalf("create migrated vnf: %v", err)
	}
	otherNetwork := createNetworkAPI(t, handler, "other")
	disabled, err := env.store.CreateDHCPVNF(ctx, store.NewDHCPVNF{
		Name: "disabled-v4", NetworkID: otherNetwork.ID, Family: store.VNFTypeDHCPv4, Enabled: false,
		Config: store.DHCPVNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
	})
	if err != nil {
		t.Fatalf("create disabled vnf: %v", err)
	}

	if err := env.server.ReconcileVNFs(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(env.vnfs.started) != 2 {
		t.Fatalf("started VNFs = %v, want 2", env.vnfs.started)
	}
	if env.vnfs.startedHosts[0] != "vnf77" {
		t.Fatalf("first start host = %q, want existing tap", env.vnfs.startedHosts[0])
	}
	if !env.vnfs.running[migrated.ID] {
		t.Fatalf("migrated VNF was not started: %+v", env.vnfs)
	}
	if !env.vnfs.running[existing.ID] {
		t.Fatalf("existing VNF was not started: %+v", env.vnfs)
	}
	if env.vnfs.running[disabled.ID] {
		t.Fatal("disabled VNF was started")
	}

	gotMigrated, err := env.store.GetVNF(ctx, migrated.ID)
	if err != nil {
		t.Fatalf("get migrated vnf: %v", err)
	}
	if !gotMigrated.Enabled || gotMigrated.TapSwIfIndex == 0 ||
		gotMigrated.TapHostName != fmt.Sprintf("vnf%d", migrated.ID) {
		t.Fatalf("migrated vnf after reconcile = %+v", gotMigrated)
	}
	if !slices.Contains(env.networks.createdTaps, gotMigrated.TapHostName) {
		t.Fatalf("recreated tap %q not in %v", gotMigrated.TapHostName, env.networks.createdTaps)
	}

	// A second reconciliation is a no-op for running VNFs.
	before := len(env.vnfs.started)
	if err := env.server.ReconcileVNFs(ctx); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(env.vnfs.started) != before {
		t.Fatalf("second reconcile restarted VNFs: %v", env.vnfs.started)
	}
}

// TestReconcileRecreatesUnusableTap covers the stale-tap repair path.
func TestReconcileRecreatesUnusableTap(t *testing.T) {
	ctx := context.Background()
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "lab")
	vnf, err := env.store.CreateDHCPVNF(ctx, store.NewDHCPVNF{
		Name: "stale-tap", NetworkID: network.ID, Family: store.VNFTypeDHCPv4, Enabled: true,
		Config: store.DHCPVNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
	})
	if err != nil {
		t.Fatalf("create vnf: %v", err)
	}
	vnf.TapSwIfIndex = 701
	vnf.TapHostName = "vnfstale"
	if _, err := env.store.UpdateVNFIdentity(ctx, vnf); err != nil {
		t.Fatalf("store stale tap: %v", err)
	}

	env.vnfs.startFailures = 1
	if err := env.server.ReconcileVNFs(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(env.vnfs.stopped) == 0 {
		t.Fatal("stale runtime was not stopped")
	}
	if !slices.Contains(env.networks.deletedTaps, uint32(701)) {
		t.Fatalf("stale tap was not deleted: %v", env.networks.deletedTaps)
	}
	got, err := env.store.GetVNF(ctx, vnf.ID)
	if err != nil {
		t.Fatalf("get vnf: %v", err)
	}
	if got.TapHostName != fmt.Sprintf("vnf%d", vnf.ID) || got.TapSwIfIndex == 0 {
		t.Fatalf("vnf after recreation = %+v", got)
	}
	if !env.vnfs.Running(ctx, got) {
		t.Fatal("recreated VNF is not running")
	}
}

// TestRunInfrastructureReconcilerStartsEnabledVNFs covers the startup pass
// starting an enabled DHCP VNF and ensuring its bridge domain.
func TestRunInfrastructureReconcilerStartsEnabledVNFs(t *testing.T) {
	env := newTestAPI(t)
	network := createNetworkAPI(t, env.handler, "lab")
	vnf, err := env.store.CreateDHCPVNF(context.Background(), store.NewDHCPVNF{
		Name: "startup-v4", NetworkID: network.ID, Family: store.VNFTypeDHCPv4, Enabled: true,
		Config: store.DHCPVNFConfig{Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200"},
	})
	if err != nil {
		t.Fatalf("create vnf: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		env.server.RunInfrastructureReconciler(ctx)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !env.vnfs.Running(context.Background(), vnf) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("VNF reconciler did not stop")
	}
	if !env.vnfs.Running(context.Background(), vnf) {
		t.Fatal("enabled VNF was not started by the reconciler")
	}
	if !slices.Contains(env.networks.ensuredBDs, network.BridgeDomainID) {
		t.Fatalf("reconciler did not ensure bridge domain %d", network.BridgeDomainID)
	}
}
