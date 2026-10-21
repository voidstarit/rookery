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
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
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
	Stop(ctx context.Context, vnfID int64, removeLeases bool) error
	Running(vnfID int64) bool
	Healthy(vnfID int64) bool
	Leases(ctx context.Context, vnfID int64) ([]store.VNFLease, error)
	BGPStatus(ctx context.Context, vnfID int64) (host.BGPStatus, error)
	CleanupLegacyNetwork(networkID int64)
	ResetRuntime(ctx context.Context) error
	Close() error
}

const (
	defaultSuperviseInterval = 5 * time.Second
	defaultRestartBackoff    = 10 * time.Second
	resetGracePeriod         = 60 * time.Second
)

type desiredVNF struct {
	spec *rookeryv1.VNFSpec
	hash string
}

// Server implements the VNFDaemon gRPC service.
type Server struct {
	rookeryv1.UnimplementedVNFDaemonServer

	manager Manager
	dir     string // desired-spec directory
	logger  *slog.Logger

	mu      sync.Mutex
	desired map[int64]*desiredVNF
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
func NewServer(manager Manager, dir string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		manager:           manager,
		dir:               dir,
		logger:            logger,
		desired:           make(map[int64]*desiredVNF),
		details:           make(map[int64]string),
		lastTry:           make(map[int64]time.Time),
		superviseInterval: defaultSuperviseInterval,
		restartBackoff:    defaultRestartBackoff,
		now:               time.Now,
	}
	return s
}

// Load restores the persisted desired specs. Runtimes are not started here;
// call Start or run the supervision loop.
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
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "desired-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			s.logger.Warn("could not read persisted VNF spec", "file", name, "error", err)
			continue
		}
		spec := &rookeryv1.VNFSpec{}
		if err := protojson.Unmarshal(data, spec); err != nil {
			s.logger.Warn("could not parse persisted VNF spec", "file", name, "error", err)
			continue
		}
		if spec.GetId() <= 0 {
			continue
		}
		s.desired[spec.GetId()] = &desiredVNF{spec: spec, hash: specHash(spec)}
	}
	return nil
}

func specHash(spec *rookeryv1.VNFSpec) string {
	data, err := protojson.Marshal(spec)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Server) specPath(id int64) string {
	return filepath.Join(s.dir, fmt.Sprintf("desired-%d.json", id))
}

func (s *Server) persist(spec *rookeryv1.VNFSpec) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := protojson.MarshalOptions{Indent: "  "}.Marshal(spec)
	if err != nil {
		return err
	}
	tmp := s.specPath(spec.GetId()) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.specPath(spec.GetId()))
}

func (s *Server) forget(id int64) {
	_ = os.Remove(s.specPath(id))
}

// EnsureVNF starts or restarts a VNF runtime for the given spec.
func (s *Server) EnsureVNF(ctx context.Context, request *rookeryv1.EnsureVNFRequest) (*rookeryv1.VNFState, error) {
	spec := request.GetVnf()
	if spec == nil || spec.GetId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "vnf spec is required")
	}
	hash := specHash(spec)

	s.opMu.Lock()
	defer s.opMu.Unlock()

	s.mu.Lock()
	current := s.desired[spec.GetId()]
	running := s.manager.Running(spec.GetId())
	s.mu.Unlock()
	if current != nil && current.hash == hash && running && s.manager.Healthy(spec.GetId()) {
		return s.state(spec.GetId()), nil
	}

	if running {
		if err := s.manager.Stop(ctx, spec.GetId(), false); err != nil {
			s.setDetail(spec.GetId(), err.Error())
			return nil, status.Error(codes.Internal, "could not restart VNF: "+err.Error())
		}
	}

	vnf := storeVNF(spec)
	var err error
	switch spec.GetTransport() {
	case "memif":
		if strings.TrimSpace(spec.GetMemifSocket()) == "" {
			err = errors.New("memif socket path is required")
		} else {
			err = s.manager.StartMemif(ctx, vnf, spec.GetMemifSocket(), spec.GetMemifSwIfIndex())
		}
	default:
		if strings.TrimSpace(spec.GetTapHostName()) == "" {
			err = errors.New("tap interface name is required")
		} else {
			err = s.manager.Start(ctx, vnf, spec.GetTapHostName())
		}
	}
	if err != nil {
		s.setDetail(spec.GetId(), err.Error())
		return nil, status.Error(codes.Internal, "could not start VNF: "+err.Error())
	}
	if err := s.persist(spec); err != nil {
		s.logger.Warn("could not persist VNF spec", "vnf_id", spec.GetId(), "error", err)
	}
	s.mu.Lock()
	s.desired[spec.GetId()] = &desiredVNF{spec: spec, hash: hash}
	s.details[spec.GetId()] = ""
	s.lastTry[spec.GetId()] = s.now()
	s.mu.Unlock()
	return s.state(spec.GetId()), nil
}

