package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

func TestLightVMDiskSerials(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "lightvm-serials.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{Name: "vmlinuz", Path: "/tmp/vmlinuz", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{Name: "initramfs", Path: "/tmp/initramfs", SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := st.CreateStorageBackend(ctx, store.StorageBackend{
		Name: "default", Driver: "lvm-thin", Config: store.StorageBackendConfig{VG: "vg0", ThinPool: "pool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := st.CreateDisk(ctx, store.Disk{Name: "root", SizeGB: 10, VG: "vg0", ThinPool: "pool", StorageBackendID: &backend.ID})
	if err != nil {
		t.Fatal(err)
	}
	data, err := st.CreateDisk(ctx, store.Disk{Name: "data", SizeGB: 10, VG: "vg0", ThinPool: "pool", StorageBackendID: &backend.ID})
	if err != nil {
		t.Fatal(err)
	}

	gw, err := gateway.New(st, fakeReadiness{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	created, err := http.Post(server.URL+"/api/lightvm/instances", "application/json", strings.NewReader(
		`{"name":"serial-lightvm","vcpus":1,"memory_mb":128,"kernel_id":`+itoa(kernel.ID)+
			`,"initramfs_id":`+itoa(initramfs.ID)+`,"image":"alpine","executable_path":"/bin/sh","disk_ids":[`+
			itoa(root.ID)+`,`+itoa(data.ID)+`],"disk_serials":{"`+itoa(root.ID)+`":"root","`+itoa(data.ID)+`":"data"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var vm map[string]any
	decodeBody(t, created, &vm)
	id, _ := vm["id"].(string)
	if id == "" {
		t.Fatalf("created lightvm = %+v", vm)
	}
	disks, _ := vm["disks"].([]any)
	if len(disks) != 2 || disks[0].(map[string]any)["serial"] != "root" || disks[1].(map[string]any)["serial"] != "data" {
		t.Fatalf("created disks = %+v", vm["disks"])
	}

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/lightvm/instances/"+id, strings.NewReader(
		`{"name":"serial-lightvm","vcpus":1,"memory_mb":128,"kernel_id":`+itoa(kernel.ID)+
			`,"initramfs_id":`+itoa(initramfs.ID)+`,"image":"alpine","executable_path":"/bin/sh","disk_serials":{"`+
			itoa(root.ID)+`":"os","`+itoa(data.ID)+`":"logs"}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	updated, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("update serial status = %d, body = %s", updated.StatusCode, readBody(t, updated))
	}
	var updatedVM map[string]any
	decodeBody(t, updated, &updatedVM)
	updatedDisks, _ := updatedVM["disks"].([]any)
	if len(updatedDisks) != 2 || updatedDisks[0].(map[string]any)["serial"] != "os" || updatedDisks[1].(map[string]any)["serial"] != "logs" {
		t.Fatalf("updated disks = %+v", updatedVM["disks"])
	}

	request, err = http.NewRequest(http.MethodPut, server.URL+"/api/lightvm/instances/"+id, strings.NewReader(
		`{"name":"serial-lightvm","vcpus":1,"memory_mb":128,"kernel_id":`+itoa(kernel.ID)+
			`,"initramfs_id":`+itoa(initramfs.ID)+`,"image":"alpine","executable_path":"/bin/sh","disk_serials":{"`+
			itoa(root.ID)+`":"bad serial"}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	invalid, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid serial status = %d, want 400", invalid.StatusCode)
	}
	_ = invalid.Body.Close()
}
