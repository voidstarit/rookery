// Package api exposes the HTTP API and embedded management UI.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
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
	CreateMemif(ctx context.Context, hostName, socketPath string, ifID uint32, mac net.HardwareAddr, bdID uint32) (uint32, error)
	DeleteMemif(ctx context.Context, swIfIndex uint32) error
	DeleteMemifByName(ctx context.Context, hostName string) error
	DeleteTap(ctx context.Context, swIfIndex uint32) error
	Ping(ctx context.Context) error
	Identity() (string, error)
	InterfaceCounters(ctx context.Context, names []string) (map[string]host.InterfaceCounters, error)
	VPPNodeGraph(ctx context.Context) (host.VPPGraph, error)
	ACLInterfaceCounters(ctx context.Context) ([]host.ACLInterfaceCounters, error)
	VPPInterfaces(ctx context.Context) ([]host.VPPInterface, error)
	StartPacketCapture(ctx context.Context, options host.PacketCaptureOptions) (host.PacketCaptureStatus, error)
	StopPacketCapture(ctx context.Context) (host.PacketCaptureStatus, error)
	PacketCaptureStatus(ctx context.Context) (host.PacketCaptureStatus, error)
	ListPacketCaptures() ([]host.PacketCaptureFile, error)
	OpenPacketCapture(name string) (io.ReadSeekCloser, host.PacketCaptureFile, error)
	DeletePacketCapture(name string) error
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
	StartMemif(ctx context.Context, vnf store.VNF, socketPath string, ifID uint32) error
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

	memifDHCP bool
	memifDir  string

	// resourceMu serializes host provisioning (disks, bridge domains, vhost
	// interfaces) with VM attachment so resources cannot be removed mid-operation.
	resourceMu sync.Mutex

	// runMu serializes start/stop requests for a consistent runtime state.
	runMu sync.Mutex
}

// RunMutex returns the lock that serializes VM lifecycle operations. The
// grpc-gateway shares it while both listeners are active.
func (s *Server) RunMutex() *sync.Mutex {
	return &s.runMu
}

// ResourceMutex returns the lock that serializes host resource provisioning.
func (s *Server) ResourceMutex() *sync.Mutex {
	return &s.resourceMu
}

// ConfigureMemifDHCP enables the memif transport for DHCPv4 VNFs and sets the
// directory where VPP creates memif socket files.
func (s *Server) ConfigureMemifDHCP(enabled bool, dir string) {
	s.memifDHCP = enabled
	s.memifDir = dir
}

func newServer(st *store.Store, ready Readiness, disks DiskProvisioner, vms VMController, networks NetworkProvisioner, vnfs VNFManager, nat NAT44Provisioner, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{store: st, readiness: ready, disks: disks, vms: vms, networks: networks, vnfs: vnfs, nat: nat, logger: logger, natRunning: make(map[int64]bool)}
}

// New returns the API server with only the protocol-native routes used by
// serve.
func New(st *store.Store, ready Readiness, disks DiskProvisioner, vms VMController, networks NetworkProvisioner, vnfs VNFManager, nat NAT44Provisioner, logger *slog.Logger) *Server {
	s := newServer(st, ready, disks, vms, networks, vnfs, nat, logger)
	s.handler = s.NativeHandler()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
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

// SecurityHeaders applies the API's standard security headers. It is exported
// for the grpc-gateway listener, which uses the same browser-facing policy.
func SecurityHeaders(next http.Handler) http.Handler {
	return securityHeaders(next)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webshark := strings.HasPrefix(r.URL.Path, "/webshark")
		if webshark {
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'self'; "+
					"img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; "+
					"script-src 'self' 'wasm-unsafe-eval' 'unsafe-inline' 'unsafe-eval'; "+
					"worker-src 'self' blob:; child-src 'self' blob:; frame-src 'self'; "+
					"media-src 'self' blob:; font-src 'self' data:; connect-src 'self' ws: wss:")
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		} else {
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; "+
					"img-src 'self' data:; style-src 'self'; script-src 'self'; "+
					"connect-src 'self' ws: wss:; frame-src 'self'")
			w.Header().Set("X-Frame-Options", "DENY")
		}
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
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
