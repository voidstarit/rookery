// Package vnfd implements the control protocol between the API server and the
// separate VNF daemon (cmd/vnfs). The API owns VPP provisioning and the
// database; the daemon owns the long-running userspace runtimes (CoreDHCP,
// gVisor memif stacks, GoBGP) so API restarts do not interrupt them.
package vnfd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

// Manager is the userspace VNF runtime run by the daemon. *host.VNFManager
// implements it; tests use a fake.
type Manager interface {
	Start(ctx context.Context, vnf store.VNF, hostIf string) error
	StartMemif(ctx context.Context, vnf store.VNF, socketPath string, ifID uint32) error
	StartShared(ctx context.Context, vnf store.VNF, socketPath string, ifID uint32, address, ipv6Address string) error
	Stop(ctx context.Context, vnfID int64, removeLeases bool) error
	Running(vnfID int64) bool
	Healthy(vnfID int64) bool
	Reload(ctx context.Context, vnf store.VNF) error
	Leases(ctx context.Context, vnfID int64) ([]store.VNFLease, error)
	RemoveLeases(ctx context.Context, vnfID int64, clientIDs []string) (int, error)
	DNSRecords(vnfID int64) []host.DNSRecordView
	BGPStatus(ctx context.Context, vnfID int64) (host.BGPStatus, error)
	CleanupLegacyNetwork(networkID int64)
	ResetRuntime(ctx context.Context) error
	Close() error
}

const (
	defaultSuperviseInterval = 5 * time.Second
	defaultRestartBackoff    = 10 * time.Second
	resetGracePeriod         = 60 * time.Second

	runtimeDHCP = "dhcp"
	runtimeDNS  = "dns"
	runtimeBGP  = "bgp"
)

type desiredRuntime struct {
	kind string
	hash string
	spec *rookeryv1.DesiredRuntime
}

// Server implements the DHCPDaemon, DNSDaemon, BGPDaemon, and VNFRuntimeAdmin
// gRPC services. The services share one manager, desired-spec map, and
// supervision loop; only the wire boundaries are separate.
type Server struct {
	rookeryv1.UnimplementedDHCPDaemonServer
	rookeryv1.UnimplementedDNSDaemonServer
	rookeryv1.UnimplementedBGPDaemonServer
	rookeryv1.UnimplementedVNFRuntimeAdminServer

	manager Manager
	dir     string // desired-spec directory
	logger  zerolog.Logger

	mu      sync.Mutex
	desired map[int64]*desiredRuntime
	details map[int64]string
	lastTry map[int64]time.Time

	// opMu serializes mutating runtime operations.
	opMu sync.Mutex

	superviseInterval time.Duration
	restartBackoff    time.Duration
	now               func() time.Time

	pausedUntil time.Time
}

// NewServer returns a daemon server persisting desired specs under dir.
func NewServer(manager Manager, dir string, logger zerolog.Logger) *Server {
	s := &Server{
		manager:           manager,
		dir:               dir,
		logger:            logger,
		desired:           make(map[int64]*desiredRuntime),
		details:           make(map[int64]string),
		lastTry:           make(map[int64]time.Time),
		superviseInterval: defaultSuperviseInterval,
		restartBackoff:    defaultRestartBackoff,
		now:               time.Now,
	}
	return s
}

// Load restores the persisted desired runtimes. Runtimes are not started here;
// call Start or run the supervision loop. Files that do not parse as the
// DesiredRuntime wrapper (including the old single-VNFSpec files) are ignored.
func (s *Server) Load() error {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read VNF state directory: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	restored := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "desired-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			s.logger.Warn().Str("file", name).Err(err).Msg("could not read persisted desired runtime")
			continue
		}
		spec := &rookeryv1.DesiredRuntime{}
		if err := protojson.Unmarshal(data, spec); err != nil {
			s.logger.Warn().Str("file", name).Err(err).Msg("could not parse persisted desired runtime")
			continue
		}
		if err := spec.Validate(); err != nil {
			s.logger.Warn().Str("file", name).Err(err).Msg("ignoring invalid persisted desired runtime")
			continue
		}
		id := desiredID(spec)
		kind := desiredKind(spec)
		if id <= 0 || kind == "" {
			continue
		}
		s.desired[id] = &desiredRuntime{
			kind: kind,
			hash: specHash(desiredMessage(spec)),
			spec: spec,
		}
		restored++
		s.logger.Trace().
			Int64("vnf_id", id).
			Str("type", kind).
			Str("transport", desiredTransport(spec)).
			Msg("restored desired runtime spec")
	}
	if restored > 0 {
		s.logger.Info().Int("vnfs", restored).Msg("restored desired runtime specs")
	}
	return nil
}