// RemoveVNF stops a runtime and forgets its spec.
func (s *Server) RemoveVNF(ctx context.Context, request *rookeryv1.RemoveVNFRequest) (*emptypb.Empty, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.manager.Stop(ctx, request.GetId(), request.GetRemoveLeases()); err != nil {
		s.setDetail(request.GetId(), err.Error())
		return nil, status.Error(codes.Internal, "could not stop VNF: "+err.Error())
	}
	s.mu.Lock()
	delete(s.desired, request.GetId())
	delete(s.details, request.GetId())
	delete(s.lastTry, request.GetId())
	s.mu.Unlock()
	s.forget(request.GetId())
	return &emptypb.Empty{}, nil
}

// ResetRuntime stops every runtime but keeps the desired specs. Supervision
// pauses briefly so the API can rebuild VPP resources before runtimes restart.
func (s *Server) ResetRuntime(ctx context.Context, _ *rookeryv1.ResetRuntimeRequest) (*emptypb.Empty, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.manager.ResetRuntime(ctx); err != nil {
		return nil, status.Error(codes.Internal, "could not reset VNF runtime: "+err.Error())
	}
	s.mu.Lock()
	s.pausedUntil = s.now().Add(resetGracePeriod)
	s.mu.Unlock()
	return &emptypb.Empty{}, nil
}

// CleanupLegacyNetwork removes files left by the legacy per-network DHCP.
func (s *Server) CleanupLegacyNetwork(_ context.Context, request *rookeryv1.CleanupLegacyNetworkRequest) (*emptypb.Empty, error) {
	s.manager.CleanupLegacyNetwork(request.GetNetworkId())
	return &emptypb.Empty{}, nil
}

