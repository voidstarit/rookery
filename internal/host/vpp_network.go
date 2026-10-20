package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.fd.io/govpp"
	"go.fd.io/govpp/binapi/ethernet_types"
	"go.fd.io/govpp/binapi/fib_types"
	"go.fd.io/govpp/binapi/interface"
	"go.fd.io/govpp/binapi/interface_types"
	ipapi "go.fd.io/govpp/binapi/ip"
	"go.fd.io/govpp/binapi/ip_types"
	"go.fd.io/govpp/binapi/l2"
	"go.fd.io/govpp/binapi/memif"
	"go.fd.io/govpp/binapi/tapv2"
	"go.fd.io/govpp/binapi/vhost_user"
	"go.fd.io/govpp/core"
)

const (
	// DefaultVhostDir is where VPP vhost-user sockets are created.
	DefaultVhostDir   = "/run/vpp"
	vppCleanupTimeout = 3 * time.Second
)

// VPPNetwork provisions VPP bridge domains and vhost-user interfaces.
type VPPNetwork struct {
	Socket   string
	VhostDir string
	CLI      string
	// CaptureDir is where VPP writes packet capture files. VPP's pcap CLI
	// only accepts a basename and always writes under /tmp.
	CaptureDir string

	captureMu sync.Mutex
	capture   *PacketCaptureStatus
}

// NewVPPNetwork returns a VPP network provisioner.
func NewVPPNetwork(socket, vhostDir string) (*VPPNetwork, error) {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		return nil, fmt.Errorf("VPP API socket is not configured")
	}
	if strings.TrimSpace(vhostDir) == "" {
		vhostDir = DefaultVhostDir
	}
	abs, err := filepath.Abs(vhostDir)
	if err != nil {
		return nil, fmt.Errorf("resolve vhost directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create vhost directory: %w", err)
	}
	return &VPPNetwork{Socket: socket, VhostDir: abs, CLI: "vppctl", CaptureDir: "/tmp"}, nil
}

// Ping checks the VPP binary API with a control ping.
func (v *VPPNetwork) Ping(ctx context.Context) error {
	return PingVPP(ctx, v.Socket)
}

// Identity returns a stable identity for the VPP API socket. VPP recreates the
// socket when it restarts, so a change indicates a new VPP instance.
func (v *VPPNetwork) Identity() (string, error) {
	info, err := os.Stat(v.Socket)
	if err != nil {
		return "", fmt.Errorf("stat VPP API socket %s: %w", v.Socket, err)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
	}
	return fmt.Sprintf("%d", info.ModTime().UnixNano()), nil
}

// InterfaceCounters contains cumulative VPP counters for a vhost interface.
type InterfaceCounters struct {
	RXPackets uint64 `json:"rx_packets"`
	TXPackets uint64 `json:"tx_packets"`
	RXBytes   uint64 `json:"rx_bytes"`
	TXBytes   uint64 `json:"tx_bytes"`
}

// InterfaceCounters returns counters for the named VPP interfaces.
func (v *VPPNetwork) InterfaceCounters(ctx context.Context, names []string) (map[string]InterfaceCounters, error) {
	result := make(map[string]InterfaceCounters, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		out, err := v.runCLI(ctx, "show", "interface", name)
		if err != nil {
			return nil, fmt.Errorf("show interface %s: %w: %s", name, err, out)
		}
		var counters InterfaceCounters
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			value, err := strconv.ParseUint(fields[2], 10, 64)
			if err != nil {
				continue
			}
			switch fields[0] + " " + fields[1] {
			case "rx packets":
				counters.RXPackets = value
			case "tx packets":
				counters.TXPackets = value
			case "rx bytes":
				counters.RXBytes = value
			case "tx bytes":
				counters.TXBytes = value
			}
		}
		result[name] = counters
	}
	return result, nil
}

