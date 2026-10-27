package vnfd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
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
)

// Client is the API server's view of the VNF daemon. It implements
// api.VNFManager and gateway.VNFRuntime. Runtime state is queried on demand;
// the client does not poll the daemon.
type Client struct {
	conn   *grpc.ClientConn
	client rookeryv1.VNFDaemonClient
	logger zerolog.Logger
}

// NewClient connects to the VNF daemon. The connection is lazy, so a missing
// daemon does not fail startup; callers query state only when they need it.
func NewClient(addr string, logger zerolog.Logger) (*Client, error) {
	target, err := dialTarget(addr)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	logger.Trace().Str("target", target).Msg("VNF daemon client created")
	return &Client{
		conn:   conn,
		client: rookeryv1.NewVNFDaemonClient(conn),
		logger: logger,
	}, nil
}

// Close closes the gRPC connection.
func (c *Client) Close() {
	_ = c.conn.Close()
}

// Start starts a tap-backed runtime through the daemon.
func (c *Client) Start(ctx context.Context, vnf store.VNF, hostIf string) error {
	return c.ensure(ctx, vnf, "tap", hostIf, "", 0, "", "")
}

// StartMemif starts a dedicated memif-backed runtime through the daemon.
func (c *Client) StartMemif(ctx context.Context, vnf store.VNF, socketPath string, ifID uint32) error {
	return c.ensure(ctx, vnf, "memif", "", socketPath, ifID, "", "")
}

// StartShared starts a runtime on its network's shared memif.
func (c *Client) StartShared(ctx context.Context, vnf store.VNF, socketPath string, ifID uint32, address, ipv6Address string) error {
	return c.ensure(ctx, vnf, "shared", "", socketPath, ifID, address, ipv6Address)
}

func (c *Client) ensure(ctx context.Context, vnf store.VNF, transport, tapHost string, socket string, ifID uint32, sharedAddress, sharedIPv6Address string) error {
	c.logger.Trace().
		Int64("vnf_id", vnf.ID).
		Str("type", vnf.Type).
		Str("transport", transport).
		Str("tap", tapHost).
		Str("memif", socket).
		Uint32("interface_id", ifID).
		Str("shared_address", sharedAddress).
		Msg("ensuring VNF runtime")
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	response, err := c.client.EnsureVNF(callCtx, &rookeryv1.EnsureVNFRequest{
		Vnf: specFor(vnf, transport, tapHost, socket, ifID, sharedAddress, sharedIPv6Address),
	})
	if err != nil {
		c.logger.Trace().Int64("vnf_id", vnf.ID).Str("transport", transport).Err(err).Msg("ensure VNF runtime failed")
		return grpcError("start VNF", err)
	}
	c.logger.Trace().Int64("vnf_id", vnf.ID).Bool("running", response.GetRunning()).Msg("VNF runtime ensured")
	return nil
}

// Stop removes a runtime through the daemon.
func (c *Client) Stop(ctx context.Context, vnfID int64, removeLeases bool) error {
	c.logger.Trace().Int64("vnf_id", vnfID).Bool("remove_leases", removeLeases).Msg("removing VNF runtime")
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if _, err := c.client.RemoveVNF(callCtx, &rookeryv1.RemoveVNFRequest{Id: vnfID, RemoveLeases: removeLeases}); err != nil {
		c.logger.Trace().Int64("vnf_id", vnfID).Err(err).Msg("remove VNF runtime failed")
		return grpcError("stop VNF", err)
	}
	c.logger.Trace().Int64("vnf_id", vnfID).Msg("VNF runtime removed")
	return nil
}

// Running asks the daemon whether a runtime is currently alive. It is only
// called when a caller needs the state (a view or a reconciliation decision),
// never by a background poll.
func (c *Client) Running(ctx context.Context, vnfID int64) bool {
	callCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	state, err := c.client.GetVNFState(callCtx, &rookeryv1.GetVNFStateRequest{Id: vnfID})
	if err != nil {
		c.logger.Trace().Int64("vnf_id", vnfID).Err(err).Msg("VNF state query failed")
		return false
	}
	c.logger.Trace().Int64("vnf_id", vnfID).Bool("running", state.GetRunning()).Msg("VNF state queried")
	return state.GetRunning()
}

