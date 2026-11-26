package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// lifecycleChildKind describes one typed child route for the shared matrices.
type lifecycleChildKind struct {
	name        string
	parentKind  string
	wrongParent string
	create      func(t *testing.T, service *Service, parentID int64) int64
	duplicate   func(t *testing.T, service *Service, parentID int64) error
	update      func(service *Service, parentID, childID int64) error
	remove      func(service *Service, parentID, childID int64) error
	list        func(st *store.Store, parentID int64) (int, error)
}

// lifecycleChildKinds returns every typed child route.
func lifecycleChildKinds() []lifecycleChildKind {
	ctx := context.Background()
	return []lifecycleChildKind{
		{
			name: "dhcpv4 assignment", parentKind: store.VNFTypeDHCPv4, wrongParent: store.VNFTypeDNS,
			create: func(t *testing.T, service *Service, parentID int64) int64 {
				item, err := service.CreateDHCPStaticAssignment(ctx, &rookeryv1.CreateDHCPStaticAssignmentRequest{
					VnfId: parentID, Mac: "02:00:00:00:00:01", Ip: "10.0.0.50",
				})
				if err != nil {
					t.Fatalf("create assignment: %v", err)
				}
				return item.ID
			},
			duplicate: func(t *testing.T, service *Service, parentID int64) error {
				_, err := service.CreateDHCPStaticAssignment(ctx, &rookeryv1.CreateDHCPStaticAssignmentRequest{
					VnfId: parentID, Mac: "02:00:00:00:00:01", Ip: "10.0.0.51",
				})
				return err
			},
			update: func(service *Service, parentID, childID int64) error {
				_, err := service.UpdateDHCPStaticAssignment(ctx, &rookeryv1.UpdateDHCPStaticAssignmentRequest{
					VnfId: parentID, AssignmentId: childID, Mac: "02:00:00:00:00:02", Ip: "10.0.0.60",
				})
				return err
			},
			remove: func(service *Service, parentID, childID int64) error {
				return service.DeleteDHCPStaticAssignment(ctx, &rookeryv1.DeleteDHCPStaticAssignmentRequest{
					VnfId: parentID, AssignmentId: childID,
				})
			},
			list: func(st *store.Store, parentID int64) (int, error) {
				items, err := st.ListDHCPStaticAssignments(ctx, parentID)
				return len(items), err
			},
		},
		{
			name: "dhcpv4 option", parentKind: store.VNFTypeDHCPv4, wrongParent: store.VNFTypeDNS,
			create: func(t *testing.T, service *Service, parentID int64) int64 {
				item, err := service.CreateDHCPOption(ctx, &rookeryv1.CreateDHCPOptionRequest{
					VnfId: parentID, Code: 15, Format: "string", Value: "lab.test",
				})
				if err != nil {
					t.Fatalf("create option: %v", err)
				}
				return item.ID
			},
			duplicate: func(t *testing.T, service *Service, parentID int64) error {
				_, err := service.CreateDHCPOption(ctx, &rookeryv1.CreateDHCPOptionRequest{
					VnfId: parentID, Code: 15, Format: "string", Value: "other.test",
				})
				return err
			},
			update: func(service *Service, parentID, childID int64) error {
				_, err := service.UpdateDHCPOption(ctx, &rookeryv1.UpdateDHCPOptionRequest{
					VnfId: parentID, OptionId: childID, Code: 15, Format: "string", Value: "updated.test",
				})
				return err
			},
			remove: func(service *Service, parentID, childID int64) error {
				return service.DeleteDHCPOption(ctx, &rookeryv1.DeleteDHCPOptionRequest{
					VnfId: parentID, OptionId: childID,
				})
			},
			list: func(st *store.Store, parentID int64) (int, error) {
				items, err := st.ListDHCPOptions(ctx, parentID)
				return len(items), err
			},
		},
		{
			name: "dhcpv6 option", parentKind: store.VNFTypeDHCPv6, wrongParent: store.VNFTypeDNS,
			create: func(t *testing.T, service *Service, parentID int64) int64 {
				item, err := service.CreateDHCPOption(ctx, &rookeryv1.CreateDHCPOptionRequest{
					VnfId: parentID, Code: 23, Format: "ipv6", Value: "fd00::2",
				})
				if err != nil {
					t.Fatalf("create option: %v", err)
				}
				return item.ID
			},
			duplicate: func(t *testing.T, service *Service, parentID int64) error {
				_, err := service.CreateDHCPOption(ctx, &rookeryv1.CreateDHCPOptionRequest{
					VnfId: parentID, Code: 23, Format: "ipv6", Value: "fd00::3",
				})
				return err
			},
			update: func(service *Service, parentID, childID int64) error {
				_, err := service.UpdateDHCPOption(ctx, &rookeryv1.UpdateDHCPOptionRequest{
					VnfId: parentID, OptionId: childID, Code: 23, Format: "ipv6", Value: "fd00::4",
				})
				return err
			},
			remove: func(service *Service, parentID, childID int64) error {
				return service.DeleteDHCPOption(ctx, &rookeryv1.DeleteDHCPOptionRequest{
					VnfId: parentID, OptionId: childID,
				})
			},
			list: func(st *store.Store, parentID int64) (int, error) {
				items, err := st.ListDHCPOptions(ctx, parentID)
				return len(items), err
			},
		},
		{
			name: "dns record", parentKind: store.VNFTypeDNS, wrongParent: store.VNFTypeDHCPv4,
			create: func(t *testing.T, service *Service, parentID int64) int64 {
				item, err := service.CreateDNSVNFRecord(ctx, &rookeryv1.CreateDNSVNFRecordRequest{
					VnfId: parentID, Name: "host.lab.test", Type: "A", Value: "10.0.0.50", Ttl: 300,
				})
				if err != nil {
					t.Fatalf("create dns record: %v", err)
				}
				return item.ID
			},
			duplicate: func(t *testing.T, service *Service, parentID int64) error {
				_, err := service.CreateDNSVNFRecord(ctx, &rookeryv1.CreateDNSVNFRecordRequest{
					VnfId: parentID, Name: "host.lab.test", Type: "A", Value: "10.0.0.50", Ttl: 300,
				})
				return err
			},
			update: func(service *Service, parentID, childID int64) error {
				_, err := service.UpdateDNSVNFRecord(ctx, &rookeryv1.UpdateDNSVNFRecordRequest{
					VnfId: parentID, RecordId: childID, Name: "host.lab.test", Type: "A",
					Value: "10.0.0.51", Ttl: 600,
				})
				return err
			},
			remove: func(service *Service, parentID, childID int64) error {
				return service.DeleteDNSVNFRecord(ctx, &rookeryv1.DeleteDNSVNFRecordRequest{
					VnfId: parentID, RecordId: childID,
				})
			},
			list: func(st *store.Store, parentID int64) (int, error) {
				items, err := st.ListDNSVNFRecords(ctx, parentID)
				return len(items), err
			},
		},
		{
			name: "dns forwarder", parentKind: store.VNFTypeDNS, wrongParent: store.VNFTypeNAT44,
			create: func(t *testing.T, service *Service, parentID int64) int64 {
				item, err := service.CreateDNSVNFForwarder(ctx, &rookeryv1.CreateDNSVNFForwarderRequest{
					VnfId: parentID, Address: "1.1.1.1", Position: 0,
				})
				if err != nil {
					t.Fatalf("create forwarder: %v", err)
				}
				return item.ID
			},
			duplicate: func(t *testing.T, service *Service, parentID int64) error {
				_, err := service.CreateDNSVNFForwarder(ctx, &rookeryv1.CreateDNSVNFForwarderRequest{
					VnfId: parentID, Address: "1.1.1.1", Position: 1,
				})
				return err
			},
			update: func(service *Service, parentID, childID int64) error {
				_, err := service.UpdateDNSVNFForwarder(ctx, &rookeryv1.UpdateDNSVNFForwarderRequest{
					VnfId: parentID, ForwarderId: childID, Address: "9.9.9.9", Position: 2,
				})
				return err
			},
			remove: func(service *Service, parentID, childID int64) error {
				return service.DeleteDNSVNFForwarder(ctx, &rookeryv1.DeleteDNSVNFForwarderRequest{
					VnfId: parentID, ForwarderId: childID,
				})
			},
			list: func(st *store.Store, parentID int64) (int, error) {
				items, err := st.ListDNSVNFForwarders(ctx, parentID)
				return len(items), err
			},
		},
		{
			name: "nat44 dnat rule", parentKind: store.VNFTypeNAT44, wrongParent: store.VNFTypeBGP,
			create: func(t *testing.T, service *Service, parentID int64) int64 {
				item, err := service.CreateNAT44DNATRule(ctx, &rookeryv1.CreateNAT44DNATRuleRequest{
					VnfId: parentID, Protocol: "tcp", ExternalPort: 80, LocalIp: "10.0.0.10", LocalPort: 8080,
				})
				if err != nil {
					t.Fatalf("create dnat rule: %v", err)
				}
				return item.ID
			},
			duplicate: func(t *testing.T, service *Service, parentID int64) error {
				_, err := service.CreateNAT44DNATRule(ctx, &rookeryv1.CreateNAT44DNATRuleRequest{
					VnfId: parentID, Protocol: "tcp", ExternalPort: 80, LocalIp: "10.0.0.11", LocalPort: 8081,
				})
				return err
			},
			update: func(service *Service, parentID, childID int64) error {
				_, err := service.UpdateNAT44DNATRule(ctx, &rookeryv1.UpdateNAT44DNATRuleRequest{
					VnfId: parentID, RuleId: childID, Protocol: "udp", ExternalPort: 53,
					LocalIp: "10.0.0.11", LocalPort: 5353,
				})
				return err
			},
			remove: func(service *Service, parentID, childID int64) error {
				return service.DeleteNAT44DNATRule(ctx, &rookeryv1.DeleteNAT44DNATRuleRequest{
					VnfId: parentID, RuleId: childID,
				})
			},
			list: func(st *store.Store, parentID int64) (int, error) {
				items, err := st.ListNAT44DNATRules(ctx, parentID)
				return len(items), err
			},
		},
		{
			name: "bgp advertised prefix", parentKind: store.VNFTypeBGP, wrongParent: store.VNFTypeNAT44,
			create: func(t *testing.T, service *Service, parentID int64) int64 {
				item, err := service.CreateBGPVNFAdvertisedPrefix(ctx, &rookeryv1.CreateBGPVNFAdvertisedPrefixRequest{
					VnfId: parentID, Prefix: "10.50.0.0/24",
				})
				if err != nil {
					t.Fatalf("create advertised prefix: %v", err)
				}
				return item.ID
			},
			duplicate: func(t *testing.T, service *Service, parentID int64) error {
				_, err := service.CreateBGPVNFAdvertisedPrefix(ctx, &rookeryv1.CreateBGPVNFAdvertisedPrefixRequest{
					VnfId: parentID, Prefix: "10.50.0.0/24",
				})
				return err
			},
			update: func(service *Service, parentID, childID int64) error {
				_, err := service.UpdateBGPVNFAdvertisedPrefix(ctx, &rookeryv1.UpdateBGPVNFAdvertisedPrefixRequest{
					VnfId: parentID, PrefixId: childID, Prefix: "10.60.0.0/24",
				})
				return err
			},
			remove: func(service *Service, parentID, childID int64) error {
				return service.DeleteBGPVNFAdvertisedPrefix(ctx, &rookeryv1.DeleteBGPVNFAdvertisedPrefixRequest{
					VnfId: parentID, PrefixId: childID,
				})
			},
			list: func(st *store.Store, parentID int64) (int, error) {
				items, err := st.ListBGPVNFAdvertisedPrefixes(ctx, parentID)
				return len(items), err
			},
		},
	}
}

