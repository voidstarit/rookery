package api

import (
	"log/slog"
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
		{"/webshark/json", http.StatusNoContent},
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
	server := &Server{logger: slog.Default()}
	handler := server.NativeHandler()

	cases := []struct {
		path string
		want int
	}{
		{"/", http.StatusOK},
		{"/webshark", http.StatusFound},
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