// specHash returns the hex SHA-256 of spec's protojson encoding, or an empty
// string when the spec cannot be marshaled.
func specHash(spec proto.Message) string {
	if spec == nil {
		return ""
	}
	data, err := protojson.Marshal(spec)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// specPath returns the file path where the desired runtime for the VNF is stored.
func (s *Server) specPath(id int64) string {
	return filepath.Join(s.dir, fmt.Sprintf("desired-%d.json", id))
}

// persist atomically writes the desired runtime wrapper as indented JSON with
// mode 0600 under the server directory, creating the directory when needed.
func (s *Server) persist(spec *rookeryv1.DesiredRuntime) error {
	id := desiredID(spec)
	if id <= 0 {
		return errors.New("desired runtime has no VNF id")
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := protojson.MarshalOptions{Indent: "  "}.Marshal(spec)
	if err != nil {
		return err
	}
	tmp := s.specPath(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.specPath(id))
}

// forget removes the persisted desired runtime for id, ignoring removal errors.
func (s *Server) forget(id int64) {
	_ = os.Remove(s.specPath(id))
}

// desiredID returns the VNF id inside the wrapper, or zero when no runtime is
// set.
func desiredID(spec *rookeryv1.DesiredRuntime) int64 {
	switch {
	case spec.GetDhcp() != nil:
		return spec.GetDhcp().GetRuntime().GetId()
	case spec.GetDns() != nil:
		return spec.GetDns().GetRuntime().GetId()
	case spec.GetBgp() != nil:
		return spec.GetBgp().GetRuntime().GetId()
	}
	return 0
}

// desiredKind returns the runtime kind stored in the wrapper, or an empty
// string when no runtime is set.
func desiredKind(spec *rookeryv1.DesiredRuntime) string {
	switch {
	case spec.GetDhcp() != nil:
		return runtimeDHCP
	case spec.GetDns() != nil:
		return runtimeDNS
	case spec.GetBgp() != nil:
		return runtimeBGP
	}
	return ""
}

// desiredTransport returns the transport stored in the wrapper.
func desiredTransport(spec *rookeryv1.DesiredRuntime) string {
	switch {
	case spec.GetDhcp() != nil:
		return spec.GetDhcp().GetRuntime().GetTransport()
	case spec.GetDns() != nil:
		return spec.GetDns().GetRuntime().GetTransport()
	case spec.GetBgp() != nil:
		return spec.GetBgp().GetRuntime().GetTransport()
	}
	return ""
}

// desiredMessage returns the type-specific spec used for the spec hash.
func desiredMessage(spec *rookeryv1.DesiredRuntime) proto.Message {
	switch {
	case spec.GetDhcp() != nil:
		return spec.GetDhcp()
	case spec.GetDns() != nil:
		return spec.GetDns()
	case spec.GetBgp() != nil:
		return spec.GetBgp()
	}
	return nil
}

// desiredSpec returns the persisted wrapper of a desired entry, or nil.
func desiredSpec(desired *desiredRuntime) *rookeryv1.DesiredRuntime {
	if desired == nil {
		return nil
	}
	return desired.spec
}

func wrapDHCP(spec *rookeryv1.DHCPRuntimeSpec) *rookeryv1.DesiredRuntime {
	return &rookeryv1.DesiredRuntime{Runtime: &rookeryv1.DesiredRuntime_Dhcp{Dhcp: spec}}
}

func wrapDNS(spec *rookeryv1.DNSRuntimeSpec) *rookeryv1.DesiredRuntime {
	return &rookeryv1.DesiredRuntime{Runtime: &rookeryv1.DesiredRuntime_Dns{Dns: spec}}
}

func wrapBGP(spec *rookeryv1.BGPRuntimeSpec) *rookeryv1.DesiredRuntime {
	return &rookeryv1.DesiredRuntime{Runtime: &rookeryv1.DesiredRuntime_Bgp{Bgp: spec}}
}

// EnsureDHCP starts a DHCP runtime, or restarts it when the spec changed.
func (s *Server) EnsureDHCP(ctx context.Context, request *rookeryv1.EnsureDHCPRequest) (*rookeryv1.VNFState, error) {
	spec, err := validDHCPRuntime(request.GetSpec())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return s.ensure(ctx, spec.GetRuntime(), runtimeDHCP, wrapDHCP(spec), func(ctx context.Context) error {
		return s.startDHCP(ctx, spec)
	})
}

// EnsureDNS starts a DNS runtime, or restarts it when the spec changed.
func (s *Server) EnsureDNS(ctx context.Context, request *rookeryv1.EnsureDNSRequest) (*rookeryv1.VNFState, error) {
	spec, err := validDNSRuntime(request.GetSpec())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return s.ensure(ctx, spec.GetRuntime(), runtimeDNS, wrapDNS(spec), func(ctx context.Context) error {
		return s.startDNS(ctx, spec)
	})
}

// EnsureBGP starts a BGP runtime, or restarts it when the spec changed.
func (s *Server) EnsureBGP(ctx context.Context, request *rookeryv1.EnsureBGPRequest) (*rookeryv1.VNFState, error) {
	spec, err := validBGPRuntime(request.GetSpec())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return s.ensure(ctx, spec.GetRuntime(), runtimeBGP, wrapBGP(spec), func(ctx context.Context) error {
		return s.startBGP(ctx, spec)
	})
}

// ensure is the shared idempotent start path for every runtime type. It stops
// and restarts an existing runtime only when the type or spec hash changed.
func (s *Server) ensure(ctx context.Context, runtime *rookeryv1.RuntimeSpec, kind string, spec *rookeryv1.DesiredRuntime, start func(context.Context) error) (*rookeryv1.VNFState, error) {
	id := runtime.GetId()
	hash := specHash(desiredMessage(spec))
	s.logger.Trace().
		Int64("vnf_id", id).
		Str("type", kind).
		Str("transport", runtime.GetTransport()).
		Str("tap", runtime.GetTapHostName()).
		Str("memif", runtime.GetMemifSocket()).
		Uint32("interface_id", runtime.GetMemifSwIfIndex()).
		Msg("ensure VNF runtime requested")

	s.opMu.Lock()
	defer s.opMu.Unlock()

	s.mu.Lock()
	current := s.desired[id]
	running := s.manager.Running(id)
	s.mu.Unlock()
	if current != nil && current.kind == kind && current.hash == hash && running && s.manager.Healthy(id) {
		s.logger.Trace().Int64("vnf_id", id).Msg("VNF runtime is unchanged")
		return s.state(id), nil
	}

	if running {
		s.logger.Trace().Int64("vnf_id", id).Msg("VNF runtime spec changed; stopping runtime")
		if err := s.manager.Stop(ctx, id, false); err != nil {
			s.setDetail(id, err.Error())
			return nil, status.Error(codes.Internal, "could not restart VNF: "+err.Error())
		}
	}

	if err := start(ctx); err != nil {
		s.setDetail(id, err.Error())
		return nil, status.Error(codes.Internal, "could not start VNF: "+err.Error())
	}
	if err := s.persist(spec); err != nil {
		s.logger.Warn().Int64("vnf_id", id).Err(err).Msg("could not persist desired runtime")
	}
	s.mu.Lock()
	s.desired[id] = &desiredRuntime{kind: kind, hash: hash, spec: spec}
	s.details[id] = ""
	s.lastTry[id] = s.now()
	s.mu.Unlock()
	return s.state(id), nil
}

// startDHCP dispatches a DHCP spec to the manager by transport.
func (s *Server) startDHCP(ctx context.Context, spec *rookeryv1.DHCPRuntimeSpec) error {
	vnf, err := storeVNFFromDHCP(spec)
	if err != nil {
		return err
	}
	runtime := spec.GetRuntime()
	switch transport := runtime.GetTransport(); transport {
	case "shared":
		if strings.TrimSpace(runtime.GetMemifSocket()) == "" {
			return errors.New("shared memif socket path is required")
		}
		if strings.TrimSpace(runtime.GetSharedAddress()) == "" {
			return errors.New("shared memif address is required")
		}
		return s.manager.StartShared(ctx, vnf, runtime.GetMemifSocket(), runtime.GetMemifSwIfIndex(), runtime.GetSharedAddress(), runtime.GetSharedIpv6Address())
	case "memif", "dedicated":
		if strings.TrimSpace(runtime.GetMemifSocket()) == "" {
			return errors.New("memif socket path is required")
		}
		return s.manager.StartMemif(ctx, vnf, runtime.GetMemifSocket(), runtime.GetMemifSwIfIndex())
	case "tap":
		if strings.TrimSpace(runtime.GetTapHostName()) == "" {
			return errors.New("tap interface name is required")
		}
		return s.manager.Start(ctx, vnf, runtime.GetTapHostName())
	default:
		return fmt.Errorf("unsupported DHCP transport %q", transport)
	}
}

// startDNS starts a DNS runtime on its network's shared memif.
func (s *Server) startDNS(ctx context.Context, spec *rookeryv1.DNSRuntimeSpec) error {
	vnf, err := storeVNFFromDNS(spec)
	if err != nil {
		return err
	}
	runtime := spec.GetRuntime()
	if runtime.GetTransport() != "shared" {
		return errors.New("DNS VNFs must use the shared memif transport")
	}
	if strings.TrimSpace(runtime.GetMemifSocket()) == "" {
		return errors.New("shared memif socket path is required")
	}
	if strings.TrimSpace(runtime.GetSharedAddress()) == "" {
		return errors.New("shared memif address is required")
	}
	return s.manager.StartShared(ctx, vnf, runtime.GetMemifSocket(), runtime.GetMemifSwIfIndex(), runtime.GetSharedAddress(), runtime.GetSharedIpv6Address())
}

// startBGP dispatches a BGP spec to the manager by transport.
func (s *Server) startBGP(ctx context.Context, spec *rookeryv1.BGPRuntimeSpec) error {
	vnf, err := storeVNFFromBGP(spec)
	if err != nil {
		return err
	}
	runtime := spec.GetRuntime()
	switch transport := runtime.GetTransport(); transport {
	case "memif", "dedicated":
		if strings.TrimSpace(runtime.GetMemifSocket()) == "" {
			return errors.New("memif socket path is required")
		}
		return s.manager.StartMemif(ctx, vnf, runtime.GetMemifSocket(), runtime.GetMemifSwIfIndex())
	case "tap":
		if strings.TrimSpace(runtime.GetTapHostName()) == "" {
			return errors.New("tap interface name is required")
		}
		return s.manager.Start(ctx, vnf, runtime.GetTapHostName())
	default:
		return fmt.Errorf("unsupported BGP transport %q", transport)
	}
}

// ReloadDHCP swaps a running DHCP runtime's configuration in place.
func (s *Server) ReloadDHCP(ctx context.Context, request *rookeryv1.ReloadDHCPRequest) (*rookeryv1.VNFState, error) {
	spec, err := validDHCPRuntime(request.GetSpec())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return s.reload(ctx, spec.GetRuntime().GetId(), runtimeDHCP, func(current *rookeryv1.DesiredRuntime) (store.VNF, *rookeryv1.DesiredRuntime, error) {
		merged := mergeDHCP(current, spec)
		vnf, err := storeVNFFromDHCP(merged)
		if err != nil {
			return store.VNF{}, nil, err
		}
		return vnf, wrapDHCP(merged), nil
	})
}

// ReloadDNS swaps a running DNS runtime's records and forwarders in place.
func (s *Server) ReloadDNS(ctx context.Context, request *rookeryv1.ReloadDNSRequest) (*rookeryv1.VNFState, error) {
	spec, err := validDNSRuntime(request.GetSpec())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return s.reload(ctx, spec.GetRuntime().GetId(), runtimeDNS, func(current *rookeryv1.DesiredRuntime) (store.VNF, *rookeryv1.DesiredRuntime, error) {
		merged := mergeDNS(current, spec)
		vnf, err := storeVNFFromDNS(merged)
		if err != nil {
			return store.VNF{}, nil, err
		}
		return vnf, wrapDNS(merged), nil
	})
}

// ReloadBGP withdraws and re-advertises a running BGP runtime's prefix set.
func (s *Server) ReloadBGP(ctx context.Context, request *rookeryv1.ReloadBGPRequest) (*rookeryv1.VNFState, error) {
	spec, err := validBGPRuntime(request.GetSpec())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return s.reload(ctx, spec.GetRuntime().GetId(), runtimeBGP, func(current *rookeryv1.DesiredRuntime) (store.VNF, *rookeryv1.DesiredRuntime, error) {
		merged := mergeBGP(current, spec)
		vnf, err := storeVNFFromBGP(merged)
		if err != nil {
			return store.VNF{}, nil, err
		}
		return vnf, wrapBGP(merged), nil
	})
}

// reload is the shared in-place reload path. The desired spec is updated only
// after the manager succeeds, so a failure leaves the previous spec in place. A
// reload of a runtime that is not running returns running=false without
// starting it.
func (s *Server) reload(ctx context.Context, id int64, kind string, prepare func(*rookeryv1.DesiredRuntime) (store.VNF, *rookeryv1.DesiredRuntime, error)) (*rookeryv1.VNFState, error) {
	s.logger.Trace().Int64("vnf_id", id).Str("type", kind).Msg("reload VNF runtime requested")
	s.opMu.Lock()
	defer s.opMu.Unlock()

	s.mu.Lock()
	current := s.desired[id]
	s.mu.Unlock()
	if current == nil {
		return nil, status.Error(codes.NotFound, "vnf is not managed by the daemon")
	}
	if current.kind != kind {
		return nil, status.Error(codes.NotFound, "vnf is not managed by this daemon service")
	}
	vnf, spec, err := prepare(current.spec)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if !s.manager.Running(id) {
		// Persist the new spec even though the runtime is stopped, so supervision
		// or a manual restart starts the configuration the API just committed
		// instead of the previous one.
		s.storeDesired(id, kind, spec)
		s.logger.Trace().Int64("vnf_id", id).Msg("VNF runtime is not running; persisted the new desired spec")
		return s.state(id), nil
	}
	if err := s.manager.Reload(ctx, vnf); err != nil {
		s.logger.Trace().Int64("vnf_id", id).Err(err).Msg("reload VNF runtime failed")
		s.setDetail(id, err.Error())
		return nil, status.Error(codes.Internal, "could not reload VNF: "+err.Error())
	}
	s.storeDesired(id, kind, spec)
	s.logger.Trace().Int64("vnf_id", id).Msg("VNF runtime reloaded")
	return s.state(id), nil
}

// storeDesired persists a prepared desired spec and publishes it in memory. A
// persistence failure is logged and does not fail the runtime operation; the
// in-memory spec still lets the supervisor restart the new configuration.
func (s *Server) storeDesired(id int64, kind string, spec *rookeryv1.DesiredRuntime) {
	if err := s.persist(spec); err != nil {
		s.logger.Warn().Int64("vnf_id", id).Err(err).Msg("could not persist desired runtime")
	}
	s.mu.Lock()
	s.desired[id] = &desiredRuntime{kind: kind, hash: specHash(desiredMessage(spec)), spec: spec}
	s.details[id] = ""
	s.mu.Unlock()
}

// RemoveDHCP stops a DHCP runtime and forgets its desired spec.
func (s *Server) RemoveDHCP(ctx context.Context, request *rookeryv1.RemoveDHCPRequest) (*emptypb.Empty, error) {
	return s.remove(ctx, request.GetId(), runtimeDHCP, request.GetRemoveLeases())
}

// RemoveDNS stops a DNS runtime and forgets its desired spec.
func (s *Server) RemoveDNS(ctx context.Context, request *rookeryv1.RemoveDNSRequest) (*emptypb.Empty, error) {
	return s.remove(ctx, request.GetId(), runtimeDNS, false)
}

// RemoveBGP stops a BGP runtime and forgets its desired spec.
func (s *Server) RemoveBGP(ctx context.Context, request *rookeryv1.RemoveBGPRequest) (*emptypb.Empty, error) {
	return s.remove(ctx, request.GetId(), runtimeBGP, false)
}

// remove stops a runtime and forgets its desired spec. The stored runtime kind
// must match the service that received the call.
func (s *Server) remove(ctx context.Context, id int64, kind string, removeLeases bool) (*emptypb.Empty, error) {
	if id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "vnf id is required")
	}
	s.logger.Trace().Int64("vnf_id", id).Bool("remove_leases", removeLeases).Msg("remove VNF runtime requested")
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	desired := s.desired[id]
	s.mu.Unlock()
	if desired == nil || desired.kind != kind {
		return nil, status.Error(codes.NotFound, "vnf is not managed by this daemon service")
	}
	if err := s.manager.Stop(ctx, id, removeLeases); err != nil {
		s.setDetail(id, err.Error())
		return nil, status.Error(codes.Internal, "could not stop VNF: "+err.Error())
	}
	s.mu.Lock()
	delete(s.desired, id)
	delete(s.details, id)
	delete(s.lastTry, id)
	s.mu.Unlock()
	s.forget(id)
	s.logger.Trace().Int64("vnf_id", id).Msg("VNF runtime removed")
	return &emptypb.Empty{}, nil
}

