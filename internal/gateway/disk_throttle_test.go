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
}
