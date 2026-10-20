package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterAllowsBurstThenRefills(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := NewRateLimiter(2)
	limiter.now = func() time.Time { return now }
	limiter.last = now

	if !limiter.Allow() || !limiter.Allow() {
		t.Fatal("expected the initial burst of two requests to be allowed")
	}
	if limiter.Allow() {
		t.Fatal("expected the third request to be limited")
	}

	now = now.Add(500 * time.Millisecond)
	if !limiter.Allow() {
		t.Fatal("expected a refilled token to allow one request")
	}
	if limiter.Allow() {
		t.Fatal("expected the bucket to be empty again")
	}
}

func TestRateLimiterBelowOneRPSBurstsOnce(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := NewRateLimiter(0.5)
	limiter.now = func() time.Time { return now }
	limiter.last = now

	if !limiter.Allow() {
		t.Fatal("expected the first request to be allowed")
	}
	if limiter.Allow() {
		t.Fatal("expected the second immediate request to be limited")
	}

	now = now.Add(2 * time.Second)
	if !limiter.Allow() {
		t.Fatal("expected a token after one refill period")
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	for _, limiter := range []*RateLimiter{nil, NewRateLimiter(0), NewRateLimiter(-1)} {
		for range 100 {
			if limiter.Enabled() {
				t.Fatal("disabled limiter reported enabled")
			}
			if !limiter.Allow() {
				t.Fatal("disabled limiter rejected a request")
			}
		}
	}
}

func TestRateLimitMiddlewareRejectsWith429(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := NewRateLimiter(1)
	limiter.now = func() time.Time { return now }
	limiter.last = now
	limiter.lastWarn = now

	handler := limiter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/vms", nil))
	if first.Code != http.StatusNoContent {
		t.Fatalf("first request status = %d, want %d", first.Code, http.StatusNoContent)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/disks", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}
	if got := second.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}
}

func TestRateLimitMiddlewareDisabledPassesThrough(t *testing.T) {
	handler := NewRateLimiter(0).Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for range 100 {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
		}
	}
}
