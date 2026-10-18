// Package api exposes the HTTP API and embedded management UI.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
	"github.com/voidstarit/rookery/internal/webui"
)

// Readiness reports whether host prerequisites for VM creation are available.
type Readiness interface {
	Ready() bool
	Status() readiness.Status
}

// DiskProvisioner creates and deletes thin-LVM volumes for disk records.
type DiskProvisioner interface {
	CreateThinVolume(ctx context.Context, vg, thinPool, name string, sizeGB int) error
	DeleteThinVolume(ctx context.Context, vg, name string) error
	ListThinVolumes(ctx context.Context, vg, thinPool string) ([]host.ThinVolume, error)
}

// VMController starts and stops QEMU processes and reports their VNC sockets.
type VMController interface {
	Start(ctx context.Context, vm store.VM) error
	Stop(ctx context.Context, vmID string) error
	Reset(ctx context.Context, vmID string) error
	SendCtrlAltDel(ctx context.Context, vmID string) error
	Running(vmID string) bool
	VNCSocket(vmID string) string
	Metrics(vmID string) (qemu.Metrics, error)
}

// NetworkProvisioner manages VPP bridge domains, vhost-user interfaces, and
// DHCP tap interfaces.
type NetworkProvisioner interface {
	CreateBridgeDomain(ctx context.Context, name string, reserved []uint32) (uint32, error)
	EnsureBridgeDomain(ctx context.Context, bdID uint32, name string) error
	DeleteBridgeDomain(ctx context.Context, bdID uint32) error
	CreateVhostUser(ctx context.Context, name, mac string, bdID uint32) (socketPath string, swIfIndex uint32, err error)
	EnsureVhostUser(ctx context.Context, name, mac string, bdID uint32) (socketPath string, swIfIndex uint32, err error)
	DeleteVhostUser(ctx context.Context, swIfIndex uint32) error
	CreateTap(ctx context.Context, hostName, gatewayCIDR string, bdID uint32) (uint32, error)
	DeleteTap(ctx context.Context, swIfIndex uint32) error
	Ping(ctx context.Context) error
	Identity() (string, error)
	InterfaceCounters(ctx context.Context, names []string) (map[string]host.InterfaceCounters, error)
	EnsureVPPACL(ctx context.Context, aclID int64, rules []store.ACLRule) (uint32, error)
	DeleteVPPACL(ctx context.Context, index uint32) error
	SetInterfaceACLs(ctx context.Context, swIfIndex uint32, ingress, egress uint32) error
}

// NAT44Provisioner configures VPP NAT44 and the BVI/outside interfaces.
type NAT44Provisioner interface {
	EnsureBVI(ctx context.Context, networkID int64, bdID uint32) (string, uint32, error)
	DeleteBVIByName(ctx context.Context, name string) error
	AddInterfaceIPv4(ctx context.Context, name, cidr string) error
	EnsureHostInterface(ctx context.Context, nic string) (string, error)
	ApplyNAT44(ctx context.Context, bviName, outsideName, outsideAddress, outsideGateway, pool string) error
	RemoveNAT44(ctx context.Context, bviName, outsideName, pool string) error
	ApplyNAT44StaticMappings(ctx context.Context, outsideName string, vnfID int64, rules []store.DNATRule) error
	RemoveNAT44StaticMappings(ctx context.Context, outsideName string, vnfID int64) error
	NAT44Sessions(ctx context.Context) ([]string, error)
}

// VNFManager runs type-specific network functions (DHCPv4/DHCPv6) per VNF.
type VNFManager interface {
	Start(ctx context.Context, vnf store.VNF, hostIf string) error
	Stop(ctx context.Context, vnfID int64, removeLeases bool) error
	Running(vnfID int64) bool
	Leases(ctx context.Context, vnfID int64) ([]store.VNFLease, error)
	CleanupLegacyNetwork(networkID int64)
	ResetRuntime(ctx context.Context) error
}

