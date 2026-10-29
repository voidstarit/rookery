package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

func TestDiskTemplatesAndPreparationScript(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "templates.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	output := filepath.Join(t.TempDir(), "script.jsonl")
	script := filepath.Join(t.TempDir(), "prepare.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$ROOKERY_DISK_JSON\" >> "+output+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	provisionOutput := filepath.Join(t.TempDir(), "provision.jsonl")
	provisionScript := filepath.Join(t.TempDir(), "provision.sh")
	if err := os.WriteFile(provisionScript, []byte("#!/bin/sh\nprintf '%s\\n' \"$ROOKERY_PROVISION_JSON\" >> "+provisionOutput+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	backend, err := st.CreateStorageBackend(context.Background(), store.StorageBackend{
		Name: "templates", Driver: "lvm-thin",
		Config: store.StorageBackendConfig{
			VG: "vg0", ThinPool: "pool", SupportsTemplates: true,
			PreparationScript: script, PreparationTimeout: "5s",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	provisioner := &fakeDiskProvisioner{}
	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithDiskProvisioner(provisioner))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	post := func(body string) *http.Response {
		t.Helper()
		response, err := http.Post(server.URL+"/api/disks", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	templateResponse := post(fmt.Sprintf(`{"name":"template-1","size_gb":5,"storage_backend_id":%d,"is_template":true}`, backend.ID))
	if templateResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create template status = %d, body = %s", templateResponse.StatusCode, readBody(t, templateResponse))
	}
	var template map[string]any
	decodeBody(t, templateResponse, &template)
	if template["is_template"] != true {
		t.Fatalf("template = %+v", template)
	}
	templateID := jsonID(template["id"])
	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/disks/"+templateID,
		strings.NewReader(fmt.Sprintf(`{"provisioning_script":%q,"provisioning_timeout":"5s"}`, provisionScript)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	updatedTemplate, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if updatedTemplate.StatusCode != http.StatusOK {
		t.Fatalf("update template provisioning status = %d, body = %s", updatedTemplate.StatusCode, readBody(t, updatedTemplate))
	}
	var templateConfig map[string]any
	decodeBody(t, updatedTemplate, &templateConfig)
	if templateConfig["provisioning_script"] != provisionScript || templateConfig["provisioning_timeout"] != "5s" {
		t.Fatalf("template provisioning = %+v", templateConfig)
	}

	plainResponse := post(fmt.Sprintf(`{"name":"plain","size_gb":3,"storage_backend_id":%d}`, backend.ID))
	if plainResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create plain status = %d, body = %s", plainResponse.StatusCode, readBody(t, plainResponse))
	}
	var plain map[string]any
	decodeBody(t, plainResponse, &plain)
	plainID := jsonID(plain["id"])

	request, err = http.NewRequest(http.MethodPut, server.URL+"/api/disks/"+plainID, strings.NewReader(`{"is_template":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	marked, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if marked.StatusCode != http.StatusOK {
		t.Fatalf("mark template status = %d, body = %s", marked.StatusCode, readBody(t, marked))
	}
	_ = marked.Body.Close()

	noScriptClone := post(fmt.Sprintf(`{"name":"no-script-clone","storage_backend_id":%d,"template_disk_id":%s,"provisioning":{"hostname":"nope"}}`, backend.ID, plainID))
	if noScriptClone.StatusCode != http.StatusBadRequest {
		t.Fatalf("provisioning without template script status = %d, body = %s", noScriptClone.StatusCode, readBody(t, noScriptClone))
	}
	_ = noScriptClone.Body.Close()

	cloneResponse := post(fmt.Sprintf(`{"name":"clone-1","storage_backend_id":%d,"template_disk_id":%s,"provisioning":{"hostname":"clone-1","timezone":"UTC","ssh_authorized_keys":["ssh-ed25519 AAAA"]}}`, backend.ID, templateID))
	if cloneResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create clone status = %d, body = %s", cloneResponse.StatusCode, readBody(t, cloneResponse))
	}
	var clone map[string]any
	decodeBody(t, cloneResponse, &clone)
	cloneID := jsonID(clone["id"])
	if clone["template_disk_id"] == nil || jsonID(clone["template_disk_id"]) != templateID {
		t.Fatalf("clone = %+v", clone)
	}
	lines := readScriptLines(t, output)
	if len(lines) != 1 {
		t.Fatalf("script runs = %d", len(lines))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["operation"] != "create" || payload["template"].(map[string]any)["name"] != "template-1" {
		t.Fatalf("script payload = %+v", payload)
	}
	if payload["disk"].(map[string]any)["size_gb"].(float64) != 5 {
		t.Fatalf("script disk = %+v", payload["disk"])
	}
	provisionLines := readScriptLines(t, provisionOutput)
	if len(provisionLines) != 1 {
		t.Fatalf("provisioning script runs = %d", len(provisionLines))
	}
	var provisionPayload map[string]any
	if err := json.Unmarshal([]byte(provisionLines[0]), &provisionPayload); err != nil {
		t.Fatal(err)
	}
	properties := provisionPayload["properties"].(map[string]any)
	if provisionPayload["operation"] != "provision" || properties["hostname"] != "clone-1" || properties["timezone"] != "UTC" {
		t.Fatalf("provisioning payload = %+v", provisionPayload)
	}
	keys := properties["ssh_authorized_keys"].([]any)
	if len(keys) != 1 || keys[0] != "ssh-ed25519 AAAA" {
		t.Fatalf("provisioning keys = %+v", properties["ssh_authorized_keys"])
	}

	// Cannot delete a template while a clone exists by default.
	request, err = http.NewRequest(http.MethodDelete, server.URL+"/api/disks/"+templateID, nil)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.StatusCode != http.StatusConflict {
		t.Fatalf("delete template status = %d, body = %s", blocked.StatusCode, readBody(t, blocked))
	}
	_ = blocked.Body.Close()

	// Delete the clone through the preparation script.
	request, err = http.NewRequest(http.MethodDelete, server.URL+"/api/disks/"+cloneID, nil)
	if err != nil {
		t.Fatal(err)
	}
	deletedClone, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deletedClone.StatusCode != http.StatusNoContent {
		t.Fatalf("delete clone status = %d, body = %s", deletedClone.StatusCode, readBody(t, deletedClone))
	}
	_ = deletedClone.Body.Close()
	lines = readScriptLines(t, output)
	if len(lines) != 2 || !strings.Contains(lines[1], `"operation":"delete"`) {
		t.Fatalf("script lines = %v", lines)
	}

	// Enable template deletion with clones, create another clone, then delete the template.
	if _, err := st.UpdateStorageBackend(context.Background(), store.StorageBackend{
		ID: backend.ID, Name: backend.Name, Title: backend.Title, Description: backend.Description,
		Driver: backend.Driver,
		Config: store.StorageBackendConfig{
			VG: backend.Config.VG, ThinPool: backend.Config.ThinPool, SupportsTemplates: true,
			PreparationScript: script, PreparationTimeout: "5s", AllowTemplateDeleteWithClones: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	secondClone := post(fmt.Sprintf(`{"name":"clone-2","storage_backend_id":%d,"template_disk_id":%s}`, backend.ID, templateID))
	if secondClone.StatusCode != http.StatusCreated {
		t.Fatalf("second clone status = %d, body = %s", secondClone.StatusCode, readBody(t, secondClone))
	}
	_ = secondClone.Body.Close()

	request, err = http.NewRequest(http.MethodDelete, server.URL+"/api/disks/"+templateID, nil)
	if err != nil {
		t.Fatal(err)
	}
	deletedTemplate, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deletedTemplate.StatusCode != http.StatusNoContent {
		t.Fatalf("delete allowed template status = %d, body = %s", deletedTemplate.StatusCode, readBody(t, deletedTemplate))
	}
	_ = deletedTemplate.Body.Close()
}

func readScriptLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func TestDiskTemplateProvisioningFailureRollsBackClone(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "provision-failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	prepOutput := filepath.Join(t.TempDir(), "prep.jsonl")
	prepScript := filepath.Join(t.TempDir(), "prepare.sh")
	if err := os.WriteFile(prepScript, []byte("#!/bin/sh\nprintf '%s\n' \"$ROOKERY_DISK_JSON\" >> "+prepOutput+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	failScript := filepath.Join(t.TempDir(), "provision-fail.sh")
	if err := os.WriteFile(failScript, []byte("#!/bin/sh\necho provisioning failed >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	backend, err := st.CreateStorageBackend(ctx, store.StorageBackend{
		Name: "templates", Driver: "lvm-thin",
		Config: store.StorageBackendConfig{
			VG: "vg0", ThinPool: "pool", SupportsTemplates: true,
			PreparationScript: prepScript, PreparationTimeout: "5s",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithDiskProvisioner(&fakeDiskProvisioner{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	post := func(body string) *http.Response {
		t.Helper()
		response, err := http.Post(server.URL+"/api/disks", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	templateResponse := post(fmt.Sprintf(`{"name":"template-fail","size_gb":5,"storage_backend_id":%d,"is_template":true,"provisioning_script":%q}`, backend.ID, failScript))
	if templateResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create template status = %d, body = %s", templateResponse.StatusCode, readBody(t, templateResponse))
	}
	var template map[string]any
	decodeBody(t, templateResponse, &template)
	templateID := jsonID(template["id"])

	cloneResponse := post(fmt.Sprintf(`{"name":"clone-fail","storage_backend_id":%d,"template_disk_id":%s,"provisioning":{"hostname":"should-not-exist"}}`, backend.ID, templateID))
	if cloneResponse.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failing clone status = %d, body = %s", cloneResponse.StatusCode, readBody(t, cloneResponse))
	}
	_ = cloneResponse.Body.Close()
	if _, err := st.GetDiskByName(ctx, "clone-fail"); !errors.Is(err, store.ErrDiskNotFound) {
		t.Fatalf("clone record after failure = %v, want ErrDiskNotFound", err)
	}
	lines := readScriptLines(t, prepOutput)
	if len(lines) != 2 {
		t.Fatalf("preparation runs = %d, want create and delete", len(lines))
	}
	if !strings.Contains(lines[0], `"operation":"create"`) || !strings.Contains(lines[1], `"operation":"delete"`) {
		t.Fatalf("preparation operations = %v", lines)
	}
}