// Leases returns the daemon's persisted leases for a VNF.
func (c *Client) Leases(ctx context.Context, vnfID int64) ([]store.VNFLease, error) {
	c.logger.Trace().Int64("vnf_id", vnfID).Msg("reading VNF leases")
	callCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	response, err := c.client.ListLeases(callCtx, &rookeryv1.ListLeasesRequest{Id: vnfID})
	if err != nil {
		c.logger.Trace().Int64("vnf_id", vnfID).Err(err).Msg("read VNF leases failed")
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
	c.logger.Trace().Int64("vnf_id", vnfID).Int("leases", len(leases)).Msg("VNF leases read")
	return leases, nil
}

// BGPStatus returns the daemon's live GoBGP snapshot for a BGP VNF.
func (c *Client) BGPStatus(ctx context.Context, vnfID int64) (host.BGPStatus, error) {
	c.logger.Trace().Int64("vnf_id", vnfID).Msg("reading BGP status")
	callCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	response, err := c.client.GetBGPStatus(callCtx, &rookeryv1.GetBGPStatusRequest{Id: vnfID})
	if err != nil {
		c.logger.Trace().Int64("vnf_id", vnfID).Err(err).Msg("read BGP status failed")
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
	c.logger.Trace().
		Int64("vnf_id", vnfID).
		Bool("established", status.Established).
		Str("session_state", status.SessionState).
		Int("received", len(status.Received)).
		Msg("BGP status read")
	return status, nil
}

// CleanupLegacyNetwork asks the daemon to remove legacy DHCP runtime files.
func (c *Client) CleanupLegacyNetwork(networkID int64) {
	c.logger.Trace().Int64("network_id", networkID).Msg("cleaning up legacy VNF runtime")
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	if _, err := c.client.CleanupLegacyNetwork(ctx, &rookeryv1.CleanupLegacyNetworkRequest{NetworkId: networkID}); err != nil {
		c.logger.Warn().Int64("network_id", networkID).Err(err).Msg("cleanup legacy VNF runtime failed")
	}
}

// ResetRuntime stops every daemon runtime while keeping the desired specs.
func (c *Client) ResetRuntime(ctx context.Context) error {
	c.logger.Trace().Msg("resetting VNF runtimes")
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if _, err := c.client.ResetRuntime(callCtx, &rookeryv1.ResetRuntimeRequest{}); err != nil {
		c.logger.Trace().Err(err).Msg("reset VNF runtimes failed")
		return grpcError("reset VNF runtime", err)
	}
	c.logger.Trace().Msg("VNF runtimes reset")
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
func specFor(vnf store.VNF, transport, tapHost string, socket string, ifID uint32, sharedAddress, sharedIPv6Address string) *rookeryv1.VNFSpec {
	return &rookeryv1.VNFSpec{
		Id:                vnf.ID,
		Name:              vnf.Name,
		Type:              vnf.Type,
		NetworkId:         vnf.NetworkID,
		Transport:         transport,
		Config:            configToProto(vnf.Config),
		TapHostName:       tapHost,
		MemifSocket:       socket,
		MemifSwIfIndex:    ifID,
		SharedAddress:     sharedAddress,
		SharedIpv6Address: sharedIPv6Address,
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
	dnsRecords := make([]*rookeryv1.DNSRecord, 0, len(config.DNSRecords))
	for _, record := range config.DNSRecords {
		dnsRecords = append(dnsRecords, &rookeryv1.DNSRecord{
			Name: record.Name, Type: record.Type, Value: record.Value, Ttl: record.TTL,
		})
	}
	dhcpOptions := make([]*rookeryv1.DHCPOption, 0, len(config.DHCPOptions))
	for _, option := range config.DHCPOptions {
		dhcpOptions = append(dhcpOptions, &rookeryv1.DHCPOption{
			Code: uint32(option.Code), Format: option.Format, Value: option.Value,
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
		DnsRecords:        dnsRecords,
		DnsForward:        config.DNSForward,
		DnsForwarders:     append([]string(nil), config.DNSForwarders...),
		DnsVnfId:          config.DNSVNFID,
		DnsDomain:         config.DNSDomain,
		DnsTtl:            config.DNSTTL,
		DhcpOptions:       dhcpOptions,
	}
}
