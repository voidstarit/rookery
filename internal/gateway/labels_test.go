package gateway_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

func TestEntityLabelRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "labels-gateway.db"))
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

	request := func(method, path, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	put := request(http.MethodPut, "/api/labels/vm/vm-1", `{"labels":["web","prod","web"]}`)
	if put.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(put.Body)
		_ = put.Body.Close()
		t.Fatalf("set labels status = %d: %s", put.StatusCode, body)
	}
	var setBody struct {
		EntityType string   `json:"entity_type"`
		EntityID   string   `json:"entity_id"`
		Labels     []string `json:"labels"`
	}
	if err := json.NewDecoder(put.Body).Decode(&setBody); err != nil {
		t.Fatal(err)
	}
	_ = put.Body.Close()
	if setBody.EntityType != "vm" || setBody.EntityID != "vm-1" ||
		len(setBody.Labels) != 2 || setBody.Labels[0] != "prod" || setBody.Labels[1] != "web" {
		t.Fatalf("set labels = %+v", setBody)
	}

	list := request(http.MethodGet, "/api/labels/vm", "")
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list labels status = %d", list.StatusCode)
	}
	var records []struct {
		EntityID string   `json:"entity_id"`
		Labels   []string `json:"labels"`
	}
	if err := json.NewDecoder(list.Body).Decode(&records); err != nil {
		t.Fatal(err)
	}
	_ = list.Body.Close()
	if len(records) != 1 || records[0].EntityID != "vm-1" || len(records[0].Labels) != 2 {
		t.Fatalf("labels = %+v", records)
	}

	get := request(http.MethodGet, "/api/labels/vm/vm-1", "")
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get labels status = %d", get.StatusCode)
	}
	_ = get.Body.Close()

	missing := request(http.MethodGet, "/api/labels/vm/missing", "")
	if missing.StatusCode != http.StatusOK {
		t.Fatalf("missing labels status = %d", missing.StatusCode)
	}
	_ = missing.Body.Close()

	unknown := request(http.MethodGet, "/api/labels/bogus", "")
	if unknown.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown entity type status = %d", unknown.StatusCode)
	}
	_ = unknown.Body.Close()

	invalid := request(http.MethodPut, "/api/labels/vm/vm-1", `{"labels":["bad,label"]}`)
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid label status = %d", invalid.StatusCode)
	}
	_ = invalid.Body.Close()
}
