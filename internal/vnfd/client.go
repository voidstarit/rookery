package vnfd

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

const (
	requestTimeout = 30 * time.Second
	statusTimeout  = 5 * time.Second
	refreshPeriod  = 2 * time.Second
)

// Client is the API server's view of the VNF daemon. It implements
// api.VNFManager and gateway.VNFRuntime.
type Client struct {
	conn   *grpc.ClientConn
	client rookeryv1.VNFDaemonClient
	logger *slog.Logger

	mu        sync.RWMutex
	running   map[int64]bool
	available bool

	done chan struct{}
	wg   sync.WaitGroup
}

// NewClient connects to the VNF daemon. The connection is lazy, so a missing
// daemon does not fail startup; runtimes report as not running until it
// appears.
func NewClient(addr string, logger *slog.Logger) (*Client, error) {
	if logger == nil {
		logger = slog.Default()
	}
	target, err := dialTarget(addr)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:    conn,
		client:  rookeryv1.NewVNFDaemonClient(conn),
		logger:  logger,
		running: make(map[int64]bool),
		done:    make(chan struct{}),
	}
	// Prime the state cache with a short timeout so an absent daemon does not
	// delay API startup; the background loop keeps it fresh afterwards.
	c.refreshWithin(time.Second)
	c.wg.Add(1)
	go c.refreshLoop()
	return c, nil
}

// Close stops the status poller and the gRPC connection.
func (c *Client) Close() {
	select {
	case <-c.done:
		return
	default:
	}
	close(c.done)
	c.wg.Wait()
	_ = c.conn.Close()
}

func (c *Client) refreshLoop() {
	defer c.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-timer.C:
			timer.Reset(refreshPeriod)
		}
		c.refresh()
	}
}

func (c *Client) refresh() {
	c.refreshWithin(statusTimeout)
}

func (c *Client) refreshWithin(timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	response, err := c.client.ListVNFStates(ctx, &rookeryv1.ListVNFStatesRequest{})
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.available = false
		return
	}
	c.available = true
	next := make(map[int64]bool, len(response.GetVnfs()))
	for _, item := range response.GetVnfs() {
		next[item.GetId()] = item.GetRunning()
	}
	c.running = next
}

// availableNow reports whether the last status poll reached the daemon.
func (c *Client) availableNow() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.available
}

func (c *Client) unavailableError() error {
	return &clientError{action: "VNF daemon", message: "is not reachable"}
}

// Start starts a tap-backed runtime through the daemon.
func (c *Client) Start(ctx context.Context, vnf store.VNF, hostIf string) error {
	return c.ensure(ctx, vnf, "tap", hostIf, "", 0)
}

// StartMemif starts a memif-backed runtime through the daemon.
func (c *Client) StartMemif(ctx context.Context, vnf store.VNF, socketPath string, ifID uint32) error {
	return c.ensure(ctx, vnf, "memif", "", socketPath, ifID)
}

func (c *Client) ensure(ctx context.Context, vnf store.VNF, transport, tapHost string, socket string, ifID uint32) error {
	if !c.availableNow() {
		return c.unavailableError()
	}
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	response, err := c.client.EnsureVNF(callCtx, &rookeryv1.EnsureVNFRequest{
		Vnf: specFor(vnf, transport, tapHost, socket, ifID),
	})
	if err != nil {
		return grpcError("start VNF", err)
	}
	c.setRunning(vnf.ID, response.GetRunning())
	return nil
}

// Stop removes a runtime through the daemon.
func (c *Client) Stop(ctx context.Context, vnfID int64, removeLeases bool) error {
	if !c.availableNow() {
		return c.unavailableError()
	}
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if _, err := c.client.RemoveVNF(callCtx, &rookeryv1.RemoveVNFRequest{Id: vnfID, RemoveLeases: removeLeases}); err != nil {
		return grpcError("stop VNF", err)
	}
	c.setRunning(vnfID, false)
	return nil
}

