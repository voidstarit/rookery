package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// createNamedDNS creates a DNS VNF with the given name on networkID.
func createNamedDNS(t *testing.T, service *Service, networkID int64, name string, enabled bool) store.VNF {
	t.Helper()
	vnf, err := service.CreateDNSVNF(context.Background(), &rookeryv1.CreateDNSVNFRequest{
		Name: name, NetworkId: networkID, Enabled: boolPtr(enabled),
	})
	if err != nil {
		t.Fatalf("create DNS VNF %q: %v", name, err)
	}
	return vnf
}

// TestParentUpdateRestoresRuntimeAfterStoreFailure proves a rejected rename does
// not leave the previous runtime stopped while the row still says enabled.
func TestParentUpdateRestoresRuntimeAfterStoreFailure(t *testing.T) {
	ctx := context.Background()
	st, service, backend, network, other := newVNFService(t)
	first := createNamedDNS(t, service, network.ID, "first", true)
	createNamedDNS(t, service, other.ID, "second", false)
	activatedBefore := backend.activated
	stoppedBefore := backend.stopped

	_, err := service.UpdateDNSVNF(ctx, &rookeryv1.UpdateDNSVNFRequest{
		Id: first.ID, Name: strPtr("second"),
	})
	if err == nil {
		t.Fatal("duplicate-name update succeeded")
	}
	expectVNFStatus(t, err, http.StatusConflict)
	if backend.stopped != stoppedBefore+1 || backend.activated != activatedBefore+1 {
		t.Fatalf("stopped=%d activated=%d, want one stop and one restore", backend.stopped-stoppedBefore, backend.activated-activatedBefore)
	}
	saved, err := st.GetDNSVNF(ctx, first.ID)
	if err != nil {
		t.Fatalf("read the VNF after the rejected update: %v", err)
	}
	if saved.Name != "first" || !saved.Enabled {
		t.Fatalf("rejected update changed the VNF: %+v", saved)
	}
}

// TestParentUpdateRestoreFailureNamesBothErrors proves a failed restore is
// surfaced instead of being hidden behind the generic 500 message.
func TestParentUpdateRestoreFailureNamesBothErrors(t *testing.T) {
	ctx := context.Background()
	_, service, backend, network, other := newVNFService(t)
	first := createNamedDNS(t, service, network.ID, "first", true)
	createNamedDNS(t, service, other.ID, "second", false)
	backend.activateErr = errors.New("runtime restore failed")

	_, err := service.UpdateDNSVNF(ctx, &rookeryv1.UpdateDNSVNFRequest{
		Id: first.ID, Name: strPtr("second"),
	})
	if err == nil {
		t.Fatal("duplicate-name update succeeded")
	}
	var vnfErr *VNFError
	if !errors.As(err, &vnfErr) {
		t.Fatalf("error = %v, want *VNFError", err)
	}
	status, message := vnfErr.VNFStatus()
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d", status)
	}
	for _, want := range []string{"could not save", "could not restore"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message = %q, want %q", message, want)
		}
	}
}

// TestChildWriteOnDegradedParentKeepsRow proves an enabled but stopped runtime
// does not turn a child write into a 500 or disable the parent.
func TestChildWriteOnDegradedParentKeepsRow(t *testing.T) {
	ctx := context.Background()
	st, service, backend, network, _ := newVNFService(t)
	vnf := createEnabledDHCP(t, service, network.ID, "dhcp")
	backend.notRunning = map[int64]bool{vnf.ID: true}
	backend.reloadErr = errors.New("daemon unavailable")
	backend.reloadFailures = 0

	created, err := service.CreateDHCPStaticAssignment(ctx, &rookeryv1.CreateDHCPStaticAssignmentRequest{
		VnfId: vnf.ID, Mac: "02:00:00:00:00:01", Ip: "10.0.0.50",
	})
	if err != nil {
		t.Fatalf("child write on a degraded parent: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("child row was not created")
	}
	saved, err := st.GetDHCPVNF(ctx, vnf.ID)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if !saved.Enabled {
		t.Fatal("degraded parent was disabled by a child write")
	}
	items, err := st.ListDHCPStaticAssignments(ctx, vnf.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("child rows = %+v, err = %v", items, err)
	}
}

// TestNAT44ChildWriteOnDegradedParentKeepsRow covers the NAT44 mapping path.
func TestNAT44ChildWriteOnDegradedParentKeepsRow(t *testing.T) {
	ctx := context.Background()
	st, service, backend, network, _ := newVNFService(t)
	nat44, err := service.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{
		Name: "nat44", NetworkId: network.ID, Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("create NAT44: %v", err)
	}
	backend.notRunning = map[int64]bool{nat44.ID: true}
	rule, err := service.CreateNAT44DNATRule(ctx, &rookeryv1.CreateNAT44DNATRuleRequest{
		VnfId: nat44.ID, Protocol: "tcp", ExternalPort: 80, LocalIp: "10.0.0.10", LocalPort: 8080,
	})
	if err != nil {
		t.Fatalf("DNAT write on a degraded NAT44: %v", err)
	}
	if backend.natRules != 0 {
		t.Fatalf("degraded NAT44 applied %d mapping sets", backend.natRules)
	}
	if rules, err := st.ListNAT44DNATRules(ctx, nat44.ID); err != nil || len(rules) != 1 || rules[0].ID != rule.ID {
		t.Fatalf("DNAT rows = %+v, err = %v", rules, err)
	}
}

// TestChildRestoreFailureMessageNamesFailures proves the double-failure path
// returns a message naming the reload and restore failures.
func TestChildRestoreFailureMessageNamesFailures(t *testing.T) {
	ctx := context.Background()
	_, service, backend, network, _ := newVNFService(t)
	vnf := createEnabledDHCP(t, service, network.ID, "dhcp")
	backend.reloadFailures = 2
	_, err := service.CreateDHCPOption(ctx, &rookeryv1.CreateDHCPOptionRequest{
		VnfId: vnf.ID, Code: 15, Format: "string", Value: "lab.test",
	})
	if err == nil {
		t.Fatal("child write succeeded despite two reload failures")
	}
	var vnfErr *VNFError
	if !errors.As(err, &vnfErr) {
		t.Fatalf("error = %v, want *VNFError", err)
	}
	status, message := vnfErr.VNFStatus()
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d", status)
	}
	for _, want := range []string{"could not reload", "could not reload the previous configuration", "disabled"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message = %q, want %q", message, want)
		}
	}
}
