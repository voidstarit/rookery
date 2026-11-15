package api

import (
	"github.com/rs/zerolog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCutoverHandlerRoutes(t *testing.T) {
	gateway := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	legacy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := CutoverHandler(gateway, legacy)

	cases := []struct {
		path string
		want int
	}{
		{"/api/vms", http.StatusTeapot},
		{"/api/status", http.StatusTeapot},
		{"/api/troubleshoot/capture/files", http.StatusTeapot},
		{"/api/troubleshoot/capture/files/rookery-test.pcap", http.StatusNoContent},
		{"/ws/vms/abc/vnc", http.StatusNoContent},
		{"/console.html", http.StatusNoContent},
		{"/", http.StatusNoContent},
	}
	for _, test := range cases {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.want {
			t.Errorf("%s status = %d, want %d", test.path, recorder.Code, test.want)
		}
	}
}

func TestNativeHandlerRoutes(t *testing.T) {
	server := &Server{logger: zerolog.Nop()}
	handler := server.NativeHandler()

	cases := []struct {
		path string
		want int
	}{
		{"/", http.StatusOK},
		{"/api/troubleshoot/capture/files/rookery-test.pcap", http.StatusServiceUnavailable},
	}
	for _, test := range cases {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.want {
			t.Errorf("%s status = %d, want %d", test.path, recorder.Code, test.want)
		}
	}
}

func TestConsoleWebSocketHandlerRoutes(t *testing.T) {
	gateway := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	console := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := ConsoleWebSocketHandler(gateway, console)

	cases := []struct {
		path string
		want int
	}{
		{"/ws/lightvm-console/lvc_test", http.StatusNoContent},
		{"/api/vms", http.StatusTeapot},
		{"/ws/vms/abc/vnc", http.StatusTeapot},
	}
	for _, test := range cases {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.want {
			t.Errorf("%s status = %d, want %d", test.path, recorder.Code, test.want)
		}
	}
}

func TestConsoleAccessHandlerRejectsUnknownRoutes(t *testing.T) {
	server := &Server{logger: zerolog.Nop()}
	handler := server.ConsoleAccessHandler()

	cases := []string{
		"/ws/lightvm-console/not-a-token",
		"/ws/lightvm-console/",
		"/api/vms",
	}
	for _, path := range cases {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, recorder.Code)
		}
	}
}

func TestRedactRequestPath(t *testing.T) {
	if got := redactRequestPath("/ws/lightvm-console/lvc_secret"); got != "/ws/lightvm-console/[redacted]" {
		t.Fatalf("redacted console path = %q", got)
	}
	if got := redactRequestPath("/api/vms"); got != "/api/vms" {
		t.Fatalf("unrelated path = %q", got)
	}
}