// TestChildUpdateAndDeleteAllKinds exercises update and delete for every child
// route, including the reload on an enabled parent.
func TestChildUpdateAndDeleteAllKinds(t *testing.T) {
	for _, kind := range lifecycleChildKinds() {
		t.Run(kind.name, func(t *testing.T) {
			st, service, backend, network, _ := newVNFService(t)
			parent := createLifecycleVNF(t, service, network.ID, kind.parentKind, "parent", true)
			childID := kind.create(t, service, parent.ID)
			if err := kind.update(service, parent.ID, childID); err != nil {
				t.Fatalf("update child: %v", err)
			}
			if count, err := kind.list(st, parent.ID); err != nil || count != 1 {
				t.Fatalf("list after update = %d, err = %v", count, err)
			}
			if err := kind.remove(service, parent.ID, childID); err != nil {
				t.Fatalf("delete child: %v", err)
			}
			if count, err := kind.list(st, parent.ID); err != nil || count != 0 {
				t.Fatalf("list after delete = %d, err = %v", count, err)
			}
			if kind.parentKind == store.VNFTypeNAT44 {
				if backend.natRules != 3 {
					t.Fatalf("mapping calls = %d, want 3 (create, update, delete)", backend.natRules)
				}
			} else if backend.reloaded != 3 {
				t.Fatalf("reload count = %d, want 3 (create, update, delete)", backend.reloaded)
			}
		})
	}
}

