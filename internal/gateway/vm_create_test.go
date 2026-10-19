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

func TestVMCreateAndUpdate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "vm-create.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	backend, err := st.CreateStorageBackend(context.Background(), store.StorageBackend{
		Name: "default", Driver: "lvm-thin", Config: store.StorageBackendConfig{VG: "vg0", ThinPool: "pool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := st.CreateDisk(context.Background(), store.Disk{
		Name: "disk-1", SizeGB: 10, VG: "vg0", ThinPool: "pool", StorageBackendID: &backend.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{Ready: true}}, gateway.WithVMController(&fakeVMController{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	created, err := http.Post(server.URL+"/api/vms", "application/json", strings.NewReader(`{"name":"vm-1","description":"first","vcpus":2,"memory_mb":1024,"disk_ids":[`+itoa(disk.ID)+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create VM status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var vm map[string]any
	decodeBody(t, created, &vm)
	id := vm["id"].(string)
	if vm["name"] != "vm-1" || vm["vcpus"].(float64) != 2 || vm["memory_mb"].(float64) != 1024 {
		t.Fatalf("vm = %+v", vm)
	}

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/vms/"+id, strings.NewReader(`{"name":"vm-1b","description":"updated","vcpus":4}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	updated, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("update VM status = %d, body = %s", updated.StatusCode, readBody(t, updated))
	}
	var updatedVM map[string]any
	decodeBody(t, updated, &updatedVM)
	if updatedVM["name"] != "vm-1b" || updatedVM["vcpus"].(float64) != 4 || updatedVM["memory_mb"].(float64) != 1024 {
		t.Fatalf("updated vm = %+v", updatedVM)
	}

	missingDisk, err := http.Post(server.URL+"/api/vms", "application/json", strings.NewReader(`{"name":"vm-2"}`))
	if err != nil {
		t.Fatal(err)
	}
	if missingDisk.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing disk status = %d, body = %s", missingDisk.StatusCode, readBody(t, missingDisk))
	}
	_ = missingDisk.Body.Close()

	// Empty repeated fields mean "leave unchanged" during migration; sending a
	// non-empty disk list replaces the set.
	request, err = http.NewRequest(http.MethodPut, server.URL+"/api/vms/"+id, strings.NewReader(`{"disk_ids":[`+itoa(disk.ID)+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	replaced, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.StatusCode != http.StatusOK {
		t.Fatalf("replace disks status = %d, body = %s", replaced.StatusCode, readBody(t, replaced))
	}
	_ = replaced.Body.Close()
}
