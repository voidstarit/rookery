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
	"google.golang.org/grpc/codes"
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
	dhcp   rookeryv1.DHCPDaemonClient
	dns    rookeryv1.DNSDaemonClient
	bgp    rookeryv1.BGPDaemonClient
	admin  rookeryv1.VNFRuntimeAdminClient
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
		dhcp:   rookeryv1.NewDHCPDaemonClient(conn),
		dns:    rookeryv1.NewDNSDaemonClient(conn),
		bgp:    rookeryv1.NewBGPDaemonClient(conn),
		admin:  rookeryv1.NewVNFRuntimeAdminClient(conn),
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

// ensure submits the type-specific runtime spec for the given transport and
// endpoint to the daemon and wraps RPC failures with the start action; it backs
// Start, StartMemif, and StartShared.
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
	runtime := runtimeSpecFor(vnf, transport, tapHost, socket, ifID, sharedAddress, sharedIPv6Address)
	var running bool
	switch vnf.Type {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
		state, err := c.dhcp.EnsureDHCP(callCtx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpRuntimeSpec(vnf, runtime)})
		if err != nil {
			c.logger.Trace().Int64("vnf_id", vnf.ID).Str("transport", transport).Err(err).Msg("ensure DHCP runtime failed")
			return grpcError("start VNF", err)
		}
		running = state.GetRunning()
	case store.VNFTypeDNS:
		state, err := c.dns.EnsureDNS(callCtx, &rookeryv1.EnsureDNSRequest{Spec: dnsRuntimeSpec(vnf, runtime)})
		if err != nil {
			c.logger.Trace().Int64("vnf_id", vnf.ID).Str("transport", transport).Err(err).Msg("ensure DNS runtime failed")
			return grpcError("start VNF", err)
		}
		running = state.GetRunning()
	case store.VNFTypeBGP:
		state, err := c.bgp.EnsureBGP(callCtx, &rookeryv1.EnsureBGPRequest{Spec: bgpRuntimeSpec(vnf, runtime)})
		if err != nil {
			c.logger.Trace().Int64("vnf_id", vnf.ID).Str("transport", transport).Err(err).Msg("ensure BGP runtime failed")
			return grpcError("start VNF", err)
		}
		running = state.GetRunning()
	default:
		return grpcError("start VNF", status.Error(codes.InvalidArgument, "VNF type "+vnf.Type+" is not served by the daemon"))
	}
	c.logger.Trace().Int64("vnf_id", vnf.ID).Bool("running", running).Msg("VNF runtime ensured")
	return nil
}

// Stop removes a runtime through the daemon. NAT44 has no daemon runtime, so
// stopping it is a no-op.
func (c *Client) Stop(ctx context.Context, vnf store.VNF, removeLeases bool) error {
	c.logger.Trace().Int64("vnf_id", vnf.ID).Str("type", vnf.Type).Bool("remove_leases", removeLeases).Msg("removing VNF runtime")
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var err error
	switch vnf.Type {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
		_, err = c.dhcp.RemoveDHCP(callCtx, &rookeryv1.RemoveDHCPRequest{Id: vnf.ID, RemoveLeases: removeLeases})
	case store.VNFTypeDNS:
		_, err = c.dns.RemoveDNS(callCtx, &rookeryv1.RemoveDNSRequest{Id: vnf.ID})
	case store.VNFTypeBGP:
		_, err = c.bgp.RemoveBGP(callCtx, &rookeryv1.RemoveBGPRequest{Id: vnf.ID})
	case store.VNFTypeNAT44:
		return nil
	default:
		return grpcError("stop VNF", status.Error(codes.InvalidArgument, "VNF type "+vnf.Type+" is not served by the daemon"))
	}
	if err != nil {
		c.logger.Trace().Int64("vnf_id", vnf.ID).Err(err).Msg("remove VNF runtime failed")
		return grpcError("stop VNF", err)
	}
	c.logger.Trace().Int64("vnf_id", vnf.ID).Msg("VNF runtime removed")
	return nil
}

