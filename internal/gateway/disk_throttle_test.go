package gateway_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/diskthrottle"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

func TestDiskThrottleClassRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "disk-throttle-routes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	gw, err := gateway.New(st, fakeReadiness{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	post := func(path, body string) *http.Response {
		t.Helper()
		response, err := http.Post(server.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	created := post("/api/disk-throttle-classes", `{"name":"standard","description":"first","limits":{"bps_read":1048576}}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create class = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	if location := created.Header.Get("Location"); location != "/api/disk-throttle-classes/1" {
		t.Fatalf("Location = %q", location)
	}
	var class map[string]any
	decodeBody(t, created, &class)
	id := jsonID(class["id"])
	if class["name"] != "standard" || class["description"] != "first" {
		t.Fatalf("class = %+v", class)
	}
	limits := class["limits"].(map[string]any)
	if limits["bps_read"].(float64) != 1048576 {
		t.Fatalf("limits = %+v", limits)
	}

	duplicate := post("/api/disk-throttle-classes", `{"name":"STANDARD","limits":{"bps_total":1}}`)
	if duplicate.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate status = %d, body = %s", duplicate.StatusCode, readBody(t, duplicate))
	}
	invalid := post("/api/disk-throttle-classes", `{"name":"invalid","limits":{"bps_read":1,"bps_total":1}}`)
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, body = %s", invalid.StatusCode, readBody(t, invalid))
	}
	missingLimits := post("/api/disk-throttle-classes", `{"name":"empty"}`)
	if missingLimits.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing limits status = %d, body = %s", missingLimits.StatusCode, readBody(t, missingLimits))
	}
	getResponse, err := http.Get(server.URL + "/api/disk-throttle-classes/" + id)
	if err != nil {
		t.Fatal(err)
	}
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("get class status = %d, body = %s", getResponse.StatusCode, readBody(t, getResponse))
	}
	_ = getResponse.Body.Close()
	missingGet, err := http.Get(server.URL + "/api/disk-throttle-classes/999")
	if err != nil {
		t.Fatal(err)
	}
	if missingGet.StatusCode != http.StatusNotFound {
		t.Fatalf("missing class status = %d, body = %s", missingGet.StatusCode, readBody(t, missingGet))
	}
	_ = missingGet.Body.Close()

	updateRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/disk-throttle-classes/"+id,
		strings.NewReader(`{"description":"second"}`))
	if err != nil {
		t.Fatal(err)
	}
	updateRequest.Header.Set("Content-Type", "application/json")
	updated, err := http.DefaultClient.Do(updateRequest)
	if err != nil {
		t.Fatal(err)
	}
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updated.StatusCode, readBody(t, updated))
	}
	var updatedClass map[string]any
	decodeBody(t, updated, &updatedClass)
	if updatedClass["description"] != "second" || updatedClass["limits"].(map[string]any)["bps_read"].(float64) != 1048576 {
		t.Fatalf("merged class = %+v", updatedClass)
	}
	nameRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/disk-throttle-classes/"+id,
		strings.NewReader(`{"name":"standard-renamed"}`))
	if err != nil {
		t.Fatal(err)
	}
	nameRequest.Header.Set("Content-Type", "application/json")
	renamed, err := http.DefaultClient.Do(nameRequest)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.StatusCode != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", renamed.StatusCode, readBody(t, renamed))
	}
	_ = renamed.Body.Close()
	invalidUpdate, err := http.NewRequest(http.MethodPut, server.URL+"/api/disk-throttle-classes/"+id,
		strings.NewReader(`{"limits":{"bps_read":1,"bps_total":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	invalidUpdate.Header.Set("Content-Type", "application/json")
	invalidUpdated, err := http.DefaultClient.Do(invalidUpdate)
	if err != nil {
		t.Fatal(err)
	}
	if invalidUpdated.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid update status = %d, body = %s", invalidUpdated.StatusCode, readBody(t, invalidUpdated))
	}
	_ = invalidUpdated.Body.Close()
	missingUpdate, err := http.NewRequest(http.MethodPut, server.URL+"/api/disk-throttle-classes/999",
		strings.NewReader(`{"description":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	missingUpdate.Header.Set("Content-Type", "application/json")
	missingUpdated, err := http.DefaultClient.Do(missingUpdate)
	if err != nil {
		t.Fatal(err)
	}
	if missingUpdated.StatusCode != http.StatusNotFound {
		t.Fatalf("missing update status = %d, body = %s", missingUpdated.StatusCode, readBody(t, missingUpdated))
	}
	_ = missingUpdated.Body.Close()

	classIDValue := int64(1)
	disk, err := st.CreateDisk(context.Background(), store.Disk{
		Name: "data", SizeGB: 5, VG: "vg0", ThinPool: "pool",
		ThrottleClassID: &classIDValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	deleteRequest, err := http.NewRequest(http.MethodDelete, server.URL+"/api/disk-throttle-classes/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	inUse, err := http.DefaultClient.Do(deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if inUse.StatusCode != http.StatusConflict {
		t.Fatalf("delete in-use status = %d, body = %s", inUse.StatusCode, readBody(t, inUse))
	}
	if !strings.Contains(readBody(t, inUse), "data") {
		t.Fatalf("delete in-use body does not name the disk")
	}
	if _, err := st.SetDiskThrottleClass(context.Background(), disk.ID, nil); err != nil {
		t.Fatal(err)
	}
	deleteRequest, err = http.NewRequest(http.MethodDelete, server.URL+"/api/disk-throttle-classes/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	missingDelete, err := http.NewRequest(http.MethodDelete, server.URL+"/api/disk-throttle-classes/999", nil)
	if err != nil {
		t.Fatal(err)
	}
	missingDeleted, err := http.DefaultClient.Do(missingDelete)
	if err != nil {
		t.Fatal(err)
	}
	if missingDeleted.StatusCode != http.StatusNotFound {
		t.Fatalf("missing delete status = %d, body = %s", missingDeleted.StatusCode, readBody(t, missingDeleted))
	}
	_ = missingDeleted.Body.Close()
}

func TestDiskThrottleClassCorruptValidationError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "disk-throttle-corrupt.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	class, err := st.CreateDiskThrottleClass(context.Background(), store.DiskThrottleClass{
		Name:   "corrupt",
		Limits: diskthrottle.Limits{BPSRead: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE disk_throttle_classes SET limits = 'not-json' WHERE id = ?`, class.ID); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	gw, err := gateway.New(st, fakeReadiness{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)
	response, err := http.Get(server.URL + "/api/disk-throttle-classes")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list corrupt class = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var classes []map[string]any
	decodeBody(t, response, &classes)
	if len(classes) != 1 || classes[0]["validation_error"] == "" {
		t.Fatalf("classes = %+v", classes)
	}
	repairRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/disk-throttle-classes/1",
		strings.NewReader(`{"description":"still corrupt"}`))
	if err != nil {
		t.Fatal(err)
	}
	repairRequest.Header.Set("Content-Type", "application/json")
	repair, err := http.DefaultClient.Do(repairRequest)
	if err != nil {
		t.Fatal(err)
	}
	if repair.StatusCode != http.StatusBadRequest {
		t.Fatalf("metadata-only corrupt update status = %d, body = %s", repair.StatusCode, readBody(t, repair))
	}
	_ = repair.Body.Close()
	validRequest, err := http.NewRequest(http.MethodPut, server.URL+"/api/disk-throttle-classes/1",
		strings.NewReader(`{"description":"repaired","limits":{"bps_read":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	validRequest.Header.Set("Content-Type", "application/json")
	valid, err := http.DefaultClient.Do(validRequest)
	if err != nil {
		t.Fatal(err)
	}
	if valid.StatusCode != http.StatusOK {
		t.Fatalf("repair status = %d, body = %s", valid.StatusCode, readBody(t, valid))
	}
	_ = valid.Body.Close()
}