// GetDHCPState reports one desired DHCP runtime.
func (s *Server) GetDHCPState(_ context.Context, request *rookeryv1.GetDHCPStateRequest) (*rookeryv1.VNFState, error) {
	return s.getState(request.GetId(), runtimeDHCP)
}

// GetDNSState reports one desired DNS runtime.
func (s *Server) GetDNSState(_ context.Context, request *rookeryv1.GetDNSStateRequest) (*rookeryv1.VNFState, error) {
	return s.getState(request.GetId(), runtimeDNS)
}

// GetBGPState reports one desired BGP runtime.
func (s *Server) GetBGPState(_ context.Context, request *rookeryv1.GetBGPStateRequest) (*rookeryv1.VNFState, error) {
	return s.getState(request.GetId(), runtimeBGP)
}

// getState reports one managed runtime of the requested type.
func (s *Server) getState(id int64, kind string) (*rookeryv1.VNFState, error) {
	s.mu.Lock()
	desired := s.desired[id]
	s.mu.Unlock()
	if desired == nil || desired.kind != kind {
		s.logger.Trace().Int64("vnf_id", id).Str("type", kind).Msg("runtime state requested for an unknown VNF")
		return nil, status.Error(codes.NotFound, "vnf is not managed by the daemon")
	}
	s.logger.Trace().Int64("vnf_id", id).Str("type", kind).Msg("runtime state requested")
	return s.state(id), nil
}

