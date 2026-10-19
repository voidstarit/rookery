package api

import (
	"net/http"
	"strings"

	"github.com/voidstarit/rookery/internal/webui"
)

// CutoverHandler routes browser-facing API traffic through the grpc-gateway
// while keeping protocol-native handlers on the legacy server.
func CutoverHandler(gateway, legacy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/api/troubleshoot/capture/files/"):
			legacy.ServeHTTP(w, r)
		case strings.HasPrefix(path, "/ws/"), strings.HasPrefix(path, "/webshark"):
			legacy.ServeHTTP(w, r)
		case strings.HasPrefix(path, "/api/"), path == "/api":
			gateway.ServeHTTP(w, r)
		default:
			legacy.ServeHTTP(w, r)
		}
	})
}

// LogRequests exposes the API request logger for the cutover gateway.
func (s *Server) LogRequests(next http.Handler) http.Handler {
	return s.logRequests(next)
}

// NativeHandler exposes only the protocol-native routes that remain on the
// legacy server: VNC, WebShark, pcap downloads, and static UI assets.
func (s *Server) NativeHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/vms/{id}/vnc", s.handleVNCProxy)
	mux.HandleFunc("GET /api/troubleshoot/capture/files/{name}", s.handleDownloadPacketCapture)
	mux.HandleFunc("DELETE /api/troubleshoot/capture/files/{name}", s.handleDeletePacketCapture)
	mux.HandleFunc("GET /webshark/json", s.handleWebSharkJSON)
	mux.HandleFunc("GET /webshark/captures/{name}", s.handleWebSharkCapture)
	mux.HandleFunc("GET /webshark/watch", s.handleWebSharkWatch)
	mux.HandleFunc("GET /webshark", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/webshark/", http.StatusFound)
	})
	mux.Handle("/", webui.Handler())

	var handler http.Handler = mux
	handler = s.recoverPanic(handler)
	handler = securityHeaders(handler)
	return s.logRequests(handler)
}
