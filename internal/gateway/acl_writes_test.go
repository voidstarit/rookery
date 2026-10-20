package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

type fakeACLRuntime struct {
	index   uint32
	deleted []uint32
}

func (f *fakeACLRuntime) EnsureVPPACL(_ context.Context, _ int64, _ []store.ACLRule) (uint32, error) {
	if f.index == 0 {
		f.index = 41
	}
	return f.index, nil
}
func (f *fakeACLRuntime) DeleteVPPACL(_ context.Context, index uint32) error {
	f.deleted = append(f.deleted, index)
	return nil
}
func (f *fakeACLRuntime) SetInterfaceACLs(_ context.Context, _ uint32, _, _ []uint32) error {
	return nil
}

func TestACLWriteRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "acl-writes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	runtime := &fakeACLRuntime{index: 12}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{
		Ready: true, VPP: readiness.VPPStatus{ComponentStatus: readiness.ComponentStatus{Ready: true}},
	}}, gateway.WithACLRuntime(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	body := `{"name":"web","title":"Web","rules":[{"action":"permit","protocol":"tcp","source":"10.0.0.0/24","destination":"0.0.0.0/0","destination_port_start":443}]}`
	created, err := http.Post(server.URL+"/api/acls", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create ACL status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var acl map[string]any
	decodeBody(t, created, &acl)
	id := jsonID(acl["id"])
	if acl["vpp_index"].(float64) != 12 {
		t.Fatalf("acl = %+v", acl)
	}

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/acls/"+id, strings.NewReader(`{"name":"web","title":"Web updated","rules":[{"action":"deny","protocol":"any","source":"10.0.0.0/24","destination":"0.0.0.0/0"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	updated, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("update ACL status = %d, body = %s", updated.StatusCode, readBody(t, updated))
	}
	var updatedACL map[string]any
	decodeBody(t, updated, &updatedACL)
	if updatedACL["title"] != "Web updated" {
		t.Fatalf("updated acl = %+v", updatedACL)
	}

	request, err = http.NewRequest(http.MethodDelete, server.URL+"/api/acls/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete ACL status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	_ = deleted.Body.Close()
	if len(runtime.deleted) != 1 || runtime.deleted[0] != 12 {
		t.Fatalf("deleted VPP ACLs = %+v", runtime.deleted)
	}
}