// state snapshots id's remembered detail together with whether the manager
// reports the runtime both running and healthy.
func (s *Server) state(id int64) *rookeryv1.VNFState {
	s.mu.Lock()
	detail := s.details[id]
	s.mu.Unlock()
	running := s.manager.Running(id) && s.manager.Healthy(id)
	return &rookeryv1.VNFState{Id: id, Running: running, Detail: detail}
}

// setDetail records a human-readable detail string for the VNF, replacing any
// previous value under the server mutex.
func (s *Server) setDetail(id int64, detail string) {
	s.mu.Lock()
	s.details[id] = detail
	s.mu.Unlock()
}

// ResetRuntime stops every runtime but keeps the desired specs. Supervision
// pauses briefly so the API can rebuild VPP resources before runtimes restart.
func (s *Server) ResetRuntime(ctx context.Context, _ *rookeryv1.ResetRuntimeRequest) (*emptypb.Empty, error) {
	s.logger.Trace().Msg("reset VNF runtime requested")
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.manager.ResetRuntime(ctx); err != nil {
		return nil, status.Error(codes.Internal, "could not reset VNF runtime: "+err.Error())
	}
	s.mu.Lock()
	s.pausedUntil = s.now().Add(resetGracePeriod)
	pausedUntil := s.pausedUntil
	s.mu.Unlock()
	s.logger.Trace().Time("supervision_resumes", pausedUntil).Msg("VNF runtimes reset")
	return &emptypb.Empty{}, nil
}

