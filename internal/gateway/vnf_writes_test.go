package gateway_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

func TestVNFWriteRoutes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "vnf-writes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var paths []string
	legacy := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.Method+" "+request.URL.Path)
		switch {
		case request.Method == http.MethodDelete:
			writer.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(request.URL.Path, "/restart"):
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"id":9,"name":"dhcp","network_id":3,"type":"dhcpv4","enabled":true,"status":"running","config":{"gateway":"10.0.0.1/24","range_start":"10.0.0.10","range_end":"10.0.0.20"}}`)
		case request.Method == http.MethodPost:
			writer.WriteHeader(http.StatusCreated)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"id":9,"name":"dhcp","network_id":3,"type":"dhcpv4","enabled":false,"status":"disabled","config":{"gateway":"10.0.0.1/24","range_start":"10.0.0.10","range_end":"10.0.0.20"}}`)
		case request.Method == http.MethodPut:
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"id":9,"name":"dhcp2","network_id":3,"type":"dhcpv4","enabled":true,"status":"running","config":{"gateway":"10.0.0.1/24","range_start":"10.0.0.10","range_end":"10.0.0.20"}}`)
		default:
			http.Error(writer, `{"error":"unexpected"}`, http.StatusInternalServerError)
		}
	})

	gw, err := gateway.New(st, fakeReadiness{}, gateway.WithLegacyVNFHandler(legacy))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gw.Close)
	server := httptest.NewServer(gw.Handler)
	t.Cleanup(server.Close)

	created, err := http.Post(server.URL+"/api/vnfs", "application/json", strings.NewReader(`{"name":"dhcp","network_id":3,"type":"dhcpv4","config":{"gateway":"10.0.0.1/24","range_start":"10.0.0.10","range_end":"10.0.0.20"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var createdVNF map[string]any
	decodeBody(t, created, &createdVNF)
	if jsonID(createdVNF["id"]) != "9" || createdVNF["name"] != "dhcp" {
		t.Fatalf("created vnf = %+v", createdVNF)
	}

	request, err := http.NewRequest(http.MethodPut, server.URL+"/api/vnfs/9", strings.NewReader(`{"name":"dhcp2","enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	updated, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updated.StatusCode, readBody(t, updated))
	}
	var updatedVNF map[string]any
	decodeBody(t, updated, &updatedVNF)
	if updatedVNF["name"] != "dhcp2" || updatedVNF["status"] != "running" {
		t.Fatalf("updated vnf = %+v", updatedVNF)
	}

	restarted, err := http.Post(server.URL+"/api/vnfs/9/restart", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.StatusCode != http.StatusOK {
		t.Fatalf("restart status = %d, body = %s", restarted.StatusCode, readBody(t, restarted))
	}
	_ = restarted.Body.Close()

	request, err = http.NewRequest(http.MethodDelete, server.URL+"/api/vnfs/9", nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleted.StatusCode, readBody(t, deleted))
	}
	_ = deleted.Body.Close()

	wantPaths := []string{"POST /api/vnfs", "PUT /api/vnfs/9", "POST /api/vnfs/9/restart", "DELETE /api/vnfs/9"}
	if fmt.Sprint(paths) != fmt.Sprint(wantPaths) {
		t.Fatalf("legacy paths = %v", paths)
	}
}