// CreateBridgeDomain creates a VPP bridge domain and returns its ID.
func (v *VPPNetwork) CreateBridgeDomain(ctx context.Context, name string, reserved []uint32) (uint32, error) {
	conn, err := v.connect()
	if err != nil {
		return 0, err
	}
	defer conn.Disconnect()

	used := make(map[uint32]struct{}, len(reserved))
	for _, id := range reserved {
		used[id] = struct{}{}
	}

	client := l2.NewServiceClient(conn)
	for bdID := uint32(100); bdID < 10000; bdID++ {
		if _, skip := used[bdID]; skip {
			continue
		}
		reply, err := client.BridgeDomainAddDelV2(ctx, &l2.BridgeDomainAddDelV2{
			BdID:    bdID,
			Flood:   true,
			UuFlood: true,
			Forward: true,
			Learn:   true,
			ArpTerm: true,
			BdTag:   name,
			IsAdd:   true,
		})
		if err != nil {
			if isBridgeDomainExists(err) {
				continue
			}
			return 0, fmt.Errorf("create bridge domain: %w", err)
		}
		if err := checkVPPRetval("create bridge domain", reply.Retval); err != nil {
			if isBridgeDomainExists(err) {
				continue
			}
			return 0, err
		}
		return reply.BdID, nil
	}
	return 0, fmt.Errorf("create bridge domain: no free bridge domain ID")
}

// EnsureBridgeDomain creates a bridge domain with a specific ID if it is
// missing. It is used to restore network state after a VPP restart.
func (v *VPPNetwork) EnsureBridgeDomain(ctx context.Context, bdID uint32, name string) error {
	if bdID == 0 {
		return errors.New("bridge domain ID is required")
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	client := l2.NewServiceClient(conn)
	stream, err := client.BridgeDomainDump(ctx, &l2.BridgeDomainDump{BdID: bdID})
	if err != nil {
		return fmt.Errorf("dump bridge domain %d: %w", bdID, err)
	}
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read bridge domain dump: %w", err)
		}
		if details.BdID != bdID {
			continue
		}
		if details.BdTag != "" && details.BdTag != name {
			return fmt.Errorf("bridge domain %d already exists with tag %q", bdID, details.BdTag)
		}
		return nil
	}

	reply, err := client.BridgeDomainAddDelV2(ctx, &l2.BridgeDomainAddDelV2{
		BdID:    bdID,
		Flood:   true,
		UuFlood: true,
		Forward: true,
		Learn:   true,
		ArpTerm: true,
		BdTag:   name,
		IsAdd:   true,
	})
	if err != nil {
		if isBridgeDomainExists(err) {
			return nil
		}
		return fmt.Errorf("recreate bridge domain %d: %w", bdID, err)
	}
	if reply.Retval != 0 {
		return fmt.Errorf("recreate bridge domain %d: VPP returned retval %d", bdID, reply.Retval)
	}
	return nil
}

// DeleteBridgeDomain removes a VPP bridge domain.
func (v *VPPNetwork) DeleteBridgeDomain(ctx context.Context, bdID uint32) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := l2.NewServiceClient(conn).BridgeDomainAddDelV2(ctx, &l2.BridgeDomainAddDelV2{
		BdID:  bdID,
		IsAdd: false,
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete bridge domain %d: %w", bdID, err)
	}
	return checkVPPRetval("delete bridge domain", reply.Retval)
}