// CleanupLegacyNetwork removes files left by the legacy per-network DHCP.
func (s *Server) CleanupLegacyNetwork(_ context.Context, request *rookeryv1.CleanupLegacyNetworkRequest) (*emptypb.Empty, error) {
	s.logger.Trace().Int64("network_id", request.GetNetworkId()).Msg("cleanup legacy network requested")
	s.manager.CleanupLegacyNetwork(request.GetNetworkId())
	return &emptypb.Empty{}, nil
}

// ListDHCPLeases returns the persisted DHCP leases for one VNF.
func (s *Server) ListDHCPLeases(ctx context.Context, request *rookeryv1.ListDHCPLeasesRequest) (*rookeryv1.ListDHCPLeasesResponse, error) {
	if err := s.requireRuntime(request.GetId(), runtimeDHCP); err != nil {
		return nil, err
	}
	leases, err := s.manager.Leases(ctx, request.GetId())
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read VNF leases: "+err.Error())
	}
	s.logger.Trace().Int64("vnf_id", request.GetId()).Int("leases", len(leases)).Msg("listing DHCP leases")
	response := &rookeryv1.ListDHCPLeasesResponse{
		VnfId:  request.GetId(),
		Leases: make([]*rookeryv1.VNFLease, 0, len(leases)),
	}
	for _, lease := range leases {
		item := &rookeryv1.VNFLease{
			ClientId: lease.ClientID, Address: lease.Address, Hostname: lease.Hostname, Active: lease.Active,
		}
		if !lease.ExpiresAt.IsZero() {
			item.ExpiresAt = timestamppb.New(lease.ExpiresAt)
		}
		response.Leases = append(response.Leases, item)
	}
	return response, nil
}

// RemoveDHCPLeases drops specific DHCP leases from a VNF runtime.
func (s *Server) RemoveDHCPLeases(ctx context.Context, request *rookeryv1.RemoveDHCPLeasesRequest) (*rookeryv1.RemoveDHCPLeasesResponse, error) {
	if request.GetId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "vnf id is required")
	}
	if len(request.GetClientIds()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "client ids are required")
	}
	s.logger.Trace().
		Int64("vnf_id", request.GetId()).
		Int("leases", len(request.GetClientIds())).
		Msg("remove DHCP leases requested")
	s.opMu.Lock()
	defer s.opMu.Unlock()
	removed, err := s.manager.RemoveLeases(ctx, request.GetId(), request.GetClientIds())
	if err != nil {
		return nil, status.Error(codes.Internal, "could not remove VNF leases: "+err.Error())
	}
	return &rookeryv1.RemoveDHCPLeasesResponse{Removed: int64(removed)}, nil
}

// ListDNSRecords returns the dynamic records currently registered by DHCP
// leases for a DNS VNF. Static records are stored by the API.
func (s *Server) ListDNSRecords(_ context.Context, request *rookeryv1.ListDNSRecordsRequest) (*rookeryv1.ListDNSRecordsResponse, error) {
	if err := s.requireRuntime(request.GetId(), runtimeDNS); err != nil {
		return nil, err
	}
	response := &rookeryv1.ListDNSRecordsResponse{VnfId: request.GetId()}
	for _, record := range s.manager.DNSRecords(request.GetId()) {
		response.Records = append(response.Records, &rookeryv1.DNSRecordView{
			Name: record.Name, Type: record.Type, Value: record.Value, Ttl: record.TTL,
			Source: record.Source, SourceVnfId: record.SourceVNFID,
		})
	}
	return response, nil
}

