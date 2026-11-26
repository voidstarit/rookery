package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// newVNFService opens a store with two networks and a Service using fakeBackend.
func newVNFService(t *testing.T) (*store.Store, *Service, *fakeBackend, store.Network, store.Network) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "vnfs.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	network, err := st.CreateNetwork(ctx, store.Network{Name: "lab", BridgeDomainID: 100})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	other, err := st.CreateNetwork(ctx, store.Network{Name: "other", BridgeDomainID: 101})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	backend := &fakeBackend{store: st}
	return st, New(st, backend, &sync.Mutex{}), backend, network, other
}

// expectVNFStatus asserts that err carries the expected HTTP status.
func expectVNFStatus(t *testing.T, err error, want int) {
	t.Helper()
	var vnfErr *VNFError
	if !errors.As(err, &vnfErr) {
		t.Fatalf("error = %v, want *VNFError", err)
	}
	if vnfErr.Status != want {
		t.Fatalf("error status = %d, want %d (%v)", vnfErr.Status, want, err)
	}
}

// TestTypedVNFCreateAndDelete covers per-type create, activation, delete, and
// the one-VNF-of-each-type-per-network rule.
func TestTypedVNFCreateAndDelete(t *testing.T) {
	ctx := context.Background()
	_, service, backend, network, other := newVNFService(t)

	dhcp, err := service.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{
		Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4,
	})
	if err != nil {
		t.Fatalf("create dhcp: %v", err)
	}
	if dhcp.Enabled || backend.activated != 0 {
		t.Fatalf("disabled create activated the VNF: %+v", dhcp)
	}
	if dhcp.Config.Gateway != "10.0.0.1/24" || dhcp.Config.RangeStart != "10.0.0.10" {
		t.Fatalf("normalizer was not applied: %+v", dhcp.Config)
	}

	if _, err := service.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{
		Name: "dhcp-second", NetworkId: network.ID, Family: store.VNFTypeDHCPv4,
	}); err == nil {
		t.Fatal("second DHCPv4 VNF on the same network was accepted")
	} else {
		expectVNFStatus(t, err, http.StatusConflict)
	}

	dns, err := service.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{
		Name: "dns", NetworkId: network.ID, Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("create dns: %v", err)
	}
	if !dns.Enabled || backend.activated != 1 {
		t.Fatalf("enabled DNS create did not activate: %+v", dns)
	}
	nat44, err := service.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{
		Name: "nat44", NetworkId: network.ID, Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("create nat44: %v", err)
	}
	bgp, err := service.CreateBGPVNF(ctx, &rookeryv1.CreateBGPVNFRequest{
		Name: "bgp", NetworkId: network.ID, Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("create bgp: %v", err)
	}
	if backend.activated != 3 {
		t.Fatalf("activation count = %d, want 3", backend.activated)
	}

	// The same type is allowed on another network.
	if _, err := service.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{
		Name: "dhcp-other", NetworkId: other.ID, Family: store.VNFTypeDHCPv4,
	}); err != nil {
		t.Fatalf("create dhcp on other network: %v", err)
	}

	if err := service.DeleteDHCPVNF(ctx, &rookeryv1.DeleteDHCPVNFRequest{Id: dhcp.ID}); err != nil {
		t.Fatalf("delete dhcp: %v", err)
	}
	if err := service.DeleteDNSVNF(ctx, &rookeryv1.DeleteDNSVNFRequest{Id: dns.ID}); err != nil {
		t.Fatalf("delete dns: %v", err)
	}
	if err := service.DeleteNAT44VNF(ctx, &rookeryv1.DeleteNAT44VNFRequest{Id: nat44.ID}); err != nil {
		t.Fatalf("delete nat44: %v", err)
	}
	if err := service.DeleteBGPVNF(ctx, &rookeryv1.DeleteBGPVNFRequest{Id: bgp.ID}); err != nil {
		t.Fatalf("delete bgp: %v", err)
	}
	if backend.stopped != 4 {
		t.Fatalf("stop count = %d, want 4", backend.stopped)
	}
}