// Running asks the daemon whether a runtime is currently alive. It is only
// called when a caller needs the state (a view or a reconciliation decision),
// never by a background poll. NAT44 has no daemon runtime and is never running.
func (c *Client) Running(ctx context.Context, vnf store.VNF) bool {
	switch vnf.Type {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6, store.VNFTypeDNS, store.VNFTypeBGP:
	default:
		return false
	}
	callCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	state, err := c.state(callCtx, vnf)
	if err != nil {
		c.logger.Trace().Int64("vnf_id", vnf.ID).Str("type", vnf.Type).Err(err).Msg("VNF state query failed")
		return false
	}
	c.logger.Trace().Int64("vnf_id", vnf.ID).Bool("running", state.GetRunning()).Msg("VNF state queried")
	return state.GetRunning()
}

// state queries the per-type state RPC.
func (c *Client) state(ctx context.Context, vnf store.VNF) (*rookeryv1.VNFState, error) {
	switch vnf.Type {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
		return c.dhcp.GetDHCPState(ctx, &rookeryv1.GetDHCPStateRequest{Id: vnf.ID})
	case store.VNFTypeDNS:
		return c.dns.GetDNSState(ctx, &rookeryv1.GetDNSStateRequest{Id: vnf.ID})
	case store.VNFTypeBGP:
		return c.bgp.GetBGPState(ctx, &rookeryv1.GetBGPStateRequest{Id: vnf.ID})
	}
	return nil, status.Error(codes.InvalidArgument, "VNF type "+vnf.Type+" is not served by the daemon")
}

// ReloadVNF swaps a running runtime's configuration in place. It returns nil
// when the runtime is not running; the VNF then stays degraded. NAT44 has no
// daemon runtime, so reloading it is a no-op.
func (c *Client) ReloadVNF(ctx context.Context, vnf store.VNF) error {
	c.logger.Trace().Int64("vnf_id", vnf.ID).Str("type", vnf.Type).Msg("reloading VNF runtime")
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	runtime := runtimeSpecFor(vnf, vnf.Config.Transport, vnf.TapHostName, vnf.MemifSocket, vnf.MemifSwIfIndex, "", "")
	var err error
	switch vnf.Type {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
		_, err = c.dhcp.ReloadDHCP(callCtx, &rookeryv1.ReloadDHCPRequest{Spec: dhcpRuntimeSpec(vnf, runtime)})
	case store.VNFTypeDNS:
		_, err = c.dns.ReloadDNS(callCtx, &rookeryv1.ReloadDNSRequest{Spec: dnsRuntimeSpec(vnf, runtime)})
	case store.VNFTypeBGP:
		_, err = c.bgp.ReloadBGP(callCtx, &rookeryv1.ReloadBGPRequest{Spec: bgpRuntimeSpec(vnf, runtime)})
	case store.VNFTypeNAT44:
		return nil
	default:
		return grpcError("reload VNF", status.Error(codes.InvalidArgument, "VNF type "+vnf.Type+" is not served by the daemon"))
	}
	if err != nil {
		c.logger.Trace().Int64("vnf_id", vnf.ID).Err(err).Msg("reload VNF runtime failed")
		return grpcError("reload VNF", err)
	}
	c.logger.Trace().Int64("vnf_id", vnf.ID).Msg("VNF runtime reloaded")
	return nil
}