// GetBGPStatus returns the live GoBGP session and RIB snapshot.
func (s *Server) GetBGPStatus(ctx context.Context, request *rookeryv1.GetBGPStatusRequest) (*rookeryv1.BGPStatus, error) {
	if err := s.requireRuntime(request.GetId(), runtimeBGP); err != nil {
		return nil, err
	}
	snapshot, err := s.manager.BGPStatus(ctx, request.GetId())
	if err != nil {
		s.logger.Trace().Int64("vnf_id", request.GetId()).Err(err).Msg("BGP status request failed")
		return nil, status.Error(codes.Internal, "could not read BGP status: "+err.Error())
	}
	s.logger.Trace().
		Int64("vnf_id", request.GetId()).
		Bool("established", snapshot.Established).
		Str("session_state", snapshot.SessionState).
		Int("received", len(snapshot.Received)).
		Msg("BGP status requested")
	result := &rookeryv1.BGPStatus{
		VnfId:        request.GetId(),
		Running:      snapshot.Running,
		Established:  snapshot.Established,
		SessionState: snapshot.SessionState,
		PeerAddress:  snapshot.PeerAddress,
		PeerAs:       snapshot.PeerAS,
		LocalAs:      snapshot.LocalAS,
		Advertised:   append([]string(nil), snapshot.Advertised...),
	}
	for _, prefix := range snapshot.Received {
		result.Received = append(result.Received, &rookeryv1.BGPReceivedPrefix{
			Prefix:  prefix.Prefix,
			NextHop: prefix.NextHop,
			AsPath:  append([]uint32(nil), prefix.ASPath...),
		})
	}
	return result, nil
}

// requireRuntime verifies that id names a desired runtime of the given type.
func (s *Server) requireRuntime(id int64, kind string) error {
	s.mu.Lock()
	desired := s.desired[id]
	s.mu.Unlock()
	if desired == nil || desired.kind != kind {
		return status.Error(codes.NotFound, "vnf is not managed by the daemon")
	}
	return nil
}

// Supervise restarts desired runtimes that are not alive. It runs until ctx
// is canceled.
func (s *Server) Supervise(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			timer.Reset(s.superviseInterval)
		}
		s.superviseOnce(ctx)
	}
}

// superviseOnce runs one restart pass over desired runtimes whose runtime is
// not running and healthy, skipping ids still within the restart backoff and
// doing nothing while supervision is paused after a reset.
func (s *Server) superviseOnce(ctx context.Context) {
	s.mu.Lock()
	paused := s.now().Before(s.pausedUntil)
	ids := make([]int64, 0, len(s.desired))
	for id := range s.desired {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	if paused {
		s.logger.Trace().Msg("VNF supervision is paused after a runtime reset")
		return
	}
	for _, id := range ids {
		if s.manager.Running(id) && s.manager.Healthy(id) {
			continue
		}
		s.mu.Lock()
		last := s.lastTry[id]
		desired := s.desired[id]
		s.mu.Unlock()
		if !last.IsZero() && s.now().Sub(last) < s.restartBackoff {
			s.logger.Trace().
				Int64("vnf_id", id).
				Dur("retry_in", s.restartBackoff-s.now().Sub(last)).
				Msg("VNF runtime restart delayed")
			continue
		}
		if desired == nil {
			continue
		}
		s.logger.Info().Int64("vnf_id", id).Str("type", desired.kind).Msg("restarting VNF runtime after failure")
		if err := s.restart(ctx, id, desired); err != nil {
			s.logger.Warn().Int64("vnf_id", id).Err(err).Msg("VNF runtime restart failed")
			s.mu.Lock()
			s.lastTry[id] = s.now()
			s.details[id] = err.Error()
			s.mu.Unlock()
		}
	}
}

// restart starts a desired runtime under the operation lock, stopping a
// half-dead runtime first. It is used by supervision, not by the ensure path.
func (s *Server) restart(ctx context.Context, id int64, desired *desiredRuntime) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.mu.Lock()
	current := s.desired[id]
	s.mu.Unlock()
	if current == nil || current != desired {
		return nil
	}
	if s.manager.Running(id) && s.manager.Healthy(id) {
		return nil
	}
	if s.manager.Running(id) {
		if err := s.manager.Stop(ctx, id, false); err != nil {
			return err
		}
	}
	var err error
	switch desired.kind {
	case runtimeDHCP:
		err = s.startDHCP(ctx, desired.spec.GetDhcp())
	case runtimeDNS:
		err = s.startDNS(ctx, desired.spec.GetDns())
	case runtimeBGP:
		err = s.startBGP(ctx, desired.spec.GetBgp())
	default:
		err = fmt.Errorf("unsupported runtime type %q", desired.kind)
	}
	s.mu.Lock()
	s.lastTry[id] = s.now()
	if err != nil {
		s.details[id] = err.Error()
	} else {
		s.details[id] = ""
	}
	s.mu.Unlock()
	return err
}

// Close stops the manager and cancels nothing else.
func (s *Server) Close() error {
	return s.manager.Close()
}