// TestTypedVNFUpdateStopsAndStarts covers parent updates and wrong-type routes.
func TestTypedVNFUpdateStopsAndStarts(t *testing.T) {
	ctx := context.Background()
	st, service, backend, network, _ := newVNFService(t)
	dhcp, err := service.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{
		Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4,
	})
	if err != nil {
		t.Fatalf("create dhcp: %v", err)
	}
	updated, err := service.UpdateDHCPVNF(ctx, &rookeryv1.UpdateDHCPVNFRequest{
		Id: dhcp.ID, RangeEnd: strPtr("10.0.0.30"), Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("update dhcp: %v", err)
	}
	if !updated.Enabled || updated.Config.RangeEnd != "10.0.0.30" {
		t.Fatalf("updated vnf = %+v", updated)
	}
	if backend.activated != 1 {
		t.Fatalf("activation count = %d, want 1", backend.activated)
	}
	if _, err := st.GetDHCPVNF(ctx, dhcp.ID); err != nil {
		t.Fatalf("get dhcp: %v", err)
	}

	// Disabling stops the runtime.
	disabled, err := service.UpdateDHCPVNF(ctx, &rookeryv1.UpdateDHCPVNFRequest{
		Id: dhcp.ID, Enabled: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("disable dhcp: %v", err)
	}
	if disabled.Enabled || backend.stopped != 1 {
		t.Fatalf("disable did not stop the VNF: %+v stopped=%d", disabled, backend.stopped)
	}

	// A typed route rejects another VNF type.
	if _, err := service.UpdateDNSVNF(ctx, &rookeryv1.UpdateDNSVNFRequest{Id: dhcp.ID}); err == nil {
		t.Fatal("DNS update accepted a DHCP VNF")
	} else {
		expectVNFStatus(t, err, http.StatusNotFound)
	}
	if _, err := service.RestartBGPVNF(ctx, &rookeryv1.RestartBGPVNFRequest{Id: dhcp.ID}); err == nil {
		t.Fatal("BGP restart accepted a DHCP VNF")
	} else {
		expectVNFStatus(t, err, http.StatusNotFound)
	}
}

// TestRestartVNFStopsAndStarts covers the restart path.
func TestRestartVNFStopsAndStarts(t *testing.T) {
	ctx := context.Background()
	_, service, backend, network, _ := newVNFService(t)
	dhcp, err := service.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{
		Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4,
	})
	if err != nil {
		t.Fatalf("create dhcp: %v", err)
	}
	restarted, err := service.RestartDHCPVNF(ctx, &rookeryv1.RestartDHCPVNFRequest{Id: dhcp.ID})
	if err != nil {
		t.Fatalf("restart dhcp: %v", err)
	}
	if !restarted.Enabled || backend.stopped != 1 || backend.activated != 1 {
		t.Fatalf("restart = %+v stopped=%d activated=%d", restarted, backend.stopped, backend.activated)
	}
}

// TestDeleteDNSVNFRejectedWhileReferenced covers the DNS reference check.
func TestDeleteDNSVNFRejectedWhileReferenced(t *testing.T) {
	ctx := context.Background()
	_, service, _, network, _ := newVNFService(t)
	dns, err := service.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{
		Name: "dns", NetworkId: network.ID,
	})
	if err != nil {
		t.Fatalf("create dns: %v", err)
	}
	if _, err := service.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{
		Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4, DnsVnfId: dns.ID, DnsDomain: "lab.test",
	}); err != nil {
		t.Fatalf("create dhcp: %v", err)
	}
	if err := service.DeleteDNSVNF(ctx, &rookeryv1.DeleteDNSVNFRequest{Id: dns.ID}); err == nil {
		t.Fatal("referenced DNS VNF was deleted")
	} else {
		expectVNFStatus(t, err, http.StatusConflict)
	}
}

// boolPtr and strPtr are small helpers for proto optional fields.
func boolPtr(value bool) *bool    { return &value }
func strPtr(value string) *string { return &value }