// ListVNFStates reports every desired VNF and whether it is running.
func (s *Server) ListVNFStates(context.Context, *rookeryv1.ListVNFStatesRequest) (*rookeryv1.ListVNFStatesResponse, error) {
	s.mu.Lock()
	ids := make([]int64, 0, len(s.desired))
	for id := range s.desired {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	response := &rookeryv1.ListVNFStatesResponse{Vnfs: make([]*rookeryv1.VNFState, 0, len(ids))}
	for _, id := range ids {
		response.Vnfs = append(response.Vnfs, s.state(id))
	}
	return response, nil
}

// GetVNFState reports one desired VNF.
func (s *Server) GetVNFState(_ context.Context, request *rookeryv1.GetVNFStateRequest) (*rookeryv1.VNFState, error) {
	s.mu.Lock()
	_, ok := s.desired[request.GetId()]
	s.mu.Unlock()
	if !ok {
		return nil, status.Error(codes.NotFound, "vnf is not managed by the daemon")
	}
	return s.state(request.GetId()), nil
}

func (s *Server) state(id int64) *rookeryv1.VNFState {
	s.mu.Lock()
	detail := s.details[id]
	s.mu.Unlock()
	running := s.manager.Running(id) && s.manager.Healthy(id)
	return &rookeryv1.VNFState{Id: id, Running: running, Detail: detail}
}

func (s *Server) setDetail(id int64, detail string) {
	s.mu.Lock()
	s.details[id] = detail
	s.mu.Unlock()
}

// ListLeases returns the persisted DHCP leases for one VNF.
func (s *Server) ListLeases(ctx context.Context, request *rookeryv1.ListLeasesRequest) (*rookeryv1.ListLeasesResponse, error) {
	s.mu.Lock()
	desired := s.desired[request.GetId()]
	s.mu.Unlock()
	if desired == nil {
		return nil, status.Error(codes.NotFound, "vnf is not managed by the daemon")
	}
	leases, err := s.manager.Leases(ctx, request.GetId())
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read VNF leases: "+err.Error())
	}
	response := &rookeryv1.ListLeasesResponse{
		VnfId:  request.GetId(),
		Type:   desired.spec.GetType(),
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

// GetBGPStatus returns the live GoBGP session and RIB snapshot.
func (s *Server) GetBGPStatus(ctx context.Context, request *rookeryv1.GetBGPStatusRequest) (*rookeryv1.VNFBGPStatus, error) {
	s.mu.Lock()
	desired := s.desired[request.GetId()]
	s.mu.Unlock()
	if desired == nil {
		return nil, status.Error(codes.NotFound, "vnf is not managed by the daemon")
	}
	snapshot, err := s.manager.BGPStatus(ctx, request.GetId())
	if err != nil {
		return nil, status.Error(codes.Internal, "could not read BGP status: "+err.Error())
	}
	result := &rookeryv1.VNFBGPStatus{
		VnfId:        request.GetId(),
		Type:         desired.spec.GetType(),
		Running:      snapshot.Running,
		Established:  snapshot.Established,
		SessionState: snapshot.SessionState,
		PeerAddress:  snapshot.PeerAddress,
		PeerAs:       snapshot.PeerAS,
		LocalAs:      snapshot.LocalAS,
		Advertised:   append([]string(nil), snapshot.Advertised...),
	}
	for _, prefix := range snapshot.Received {
		result.Received = append(result.Received, &rookeryv1.VNFBGPReceivedPrefix{
			Prefix:  prefix.Prefix,
			NextHop: prefix.NextHop,
			AsPath:  append([]uint32(nil), prefix.ASPath...),
		})
	}
	return result, nil
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

func (s *Server) superviseOnce(ctx context.Context) {
	s.mu.Lock()
	paused := s.now().Before(s.pausedUntil)
	ids := make([]int64, 0, len(s.desired))
	for id := range s.desired {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	if paused {
		return
	}
	for _, id := range ids {
		if s.manager.Running(id) && s.manager.Healthy(id) {
			continue
		}
		s.mu.Lock()
		last := s.lastTry[id]
		s.mu.Unlock()
		if !last.IsZero() && s.now().Sub(last) < s.restartBackoff {
			continue
		}
		s.mu.Lock()
		spec := s.desired[id].spec
		s.mu.Unlock()
		if spec == nil {
			continue
		}
		s.logger.Info("restarting VNF runtime after failure", "vnf_id", id)
		if _, err := s.EnsureVNF(ctx, &rookeryv1.EnsureVNFRequest{Vnf: spec}); err != nil {
			s.logger.Warn("VNF runtime restart failed", "vnf_id", id, "error", err)
			s.mu.Lock()
			s.lastTry[id] = s.now()
			s.mu.Unlock()
			continue
		}
	}
}

// Close stops the manager and cancels nothing else.
func (s *Server) Close() error {
	return s.manager.Close()
}

// storeVNF converts a control spec into the record shape the runtime expects.
func storeVNF(spec *rookeryv1.VNFSpec) store.VNF {
	return store.VNF{
		ID:             spec.GetId(),
		Name:           spec.GetName(),
		NetworkID:      spec.GetNetworkId(),
		Type:           spec.GetType(),
		Enabled:        true,
		TapHostName:    spec.GetTapHostName(),
		MemifSocket:    spec.GetMemifSocket(),
		MemifSwIfIndex: spec.GetMemifSwIfIndex(),
		Config:         configFromProto(spec.GetConfig()),
	}
}

// configFromProto mirrors the gateway's VNF config mapping in reverse.
func configFromProto(c *rookeryv1.VNFConfig) store.VNFConfig {
	if c == nil {
		return store.VNFConfig{}
	}
	static := make([]store.DHCPStaticAssignment, 0, len(c.GetStaticAssignments()))
	for _, assignment := range c.GetStaticAssignments() {
		static = append(static, store.DHCPStaticAssignment{MAC: assignment.GetMac(), IP: assignment.GetIp()})
	}
	dnat := make([]store.DNATRule, 0, len(c.GetDnatRules()))
	for _, rule := range c.GetDnatRules() {
		dnat = append(dnat, store.DNATRule{
			Protocol:     rule.GetProtocol(),
			ExternalPort: int(rule.GetExternalPort()),
			LocalIP:      rule.GetLocalIp(),
			LocalPort:    int(rule.GetLocalPort()),
		})
	}
	return store.VNFConfig{
		Gateway:           c.GetGateway(),
		RangeStart:        c.GetRangeStart(),
		RangeEnd:          c.GetRangeEnd(),
		DNS:               c.GetDns(),
		LeaseTime:         c.GetLeaseTime(),
		BindIP:            c.GetBindIp(),
		StaticAssignments: static,
		OutsideInterface:  c.GetOutsideInterface(),
		OutsideAddress:    c.GetOutsideAddress(),
		OutsideGateway:    c.GetOutsideGateway(),
		NAT44Pool:         c.GetNat44Pool(),
		DNATRules:         dnat,
		Transport:         c.GetTransport(),
		LocalAS:           c.GetLocalAs(),
		RouterID:          c.GetRouterId(),
		PeerAS:            c.GetPeerAs(),
		PeerIP:            c.GetPeerIp(),
		LocalIP:           c.GetLocalIp(),
		PeerGateway:       c.GetPeerGateway(),
		MultihopTTL:       c.GetMultihopTtl(),
		Password:          c.GetPassword(),
		Passive:           c.GetPassive(),
		TargetTableID:     c.GetTargetTableId(),
		AdvertisePrefixes: append([]string(nil), c.GetAdvertisePrefixes()...),
		NextHop:           c.GetNextHop(),
	}
}