// CreateVhostUser creates a vhost-user interface, brings it up, and adds it to
// the bridge domain. It returns the socket path and VPP sw_if_index.
func (v *VPPNetwork) CreateVhostUser(ctx context.Context, name, mac string, bdID uint32) (string, uint32, error) {
	macAddr, err := ethernet_types.ParseMacAddress(mac)
	if err != nil {
		return "", 0, fmt.Errorf("parse MAC %q: %w", mac, err)
	}
	socket := filepath.Join(v.VhostDir, name+".sock")
	if len(socket) > 255 {
		return "", 0, fmt.Errorf("vhost socket path is too long: %s", socket)
	}

	conn, err := v.connect()
	if err != nil {
		return "", 0, err
	}
	defer conn.Disconnect()

	vhostClient := vhost_user.NewServiceClient(conn)
	reply, err := vhostClient.CreateVhostUserIf(ctx, &vhost_user.CreateVhostUserIf{
		IsServer:     true,
		SockFilename: socket,
		UseCustomMac: true,
		MacAddress:   macAddr,
		Tag:          name,
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unknown message") {
			return "", 0, fmt.Errorf("create vhost-user interface: %w (install/load vpp-plugin-core and restart VPP)", err)
		}
		return "", 0, fmt.Errorf("create vhost-user interface: %w", err)
	}
	if err := checkVPPRetval("create vhost-user interface", reply.Retval); err != nil {
		return "", 0, err
	}
	swIfIndex := reply.SwIfIndex

	rollback := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), vppCleanupTimeout)
		defer cancel()
		_, _ = vhostClient.DeleteVhostUserIf(cleanupCtx, &vhost_user.DeleteVhostUserIf{SwIfIndex: swIfIndex})
	}

	nameReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetInterfaceName(ctx, &interfaces.SwInterfaceSetInterfaceName{
		SwIfIndex: swIfIndex,
		Name:      name,
	})
	if err != nil {
		rollback()
		return "", 0, fmt.Errorf("name vhost-user interface: %w", err)
	}
	if err := checkVPPRetval("name vhost-user interface", nameReply.Retval); err != nil {
		rollback()
		return "", 0, err
	}

	flagsReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetFlags(ctx, &interfaces.SwInterfaceSetFlags{
		SwIfIndex: swIfIndex,
		Flags:     interface_types.IF_STATUS_API_FLAG_ADMIN_UP,
	})
	if err != nil {
		rollback()
		return "", 0, fmt.Errorf("set vhost-user interface up: %w", err)
	}
	if err := checkVPPRetval("set vhost-user interface up", flagsReply.Retval); err != nil {
		rollback()
		return "", 0, err
	}

	l2Reply, err := l2.NewServiceClient(conn).SwInterfaceSetL2Bridge(ctx, &l2.SwInterfaceSetL2Bridge{
		RxSwIfIndex: swIfIndex,
		BdID:        bdID,
		PortType:    l2.L2_API_PORT_TYPE_NORMAL,
		Enable:      true,
	})
	if err != nil {
		rollback()
		return "", 0, fmt.Errorf("attach vhost-user interface to bridge domain: %w", err)
	}
	if err := checkVPPRetval("attach vhost-user interface to bridge domain", l2Reply.Retval); err != nil {
		rollback()
		return "", 0, err
	}

	// VPP creates the Unix socket as part of interface creation.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			rollback()
			return "", 0, fmt.Errorf("vhost socket %s was not created", socket)
		}
		time.Sleep(50 * time.Millisecond)
	}

	return socket, uint32(swIfIndex), nil
}

// EnsureVhostUser finds or recreates a vhost-user interface by name. It is used
// by startup reconciliation after a VPP restart, when all vhost interfaces have
// been lost but their Unix socket files may still exist.
func (v *VPPNetwork) EnsureVhostUser(ctx context.Context, name, mac string, bdID uint32) (string, uint32, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", 0, errors.New("vhost interface name is required")
	}
	if bdID == 0 {
		return "", 0, errors.New("bridge domain id is required")
	}
	expectedSocket := filepath.Join(v.VhostDir, name+".sock")
	conn, err := v.connect()
	if err != nil {
		return "", 0, err
	}
	defer conn.Disconnect()

	vhostClient := vhost_user.NewServiceClient(conn)
	stream, err := vhostClient.SwInterfaceVhostUserDump(ctx, &vhost_user.SwInterfaceVhostUserDump{
		SwIfIndex: interface_types.InterfaceIndex(^uint32(0)),
	})
	if err != nil {
		return "", 0, fmt.Errorf("dump vhost-user interfaces: %w", err)
	}
	var foundIndex uint32
	var foundSocket string
	for {
		details, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return "", 0, fmt.Errorf("read vhost-user interface dump: %w", recvErr)
		}
		if details.InterfaceName == name {
			foundIndex = uint32(details.SwIfIndex)
			foundSocket = strings.TrimSpace(details.SockFilename)
			break
		}
	}
	if foundIndex != 0 {
		if foundSocket == "" {
			foundSocket = expectedSocket
		}
		flagsReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetFlags(ctx, &interfaces.SwInterfaceSetFlags{
			SwIfIndex: interface_types.InterfaceIndex(foundIndex),
			Flags:     interface_types.IF_STATUS_API_FLAG_ADMIN_UP,
		})
		if err != nil {
			return "", 0, fmt.Errorf("set vhost-user interface %s up: %w", name, err)
		}
		if err := checkVPPRetval("set vhost-user interface up", flagsReply.Retval); err != nil {
			return "", 0, err
		}
		l2Reply, err := l2.NewServiceClient(conn).SwInterfaceSetL2Bridge(ctx, &l2.SwInterfaceSetL2Bridge{
			RxSwIfIndex: interface_types.InterfaceIndex(foundIndex),
			BdID:        bdID,
			PortType:    l2.L2_API_PORT_TYPE_NORMAL,
			Enable:      true,
		})
		if err != nil {
			return "", 0, fmt.Errorf("attach vhost-user interface %s to bridge domain: %w", name, err)
		}
		if err := checkVPPRetval("attach vhost-user interface to bridge domain", l2Reply.Retval); err != nil {
			return "", 0, err
		}
		return foundSocket, foundIndex, nil
	}

	// VPP has no interface by this name. The socket file is stale (for example,
	// left over after a VPP restart), so remove it before recreating the
	// vhost-user listener.
	if err := os.Remove(expectedSocket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", 0, fmt.Errorf("remove stale vhost socket %s: %w", expectedSocket, err)
	}
	return v.CreateVhostUser(ctx, name, mac, bdID)
}