// Leases returns the daemon's persisted leases for a DHCP VNF.
func (c *Client) Leases(ctx context.Context, vnfID int64) ([]store.VNFLease, error) {
	c.logger.Trace().Int64("vnf_id", vnfID).Msg("reading VNF leases")
	callCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	response, err := c.dhcp.ListDHCPLeases(callCtx, &rookeryv1.ListDHCPLeasesRequest{Id: vnfID})
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

// RemoveLeases drops specific DHCP leases from a VNF runtime.
func (c *Client) RemoveLeases(ctx context.Context, vnfID int64, clientIDs []string) (int, error) {
	callCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	response, err := c.dhcp.RemoveDHCPLeases(callCtx, &rookeryv1.RemoveDHCPLeasesRequest{
		Id: vnfID, ClientIds: clientIDs,
	})
	if err != nil {
		return 0, grpcError("remove VNF leases", err)
	}
	removed := int(response.GetRemoved())
	c.logger.Trace().
		Int64("vnf_id", vnfID).
		Int("requested", len(clientIDs)).
		Int("removed", removed).
		Msg("VNF leases removed")
	return removed, nil
}

// DNSRecords returns the dynamic DNS records registered for a DNS VNF.
func (c *Client) DNSRecords(ctx context.Context, vnfID int64) ([]host.DNSRecordView, error) {
	callCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	response, err := c.dns.ListDNSRecords(callCtx, &rookeryv1.ListDNSRecordsRequest{Id: vnfID})
	if err != nil {
		return nil, grpcError("read DNS records", err)
	}
	records := make([]host.DNSRecordView, 0, len(response.GetRecords()))
	for _, record := range response.GetRecords() {
		records = append(records, host.DNSRecordView{
			Name: record.GetName(), Type: record.GetType(), Value: record.GetValue(), TTL: record.GetTtl(),
			Source: record.GetSource(), SourceVNFID: record.GetSourceVnfId(),
		})
	}
	return records, nil
}

// BGPStatus returns the daemon's live GoBGP snapshot for a BGP VNF.
func (c *Client) BGPStatus(ctx context.Context, vnfID int64) (host.BGPStatus, error) {
	c.logger.Trace().Int64("vnf_id", vnfID).Msg("reading BGP status")
	callCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	response, err := c.bgp.GetBGPStatus(callCtx, &rookeryv1.GetBGPStatusRequest{Id: vnfID})
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
	if _, err := c.admin.CleanupLegacyNetwork(ctx, &rookeryv1.CleanupLegacyNetworkRequest{NetworkId: networkID}); err != nil {
		c.logger.Warn().Int64("network_id", networkID).Err(err).Msg("cleanup legacy VNF runtime failed")
	}
}

// ResetRuntime stops every daemon runtime while keeping the desired specs.
func (c *Client) ResetRuntime(ctx context.Context) error {
	c.logger.Trace().Msg("resetting VNF runtimes")
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if _, err := c.admin.ResetRuntime(callCtx, &rookeryv1.ResetRuntimeRequest{}); err != nil {
		c.logger.Trace().Err(err).Msg("reset VNF runtimes failed")
		return grpcError("reset VNF runtime", err)
	}
	c.logger.Trace().Msg("VNF runtimes reset")
	return nil
}

// grpcError converts a gRPC status into a clientError carrying action and the
// status message, falling back to err's text for non-status errors.
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

// Error renders the clientError as "<action>: <message>" for logs and error
// wrapping.
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

// runtimeSpecFor builds the shared runtime identity and transport fields from
// an API-side VNF record.
func runtimeSpecFor(vnf store.VNF, transport, tapHost string, socket string, ifID uint32, sharedAddress, sharedIPv6Address string) *rookeryv1.RuntimeSpec {
	if strings.TrimSpace(transport) == "" {
		transport = vnf.Config.Transport
	}
	if strings.TrimSpace(tapHost) == "" {
		tapHost = vnf.TapHostName
	}
	if strings.TrimSpace(socket) == "" {
		socket = vnf.MemifSocket
	}
	if ifID == 0 {
		ifID = vnf.MemifSwIfIndex
	}
	return &rookeryv1.RuntimeSpec{
		Id:                vnf.ID,
		Name:              vnf.Name,
		NetworkId:         vnf.NetworkID,
		Transport:         transport,
		TapHostName:       tapHost,
		MemifSocket:       socket,
		MemifSwIfIndex:    ifID,
		SharedAddress:     sharedAddress,
		SharedIpv6Address: sharedIPv6Address,
	}
}

// dhcpRuntimeSpec builds the full DHCPv4/DHCPv6 runtime spec for a VNF.
func dhcpRuntimeSpec(vnf store.VNF, runtime *rookeryv1.RuntimeSpec) *rookeryv1.DHCPRuntimeSpec {
	spec := &rookeryv1.DHCPRuntimeSpec{Runtime: runtime, Family: vnf.Type}
	options := dhcpOptionsToProto(vnf.Config.DHCPOptions)
	switch vnf.Type {
	case store.VNFTypeDHCPv6:
		spec.Config = &rookeryv1.DHCPRuntimeSpec_Dhcpv6{Dhcpv6: &rookeryv1.DHCPV6RuntimeConfig{
			Gateway:    vnf.Config.Gateway,
			RangeStart: vnf.Config.RangeStart,
			RangeEnd:   vnf.Config.RangeEnd,
			LeaseTime:  vnf.Config.LeaseTime,
			Dns:        vnf.Config.DNS,
			BindIp:     vnf.Config.BindIP,
			DnsVnfId:   vnf.Config.DNSVNFID,
			DnsDomain:  vnf.Config.DNSDomain,
			DnsTtl:     vnf.Config.DNSTTL,
			Options:    options,
		}}
	default:
		static := make([]*rookeryv1.DHCPStaticAssignmentSpec, 0, len(vnf.Config.StaticAssignments))
		for _, assignment := range vnf.Config.StaticAssignments {
			static = append(static, &rookeryv1.DHCPStaticAssignmentSpec{Mac: assignment.MAC, Ip: assignment.IP})
		}
		spec.Family = store.VNFTypeDHCPv4
		spec.Config = &rookeryv1.DHCPRuntimeSpec_Dhcpv4{Dhcpv4: &rookeryv1.DHCPV4RuntimeConfig{
			Gateway:           vnf.Config.Gateway,
			RangeStart:        vnf.Config.RangeStart,
			RangeEnd:          vnf.Config.RangeEnd,
			LeaseTime:         vnf.Config.LeaseTime,
			Dns:               vnf.Config.DNS,
			BindIp:            vnf.Config.BindIP,
			DnsVnfId:          vnf.Config.DNSVNFID,
			DnsDomain:         vnf.Config.DNSDomain,
			DnsTtl:            vnf.Config.DNSTTL,
			StaticAssignments: static,
			Options:           options,
		}}
	}
	return spec
}

// dnsRuntimeSpec builds the full DNS runtime spec for a VNF.
func dnsRuntimeSpec(vnf store.VNF, runtime *rookeryv1.RuntimeSpec) *rookeryv1.DNSRuntimeSpec {
	records := make([]*rookeryv1.DNSRecordSpec, 0, len(vnf.Config.DNSRecords))
	for _, record := range vnf.Config.DNSRecords {
		records = append(records, &rookeryv1.DNSRecordSpec{
			Name: record.Name, Type: record.Type, Value: record.Value, Ttl: record.TTL,
		})
	}
	forwarders := make([]*rookeryv1.DNSForwarderSpec, 0, len(vnf.Config.DNSForwarders))
	for index, address := range vnf.Config.DNSForwarders {
		forwarders = append(forwarders, &rookeryv1.DNSForwarderSpec{Address: address, Position: int32(index)})
	}
	return &rookeryv1.DNSRuntimeSpec{
		Runtime: runtime,
		Config: &rookeryv1.DNSRuntimeConfig{
			Forward:    vnf.Config.DNSForward,
			Records:    records,
			Forwarders: forwarders,
		},
	}
}

// bgpRuntimeSpec builds the full BGP runtime spec for a VNF.
func bgpRuntimeSpec(vnf store.VNF, runtime *rookeryv1.RuntimeSpec) *rookeryv1.BGPRuntimeSpec {
	prefixes := make([]*rookeryv1.BGPPrefixSpec, 0, len(vnf.Config.AdvertisePrefixes))
	for _, prefix := range vnf.Config.AdvertisePrefixes {
		prefixes = append(prefixes, &rookeryv1.BGPPrefixSpec{Prefix: prefix})
	}
	return &rookeryv1.BGPRuntimeSpec{
		Runtime: runtime,
		Config: &rookeryv1.BGPRuntimeConfig{
			LocalAs:            vnf.Config.LocalAS,
			RouterId:           vnf.Config.RouterID,
			PeerAs:             vnf.Config.PeerAS,
			PeerIp:             vnf.Config.PeerIP,
			LocalIp:            vnf.Config.LocalIP,
			PeerGateway:        vnf.Config.PeerGateway,
			MultihopTtl:        vnf.Config.MultihopTTL,
			Password:           vnf.Config.Password,
			Passive:            vnf.Config.Passive,
			TargetTableId:      vnf.Config.TargetTableID,
			NextHop:            vnf.Config.NextHop,
			AdvertisedPrefixes: prefixes,
		},
	}
}

// dhcpOptionsToProto converts extra DHCPv4/v6 options.
func dhcpOptionsToProto(options []store.DHCPOption) []*rookeryv1.DHCPOptionSpec {
	items := make([]*rookeryv1.DHCPOptionSpec, 0, len(options))
	for _, option := range options {
		items = append(items, &rookeryv1.DHCPOptionSpec{
			Code: uint32(option.Code), Format: option.Format, Value: option.Value,
		})
	}
	return items
}