// Running reports the last polled runtime state. The daemon refreshes this
// cache every few seconds.
func (c *Client) Running(vnfID int64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running[vnfID]
}

func (c *Client) setRunning(vnfID int64, running bool) {
	c.mu.Lock()
	if running {
		c.running[vnfID] = true
	} else {
		delete(c.running, vnfID)
	}
	c.mu.Unlock()
}

// Leases returns the daemon's persisted leases for a VNF.
func (c *Client) Leases(ctx context.Context, vnfID int64) ([]store.VNFLease, error) {
	callCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	response, err := c.client.ListLeases(callCtx, &rookeryv1.ListLeasesRequest{Id: vnfID})
	if err != nil {
		return nil, grpcError("read VNF leases", err)
	}
	leases := make([]store.VNFLease, 0, len(response.GetLeases()))
	for _, item := range response.GetLeases() {
		lease := store.VNFLease{
			ClientID: item.GetClientId(), Address: item.GetAddress(),
			Hostname: item.GetHostname(), Active: item.GetActive(),
		}
		if item.GetExpiresAt() != nil {
			lease.ExpiresAt = item.GetExpiresAt().AsTime()
		}
		leases = append(leases, lease)
	}
	return leases, nil
}

// BGPStatus returns the daemon's live GoBGP snapshot for a BGP VNF.
func (c *Client) BGPStatus(ctx context.Context, vnfID int64) (host.BGPStatus, error) {
	callCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	response, err := c.client.GetBGPStatus(callCtx, &rookeryv1.GetBGPStatusRequest{Id: vnfID})
	if err != nil {
		return host.BGPStatus{}, grpcError("read BGP status", err)
	}
	status := host.BGPStatus{
		Running:      response.GetRunning(),
		Established:  response.GetEstablished(),
		SessionState: response.GetSessionState(),
		PeerAddress:  response.GetPeerAddress(),
		PeerAS:       response.GetPeerAs(),
		LocalAS:      response.GetLocalAs(),
		Advertised:   append([]string(nil), response.GetAdvertised()...),
		Received:     make([]host.BGPReceivedPrefix, 0, len(response.GetReceived())),
	}
	for _, prefix := range response.GetReceived() {
		status.Received = append(status.Received, host.BGPReceivedPrefix{
			Prefix:  prefix.GetPrefix(),
			NextHop: prefix.GetNextHop(),
			ASPath:  append([]uint32(nil), prefix.GetAsPath()...),
		})
	}
	return status, nil
}

// CleanupLegacyNetwork asks the daemon to remove legacy DHCP runtime files.
func (c *Client) CleanupLegacyNetwork(networkID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	if _, err := c.client.CleanupLegacyNetwork(ctx, &rookeryv1.CleanupLegacyNetworkRequest{NetworkId: networkID}); err != nil {
		c.logger.Warn("cleanup legacy VNF runtime failed", "network_id", networkID, "error", err)
	}
}

// ResetRuntime stops every daemon runtime while keeping the desired specs.
func (c *Client) ResetRuntime(ctx context.Context) error {
	if !c.availableNow() {
		return c.unavailableError()
	}
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if _, err := c.client.ResetRuntime(callCtx, &rookeryv1.ResetRuntimeRequest{}); err != nil {
		return grpcError("reset VNF runtime", err)
	}
	c.mu.Lock()
	c.running = make(map[int64]bool)
	c.mu.Unlock()
	return nil
}

func grpcError(action string, err error) error {
	if st, ok := status.FromError(err); ok {
		return &clientError{action: action, message: st.Message()}
	}
	return &clientError{action: action, message: err.Error()}
}

type clientError struct {
	action  string
	message string
}

func (e *clientError) Error() string { return e.action + ": " + e.message }