// DeleteVhostUser removes a vhost-user interface.
func (v *VPPNetwork) DeleteVhostUser(ctx context.Context, swIfIndex uint32) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := vhost_user.NewServiceClient(conn).DeleteVhostUserIf(ctx, &vhost_user.DeleteVhostUserIf{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete vhost-user interface %d: %w", swIfIndex, err)
	}
	return checkVPPRetval("delete vhost-user interface", reply.Retval)
}

// CreateTap creates a VPP tap interface, brings it up, and adds it to the
// bridge domain. The host side is named hostName and configured with gatewayCIDR.
func (v *VPPNetwork) CreateTap(ctx context.Context, hostName, gatewayCIDR string, bdID uint32) (uint32, error) {
	if len(hostName) > 15 {
		return 0, fmt.Errorf("tap host interface name %q is too long", hostName)
	}

	tap := &tapv2.TapCreateV2{
		// VPP uses ^uint32(0) as the "auto assign" tap device ID. Sending the
		// zero value instead creates device instance 0, which conflicts with
		// any existing tap and makes a second CreateTapV2 fail.
		ID:            ^uint32(0),
		UseRandomMac:  true,
		HostIfNameSet: true,
		HostIfName:    hostName,
		Tag:           "nerds-vnf:" + hostName,
	}
	if strings.Contains(gatewayCIDR, ":") {
		prefix, err := ip_types.ParseIP6Prefix(gatewayCIDR)
		if err != nil {
			return 0, fmt.Errorf("parse VNF gateway %q: %w", gatewayCIDR, err)
		}
		tap.HostIP6PrefixSet = true
		tap.HostIP6Prefix = ip_types.IP6AddressWithPrefix(prefix)
	} else {
		prefix, err := ip_types.ParseIP4Prefix(gatewayCIDR)
		if err != nil {
			return 0, fmt.Errorf("parse VNF gateway %q: %w", gatewayCIDR, err)
		}
		tap.HostIP4PrefixSet = true
		tap.HostIP4Prefix = ip_types.IP4AddressWithPrefix(prefix)
	}

	conn, err := v.connect()
	if err != nil {
		return 0, err
	}
	defer conn.Disconnect()

	tapClient := tapv2.NewServiceClient(conn)
	reply, err := tapClient.TapCreateV2(ctx, tap)
	if err != nil {
		return 0, fmt.Errorf("create VNF tap: %w", err)
	}
	if err := checkVPPRetval("create VNF tap", reply.Retval); err != nil {
		return 0, err
	}
	swIfIndex := reply.SwIfIndex

	rollback := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), vppCleanupTimeout)
		defer cancel()
		_, _ = tapClient.TapDeleteV2(cleanupCtx, &tapv2.TapDeleteV2{SwIfIndex: swIfIndex})
	}

	nameReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetInterfaceName(ctx, &interfaces.SwInterfaceSetInterfaceName{
		SwIfIndex: swIfIndex,
		Name:      hostName,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("name VNF tap: %w", err)
	}
	if err := checkVPPRetval("name VNF tap", nameReply.Retval); err != nil {
		rollback()
		return 0, err
	}

	flagsReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetFlags(ctx, &interfaces.SwInterfaceSetFlags{
		SwIfIndex: swIfIndex,
		Flags:     interface_types.IF_STATUS_API_FLAG_ADMIN_UP,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("set VNF tap up: %w", err)
	}
	if err := checkVPPRetval("set VNF tap up", flagsReply.Retval); err != nil {
		rollback()
		return 0, err
	}

	l2Reply, err := l2.NewServiceClient(conn).SwInterfaceSetL2Bridge(ctx, &l2.SwInterfaceSetL2Bridge{
		RxSwIfIndex: swIfIndex,
		BdID:        bdID,
		PortType:    l2.L2_API_PORT_TYPE_NORMAL,
		Enable:      true,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("attach VNF tap to bridge domain: %w", err)
	}
	if err := checkVPPRetval("attach VNF tap to bridge domain", l2Reply.Retval); err != nil {
		rollback()
		return 0, err
	}

	return uint32(swIfIndex), nil
}

// DeleteTapByName removes a tap by its VPP interface name. This avoids using a
// stale sw_if_index after a VPP restart, where an old index may refer to an
// unrelated interface.
func (v *VPPNetwork) DeleteTapByName(ctx context.Context, hostName string) error {
	if strings.TrimSpace(hostName) == "" {
		return nil
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	client := interfaces.NewServiceClient(conn)
	stream, err := client.SwInterfaceDump(ctx, &interfaces.SwInterfaceDump{})
	if err != nil {
		return fmt.Errorf("dump VPP interfaces: %w", err)
	}
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read VPP interface dump: %w", err)
		}
		if details.InterfaceName != hostName {
			continue
		}
		// Only delete interfaces created by this service. A user-created
		// vhost-user interface could otherwise share the same name.
		if details.Tag != "" && details.Tag != "nerds-vnf:"+hostName && details.Tag != "nerds-dhcp:"+hostName {
			return nil
		}
		reply, err := tapv2.NewServiceClient(conn).TapDeleteV2(ctx, &tapv2.TapDeleteV2{
			SwIfIndex: details.SwIfIndex,
		})
		if err != nil {
			if isVPPNotFound(err) {
				return nil
			}
			return fmt.Errorf("delete VNF tap %s: %w", hostName, err)
		}
		return checkVPPRetval("delete VNF tap", reply.Retval)
	}
}

// DeleteTap removes a VPP tap interface.
func (v *VPPNetwork) DeleteTap(ctx context.Context, swIfIndex uint32) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := tapv2.NewServiceClient(conn).TapDeleteV2(ctx, &tapv2.TapDeleteV2{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete DHCP tap %d: %w", swIfIndex, err)
	}
	return checkVPPRetval("delete VNF tap", reply.Retval)
}

// CreateMemif creates a VPP memif master interface, brings it up, and adds it
// to the bridge domain. The userspace peer connects to socketPath as a slave.
func (v *VPPNetwork) CreateMemif(ctx context.Context, name, socketPath string, ifID uint32, mac net.HardwareAddr, bdID uint32) (uint32, error) {
	if strings.TrimSpace(name) == "" {
		return 0, errors.New("memif interface name is required")
	}
	if len(name) > 63 {
		return 0, fmt.Errorf("memif interface name %q exceeds the VPP limit of 63 characters", name)
	}
	if strings.TrimSpace(socketPath) == "" {
		return 0, errors.New("memif socket path is required")
	}
	if len(mac) != 6 {
		return 0, errors.New("memif MAC address must contain 6 bytes")
	}

	conn, err := v.connect()
	if err != nil {
		return 0, err
	}
	defer conn.Disconnect()
	memifClient := memif.NewServiceClient(conn)

	// Reuse the VNF id as the memif socket id. Clear a stale registration first
	// so deleting and recreating a VNF does not fail on an old socket path.
	_, _ = memifClient.MemifSocketFilenameAddDelV2(ctx, &memif.MemifSocketFilenameAddDelV2{
		IsAdd:    false,
		SocketID: ifID,
	})
	socketReply, err := memifClient.MemifSocketFilenameAddDelV2(ctx, &memif.MemifSocketFilenameAddDelV2{
		IsAdd:          true,
		SocketID:       ifID,
		SocketFilename: socketPath,
	})
	if err != nil {
		return 0, fmt.Errorf("register memif socket %s: %w", socketPath, err)
	}
	if err := checkVPPRetval("register memif socket", socketReply.Retval); err != nil {
		return 0, err
	}

	hwAddr := ethernet_types.MacAddress{}
	copy(hwAddr[:], mac)
	reply, err := memifClient.MemifCreateV2(ctx, &memif.MemifCreateV2{
		Role:       memif.MEMIF_ROLE_API_MASTER,
		Mode:       memif.MEMIF_MODE_API_ETHERNET,
		RxQueues:   1,
		TxQueues:   1,
		ID:         ifID,
		SocketID:   socketReply.SocketID,
		RingSize:   memifDHCPRingSize,
		BufferSize: memifDHCPBufferSize,
		HwAddr:     hwAddr,
	})
	if err != nil {
		return 0, fmt.Errorf("create VNF memif: %w", err)
	}
	if err := checkVPPRetval("create VNF memif", reply.Retval); err != nil {
		return 0, err
	}
	swIfIndex := reply.SwIfIndex

	rollback := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), vppCleanupTimeout)
		defer cancel()
		_, _ = memifClient.MemifDelete(cleanupCtx, &memif.MemifDelete{SwIfIndex: swIfIndex})
	}

	nameReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetInterfaceName(ctx, &interfaces.SwInterfaceSetInterfaceName{
		SwIfIndex: swIfIndex,
		Name:      name,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("name VNF memif: %w", err)
	}
	if err := checkVPPRetval("name VNF memif", nameReply.Retval); err != nil {
		rollback()
		return 0, err
	}

	flagsReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetFlags(ctx, &interfaces.SwInterfaceSetFlags{
		SwIfIndex: swIfIndex,
		Flags:     interface_types.IF_STATUS_API_FLAG_ADMIN_UP,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("set VNF memif up: %w", err)
	}
	if err := checkVPPRetval("set VNF memif up", flagsReply.Retval); err != nil {
		rollback()
		return 0, err
	}

	l2Reply, err := l2.NewServiceClient(conn).SwInterfaceSetL2Bridge(ctx, &l2.SwInterfaceSetL2Bridge{
		RxSwIfIndex: swIfIndex,
		BdID:        bdID,
		PortType:    l2.L2_API_PORT_TYPE_NORMAL,
		Enable:      true,
	})
	if err != nil {
		rollback()
		return 0, fmt.Errorf("attach VNF memif to bridge domain: %w", err)
	}
	if err := checkVPPRetval("attach VNF memif to bridge domain", l2Reply.Retval); err != nil {
		rollback()
		return 0, err
	}

	return uint32(swIfIndex), nil
}

// DeleteMemif removes a VPP memif interface.
func (v *VPPNetwork) DeleteMemif(ctx context.Context, swIfIndex uint32) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := memif.NewServiceClient(conn).MemifDelete(ctx, &memif.MemifDelete{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete VNF memif %d: %w", swIfIndex, err)
	}
	return checkVPPRetval("delete VNF memif", reply.Retval)
}

// DeleteMemifByName removes a memif interface by its VPP name, tolerating a
// stale sw_if_index after a VPP restart.
func (v *VPPNetwork) DeleteMemifByName(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	client := interfaces.NewServiceClient(conn)
	stream, err := client.SwInterfaceDump(ctx, &interfaces.SwInterfaceDump{})
	if err != nil {
		return fmt.Errorf("dump VPP interfaces: %w", err)
	}
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read VPP interface dump: %w", err)
		}
		if details.InterfaceName != name {
			continue
		}
		if details.Tag != "" {
			return nil
		}
		reply, err := memif.NewServiceClient(conn).MemifDelete(ctx, &memif.MemifDelete{
			SwIfIndex: details.SwIfIndex,
		})
		if err != nil {
			if isVPPNotFound(err) {
				return nil
			}
			return fmt.Errorf("delete VNF memif %s: %w", name, err)
		}
		return checkVPPRetval("delete VNF memif", reply.Retval)
	}
}