// validDHCPRuntime validates a DHCP runtime spec and returns it.
func validDHCPRuntime(spec *rookeryv1.DHCPRuntimeSpec) (*rookeryv1.DHCPRuntimeSpec, error) {
	if spec == nil || spec.GetRuntime() == nil || spec.GetRuntime().GetId() <= 0 {
		return nil, errors.New("vnf runtime spec is required")
	}
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid DHCP runtime spec: %w", err)
	}
	switch spec.GetFamily() {
	case "dhcpv4":
		if spec.GetDhcpv4() == nil {
			return nil, errors.New("DHCPv4 runtime config is required")
		}
	case "dhcpv6":
		if spec.GetDhcpv6() == nil {
			return nil, errors.New("DHCPv6 runtime config is required")
		}
	default:
		return nil, fmt.Errorf("unsupported DHCP family %q", spec.GetFamily())
	}
	return spec, nil
}

// validDNSRuntime validates a DNS runtime spec and returns it.
func validDNSRuntime(spec *rookeryv1.DNSRuntimeSpec) (*rookeryv1.DNSRuntimeSpec, error) {
	if spec == nil || spec.GetRuntime() == nil || spec.GetRuntime().GetId() <= 0 {
		return nil, errors.New("vnf runtime spec is required")
	}
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid DNS runtime spec: %w", err)
	}
	return spec, nil
}

// validBGPRuntime validates a BGP runtime spec and returns it.
func validBGPRuntime(spec *rookeryv1.BGPRuntimeSpec) (*rookeryv1.BGPRuntimeSpec, error) {
	if spec == nil || spec.GetRuntime() == nil || spec.GetRuntime().GetId() <= 0 {
		return nil, errors.New("vnf runtime spec is required")
	}
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid BGP runtime spec: %w", err)
	}
	return spec, nil
}

// mergeDHCP preserves runtime transport fields (notably the shared addresses
// that a child-write reload cannot know) from the currently desired spec.
func mergeDHCP(current *rookeryv1.DesiredRuntime, spec *rookeryv1.DHCPRuntimeSpec) *rookeryv1.DHCPRuntimeSpec {
	if current == nil || current.GetDhcp() == nil || current.GetDhcp().GetRuntime() == nil {
		return spec
	}
	out := proto.Clone(spec).(*rookeryv1.DHCPRuntimeSpec)
	out.Runtime = mergeRuntimeSpec(current.GetDhcp().GetRuntime(), out.GetRuntime())
	return out
}

// mergeDNS preserves runtime transport fields from the currently desired spec.
func mergeDNS(current *rookeryv1.DesiredRuntime, spec *rookeryv1.DNSRuntimeSpec) *rookeryv1.DNSRuntimeSpec {
	if current == nil || current.GetDns() == nil || current.GetDns().GetRuntime() == nil {
		return spec
	}
	out := proto.Clone(spec).(*rookeryv1.DNSRuntimeSpec)
	out.Runtime = mergeRuntimeSpec(current.GetDns().GetRuntime(), out.GetRuntime())
	return out
}

// mergeBGP preserves runtime transport fields from the currently desired spec.
func mergeBGP(current *rookeryv1.DesiredRuntime, spec *rookeryv1.BGPRuntimeSpec) *rookeryv1.BGPRuntimeSpec {
	if current == nil || current.GetBgp() == nil || current.GetBgp().GetRuntime() == nil {
		return spec
	}
	out := proto.Clone(spec).(*rookeryv1.BGPRuntimeSpec)
	out.Runtime = mergeRuntimeSpec(current.GetBgp().GetRuntime(), out.GetRuntime())
	return out
}

// mergeRuntimeSpec fills empty transport fields from current so a reload keeps
// the runtime endpoint that the daemon was originally given.
func mergeRuntimeSpec(current, incoming *rookeryv1.RuntimeSpec) *rookeryv1.RuntimeSpec {
	if incoming == nil {
		return proto.Clone(current).(*rookeryv1.RuntimeSpec)
	}
	out := proto.Clone(incoming).(*rookeryv1.RuntimeSpec)
	// A reload never changes the transport endpoint: the runtime is already
	// running on the current one, so the persisted spec must keep it for a
	// later supervisor restart. Prefer the current non-empty value and fall
	// back to the proposed one.
	if value := current.GetTransport(); strings.TrimSpace(value) != "" {
		out.Transport = value
	}
	if value := current.GetTapHostName(); strings.TrimSpace(value) != "" {
		out.TapHostName = value
	}
	if value := current.GetMemifSocket(); strings.TrimSpace(value) != "" {
		out.MemifSocket = value
	}
	if value := current.GetMemifSwIfIndex(); value != 0 {
		out.MemifSwIfIndex = value
	}
	if value := current.GetSharedAddress(); strings.TrimSpace(value) != "" {
		out.SharedAddress = value
	}
	if value := current.GetSharedIpv6Address(); strings.TrimSpace(value) != "" {
		out.SharedIpv6Address = value
	}
	return out
}

