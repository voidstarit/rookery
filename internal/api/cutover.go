package api

import (
	"net/http"
	"strings"
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
