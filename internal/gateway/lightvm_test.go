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

func TestLightVMKernelRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "lightvm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	managedDir := filepath.Join(t.TempDir(), "lightvm")
	if err := os.MkdirAll(filepath.Join(managedDir, "kernels"), 0o755); err != nil {
		t.Fatal(err)
	}
	kernelPath := filepath.Join(managedDir, "kernels", "vmlinuz")
	if err := os.WriteFile(kernelPath, []byte("kernel-image"), 0o644); err != nil {
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

	response, created := request(http.MethodPost, "/api/lightvm/kernels", map[string]any{
		"name":        "vmlinuz-6.12",
		"description": "test kernel",
		"path":        kernelPath,
		"file_name":   "vmlinuz",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %+v", response.StatusCode, created)
	}
	if response.Header.Get("Location") != "/api/lightvm/kernels/1" {
		t.Fatalf("location = %q", response.Header.Get("Location"))
	}
	if created["managed"] != true || created["size_bytes"] != float64(len("kernel-image")) {
		t.Fatalf("created kernel = %+v", created)
	}
	if created["file_name"] != "vmlinuz" {
		t.Fatalf("created file name = %+v", created["file_name"])
	}

	response, kernel := request(http.MethodGet, "/api/lightvm/kernels/1", nil)
	if response.StatusCode != http.StatusOK || kernel["name"] != "vmlinuz-6.12" {
		t.Fatalf("get status = %d, body = %+v", response.StatusCode, kernel)
	}

	listResponse, err := http.Get(server.URL + "/api/lightvm/kernels")
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

	response, updated := request(http.MethodPut, "/api/lightvm/kernels/1", map[string]any{
		"name":        "vmlinuz-6.12.4",
		"description": "updated",
		"path":        kernelPath,
		"file_name":   "vmlinuz",
	})
	if response.StatusCode != http.StatusOK || updated["name"] != "vmlinuz-6.12.4" {
		t.Fatalf("update status = %d, body = %+v", response.StatusCode, updated)
	}

	response, _ = request(http.MethodDelete, "/api/lightvm/kernels/1", nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.StatusCode)
	}
	if _, err := os.Stat(kernelPath); !os.IsNotExist(err) {
		t.Fatalf("managed kernel file still exists: %v", err)
	}

	response, _ = request(http.MethodGet, "/api/lightvm/kernels/1", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted status = %d", response.StatusCode)
	}
}

func TestLightVMKernelRejectsDuplicateName(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "lightvm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	kernelPath := filepath.Join(t.TempDir(), "vmlinuz")
	if err := os.WriteFile(kernelPath, []byte("kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	gw, err := gateway.New(st, fakeReadiness{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	body := []byte(`{"name":"vmlinuz","path":"` + kernelPath + `"}`)
	for index, want := range []int{http.StatusCreated, http.StatusConflict} {
		response, err := http.Post(server.URL+"/api/lightvm/kernels", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("create %d status = %d, want %d", index, response.StatusCode, want)
		}
	}
	if _, err := os.Stat(kernelPath); err != nil {
		t.Fatalf("host kernel file should remain: %v", err)
	}
}
