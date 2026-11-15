package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

func TestLightVMDiskBootWithoutImage(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "lightvm-disk-boot.db"))
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
	disk, err := st.CreateDisk(ctx, store.Disk{Name: "boot", SizeGB: 10, VG: "vg0", ThinPool: "pool", StorageBackendID: &backend.ID})
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

	post := func(body map[string]any) (*http.Response, map[string]any) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Post(server.URL+"/api/lightvm/instances", "application/json", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if response.StatusCode == http.StatusCreated {
			if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
				t.Fatal(err)
			}
		}
		_ = response.Body.Close()
		return response, decoded
	}
	put := func(id string, body map[string]any) (*http.Response, map[string]any) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPut, server.URL+"/api/lightvm/instances/"+id, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if response.StatusCode == http.StatusOK {
			if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
				t.Fatal(err)
			}
		}
		_ = response.Body.Close()
		return response, decoded
	}
	base := func(name string) map[string]any {
		return map[string]any{
			"name": name, "vcpus": 1, "memory_mb": 128,
			"kernel_id": kernel.ID, "initramfs_id": initramfs.ID,
			"image": "",
		}
	}

	created, vm := post(mergeMap(base("disk-boot"), map[string]any{"disk_ids": []int64{disk.ID}}))
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", created.StatusCode)
	}
	if image, ok := vm["image"]; ok && image != "" {
		t.Fatalf("created image = %+v", vm["image"])
	}
	if disks, _ := vm["disks"].([]any); len(disks) != 1 {
		t.Fatalf("created disks = %+v", vm["disks"])
	}
	id, _ := vm["id"].(string)

	if response, _ := post(base("no-root")); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("no-image/no-disk status = %d", response.StatusCode)
	}
	if response, _ := post(mergeMap(base("exec-without-image"), map[string]any{
		"disk_ids": []int64{disk.ID}, "executable_path": "/bin/app",
	})); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("executable without image status = %d", response.StatusCode)
	}

	updated, updatedVM := put(id, base("disk-boot"))
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d", updated.StatusCode)
	}
	if disks, _ := updatedVM["disks"].([]any); len(disks) != 1 {
		t.Fatalf("updated disks = %+v", updatedVM["disks"])
	}

	cleared, _ := put(id, mergeMap(base("disk-boot"), map[string]any{"replace_disk_ids": true}))
	if cleared.StatusCode != http.StatusBadRequest {
		t.Fatalf("clearing disks without image status = %d", cleared.StatusCode)
	}
}

func mergeMap(base, extra map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}
