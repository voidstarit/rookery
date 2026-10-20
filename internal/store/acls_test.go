package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestSetInterfaceACLsKeepsOrderAndLegacyFields(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	network := createNetwork(t, st, "acl-order")
	iface := createInterface(t, st, "acl-order-iface", network.ID)

	first, err := st.CreateACL(ctx, store.ACL{
		Name:  "first",
		Rules: []store.ACLRule{{Action: "permit", Protocol: "any", Source: "0.0.0.0/0", Destination: "0.0.0.0/0"}},
	})
	if err != nil {
		t.Fatalf("create first ACL: %v", err)
	}
	second, err := st.CreateACL(ctx, store.ACL{
		Name:  "second",
		Rules: []store.ACLRule{{Action: "deny", Protocol: "any", Source: "0.0.0.0/0", Destination: "0.0.0.0/0"}},
	})
	if err != nil {
		t.Fatalf("create second ACL: %v", err)
	}

	updated, err := st.SetInterfaceACLs(ctx, iface.ID, []int64{second.ID, first.ID}, []int64{first.ID})
	if err != nil {
		t.Fatalf("set interface ACLs: %v", err)
	}
	if len(updated.IngressACLs) != 2 ||
		updated.IngressACLs[0].ID != second.ID || updated.IngressACLs[1].ID != first.ID {
		t.Fatalf("ingress order = %+v", updated.IngressACLs)
	}
	if len(updated.EgressACLs) != 1 || updated.EgressACLs[0].ID != first.ID {
		t.Fatalf("egress order = %+v", updated.EgressACLs)
	}
	if updated.IngressACLID == nil || *updated.IngressACLID != second.ID || updated.IngressACLName != "second" {
		t.Fatalf("legacy ingress fields = %+v", updated)
	}
	if updated.EgressACLID == nil || *updated.EgressACLID != first.ID || updated.EgressACLName != "first" {
		t.Fatalf("legacy egress fields = %+v", updated)
	}

	if err := st.DeleteACL(ctx, first.ID); !errors.Is(err, store.ErrACLInUse) {
		t.Fatalf("delete assigned ACL error = %v, want ErrACLInUse", err)
	}

	if _, err := st.SetInterfaceACLs(ctx, iface.ID, nil, nil); err != nil {
		t.Fatalf("clear interface ACLs: %v", err)
	}
	if err := st.DeleteACL(ctx, first.ID); err != nil {
		t.Fatalf("delete first ACL: %v", err)
	}
	if err := st.DeleteACL(ctx, second.ID); err != nil {
		t.Fatalf("delete second ACL: %v", err)
	}
}