// TestChildWritesDisabledParentAllKinds proves a disabled parent never reloads.
func TestChildWritesDisabledParentAllKinds(t *testing.T) {
	for _, kind := range lifecycleChildKinds() {
		t.Run(kind.name, func(t *testing.T) {
			st, service, backend, network, _ := newVNFService(t)
			parent := createLifecycleVNF(t, service, network.ID, kind.parentKind, "parent", false)
			kind.create(t, service, parent.ID)
			if count, err := kind.list(st, parent.ID); err != nil || count != 1 {
				t.Fatalf("list = %d, err = %v", count, err)
			}
			if backend.reloaded != 0 {
				t.Fatalf("disabled parent reloaded %d times", backend.reloaded)
			}
			if backend.natRules != 0 {
				t.Fatalf("disabled NAT44 parent replaced %d mapping sets", backend.natRules)
			}
		})
	}
}

// TestChildWritesMissingAndWrongParent covers the not-found branches of every
// child route.
func TestChildWritesMissingAndWrongParent(t *testing.T) {
	for _, kind := range lifecycleChildKinds() {
		t.Run(kind.name, func(t *testing.T) {
			st, service, backend, network, _ := newVNFService(t)
			parent := createLifecycleVNF(t, service, network.ID, kind.parentKind, "parent", false)
			childID := kind.create(t, service, parent.ID)

			if err := kind.update(service, parent.ID, childID+1000); err == nil {
				t.Fatal("update of a missing child succeeded")
			} else {
				expectVNFStatus(t, err, http.StatusNotFound)
			}
			if err := kind.remove(service, parent.ID, childID+1000); err == nil {
				t.Fatal("delete of a missing child succeeded")
			} else {
				expectVNFStatus(t, err, http.StatusNotFound)
			}

			wrong := createLifecycleVNF(t, service, network.ID, kind.wrongParent, "wrong", false)
			backend.reloaded = 0
			if err := kind.update(service, wrong.ID, childID); err == nil {
				t.Fatal("update accepted a wrong parent type")
			} else {
				expectVNFStatus(t, err, http.StatusNotFound)
			}
			if err := kind.remove(service, wrong.ID, childID); err == nil {
				t.Fatal("delete accepted a wrong parent type")
			} else {
				expectVNFStatus(t, err, http.StatusNotFound)
			}
			if err := kind.duplicate(t, service, wrong.ID); err == nil {
				t.Fatal("create accepted a wrong parent type")
			} else {
				expectVNFStatus(t, err, http.StatusNotFound)
			}
			_ = st
		})
	}
}

