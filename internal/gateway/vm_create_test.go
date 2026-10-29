package gateway_test

import (
	"context"
	"errors"
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
	network, err := st.CreateNetwork(context.Background(), store.Network{Name: "lab", BridgeDomainID: 500})
	if err != nil {
		t.Fatal(err)
	}
	iface, err := st.CreateInterface(context.Background(), store.VirtualInterface{
		Name: "tap-1", NetworkID: network.ID, SocketPath: "/run/vpp/tap-1.sock", MAC: "02:00:00:00:00:01", SwIfIndex: 10,
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
	if vm["status"] != store.StatusRunning {
		t.Fatalf("created status = %v, want %s", vm["status"], store.StatusRunning)
	}

	// Changing disks or interfaces requires a stopped VM, and stopping one is
	// always explicit.
	stop, err := http.Post(server.URL+"/api/vms/"+id+"/stop", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if stop.StatusCode != http.StatusOK {
		t.Fatalf("stop VM status = %d, body = %s", stop.StatusCode, readBody(t, stop))
	}
	_ = stop.Body.Close()

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

	request, err = http.NewRequest(http.MethodPut, server.URL+"/api/vms/"+id, strings.NewReader(`{"interface_ids":[`+itoa(iface.ID)+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	attached, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if attached.StatusCode != http.StatusOK {
		t.Fatalf("attach interface status = %d, body = %s", attached.StatusCode, readBody(t, attached))
	}
	var attachedVM map[string]any
	decodeBody(t, attached, &attachedVM)
	if len(attachedVM["interfaces"].([]any)) != 1 {
		t.Fatalf("attached vm = %+v", attachedVM)
	}

	request, err = http.NewRequest(http.MethodPut, server.URL+"/api/vms/"+id, strings.NewReader(`{"replace_interface_ids":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	cleared, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.StatusCode != http.StatusOK {
		t.Fatalf("clear interfaces status = %d, body = %s", cleared.StatusCode, readBody(t, cleared))
	}
	var clearedVM map[string]any
	decodeBody(t, cleared, &clearedVM)
	if _, ok := clearedVM["interfaces"]; ok {
		t.Fatalf("interfaces were not cleared: %+v", clearedVM)
	}

	request, err = http.NewRequest(http.MethodPut, server.URL+"/api/vms/"+id, strings.NewReader(`{"replace_disk_ids":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	emptyDisks, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if emptyDisks.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty disk replacement status = %d, body = %s", emptyDisks.StatusCode, readBody(t, emptyDisks))
	}
	_ = emptyDisks.Body.Close()

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

func TestVMCreateStartFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "vm-create-fail.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	backend, err := st.CreateStorageBackend(ctx, store.StorageBackend{
		Name: "default", Driver: "lvm-thin", Config: store.StorageBackendConfig{VG: "vg0", ThinPool: "pool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	disk, err := st.CreateDisk(ctx, store.Disk{
		Name: "disk-1", SizeGB: 10, VG: "vg0", ThinPool: "pool", StorageBackendID: &backend.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &fakeVMController{startErr: errors.New("KVM is not available")}
	gw, err := gateway.New(st, fakeReadiness{status: readiness.Status{Ready: true}}, gateway.WithVMController(runtime))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	response, err := http.Post(server.URL+"/api/vms", "application/json", strings.NewReader(`{"name":"vm-1","disk_ids":[`+itoa(disk.ID)+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("create status = %d, body = %s, want 500", response.StatusCode, readBody(t, response))
	}
	if body := readBody(t, response); !strings.Contains(body, "could not start VM") {
		t.Fatalf("body = %s, want start failure", body)
	}

	vms, err := st.ListVMs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 0 {
		t.Fatalf("vms after failed create = %+v, want none", vms)
	}
	released, err := st.GetDisk(ctx, disk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if released.AttachedVMID != nil {
		t.Fatalf("disk still attached to %v after failed create", released.AttachedVMID)
	}
}
