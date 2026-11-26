package lifecycle

import (
	"context"
	"net/http"
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// createEnabledDHCP creates an enabled DHCPv4 VNF through the lifecycle service.
func createEnabledDHCP(t *testing.T, service *Service, networkID int64, name string) store.VNF {
	t.Helper()
	vnf, err := service.CreateDHCPVNF(context.Background(), &rookeryv1.CreateDHCPVNFRequest{
		Name: name, NetworkId: networkID, Family: store.VNFTypeDHCPv4, Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("create enabled dhcp: %v", err)
	}
	return vnf
}

// TestChildWritesReloadRunningRuntime covers create, update, and delete child
// writes against an enabled parent: each one reloads the runtime.
func TestChildWritesReloadRunningRuntime(t *testing.T) {
	ctx := context.Background()
	st, service, backend, network, _ := newVNFService(t)
	vnf := createEnabledDHCP(t, service, network.ID, "dhcp")
	if backend.activated != 1 {
		t.Fatalf("activation count = %d, want 1", backend.activated)
	}

	created, err := service.CreateDHCPStaticAssignment(ctx, &rookeryv1.CreateDHCPStaticAssignmentRequest{
		VnfId: vnf.ID, Mac: "02:00:00:00:00:01", Ip: "10.0.0.50",
	})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if created.ID == 0 || backend.reloaded != 1 {
		t.Fatalf("create = %+v reloaded=%d", created, backend.reloaded)
	}
	if _, err := service.CreateDHCPOption(ctx, &rookeryv1.CreateDHCPOptionRequest{
		VnfId: vnf.ID, Code: 15, Format: "string", Value: "lab.test",
	}); err != nil {
		t.Fatalf("create option: %v", err)
	}
	if backend.reloaded != 2 {
		t.Fatalf("reload count = %d, want 2", backend.reloaded)
	}
	if _, err := service.UpdateDHCPStaticAssignment(ctx, &rookeryv1.UpdateDHCPStaticAssignmentRequest{
		VnfId: vnf.ID, AssignmentId: created.ID, Mac: "02:00:00:00:00:02", Ip: "10.0.0.50",
	}); err != nil {
		t.Fatalf("update assignment: %v", err)
	}
	if backend.reloaded != 3 {
		t.Fatalf("reload count = %d, want 3", backend.reloaded)
	}
	if err := service.DeleteDHCPStaticAssignment(ctx, &rookeryv1.DeleteDHCPStaticAssignmentRequest{
		VnfId: vnf.ID, AssignmentId: created.ID,
	}); err != nil {
		t.Fatalf("delete assignment: %v", err)
	}
	if backend.reloaded != 4 {
		t.Fatalf("reload count = %d, want 4", backend.reloaded)
	}
	items, err := st.ListDHCPStaticAssignments(ctx, vnf.ID)
	if err != nil {
		t.Fatalf("list assignments: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("assignments after delete = %+v", items)
	}
}

// TestChildWritesOnDisabledParentDoNotReload covers the disabled-parent path.
func TestChildWritesOnDisabledParentDoNotReload(t *testing.T) {
	ctx := context.Background()
	_, service, backend, network, _ := newVNFService(t)
	vnf, err := service.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{
		Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4,
	})
	if err != nil {
		t.Fatalf("create dhcp: %v", err)
	}
	if _, err := service.CreateDHCPOption(ctx, &rookeryv1.CreateDHCPOptionRequest{
		VnfId: vnf.ID, Code: 15, Format: "string", Value: "lab.test",
	}); err != nil {
		t.Fatalf("create option: %v", err)
	}
	if backend.reloaded != 0 {
		t.Fatalf("disabled parent reloaded %d times", backend.reloaded)
	}
}

// TestChildReloadFailureRestoresPreviousRow proves a failed reload restores the
// row and reloads the previous configuration.
func TestChildReloadFailureRestoresPreviousRow(t *testing.T) {
	ctx := context.Background()
	st, service, backend, network, _ := newVNFService(t)
	vnf := createEnabledDHCP(t, service, network.ID, "dhcp")
	created, err := service.CreateDHCPStaticAssignment(ctx, &rookeryv1.CreateDHCPStaticAssignmentRequest{
		VnfId: vnf.ID, Mac: "02:00:00:00:00:01", Ip: "10.0.0.50",
	})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	backend.reloadFailures = 1
	_, err = service.UpdateDHCPStaticAssignment(ctx, &rookeryv1.UpdateDHCPStaticAssignmentRequest{
		VnfId: vnf.ID, AssignmentId: created.ID, Mac: "02:00:00:00:00:02", Ip: "10.0.0.50",
	})
	if err == nil {
		t.Fatal("update succeeded despite a reload failure")
	}
	expectVNFStatus(t, err, http.StatusInternalServerError)
	if backend.reloaded != 3 {
		t.Fatalf("reload count = %d, want 3 (create, failed update, restore)", backend.reloaded)
	}
	restored, err := st.GetDHCPStaticAssignment(ctx, vnf.ID, created.ID)
	if err != nil {
		t.Fatalf("get restored assignment: %v", err)
	}
	if restored.MAC != "02:00:00:00:00:01" {
		t.Fatalf("restored assignment = %+v", restored)
	}
	parent, err := st.GetDHCPVNF(ctx, vnf.ID)
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if !parent.Enabled {
		t.Fatal("a recoverable reload failure disabled the parent")
	}
}

// TestChildRestoreFailureDisablesParent proves that when the restore reload also
// fails, the parent is disabled.
func TestChildRestoreFailureDisablesParent(t *testing.T) {
	ctx := context.Background()
	st, service, backend, network, _ := newVNFService(t)
	vnf := createEnabledDHCP(t, service, network.ID, "dhcp")
	backend.reloadFailures = 2
	_, err := service.CreateDHCPOption(ctx, &rookeryv1.CreateDHCPOptionRequest{
		VnfId: vnf.ID, Code: 15, Format: "string", Value: "lab.test",
	})
	if err == nil {
		t.Fatal("create succeeded despite two reload failures")
	}
	expectVNFStatus(t, err, http.StatusInternalServerError)
	options, listErr := st.ListDHCPOptions(ctx, vnf.ID)
	if listErr != nil {
		t.Fatalf("list options: %v", listErr)
	}
	if len(options) != 0 {
		t.Fatalf("failed child write was not rolled back: %+v", options)
	}
	parent, getErr := st.GetDHCPVNF(ctx, vnf.ID)
	if getErr != nil {
		t.Fatalf("get parent: %v", getErr)
	}
	if parent.Enabled {
		t.Fatal("parent stayed enabled after its restore reload failed")
	}
}

// TestDNATChildWritesApplyMappings covers the NAT44 path: no daemon reload, but
// the VPP mappings are replaced.
func TestDNATChildWritesApplyMappings(t *testing.T) {
	ctx := context.Background()
	_, service, backend, network, _ := newVNFService(t)
	nat44, err := service.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{
		Name: "nat44", NetworkId: network.ID, Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("create nat44: %v", err)
	}
	rule, err := service.CreateNAT44DNATRule(ctx, &rookeryv1.CreateNAT44DNATRuleRequest{
		VnfId: nat44.ID, Protocol: "tcp", ExternalPort: 80, LocalIp: "10.0.0.10", LocalPort: 8080,
	})
	if err != nil {
		t.Fatalf("create dnat rule: %v", err)
	}
	if backend.natRules != 1 || backend.reloaded != 0 {
		t.Fatalf("natRules=%d reloaded=%d", backend.natRules, backend.reloaded)
	}
	if _, err := service.UpdateNAT44DNATRule(ctx, &rookeryv1.UpdateNAT44DNATRuleRequest{
		VnfId: nat44.ID, RuleId: rule.ID, Protocol: "tcp", ExternalPort: 80,
		LocalIp: "10.0.0.11", LocalPort: 8081,
	}); err != nil {
		t.Fatalf("update dnat rule: %v", err)
	}
	if err := service.DeleteNAT44DNATRule(ctx, &rookeryv1.DeleteNAT44DNATRuleRequest{
		VnfId: nat44.ID, RuleId: rule.ID,
	}); err != nil {
		t.Fatalf("delete dnat rule: %v", err)
	}
	if backend.natRules != 3 {
		t.Fatalf("natRules = %d, want 3", backend.natRules)
	}
}

// TestDNSAndBGPChildWrites covers the DNS record/forwarder and BGP prefix
// reload paths.
func TestDNSAndBGPChildWrites(t *testing.T) {
	ctx := context.Background()
	_, service, backend, network, _ := newVNFService(t)
	dns, err := service.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{
		Name: "dns", NetworkId: network.ID, Enabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("create dns: %v", err)
	}
	record, err := service.CreateDNSVNFRecord(ctx, &rookeryv1.CreateDNSVNFRecordRequest{
		VnfId: dns.ID, Name: "host.lab.test", Type: "A", Value: "10.0.0.50", Ttl: 300,
	})
	if err != nil {
		t.Fatalf("create dns record: %v", err)
	}
	forwarder, err := service.CreateDNSVNFForwarder(ctx, &rookeryv1.CreateDNSVNFForwarderRequest{
		VnfId: dns.ID, Address: "1.1.1.1", Position: 0,
	})
	if err != nil {
		t.Fatalf("create dns forwarder: %v", err)
	}
	if _, err := service.UpdateDNSVNFRecord(ctx, &rookeryv1.UpdateDNSVNFRecordRequest{
		VnfId: dns.ID, RecordId: record.ID, Name: "host.lab.test", Type: "A", Value: "10.0.0.51", Ttl: 300,
	}); err != nil {
		t.Fatalf("update dns record: %v", err)
	}
	if err := service.DeleteDNSVNFForwarder(ctx, &rookeryv1.DeleteDNSVNFForwarderRequest{
		VnfId: dns.ID, ForwarderId: forwarder.ID,
	}); err != nil {
		t.Fatalf("delete dns forwarder: %v", err)
	}

	bgp, err := service.CreateBGPVNF(ctx, &rookeryv1.CreateBGPVNFRequest{
		Name: "bgp", NetworkId: network.ID, Enabled: boolPtr(true),
		Transport: "tap", LocalIp: "10.0.0.1/24",
	})
	if err != nil {
		t.Fatalf("create bgp: %v", err)
	}
	prefix, err := service.CreateBGPVNFAdvertisedPrefix(ctx, &rookeryv1.CreateBGPVNFAdvertisedPrefixRequest{
		VnfId: bgp.ID, Prefix: "10.50.0.0/24",
	})
	if err != nil {
		t.Fatalf("create advertised prefix: %v", err)
	}
	if err := service.DeleteBGPVNFAdvertisedPrefix(ctx, &rookeryv1.DeleteBGPVNFAdvertisedPrefixRequest{
		VnfId: bgp.ID, PrefixId: prefix.ID,
	}); err != nil {
		t.Fatalf("delete advertised prefix: %v", err)
	}
	if backend.reloaded != 6 {
		t.Fatalf("reload count = %d, want 6", backend.reloaded)
	}
}

// TestChildWriteWrongParentType covers the typed child routes.
func TestChildWriteWrongParentType(t *testing.T) {
	ctx := context.Background()
	_, service, _, network, _ := newVNFService(t)
	dns, err := service.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{Name: "dns", NetworkId: network.ID})
	if err != nil {
		t.Fatalf("create dns: %v", err)
	}
	_, err = service.CreateDHCPStaticAssignment(ctx, &rookeryv1.CreateDHCPStaticAssignmentRequest{
		VnfId: dns.ID, Mac: "02:00:00:00:00:01", Ip: "10.0.0.50",
	})
	expectVNFStatus(t, err, http.StatusNotFound)

	dhcp, err := service.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{
		Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4,
	})
	if err != nil {
		t.Fatalf("create dhcp: %v", err)
	}
	_, err = service.CreateDNSVNFRecord(ctx, &rookeryv1.CreateDNSVNFRecordRequest{
		VnfId: dhcp.ID, Name: "host.lab.test", Type: "A", Value: "10.0.0.50",
	})
	expectVNFStatus(t, err, http.StatusNotFound)
}
