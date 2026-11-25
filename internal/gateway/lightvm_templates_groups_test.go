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

func TestLightVMTemplateAndGroupRoutes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "templates.db"))
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
	gw, err := gateway.New(st, fakeReadiness{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	request := func(method, path string, body any, target any) *http.Response {
		t.Helper()
		var data []byte
		if body != nil {
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if target != nil && response.StatusCode != http.StatusNoContent {
			if err := json.NewDecoder(response.Body).Decode(target); err != nil {
				t.Fatalf("%s %s decode: %v", method, path, err)
			}
		}
		return response
	}

	var template map[string]any
	response := request(http.MethodPost, "/api/lightvm/templates", map[string]any{
		"name":        "web",
		"description": "web fleet",
		"spec": map[string]any{
			"vcpus":             2,
			"memory_mb":         1024,
			"kernel_id":         kernel.ID,
			"initramfs_id":      initramfs.ID,
			"image":             "example.test/rootfs@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"console_enabled":   true,
			"executable_script": "#!/bin/sh\n",
		},
	}, &template)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create template status = %d, body = %+v", response.StatusCode, template)
	}
	if response.Header.Get("Location") == "" || template["latest_version"].(float64) != 1 {
		t.Fatalf("template response = %+v, location = %q", template, response.Header.Get("Location"))
	}
	templateID := int64(template["id"].(float64))
	versionID := int64(template["latest_version_id"].(float64))

	var templates []map[string]any
	response = request(http.MethodGet, "/api/lightvm/templates", nil, &templates)
	if response.StatusCode != http.StatusOK || len(templates) != 1 || templates[0]["name"] != "web" {
		t.Fatalf("list templates status = %d, body = %+v", response.StatusCode, templates)
	}

	var group map[string]any
	response = request(http.MethodPost, "/api/lightvm/groups", map[string]any{
		"name":                "web-group",
		"description":         "fleet",
		"template_version_id": versionID,
		"desired_count":       1,
	}, &group)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create group status = %d, body = %+v", response.StatusCode, group)
	}
	groupID := int64(group["id"].(float64))
	if group["template_id"] != float64(templateID) || group["drift"] != float64(1) {
		t.Fatalf("group = %+v", group)
	}

	var gotGroup map[string]any
	response = request(http.MethodGet, "/api/lightvm/groups/"+itoa(groupID), nil, &gotGroup)
	if response.StatusCode != http.StatusOK || gotGroup["name"] != "web-group" {
		t.Fatalf("get group status = %d, body = %+v", response.StatusCode, gotGroup)
	}

	response = request(http.MethodDelete, "/api/lightvm/groups/"+itoa(groupID), nil, nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete empty group status = %d", response.StatusCode)
	}
	response = request(http.MethodDelete, "/api/lightvm/templates/"+itoa(templateID), nil, nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete template status = %d", response.StatusCode)
	}
}
