package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/store"
)

func TestStartupScriptUpdates(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "startup-script.db"))
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

	getJSON := func(path string) map[string]any {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, body = %s", path, response.StatusCode, readBody(t, response))
		}
		var body map[string]any
		decodeBody(t, response, &body)
		return body
	}
	putJSON := func(path, body string) *http.Response {
		t.Helper()
		request, err := http.NewRequest(http.MethodPut, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	rookery := getJSON("/api/startup-script")
	if rookery["enabled"] == true || rookery["status"] != "disabled" {
		t.Fatalf("initial Rookery startup script = %+v, want disabled/empty", rookery)
	}
	if rookery["script"] != nil && rookery["script"] != "" {
		t.Fatalf("initial Rookery startup script content = %q, want empty", rookery["script"])
	}

	vpp := getJSON("/api/vpp-startup-script")
	if vpp["enabled"] != true || vpp["status"] != "pending" {
		t.Fatalf("initial VPP startup script = %+v, want enabled/pending", vpp)
	}
	if vpp["script"] != "# Assign NAT44 workers\nvppctl set nat workers 0\n" {
		t.Fatalf("initial VPP startup script content = %q", vpp["script"])
	}

	response := putJSON("/api/startup-script", `{"enabled":true,"script":"echo rookery"}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update Rookery startup script = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	response = putJSON("/api/vpp-startup-script", `{"enabled":true,"script":"echo vpp"}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update VPP startup script = %d, body = %s", response.StatusCode, readBody(t, response))
	}

	statusBody := getJSON("/api/status")
	rookeryStatus, ok := statusBody["rookery_startup_script"].(map[string]any)
	if !ok || rookeryStatus["enabled"] != true || rookeryStatus["status"] != "pending" {
		t.Fatalf("status rookery_startup_script = %+v", statusBody["rookery_startup_script"])
	}
	vppStatus, ok := statusBody["vpp_startup_script"].(map[string]any)
	if !ok || vppStatus["enabled"] != true || vppStatus["status"] != "pending" {
		t.Fatalf("status vpp_startup_script = %+v", statusBody["vpp_startup_script"])
	}

	response = putJSON("/api/startup-script", `{"enabled":true,"script":"  "}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty Rookery startup script = %d, want 400", response.StatusCode)
	}
	response = putJSON("/api/vpp-startup-script", `{"enabled":true,"script":"  "}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty VPP startup script = %d, want 400", response.StatusCode)
	}
}
