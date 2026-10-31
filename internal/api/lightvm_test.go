package api_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/api"
	"github.com/voidstarit/rookery/internal/lightvm"
)

func newUploadServer(t *testing.T, max int64) (*httptest.Server, *lightvm.Storage) {
	t.Helper()
	storage, err := lightvm.NewStorage(filepath.Join(t.TempDir(), "lightvm"), max)
	if err != nil {
		t.Fatal(err)
	}
	server := api.New(nil, nil, nil, nil, nil, nil, nil, zerolog.Nop())
	server.ConfigureLightVM(storage)
	httpServer := httptest.NewServer(server.NativeHandler())
	t.Cleanup(httpServer.Close)
	return httpServer, storage
}

func uploadRequest(t *testing.T, url, kind, fileName string, content []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if kind != "" {
		if err := writer.WriteField("kind", kind); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url+"/api/lightvm/uploads", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestLightVMUploadAndCleanup(t *testing.T) {
	server, storage := newUploadServer(t, 1024)

	response := uploadRequest(t, server.URL, "kernel", "vmlinuz 6.12", []byte("kernel-data"))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d", response.StatusCode)
	}
	var stored lightvm.StoredFile
	if err := json.NewDecoder(response.Body).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if stored.FileName != "vmlinuz 6.12" || stored.SizeBytes != int64(len("kernel-data")) {
		t.Fatalf("stored = %+v", stored)
	}
	if !storage.Managed(stored.Path) {
		t.Fatalf("stored path is not managed: %s", stored.Path)
	}
	if content, err := os.ReadFile(stored.Path); err != nil || string(content) != "kernel-data" {
		t.Fatalf("stored content = %q, err = %v", content, err)
	}

	request, err := http.NewRequest(http.MethodDelete,
		server.URL+"/api/lightvm/uploads?path="+stored.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("cleanup status = %d", response.StatusCode)
	}
	if _, err := os.Stat(stored.Path); !os.IsNotExist(err) {
		t.Fatalf("uploaded file still exists: %v", err)
	}
}

func TestLightVMInitramfsUpload(t *testing.T) {
	server, storage := newUploadServer(t, 1024)

	response := uploadRequest(t, server.URL, "initramfs", "initramfs.img", []byte("initramfs-data"))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d", response.StatusCode)
	}
	var stored lightvm.StoredFile
	if err := json.NewDecoder(response.Body).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if stored.FileName != "initramfs.img" || stored.SizeBytes != int64(len("initramfs-data")) {
		t.Fatalf("stored = %+v", stored)
	}
	if !storage.Managed(stored.Path) {
		t.Fatalf("stored path is not managed: %s", stored.Path)
	}
	if filepath.Base(filepath.Dir(stored.Path)) != "initramfs" {
		t.Fatalf("stored path = %s, want an initramfs directory", stored.Path)
	}
	if content, err := os.ReadFile(stored.Path); err != nil || string(content) != "initramfs-data" {
		t.Fatalf("stored content = %q, err = %v", content, err)
	}
}

func TestLightVMUploadRejectsInvalidRequests(t *testing.T) {
	server, _ := newUploadServer(t, 4)

	response := uploadRequest(t, server.URL, "", "vmlinuz", []byte("data"))
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing kind status = %d", response.StatusCode)
	}
	_ = response.Body.Close()

	response = uploadRequest(t, server.URL, "disk", "vmlinuz", []byte("data"))
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown kind status = %d", response.StatusCode)
	}
	_ = response.Body.Close()

	response = uploadRequest(t, server.URL, "kernel", "vmlinuz", []byte("too-large"))
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("large upload status = %d", response.StatusCode)
	}
	_ = response.Body.Close()
}