// TestChildWriteNormalizerErrors maps a backend validation failure to 400.
func TestChildWriteNormalizerErrors(t *testing.T) {
	for _, kind := range lifecycleChildKinds() {
		t.Run(kind.name+"/create", func(t *testing.T) {
			_, service, backend, network, _ := newVNFService(t)
			parent := createLifecycleVNF(t, service, network.ID, kind.parentKind, "parent", false)
			backend.childNormalizeErr = errors.New("invalid child")
			if _, err := createChildForKind(t, kind, service, parent.ID); err == nil {
				t.Fatal("create succeeded despite a validation failure")
			} else {
				expectVNFStatus(t, err, http.StatusBadRequest)
			}
		})
		t.Run(kind.name+"/update", func(t *testing.T) {
			_, service, backend, network, _ := newVNFService(t)
			parent := createLifecycleVNF(t, service, network.ID, kind.parentKind, "parent", false)
			childID := kind.create(t, service, parent.ID)
			backend.childNormalizeErr = errors.New("invalid child")
			if err := kind.update(service, parent.ID, childID); err == nil {
				t.Fatal("update succeeded despite a validation failure")
			} else {
				expectVNFStatus(t, err, http.StatusBadRequest)
			}
		})
	}
}

// createChildForKind returns the create error for a kind without failing the test.
func createChildForKind(t *testing.T, kind lifecycleChildKind, service *Service, parentID int64) (int64, error) {
	t.Helper()
	switch kind.name {
	case "dhcpv4 assignment":
		_, err := service.CreateDHCPStaticAssignment(context.Background(), &rookeryv1.CreateDHCPStaticAssignmentRequest{
			VnfId: parentID, Mac: "02:00:00:00:00:09", Ip: "10.0.0.99",
		})
		return 0, err
	case "dhcpv4 option", "dhcpv6 option":
		_, err := service.CreateDHCPOption(context.Background(), &rookeryv1.CreateDHCPOptionRequest{
			VnfId: parentID, Code: 200, Format: "string", Value: "x",
		})
		return 0, err
	case "dns record":
		_, err := service.CreateDNSVNFRecord(context.Background(), &rookeryv1.CreateDNSVNFRecordRequest{
			VnfId: parentID, Name: "new.lab.test", Type: "A", Value: "10.0.0.99",
		})
		return 0, err
	case "dns forwarder":
		_, err := service.CreateDNSVNFForwarder(context.Background(), &rookeryv1.CreateDNSVNFForwarderRequest{
			VnfId: parentID, Address: "8.8.8.8",
		})
		return 0, err
	case "nat44 dnat rule":
		_, err := service.CreateNAT44DNATRule(context.Background(), &rookeryv1.CreateNAT44DNATRuleRequest{
			VnfId: parentID, Protocol: "tcp", ExternalPort: 8080, LocalIp: "10.0.0.99", LocalPort: 8080,
		})
		return 0, err
	case "bgp advertised prefix":
		_, err := service.CreateBGPVNFAdvertisedPrefix(context.Background(), &rookeryv1.CreateBGPVNFAdvertisedPrefixRequest{
			VnfId: parentID, Prefix: "10.99.0.0/24",
		})
		return 0, err
	default:
		panic("unknown child kind " + kind.name)
	}
}