// Server holds the dependencies shared by the HTTP handlers.
type Server struct {
	store     *store.Store
	readiness Readiness
	disks     DiskProvisioner
	vms       VMController
	networks  NetworkProvisioner
	vnfs      VNFManager
	nat       NAT44Provisioner
	logger    *slog.Logger
	handler   http.Handler

	natMu      sync.Mutex
	natRunning map[int64]bool

	// resourceMu serializes host provisioning (disks, bridge domains, vhost
	// interfaces) with VM attachment so resources cannot be removed mid-operation.
	resourceMu sync.Mutex

	// runMu serializes start/stop requests for a consistent runtime state.
	runMu sync.Mutex
}

// New returns the HTTP handler for the API and UI.
func New(st *store.Store, ready Readiness, disks DiskProvisioner, vms VMController, networks NetworkProvisioner, vnfs VNFManager, nat NAT44Provisioner, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{store: st, readiness: ready, disks: disks, vms: vms, networks: networks, vnfs: vnfs, nat: nat, logger: logger, natRunning: make(map[int64]bool)}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", s.handleHealth)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/vms", s.handleListVMs)
	mux.HandleFunc("POST /api/vms", s.handleCreateVM)
	mux.HandleFunc("GET /api/vms/{id}", s.handleGetVM)
	mux.HandleFunc("GET /api/vms/{id}/metrics", s.handleGetVMMetrics)
	mux.HandleFunc("PUT /api/vms/{id}", s.handleUpdateVM)
	mux.HandleFunc("DELETE /api/vms/{id}", s.handleDeleteVM)
	mux.HandleFunc("POST /api/vms/{id}/start", s.handleStartVM)
	mux.HandleFunc("POST /api/vms/{id}/stop", s.handleStopVM)
	mux.HandleFunc("POST /api/vms/{id}/reset", s.handleResetVM)
	mux.HandleFunc("POST /api/vms/{id}/ctrl-alt-del", s.handleVMCtrlAltDel)
	mux.HandleFunc("GET /ws/vms/{id}/vnc", s.handleVNCProxy)
	mux.HandleFunc("GET /api/storage-backends", s.handleListStorageBackends)
	mux.HandleFunc("POST /api/storage-backends", s.handleCreateStorageBackend)
	mux.HandleFunc("GET /api/storage-backends/{id}", s.handleGetStorageBackend)
	mux.HandleFunc("PUT /api/storage-backends/{id}", s.handleUpdateStorageBackend)
	mux.HandleFunc("DELETE /api/storage-backends/{id}", s.handleDeleteStorageBackend)
	mux.HandleFunc("GET /api/storage-backends/{id}/importable-disks", s.handleListImportableDisks)
	mux.HandleFunc("POST /api/storage-backends/{id}/import-disks", s.handleImportDisks)
	mux.HandleFunc("GET /api/disks", s.handleListDisks)
	mux.HandleFunc("POST /api/disks", s.handleCreateDisk)
	mux.HandleFunc("GET /api/disks/{id}", s.handleGetDisk)
	mux.HandleFunc("DELETE /api/disks/{id}", s.handleDeleteDisk)
	mux.HandleFunc("GET /api/media", s.handleListMedia)
	mux.HandleFunc("POST /api/media", s.handleCreateMedia)
	mux.HandleFunc("GET /api/media/{id}", s.handleGetMedia)
	mux.HandleFunc("DELETE /api/media/{id}", s.handleDeleteMedia)
	mux.HandleFunc("GET /api/networks", s.handleListNetworks)
	mux.HandleFunc("POST /api/networks", s.handleCreateNetwork)
	mux.HandleFunc("GET /api/networks/{id}", s.handleGetNetwork)
	mux.HandleFunc("DELETE /api/networks/{id}", s.handleDeleteNetwork)
	mux.HandleFunc("GET /api/vnfs", s.handleListVNFs)
	mux.HandleFunc("POST /api/vnfs", s.handleCreateVNF)
	mux.HandleFunc("GET /api/vnfs/{id}", s.handleGetVNF)
	mux.HandleFunc("PUT /api/vnfs/{id}", s.handleUpdateVNF)
	mux.HandleFunc("DELETE /api/vnfs/{id}", s.handleDeleteVNF)
	mux.HandleFunc("POST /api/vnfs/{id}/restart", s.handleRestartVNF)
	mux.HandleFunc("GET /api/vnfs/{id}/leases", s.handleListVNFLeases)
	mux.HandleFunc("GET /api/vnfs/{id}/sessions", s.handleListVNFSessions)
	mux.HandleFunc("GET /api/host/interfaces", s.handleListHostInterfaces)
	mux.HandleFunc("GET /api/acls", s.handleListACLs)
	mux.HandleFunc("POST /api/acls", s.handleCreateACL)
	mux.HandleFunc("GET /api/acls/{id}", s.handleGetACL)
	mux.HandleFunc("PUT /api/acls/{id}", s.handleUpdateACL)
	mux.HandleFunc("DELETE /api/acls/{id}", s.handleDeleteACL)
	mux.HandleFunc("GET /api/interfaces", s.handleListInterfaces)
	mux.HandleFunc("POST /api/interfaces", s.handleCreateInterface)
	mux.HandleFunc("GET /api/interfaces/{id}", s.handleGetInterface)
	mux.HandleFunc("PUT /api/interfaces/{id}", s.handleUpdateInterface)
	mux.HandleFunc("PUT /api/interfaces/{id}/acls", s.handleSetInterfaceACLs)
	mux.HandleFunc("DELETE /api/interfaces/{id}", s.handleDeleteInterface)
	mux.HandleFunc("/api", s.handleAPINotFound)
	mux.HandleFunc("/api/", s.handleAPINotFound)
	mux.Handle("/", webui.Handler())

	var handler http.Handler = mux
	handler = s.recoverPanic(handler)
	handler = securityHeaders(handler)
	handler = s.logRequests(handler)
	s.handler = handler
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.store.Ping(ctx); err != nil {
		s.logger.Error("health check failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.readiness.Status())
}

func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	if allow, ok := allowedMethods(r.URL.Path); ok {
		w.Header().Set("Allow", allow)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

// allowedMethods reports whether path is a known API route. Requests that
// reach the fallback with a known path can only have an unsupported method,
// because method-specific patterns are registered for the valid ones.
func allowedMethods(path string) (string, bool) {
	switch {
	case path == "/api/healthz":
		return "GET, HEAD", true
	case path == "/api/status":
		return "GET, HEAD", true
	case path == "/api/vms":
		return "GET, HEAD, POST", true
	case strings.HasPrefix(path, "/api/vms/"):
		rest := strings.TrimPrefix(path, "/api/vms/")
		parts := strings.Split(rest, "/")
		if len(parts) == 1 {
			if _, err := uuid.Parse(parts[0]); err == nil {
				return "GET, HEAD, PUT, DELETE", true
			}
		}
		if len(parts) == 2 {
			if _, err := uuid.Parse(parts[0]); err == nil && parts[1] == "metrics" {
				return "GET, HEAD", true
			}
		}
	case path == "/api/storage-backends":
		return "GET, HEAD, POST", true
	case strings.HasPrefix(path, "/api/storage-backends/"):
		id := strings.TrimPrefix(path, "/api/storage-backends/")
		if id == "" || strings.Contains(id, "/") {
			return "", false
		}
		if parsed, err := strconv.ParseInt(id, 10, 64); err == nil && parsed > 0 {
			return "GET, HEAD, PUT, DELETE", true
		}
	case path == "/api/disks":
		return "GET, HEAD, POST", true
	case strings.HasPrefix(path, "/api/disks/"):
		id := strings.TrimPrefix(path, "/api/disks/")
		if id == "" || strings.Contains(id, "/") {
			return "", false
		}
		if parsed, err := strconv.ParseInt(id, 10, 64); err == nil && parsed > 0 {
			return "GET, HEAD, DELETE", true
		}
	case path == "/api/media":
		return "GET, HEAD, POST", true
	case strings.HasPrefix(path, "/api/media/"):
		id := strings.TrimPrefix(path, "/api/media/")
		if id == "" || strings.Contains(id, "/") {
			return "", false
		}
		if parsed, err := strconv.ParseInt(id, 10, 64); err == nil && parsed > 0 {
			return "GET, HEAD, DELETE", true
		}
	case path == "/api/acls":
		return "GET, HEAD, POST", true
	case strings.HasPrefix(path, "/api/acls/"):
		id := strings.TrimPrefix(path, "/api/acls/")
		if id == "" || strings.Contains(id, "/") {
			return "", false
		}
		if parsed, err := strconv.ParseInt(id, 10, 64); err == nil && parsed > 0 {
			return "GET, HEAD, PUT, DELETE", true
		}
	case path == "/api/networks":
		return "GET, HEAD, POST", true
	case strings.HasPrefix(path, "/api/networks/"):
		rest := strings.TrimPrefix(path, "/api/networks/")
		parts := strings.Split(rest, "/")
		if len(parts) == 1 {
			if parsed, err := strconv.ParseInt(parts[0], 10, 64); err == nil && parsed > 0 {
				return "GET, HEAD, DELETE", true
			}
		}
	case path == "/api/host/interfaces":
		return "GET, HEAD", true
	case path == "/api/vnfs":
		return "GET, HEAD, POST", true
	case strings.HasPrefix(path, "/api/vnfs/"):
		rest := strings.TrimPrefix(path, "/api/vnfs/")
		parts := strings.Split(rest, "/")
		if len(parts) == 1 {
			if parsed, err := strconv.ParseInt(parts[0], 10, 64); err == nil && parsed > 0 {
				return "GET, HEAD, PUT, DELETE", true
			}
		}
		if len(parts) == 2 {
			if parsed, err := strconv.ParseInt(parts[0], 10, 64); err == nil && parsed > 0 {
				switch parts[1] {
				case "leases", "sessions":
					return "GET, HEAD", true
				case "restart":
					return "POST", true
				}
			}
		}
	case path == "/api/interfaces":
		return "GET, HEAD, POST", true
	case strings.HasPrefix(path, "/api/interfaces/"):
		rest := strings.TrimPrefix(path, "/api/interfaces/")
		parts := strings.Split(rest, "/")
		if len(parts) == 1 {
			if parsed, err := strconv.ParseInt(parts[0], 10, 64); err == nil && parsed > 0 {
				return "GET, HEAD, PUT, DELETE", true
			}
		}
		if len(parts) == 2 && parts[1] == "acls" {
			if parsed, err := strconv.ParseInt(parts[0], 10, 64); err == nil && parsed > 0 {
				return "PUT", true
			}
		}
	}
	return "", false
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("request failed",
		"method", r.Method,
		"path", r.URL.Path,
		"error", err,
	)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	const maxBodyBytes = 1 << 20

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return errors.New("request body is too large")
		case errors.Is(err, io.EOF):
			return errors.New("request body is empty")
		default:
			return fmt.Errorf("invalid JSON: %v", err)
		}
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}

	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; "+
				"img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self' ws: wss:")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// Hijack lets WebSocket upgrades take over the connection.
func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	if r.status == 0 {
		r.status = http.StatusSwitchingProtocols
	}
	return hijacker.Hijack()
}

// Flush forwards streaming responses.
func (r *responseRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}

		next.ServeHTTP(recorder, r)

		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"bytes", recorder.bytes,
			"duration", time.Since(start).Round(time.Microsecond),
		)
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}

			s.logger.Error("panic serving request",
				"method", r.Method,
				"path", r.URL.Path,
				"panic", recovered,
				"stack", string(debug.Stack()),
			)
			writeError(w, http.StatusInternalServerError, "internal server error")
		}()

		next.ServeHTTP(w, r)
	})
}
