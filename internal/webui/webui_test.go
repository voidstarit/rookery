package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSPAFallbackServesIndexForBrowserRoutes(t *testing.T) {
	handler := Handler()
	request := httptest.NewRequest(http.MethodGet, "/vms/new", nil)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content type = %q, want HTML", recorder.Header().Get("Content-Type"))
	}
	if !strings.Contains(recorder.Body.String(), `id="vms-root"`) {
		t.Fatal("SPA fallback did not serve index.html")
	}
}

func TestSPAFallbackDoesNotHideMissingAssets(t *testing.T) {
	handler := Handler()
	request := httptest.NewRequest(http.MethodGet, "/missing.js", nil)
	request.Header.Set("Accept", "*/*")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", recorder.Code)
	}
}

func TestStaticAssetsStillServed(t *testing.T) {
	handler := Handler()
	request := httptest.NewRequest(http.MethodGet, "/build/app.js", nil)
	request.Header.Set("Accept", "*/*")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want 200", recorder.Code)
	}
	body, err := io.ReadAll(recorder.Body)
	if err != nil {
		t.Fatalf("read asset: %v", err)
	}
	if len(body) == 0 || strings.Contains(string(body), `id="vms-root"`) {
		t.Fatal("asset route did not serve the embedded bundle")
	}
}