// storeVNFFromDHCP converts a DHCP runtime spec into the record shape the
// runtime expects.
func storeVNFFromDHCP(spec *rookeryv1.DHCPRuntimeSpec) (store.VNF, error) {
	runtime := spec.GetRuntime()
	switch spec.GetFamily() {
	case "dhcpv4":
		cfg := spec.GetDhcpv4()
		if cfg == nil {
			return store.VNF{}, errors.New("DHCPv4 runtime config is required")
		}
		vnf := storeVNFBase(runtime, store.VNFTypeDHCPv4)
		vnf.Config.Gateway = cfg.GetGateway()
		vnf.Config.RangeStart = cfg.GetRangeStart()
		vnf.Config.RangeEnd = cfg.GetRangeEnd()
		vnf.Config.LeaseTime = cfg.GetLeaseTime()
		vnf.Config.DNS = cfg.GetDns()
		vnf.Config.BindIP = cfg.GetBindIp()
		vnf.Config.DNSVNFID = cfg.GetDnsVnfId()
		vnf.Config.DNSDomain = cfg.GetDnsDomain()
		vnf.Config.DNSTTL = cfg.GetDnsTtl()
		vnf.Config.StaticAssignments = staticAssignmentsFromProto(cfg.GetStaticAssignments())
		vnf.Config.DHCPOptions = dhcpOptionsFromProto(cfg.GetOptions())
		return vnf, nil
	case "dhcpv6":
		cfg := spec.GetDhcpv6()
		if cfg == nil {
			return store.VNF{}, errors.New("DHCPv6 runtime config is required")
		}
		vnf := storeVNFBase(runtime, store.VNFTypeDHCPv6)
		vnf.Config.Gateway = cfg.GetGateway()
		vnf.Config.RangeStart = cfg.GetRangeStart()
		vnf.Config.RangeEnd = cfg.GetRangeEnd()
		vnf.Config.LeaseTime = cfg.GetLeaseTime()
		vnf.Config.DNS = cfg.GetDns()
		vnf.Config.BindIP = cfg.GetBindIp()
		vnf.Config.DNSVNFID = cfg.GetDnsVnfId()
		vnf.Config.DNSDomain = cfg.GetDnsDomain()
		vnf.Config.DNSTTL = cfg.GetDnsTtl()
		vnf.Config.DHCPOptions = dhcpOptionsFromProto(cfg.GetOptions())
		return vnf, nil
	}
	return store.VNF{}, fmt.Errorf("unsupported DHCP family %q", spec.GetFamily())
}

// storeVNFFromDNS converts a DNS runtime spec into the record shape the runtime
// expects.
func storeVNFFromDNS(spec *rookeryv1.DNSRuntimeSpec) (store.VNF, error) {
	vnf := storeVNFBase(spec.GetRuntime(), store.VNFTypeDNS)
	cfg := spec.GetConfig()
	if cfg == nil {
		return store.VNF{}, errors.New("DNS runtime config is required")
	}
	vnf.Config.DNSForward = cfg.GetForward()
	records := make([]store.DNSRecord, 0, len(cfg.GetRecords()))
	for _, record := range cfg.GetRecords() {
		records = append(records, store.DNSRecord{
			Name: record.GetName(), Type: record.GetType(), Value: record.GetValue(), TTL: record.GetTtl(),
		})
	}
	vnf.Config.DNSRecords = records
	forwarders := make([]string, 0, len(cfg.GetForwarders()))
	for _, forwarder := range cfg.GetForwarders() {
		forwarders = append(forwarders, forwarder.GetAddress())
	}
	vnf.Config.DNSForwarders = forwarders
	return vnf, nil
}

// storeVNFFromBGP converts a BGP runtime spec into the record shape the runtime
// expects.
func storeVNFFromBGP(spec *rookeryv1.BGPRuntimeSpec) (store.VNF, error) {
	vnf := storeVNFBase(spec.GetRuntime(), store.VNFTypeBGP)
	cfg := spec.GetConfig()
	if cfg == nil {
		return store.VNF{}, errors.New("BGP runtime config is required")
	}
	vnf.Config.LocalAS = cfg.GetLocalAs()
	vnf.Config.RouterID = cfg.GetRouterId()
	vnf.Config.PeerAS = cfg.GetPeerAs()
	vnf.Config.PeerIP = cfg.GetPeerIp()
	vnf.Config.LocalIP = cfg.GetLocalIp()
	vnf.Config.PeerGateway = cfg.GetPeerGateway()
	vnf.Config.MultihopTTL = cfg.GetMultihopTtl()
	vnf.Config.Password = cfg.GetPassword()
	vnf.Config.Passive = cfg.GetPassive()
	vnf.Config.TargetTableID = cfg.GetTargetTableId()
	vnf.Config.NextHop = cfg.GetNextHop()
	prefixes := make([]string, 0, len(cfg.GetAdvertisedPrefixes()))
	for _, prefix := range cfg.GetAdvertisedPrefixes() {
		prefixes = append(prefixes, prefix.GetPrefix())
	}
	vnf.Config.AdvertisePrefixes = prefixes
	return vnf, nil
}

// storeVNFBase converts the shared runtime fields into a store record.
func storeVNFBase(runtime *rookeryv1.RuntimeSpec, vnfType string) store.VNF {
	return store.VNF{
		ID:             runtime.GetId(),
		Name:           runtime.GetName(),
		NetworkID:      runtime.GetNetworkId(),
		Type:           vnfType,
		Enabled:        true,
		TapHostName:    runtime.GetTapHostName(),
		MemifSocket:    runtime.GetMemifSocket(),
		MemifSwIfIndex: runtime.GetMemifSwIfIndex(),
		Config:         store.VNFConfig{Transport: runtime.GetTransport()},
	}
}

// staticAssignmentsFromProto converts DHCPv4 static assignments.
func staticAssignmentsFromProto(items []*rookeryv1.DHCPStaticAssignmentSpec) []store.DHCPStaticAssignment {
	assignments := make([]store.DHCPStaticAssignment, 0, len(items))
	for _, item := range items {
		assignments = append(assignments, store.DHCPStaticAssignment{MAC: item.GetMac(), IP: item.GetIp()})
	}
	return assignments
}

// dhcpOptionsFromProto converts extra DHCPv4/v6 options.
func dhcpOptionsFromProto(items []*rookeryv1.DHCPOptionSpec) []store.DHCPOption {
	options := make([]store.DHCPOption, 0, len(items))
	for _, item := range items {
		options = append(options, store.DHCPOption{
			Code: uint16(item.GetCode()), Format: item.GetFormat(), Value: item.GetValue(),
		})
	}
	return options
}
