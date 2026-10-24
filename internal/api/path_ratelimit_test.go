package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPathRateLimiterIsolatesPaths(t *testing.T) {
	limiter := NewPathRateLimiter(0.001)

	if !limiter.Allow("/api/vms") {
		t.Fatal("expected first request for /api/vms to be allowed")
	}
	if limiter.Allow("/api/vms") {
		t.Fatal("expected second request for /api/vms to be limited")
	}
	if limiter.Allow("//api/vms") {
		t.Fatal("expected a cleaned duplicate path to share the same bucket")
	}
	if !limiter.Allow("/api/disks") {
		t.Fatal("expected another path to have its own bucket")
	}
}

func TestPathRateLimiterMiddleware(t *testing.T) {
	limiter := NewPathRateLimiter(0.001)
	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder
	}

	if got := request("/api/vms").Code; got != http.StatusNoContent {
		t.Fatalf("first /api/vms status = %d, want %d", got, http.StatusNoContent)
	}
	limited := request("/api/vms")
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("second /api/vms status = %d, want %d", limited.Code, http.StatusTooManyRequests)
	}
	if got := limited.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}
	if got := request("/api/disks").Code; got != http.StatusNoContent {
		t.Fatalf("/api/disks status = %d, want %d", got, http.StatusNoContent)
	}
}

func TestPathRateLimiterCapsTrackedPaths(t *testing.T) {
	limiter := NewPathRateLimiter(0.001)
	limiter.maxPaths = 2

	limiter.Allow("/a")
	limiter.Allow("/b")
	limiter.Allow("/c")

	limiter.mu.Lock()
	tracked := len(limiter.paths)
	limiter.mu.Unlock()
	if tracked != 2 {
		t.Fatalf("tracked paths = %d, want 2", tracked)
	}
}

func TestPathRateLimiterDisabled(t *testing.T) {
	for _, limiter := range []*PathRateLimiter{nil, NewPathRateLimiter(0), NewPathRateLimiter(-1)} {
		for range 100 {
			if limiter.Enabled() {
				t.Fatal("disabled limiter reported enabled")
			}
			if !limiter.Allow("/api/vms") {
				t.Fatal("disabled limiter rejected a request")
			}
		}
	}
}
