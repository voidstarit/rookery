package gateway_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

func TestLightVMInitramfsRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "lightvm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	managedDir := filepath.Join(t.TempDir(), "lightvm")
	if err := os.MkdirAll(filepath.Join(managedDir, "initramfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(managedDir, "initramfs", "initramfs.img")
	if err := os.WriteFile(imagePath, []byte("initramfs-image"), 0o644); err != nil {
		t.Fatal(err)
	}

	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithLightVMStorageDir(managedDir))
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

	response, created := request(http.MethodPost, "/api/lightvm/initramfs", map[string]any{
		"name":        "initramfs-6.12",
		"description": "test initramfs",
		"path":        imagePath,
		"file_name":   "initramfs.img",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %+v", response.StatusCode, created)
	}
	if response.Header.Get("Location") != "/api/lightvm/initramfs/1" {
		t.Fatalf("location = %q", response.Header.Get("Location"))
	}
	if created["managed"] != true || created["size_bytes"] != float64(len("initramfs-image")) {
		t.Fatalf("created initramfs = %+v", created)
	}

	response, image := request(http.MethodGet, "/api/lightvm/initramfs/1", nil)
	if response.StatusCode != http.StatusOK || image["name"] != "initramfs-6.12" {
		t.Fatalf("get status = %d, body = %+v", response.StatusCode, image)
	}

	listResponse, err := http.Get(server.URL + "/api/lightvm/initramfs")
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

	response, updated := request(http.MethodPut, "/api/lightvm/initramfs/1", map[string]any{
		"name":        "initramfs-6.12.4",
		"description": "updated",
		"path":        imagePath,
		"file_name":   "initramfs.img",
	})
	if response.StatusCode != http.StatusOK || updated["name"] != "initramfs-6.12.4" {
		t.Fatalf("update status = %d, body = %+v", response.StatusCode, updated)
	}

	response, _ = request(http.MethodDelete, "/api/lightvm/initramfs/1", nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.StatusCode)
	}
	if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
		t.Fatalf("managed initramfs file still exists: %v", err)
	}

	response, _ = request(http.MethodGet, "/api/lightvm/initramfs/1", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted status = %d", response.StatusCode)
	}
}