// TestChildWriteDuplicateConflicts maps a duplicate unique key to 409.
func TestChildWriteDuplicateConflicts(t *testing.T) {
	for _, kind := range lifecycleChildKinds() {
		t.Run(kind.name, func(t *testing.T) {
			_, service, _, network, _ := newVNFService(t)
			parent := createLifecycleVNF(t, service, network.ID, kind.parentKind, "parent", false)
			kind.create(t, service, parent.ID)
			if err := kind.duplicate(t, service, parent.ID); err == nil {
				t.Fatal("duplicate child was accepted")
			} else {
				expectVNFStatus(t, err, http.StatusConflict)
			}
		})
	}
}

// TestChildUpdateReloadFailureRestoresRow proves an update whose reload fails is
// rolled back and the previous row is restored.
func TestChildUpdateReloadFailureRestoresRow(t *testing.T) {
	for _, kind := range lifecycleChildKinds() {
		if kind.name == "nat44 dnat rule" {
			continue // covered through the mapping path below
		}
		t.Run(kind.name, func(t *testing.T) {
			st, service, backend, network, _ := newVNFService(t)
			parent := createLifecycleVNF(t, service, network.ID, kind.parentKind, "parent", true)
			childID := kind.create(t, service, parent.ID)
			backend.reloadFailures = 1
			if err := kind.update(service, parent.ID, childID); err == nil {
				t.Fatal("update succeeded despite a reload failure")
			} else {
				expectVNFStatus(t, err, http.StatusInternalServerError)
			}
			if count, err := kind.list(st, parent.ID); err != nil || count != 1 {
				t.Fatalf("row was not restored: count=%d err=%v", count, err)
			}
			saved, err := st.GetVNF(context.Background(), parent.ID)
			if err != nil || !saved.Enabled {
				t.Fatalf("recoverable reload failure disabled the parent: %+v, err=%v", saved, err)
			}
		})
	}
}

// TestChildDeleteReloadFailureRestoresRow proves a delete whose reload fails is
// rolled back.
func TestChildDeleteReloadFailureRestoresRow(t *testing.T) {
	for _, kind := range lifecycleChildKinds() {
		if kind.name == "nat44 dnat rule" {
			continue
		}
		t.Run(kind.name, func(t *testing.T) {
			st, service, backend, network, _ := newVNFService(t)
			parent := createLifecycleVNF(t, service, network.ID, kind.parentKind, "parent", true)
			childID := kind.create(t, service, parent.ID)
			backend.reloadFailures = 1
			if err := kind.remove(service, parent.ID, childID); err == nil {
				t.Fatal("delete succeeded despite a reload failure")
			} else {
				expectVNFStatus(t, err, http.StatusInternalServerError)
			}
			if count, err := kind.list(st, parent.ID); err != nil || count != 1 {
				t.Fatalf("deleted row was not restored: count=%d err=%v", count, err)
			}
		})
	}
}

