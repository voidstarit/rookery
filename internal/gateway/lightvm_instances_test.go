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

func TestLightVMRoutes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "lightvm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	kernel, err := st.CreateLightVMKernel(ctx, store.LightVMKernel{
		Name: "vmlinuz-6.12", Path: "/tmp/vmlinuz", FileName: "vmlinuz", SizeBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	initramfs, err := st.CreateLightVMInitramfs(ctx, store.LightVMInitramfs{
		Name: "initramfs-6.12", Path: "/tmp/initramfs.img", FileName: "initramfs.img", SizeBytes: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	network, err := st.CreateNetwork(ctx, store.Network{
		Name: "lab", IPv4CIDR: "10.0.0.0/16", IPv6CIDR: "fd00:20::/64",
	})
	if err != nil {
		t.Fatal(err)
	}
	iface, err := st.CreateInterface(ctx, store.VirtualInterface{
		Name: "app-if", NetworkID: network.ID, MAC: "02:00:00:00:00:01",
	})
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

	request := func(method, path string, body any) (*http.Response, map[string]any) {
		t.Helper()
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else {
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(data)
		}
		req, err := http.NewRequest(method, server.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if response.StatusCode != http.StatusNoContent {
			if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil && response.StatusCode != http.StatusNotFound {
				t.Fatalf("%s %s decode: %v", method, path, err)
			}
		}
		_ = response.Body.Close()
		return response, decoded
	}

	response, created := request(http.MethodPost, "/api/lightvm/instances", map[string]any{
		"name":            "app-01",
		"description":     "test lightvm",
		"vcpus":           2,
		"memory_mb":       1024,
		"console_enabled": true,
		"kernel_id":       kernel.ID,
		"initramfs_id":    initramfs.ID,
		"image":           "alpine:3.20",
		"env":             []map[string]any{{"name": "APP_ENV", "value": "test"}},
		"executable_path": "/usr/local/bin/app",
		"executable_args": []string{"--listen", ":8080"},
		"interface_ids":   []int64{iface.ID},
		"cgroup_class":    "",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %+v", response.StatusCode, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("created lightvm id = %+v", created)
	}
	if response.Header.Get("Location") != "/api/lightvm/instances/"+id {
		t.Fatalf("location = %q", response.Header.Get("Location"))
	}
	if created["kernel_name"] != "vmlinuz-6.12" || created["initramfs_name"] != "initramfs-6.12" {
		t.Fatalf("created references = %+v", created)
	}
	if created["console_enabled"] != true || created["image"] != "alpine:3.20" {
		t.Fatalf("created fields = %+v", created)
	}
	if created["append"] != store.DefaultLightVMAppend {
		t.Fatalf("created append = %+v", created["append"])
	}
	env, _ := created["env"].([]any)
	if len(env) != 1 {
		t.Fatalf("created env = %+v", created["env"])
	}
	args, _ := created["executable_args"].([]any)
	if len(args) != 2 || args[1] != ":8080" {
		t.Fatalf("created args = %+v", created["executable_args"])
	}
	interfaces, _ := created["interfaces"].([]any)
	if len(interfaces) != 1 {
		t.Fatalf("created interfaces = %+v", created["interfaces"])
	}

	response, fetched := request(http.MethodGet, "/api/lightvm/instances/"+id, nil)
	if response.StatusCode != http.StatusOK || fetched["name"] != "app-01" {
		t.Fatalf("get status = %d, body = %+v", response.StatusCode, fetched)
	}

	listResponse, err := http.Get(server.URL + "/api/lightvm/instances")
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	if err := json.NewDecoder(listResponse.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	_ = listResponse.Body.Close()
	if listResponse.StatusCode != http.StatusOK || len(list) != 1 {
		t.Fatalf("list status = %d, body = %+v", listResponse.StatusCode, list)
	}

	response, _ = request(http.MethodDelete, "/api/lightvm/kernels/1", nil)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("delete referenced kernel status = %d", response.StatusCode)
	}

	response, updated := request(http.MethodPut, "/api/lightvm/instances/"+id, map[string]any{
		"name":            "app-02",
		"description":     "updated",
		"vcpus":           4,
		"memory_mb":       2048,
		"console_enabled": false,
		"kernel_id":       kernel.ID,
		"initramfs_id":    initramfs.ID,
		"image":           "alpine:3.21",
		"env":             []map[string]any{{"name": "APP_ENV", "value": "prod"}},
		"executable_path": "/bin/app",
		"executable_args": []string{"serve"},
		"interface_ids":   []int64{},
		"append":          "console=ttyS0 rdinit=/sbin/init",
	})
	if response.StatusCode != http.StatusOK || updated["name"] != "app-02" {
		t.Fatalf("update status = %d, body = %+v", response.StatusCode, updated)
	}
	if updated["append"] != "console=ttyS0 rdinit=/sbin/init" {
		t.Fatalf("updated append = %+v", updated["append"])
	}
	updatedInterfaces, _ := updated["interfaces"].([]any)
	if len(updatedInterfaces) != 0 {
		t.Fatalf("updated interfaces = %+v", updated["interfaces"])
	}

	response, _ = request(http.MethodDelete, "/api/lightvm/instances/"+id, nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.StatusCode)
	}
	response, _ = request(http.MethodGet, "/api/lightvm/instances/"+id, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted status = %d", response.StatusCode)
	}
}