// EnsureRouteTable creates a custom VPP IP FIB/VRF table if it does not exist.
func (v *VPPNetwork) EnsureRouteTable(ctx context.Context, tableID uint32, ipv6 bool, name string) error {
	if tableID == 0 {
		return nil
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := ipapi.NewServiceClient(conn).IPTableAddDel(ctx, &ipapi.IPTableAddDel{
		IsAdd: true,
		Table: ipapi.IPTable{TableID: tableID, IsIP6: ipv6, Name: name},
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return nil
		}
		return fmt.Errorf("create route table %d: %w", tableID, err)
	}
	if reply.Retval != 0 {
		return checkVPPRetval("create route table", reply.Retval)
	}
	return nil
}

// DeleteRouteTable removes a custom VPP IP FIB/VRF table.
func (v *VPPNetwork) DeleteRouteTable(ctx context.Context, tableID uint32, ipv6 bool) error {
	if tableID == 0 {
		return nil
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := ipapi.NewServiceClient(conn).IPTableAddDel(ctx, &ipapi.IPTableAddDel{
		IsAdd: false,
		Table: ipapi.IPTable{TableID: tableID, IsIP6: ipv6},
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete route table %d: %w", tableID, err)
	}
	return checkVPPRetval("delete route table", reply.Retval)
}

// AddRoute installs a static route in an IP FIB/VRF table. swIfIndex may be 0
// when VPP can resolve the next hop through a connected route in the table.
func (v *VPPNetwork) AddRoute(ctx context.Context, tableID uint32, ipv6 bool, prefix, nextHop string, swIfIndex uint32) error {
	parsedPrefix, err := ip_types.ParsePrefix(strings.TrimSpace(prefix))
	if err != nil {
		return fmt.Errorf("parse route prefix: %w", err)
	}
	if (parsedPrefix.Address.Af == ip_types.ADDRESS_IP6) != ipv6 {
		return errors.New("route prefix family does not match the table")
	}
	nh, err := ip_types.ParseAddress(strings.TrimSpace(nextHop))
	if err != nil {
		return fmt.Errorf("parse route next hop: %w", err)
	}
	if (nh.Af == ip_types.ADDRESS_IP6) != ipv6 {
		return errors.New("route next-hop family does not match the table")
	}
	proto := fib_types.FIB_API_PATH_NH_PROTO_IP4
	if ipv6 {
		proto = fib_types.FIB_API_PATH_NH_PROTO_IP6
	}
	route := ipapi.IPRoute{
		TableID: tableID,
		Prefix:  parsedPrefix,
		Paths: []fib_types.FibPath{{
			SwIfIndex: swIfIndex,
			Type:      fib_types.FIB_API_PATH_TYPE_NORMAL,
			Proto:     proto,
			Nh:        fib_types.FibPathNh{Address: nh.Un},
		}},
	}
	return v.changeRoute(ctx, true, route)
}

// DeleteRoute removes a static route from an IP FIB/VRF table.
func (v *VPPNetwork) DeleteRoute(ctx context.Context, tableID uint32, ipv6 bool, prefix string) error {
	parsedPrefix, err := ip_types.ParsePrefix(strings.TrimSpace(prefix))
	if err != nil {
		return fmt.Errorf("parse route prefix: %w", err)
	}
	if (parsedPrefix.Address.Af == ip_types.ADDRESS_IP6) != ipv6 {
		return errors.New("route prefix family does not match the table")
	}
	return v.changeRoute(ctx, false, ipapi.IPRoute{TableID: tableID, Prefix: parsedPrefix})
}

func (v *VPPNetwork) changeRoute(ctx context.Context, isAdd bool, route ipapi.IPRoute) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()
	reply, err := ipapi.NewServiceClient(conn).IPRouteAddDel(ctx, &ipapi.IPRouteAddDel{
		IsAdd: isAdd,
		Route: route,
	})
	if err != nil {
		if !isAdd && isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("change route: %w", err)
	}
	if err := checkVPPRetval("change route", reply.Retval); err != nil {
		if !isAdd && isVPPNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// SetInterfaceRouteTable binds an interface to an IPv4 or IPv6 FIB/VRF.
func (v *VPPNetwork) SetInterfaceRouteTable(ctx context.Context, swIfIndex uint32, ipv6 bool, tableID uint32) error {
	if swIfIndex == 0 {
		return errors.New("interface index is required")
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	reply, err := interfaces.NewServiceClient(conn).SwInterfaceSetTable(ctx, &interfaces.SwInterfaceSetTable{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		IsIPv6:    ipv6,
		VrfID:     tableID,
	})
	if err != nil {
		return fmt.Errorf("set interface route table: %w", err)
	}
	return checkVPPRetval("set interface route table", reply.Retval)
}

func (v *VPPNetwork) connect() (*core.Connection, error) {
	conn, err := govpp.Connect(v.Socket)
	if err != nil {
		return nil, fmt.Errorf("connect to VPP API %s: %w", v.Socket, err)
	}
	return conn, nil
}

func checkVPPRetval(operation string, retval int32) error {
	if retval != 0 {
		return fmt.Errorf("%s: VPP returned retval %d", operation, retval)
	}
	return nil
}

func isBridgeDomainExists(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "already exists")
}

func isVPPNotFound(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"not found",
		"no such",
		"unknown interface",
		"invalid interface",
		"unknown bridge domain",
		"invalid bridge domain",
		"unknown message",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