// TestChildDeleteRestoreFailureDisablesParent proves a second reload failure
// leaves the parent disabled.
func TestChildDeleteRestoreFailureDisablesParent(t *testing.T) {
	st, service, backend, network, _ := newVNFService(t)
	parent := createLifecycleVNF(t, service, network.ID, store.VNFTypeDNS, "parent", true)
	record, err := service.CreateDNSVNFRecord(context.Background(), &rookeryv1.CreateDNSVNFRecordRequest{
		VnfId: parent.ID, Name: "host.lab.test", Type: "A", Value: "10.0.0.50", Ttl: 300,
	})
	if err != nil {
		t.Fatalf("create record: %v", err)
	}
	backend.reloadFailures = 2
	if err := service.DeleteDNSVNFRecord(context.Background(), &rookeryv1.DeleteDNSVNFRecordRequest{
		VnfId: parent.ID, RecordId: record.ID,
	}); err == nil {
		t.Fatal("delete succeeded despite two reload failures")
	} else {
		expectVNFStatus(t, err, http.StatusInternalServerError)
	}
	saved, err := st.GetVNF(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if saved.Enabled {
		t.Fatal("parent stayed enabled after its restore reload failed")
	}
	records, err := st.ListDNSVNFRecords(context.Background(), parent.ID)
	if err != nil || len(records) != 1 {
		t.Fatalf("restored records = %+v, err = %v", records, err)
	}
}

// TestChildRollbackFailurePath covers the rollback-and-restore error path when
// the rollback write itself fails.
func TestChildRollbackFailurePath(t *testing.T) {
	st, service, backend, network, _ := newVNFService(t)
	parent := createLifecycleVNF(t, service, network.ID, store.VNFTypeDHCPv4, "parent", true)
	child, err := service.CreateDHCPOption(context.Background(), &rookeryv1.CreateDHCPOptionRequest{
		VnfId: parent.ID, Code: 15, Format: "string", Value: "lab.test",
	})
	if err != nil {
		t.Fatalf("create option: %v", err)
	}
	backend.reloadFailures = 2
	backend.reloadHook = func() {
		// Closing the store makes the rollback write and the follow-up read fail.
		_ = st.Close()
	}
	if _, err := service.UpdateDHCPOption(context.Background(), &rookeryv1.UpdateDHCPOptionRequest{
		VnfId: parent.ID, OptionId: child.ID, Code: 15, Format: "string", Value: "updated.test",
	}); err == nil {
		t.Fatal("update succeeded despite a rollback failure")
	} else {
		expectVNFStatus(t, err, http.StatusInternalServerError)
	}
}

// TestNAT44ChildMappingFailures covers the mapping replace, restore, and disable
// paths of the NAT44 child routes.
func TestNAT44ChildMappingFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("restore succeeds", func(t *testing.T) {
		st, service, backend, network, _ := newVNFService(t)
		parent := createLifecycleVNF(t, service, network.ID, store.VNFTypeNAT44, "nat", true)
		backend.applyNATRuleFailures = 1
		if _, err := service.CreateNAT44DNATRule(ctx, &rookeryv1.CreateNAT44DNATRuleRequest{
			VnfId: parent.ID, Protocol: "tcp", ExternalPort: 80, LocalIp: "10.0.0.10", LocalPort: 8080,
		}); err == nil {
			t.Fatal("create succeeded despite a mapping failure")
		} else {
			expectVNFStatus(t, err, http.StatusInternalServerError)
		}
		if backend.natRules != 1 {
			t.Fatalf("mapping calls = %d, want 1 after a successful restore", backend.natRules)
		}
		rules, err := st.ListNAT44DNATRules(ctx, parent.ID)
		if err != nil || len(rules) != 0 {
			t.Fatalf("failed rule was not rolled back: %+v, err = %v", rules, err)
		}
		saved, err := st.GetVNF(ctx, parent.ID)
		if err != nil || !saved.Enabled {
			t.Fatalf("restored parent = %+v, err = %v", saved, err)
		}
	})

	t.Run("restore fails", func(t *testing.T) {
		st, service, backend, network, _ := newVNFService(t)
		parent := createLifecycleVNF(t, service, network.ID, store.VNFTypeNAT44, "nat", true)
		backend.applyNATRuleFailures = 2
		if _, err := service.CreateNAT44DNATRule(ctx, &rookeryv1.CreateNAT44DNATRuleRequest{
			VnfId: parent.ID, Protocol: "tcp", ExternalPort: 80, LocalIp: "10.0.0.10", LocalPort: 8080,
		}); err == nil {
			t.Fatal("create succeeded despite two mapping failures")
		} else {
			expectVNFStatus(t, err, http.StatusInternalServerError)
		}
		saved, err := st.GetVNF(ctx, parent.ID)
		if err != nil {
			t.Fatalf("read parent: %v", err)
		}
		if saved.Enabled {
			t.Fatal("parent stayed enabled after its mapping restore failed")
		}
	})

	t.Run("update restore", func(t *testing.T) {
		st, service, backend, network, _ := newVNFService(t)
		parent := createLifecycleVNF(t, service, network.ID, store.VNFTypeNAT44, "nat", true)
		rule, err := service.CreateNAT44DNATRule(ctx, &rookeryv1.CreateNAT44DNATRuleRequest{
			VnfId: parent.ID, Protocol: "tcp", ExternalPort: 80, LocalIp: "10.0.0.10", LocalPort: 8080,
		})
		if err != nil {
			t.Fatalf("create rule: %v", err)
		}
		backend.applyNATRuleFailures = 1
		if _, err := service.UpdateNAT44DNATRule(ctx, &rookeryv1.UpdateNAT44DNATRuleRequest{
			VnfId: parent.ID, RuleId: rule.ID, Protocol: "tcp", ExternalPort: 80,
			LocalIp: "10.0.0.11", LocalPort: 8081,
		}); err == nil {
			t.Fatal("update succeeded despite a mapping failure")
		}
		saved, err := st.GetNAT44DNATRule(ctx, parent.ID, rule.ID)
		if err != nil || saved.LocalIP != "10.0.0.10" || saved.LocalPort != 8080 {
			t.Fatalf("rule was not restored: %+v, err = %v", saved, err)
		}
	})

	t.Run("delete restore", func(t *testing.T) {
		st, service, backend, network, _ := newVNFService(t)
		parent := createLifecycleVNF(t, service, network.ID, store.VNFTypeNAT44, "nat", true)
		rule, err := service.CreateNAT44DNATRule(ctx, &rookeryv1.CreateNAT44DNATRuleRequest{
			VnfId: parent.ID, Protocol: "tcp", ExternalPort: 80, LocalIp: "10.0.0.10", LocalPort: 8080,
		})
		if err != nil {
			t.Fatalf("create rule: %v", err)
		}
		backend.applyNATRuleFailures = 1
		if err := service.DeleteNAT44DNATRule(ctx, &rookeryv1.DeleteNAT44DNATRuleRequest{
			VnfId: parent.ID, RuleId: rule.ID,
		}); err == nil {
			t.Fatal("delete succeeded despite a mapping failure")
		}
		rules, err := st.ListNAT44DNATRules(ctx, parent.ID)
		if err != nil || len(rules) != 1 {
			t.Fatalf("deleted rule was not restored: %+v, err = %v", rules, err)
		}
	})
}

// TestNAT44ChildRollbackFailurePath covers the mapping rollback failure branch.
func TestNAT44ChildRollbackFailurePath(t *testing.T) {
	st, service, backend, network, _ := newVNFService(t)
	parent := createLifecycleVNF(t, service, network.ID, store.VNFTypeNAT44, "nat", true)
	backend.applyNATRuleFailures = 2
	backend.applyHook = func() {
		// Closing the store makes the rollback write fail.
		_ = st.Close()
	}
	if _, err := service.CreateNAT44DNATRule(context.Background(), &rookeryv1.CreateNAT44DNATRuleRequest{
		VnfId: parent.ID, Protocol: "tcp", ExternalPort: 80, LocalIp: "10.0.0.10", LocalPort: 8080,
	}); err == nil {
		t.Fatal("create succeeded despite a mapping rollback failure")
	} else {
		expectVNFStatus(t, err, http.StatusInternalServerError)
	}
}