// dialTarget turns the daemon address into a gRPC target.
func dialTarget(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	switch {
	case strings.HasPrefix(addr, "unix://"):
		return addr, nil
	case strings.HasPrefix(addr, "tcp://"):
		target := strings.TrimPrefix(addr, "tcp://")
		if strings.TrimSpace(target) == "" {
			return "", &clientError{action: "VNF daemon address", message: "missing host:port"}
		}
		return target, nil
	case strings.HasPrefix(addr, "/"):
		return "unix://" + addr, nil
	}
	return "", &clientError{action: "VNF daemon address", message: "must be unix:///path or tcp://host:port"}
}

// Listen opens the daemon's gRPC listener. Unix sockets are created with 0660
// and any stale socket file is removed first.
func Listen(addr string) (net.Listener, error) {
	addr = strings.TrimSpace(addr)
	switch {
	case strings.HasPrefix(addr, "unix://"):
		path := strings.TrimPrefix(addr, "unix://")
		if strings.TrimSpace(path) == "" {
			return nil, &clientError{action: "VNF daemon listen address", message: "missing socket path"}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		_ = os.Remove(path)
		listener, err := net.Listen("unix", path)
		if err != nil {
			return nil, err
		}
		if err := os.Chmod(path, 0o660); err != nil {
			_ = listener.Close()
			return nil, err
		}
		return listener, nil
	case strings.HasPrefix(addr, "tcp://"):
		target := strings.TrimPrefix(addr, "tcp://")
		if strings.TrimSpace(target) == "" {
			return nil, &clientError{action: "VNF daemon listen address", message: "missing host:port"}
		}
		return net.Listen("tcp", target)
	}
	return nil, &clientError{action: "VNF daemon listen address", message: "must be unix:///path or tcp://host:port"}
}

// specFor builds the control spec from an API-side VNF record.
func specFor(vnf store.VNF, transport, tapHost string, socket string, ifID uint32) *rookeryv1.VNFSpec {
	return &rookeryv1.VNFSpec{
		Id:             vnf.ID,
		Name:           vnf.Name,
		Type:           vnf.Type,
		NetworkId:      vnf.NetworkID,
		Transport:      transport,
		Config:         configToProto(vnf.Config),
		TapHostName:    tapHost,
		MemifSocket:    socket,
		MemifSwIfIndex: ifID,
	}
}

// configToProto mirrors the gateway's VNF config mapping.
func configToProto(config store.VNFConfig) *rookeryv1.VNFConfig {
	static := make([]*rookeryv1.DHCPStaticAssignment, 0, len(config.StaticAssignments))
	for _, assignment := range config.StaticAssignments {
		static = append(static, &rookeryv1.DHCPStaticAssignment{Mac: assignment.MAC, Ip: assignment.IP})
	}
	dnat := make([]*rookeryv1.DNATRule, 0, len(config.DNATRules))
	for _, rule := range config.DNATRules {
		dnat = append(dnat, &rookeryv1.DNATRule{
			Protocol: rule.Protocol, ExternalPort: int32(rule.ExternalPort),
			LocalIp: rule.LocalIP, LocalPort: int32(rule.LocalPort),
		})
	}
	return &rookeryv1.VNFConfig{
		Gateway: config.Gateway, RangeStart: config.RangeStart, RangeEnd: config.RangeEnd,
		Dns: config.DNS, LeaseTime: config.LeaseTime, BindIp: config.BindIP,
		StaticAssignments: static, OutsideInterface: config.OutsideInterface,
		OutsideAddress: config.OutsideAddress, OutsideGateway: config.OutsideGateway,
		Nat44Pool: config.NAT44Pool, DnatRules: dnat,
		Transport: config.Transport, LocalAs: config.LocalAS, RouterId: config.RouterID,
		PeerAs: config.PeerAS, PeerIp: config.PeerIP, LocalIp: config.LocalIP,
		PeerGateway: config.PeerGateway, MultihopTtl: config.MultihopTTL,
		Password: config.Password, Passive: config.Passive, TargetTableId: config.TargetTableID,
		AdvertisePrefixes: append([]string(nil), config.AdvertisePrefixes...),
		NextHop:           config.NextHop,
	}
}
