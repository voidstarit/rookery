package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	govppapi "go.fd.io/govpp/api"
	"go.fd.io/govpp/binapi/interface"
	"go.fd.io/govpp/binapi/interface_types"
	"go.fd.io/govpp/binapi/ip_types"
	"go.fd.io/govpp/binapi/l2"
	"go.fd.io/govpp/binapi/nat44_ed"
	"go.fd.io/govpp/binapi/nat_types"
	"go.fd.io/govpp/core"

	"github.com/voidstarit/rookery/internal/store"
)

// HostInterface is a Linux network interface available for NAT outside use.
type HostInterface struct {
	Name         string   `json:"name"`
	Up           bool     `json:"up"`
	Addresses    []string `json:"addresses,omitempty"`
	DefaultRoute bool     `json:"default_route"`
	Physical     bool     `json:"physical"`
	Wireless     bool     `json:"wireless"`
}

// ListHostInterfaces returns Linux interfaces for the NAT outside-interface picker.
func ListHostInterfaces() ([]HostInterface, error) {
	defaults := defaultRouteInterfaces()
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list host interfaces: %w", err)
	}
	result := make([]HostInterface, 0, len(ifs))
	for _, iface := range ifs {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		_, deviceErr := os.Stat("/sys/class/net/" + iface.Name + "/device")
		entry := HostInterface{
			Name:         iface.Name,
			Up:           iface.Flags&net.FlagUp != 0,
			DefaultRoute: defaults[iface.Name],
			Physical:     deviceErr == nil,
			Wireless:     isWirelessInterface(iface.Name),
		}
		addrs, err := iface.Addrs()
		if err == nil {
			for _, addr := range addrs {
				entry.Addresses = append(entry.Addresses, addr.String())
			}
		}
		result = append(result, entry)
	}
	return result, nil
}

func defaultRouteInterfaces() map[string]bool {
	result := make(map[string]bool)
	out, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return result
	}
	fields := strings.Fields(string(out))
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "dev" {
			result[fields[i+1]] = true
		}
	}
	return result
}

func isWirelessInterface(name string) bool {
	if _, err := os.Stat("/sys/class/net/" + name + "/wireless"); err == nil {
		return true
	}
	if _, err := os.Stat("/sys/class/net/" + name + "/phy80211"); err == nil {
		return true
	}
	return false
}

// EnsureBVI creates or finds the per-network BVI and attaches it to bdID.
func (v *VPPNetwork) EnsureBVI(ctx context.Context, networkID int64, bdID uint32) (string, uint32, error) {
	if networkID < 1 {
		return "", 0, errors.New("invalid network id")
	}
	if bdID == 0 {
		return "", 0, errors.New("bridge domain id is required")
	}
	name := fmt.Sprintf("bvi%d", networkID)
	conn, err := v.connect()
	if err != nil {
		return "", 0, err
	}
	defer conn.Disconnect()

	if index, err := findInterface(ctx, conn, name); err == nil {
		return name, index, nil
	} else if !errors.Is(err, errVPPInterfaceNotFound) {
		return "", 0, err
	}

	bviClient := l2.NewServiceClient(conn)
	// Use the network ID as a stable, unique BVI instance. Auto-assigned BVI
	// instances can reuse the same internal name while a previous creation is
	// still settling, which makes VPP abort with duplicate `bviN-tx` nodes.
	reply, err := bviClient.BviCreate(ctx, &l2.BviCreate{UserInstance: uint32(networkID)})
	if err != nil {
		return "", 0, fmt.Errorf("create BVI: %w", err)
	}
	if err := checkVPPRetval("create BVI", reply.Retval); err != nil {
		return "", 0, err
	}
	swIfIndex := uint32(reply.SwIfIndex)
	rollback := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), vppCleanupTimeout)
		defer cancel()
		_, _ = bviClient.BviDelete(cleanupCtx, &l2.BviDelete{SwIfIndex: interface_types.InterfaceIndex(swIfIndex)})
	}

	nameReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetInterfaceName(ctx, &interfaces.SwInterfaceSetInterfaceName{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		Name:      name,
	})
	if err != nil {
		rollback()
		return "", 0, fmt.Errorf("name BVI: %w", err)
	}
	if err := checkVPPRetval("name BVI", nameReply.Retval); err != nil {
		rollback()
		return "", 0, err
	}

	bridgeReply, err := bviClient.SwInterfaceSetL2Bridge(ctx, &l2.SwInterfaceSetL2Bridge{
		RxSwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		BdID:        bdID,
		PortType:    l2.L2_API_PORT_TYPE_BVI,
		Enable:      true,
	})
	if err != nil {
		rollback()
		return "", 0, fmt.Errorf("attach BVI to bridge domain: %w", err)
	}
	if err := checkVPPRetval("attach BVI to bridge domain", bridgeReply.Retval); err != nil {
		rollback()
		return "", 0, err
	}

	flagsReply, err := interfaces.NewServiceClient(conn).SwInterfaceSetFlags(ctx, &interfaces.SwInterfaceSetFlags{
		SwIfIndex: interface_types.InterfaceIndex(swIfIndex),
		Flags:     interface_types.IF_STATUS_API_FLAG_ADMIN_UP,
	})
	if err != nil {
		rollback()
		return "", 0, fmt.Errorf("set BVI up: %w", err)
	}
	if err := checkVPPRetval("set BVI up", flagsReply.Retval); err != nil {
		rollback()
		return "", 0, err
	}
	return name, swIfIndex, nil
}

// DeleteBVIByName removes a network BVI if it exists.
func (v *VPPNetwork) DeleteBVIByName(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()
	index, err := findInterface(ctx, conn, name)
	if errors.Is(err, errVPPInterfaceNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	reply, err := l2.NewServiceClient(conn).BviDelete(ctx, &l2.BviDelete{
		SwIfIndex: interface_types.InterfaceIndex(index),
	})
	if err != nil {
		if isVPPNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete BVI %s: %w", name, err)
	}
	return checkVPPRetval("delete BVI", reply.Retval)
}

// AddInterfaceIPv4 assigns an IPv4 prefix to a VPP interface. It is idempotent.
func (v *VPPNetwork) AddInterfaceIPv4(ctx context.Context, name, cidr string) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(cidr) == "" {
		return errors.New("interface name and IPv4 CIDR are required")
	}
	out, err := v.runCLI(ctx, "set", "interface", "ip", "address", name, cidr)
	if err == nil {
		return nil
	}
	if cliAlreadyExists(out) && v.interfaceHasIPv4(ctx, name, cidr) {
		return nil
	}
	return fmt.Errorf("set %s address %s: %w: %s", name, cidr, err, out)
}

func (v *VPPNetwork) interfaceHasIPv4(ctx context.Context, name, cidr string) bool {
	targetIP, targetNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	out, err := v.runCLI(ctx, "show", "interface", "address", name)
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(out) {
		ip, ipnet, parseErr := net.ParseCIDR(field)
		if parseErr != nil || ip.To4() == nil {
			continue
		}
		if ip.Equal(targetIP) && ipnet.String() == targetNet.String() {
			return true
		}
	}
	return false
}

func linuxInterfaceMAC(name string) (string, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return "", fmt.Errorf("read host interface %s: %w", name, err)
	}
	if len(iface.HardwareAddr) == 0 {
		return "", fmt.Errorf("host interface %s has no MAC address", name)
	}
	return iface.HardwareAddr.String(), nil
}

// EnsureHostInterface takes over a Linux NIC as a VPP AF_PACKET outside interface.
func (v *VPPNetwork) EnsureHostInterface(ctx context.Context, nic string) (string, error) {
	nic = strings.TrimSpace(nic)
	if nic == "" {
		return "", errors.New("outside interface is required")
	}
	if nic == "lo" || strings.HasPrefix(nic, "vnf") || strings.HasPrefix(nic, "tap") || strings.HasPrefix(nic, "host-") {
		return "", fmt.Errorf("refusing to take over %s: it is a loopback or VPP-managed interface", nic)
	}
	if isWirelessInterface(nic) {
		return "", fmt.Errorf("refusing to take over %s: wireless NICs are not supported as a VPP NAT44 outside uplink", nic)
	}
	if defaultRouteInterfaces()[nic] {
		return "", fmt.Errorf("refusing to take over %s: it carries a host default route", nic)
	}
	if _, err := exec.CommandContext(ctx, "ip", "link", "show", "dev", nic).CombinedOutput(); err != nil {
		return "", fmt.Errorf("host interface %s does not exist: %w", nic, err)
	}
	vppName := "host-" + nic
	mac, err := linuxInterfaceMAC(nic)
	if err != nil {
		return "", err
	}
	conn, err := v.connect()
	if err != nil {
		return "", err
	}
	defer conn.Disconnect()
	if _, err := findInterface(ctx, conn, vppName); err == nil {
		if out, err := v.runCLI(ctx, "set", "interface", "mac", "address", vppName, mac); err != nil {
			return "", fmt.Errorf("set VPP outside interface %s MAC to %s: %w: %s", vppName, mac, err, out)
		}
		if out, err := v.runCLI(ctx, "set", "interface", "state", vppName, "up"); err != nil {
			return "", fmt.Errorf("bring existing VPP outside interface %s up: %w: %s", vppName, err, out)
		}
		return vppName, nil
	} else if !errors.Is(err, errVPPInterfaceNotFound) {
		return "", err
	}

	if out, err := exec.CommandContext(ctx, "ip", "addr", "flush", "dev", nic).CombinedOutput(); err != nil {
		return "", fmt.Errorf("flush host addresses on %s: %w: %s", nic, err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "ip", "link", "set", "up", "dev", nic).CombinedOutput(); err != nil {
		return "", fmt.Errorf("bring host interface %s up: %w: %s", nic, err, strings.TrimSpace(string(out)))
	}
	createArgs := []string{"create", "host-interface", "name", nic}
	if mac != "" {
		createArgs = append(createArgs, "hw-addr", mac)
	}
	createOut, err := v.runCLI(ctx, createArgs...)
	if err != nil {
		return "", fmt.Errorf("create VPP host-interface for %s: %w: %s", nic, err, createOut)
	}
	if _, err := findInterface(ctx, conn, vppName); err != nil {
		return "", fmt.Errorf("VPP did not create interface %s: %w: %s", vppName, err, createOut)
	}
	if out, err := v.runCLI(ctx, "set", "interface", "state", vppName, "up"); err != nil {
		return "", fmt.Errorf("bring VPP outside interface %s up: %w: %s", vppName, err, out)
	}
	return vppName, nil
}

// ApplyNAT44 configures VPP NAT44 with bviName inside and outsideName outside.
func (v *VPPNetwork) ApplyNAT44(ctx context.Context, bviName, outsideName, outsideAddress, outsideGateway, pool string) error {
	if outsideAddress != "" {
		if err := v.AddInterfaceIPv4(ctx, outsideName, outsideAddress); err != nil {
			return err
		}
		if outsideGateway != "" {
			out, err := v.runCLI(ctx, "ip", "route", "add", "0.0.0.0/0", "via", outsideGateway, outsideName)
			if err != nil && !cliAlreadyExists(out) {
				return fmt.Errorf("add NAT44 default route: %w: %s", err, out)
			}
		}
	} else {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "nerds-vms"
		}
		if out, err := v.runCLI(ctx, "set", "dhcp", "client", "intfc", outsideName, "hostname", hostname); err != nil && !cliAlreadyExists(out) {
			return fmt.Errorf("start VPP DHCP client on %s: %w: %s", outsideName, err, out)
		}
		if err := waitForVPPIPv4(ctx, v, outsideName, 30*time.Second); err != nil {
			return err
		}
	}

	if out, err := v.runCLI(ctx, "nat44", "plugin", "enable", "sessions", "10000"); err != nil && !cliAlreadyExists(out) {
		return fmt.Errorf("enable NAT44: %w: %s", err, out)
	}
	if out, err := v.runCLI(ctx, "nat44", "add", "interface", "address", outsideName); err != nil && !cliNATAddressExists(out, outsideName) {
		return fmt.Errorf("add NAT44 outside address: %w: %s", err, out)
	}
	if pool != "" {
		if out, err := v.runCLI(ctx, "nat44", "add", "address", pool); err != nil && !cliAlreadyExists(out) {
			return fmt.Errorf("add NAT44 pool: %w: %s", err, out)
		}
	}
	if err := v.setNAT44Interfaces(ctx, bviName, outsideName, true); err != nil {
		return fmt.Errorf("enable NAT44 on %s/%s: %w", bviName, outsideName, err)
	}
	return nil
}

// RemoveNAT44 removes NAT44 interface/pool configuration while leaving the
// BVI, outside interface, and outside addressing in place.
func (v *VPPNetwork) RemoveNAT44(ctx context.Context, bviName, outsideName, pool string) error {
	// The VPP CLI is not idempotent for NAT44 interface feature changes, so
	// use the binary API and tolerate an already-removed interface.
	_ = v.setNAT44Interfaces(ctx, bviName, outsideName, false)
	_, _ = v.runCLI(ctx, "nat44", "add", "interface", "address", outsideName, "del")
	if pool != "" {
		_, _ = v.runCLI(ctx, "nat44", "add", "address", pool, "del")
	}
	return nil
}

// ApplyNAT44StaticMappings installs DNAT rules as VPP NAT44 static mappings.
// Rules are tagged with the VNF ID so they can be replaced independently of
// mappings created by other NAT44 VNFs. The external address follows the
// outside interface's address, including when it is assigned by DHCP.
func (v *VPPNetwork) ApplyNAT44StaticMappings(ctx context.Context, outsideName string, vnfID int64, rules []store.DNATRule) error {
	if vnfID < 1 {
		return errors.New("invalid VNF id")
	}
	outsideName = strings.TrimSpace(outsideName)
	if outsideName == "" {
		return errors.New("NAT44 outside interface is required")
	}
	prepared := make([]nat44StaticMappingRule, 0, len(rules))
	for i, rule := range rules {
		protocol, err := nat44ProtocolNumber(rule.Protocol)
		if err != nil {
			return fmt.Errorf("DNAT rule %d: %w", i+1, err)
		}
		if rule.LocalPort < 1 || rule.LocalPort > 65535 {
			return fmt.Errorf("DNAT rule %d: local port must be between 1 and 65535", i+1)
		}
		if rule.ExternalPort < 1 || rule.ExternalPort > 65535 {
			return fmt.Errorf("DNAT rule %d: external port must be between 1 and 65535", i+1)
		}
		localIP := net.ParseIP(strings.TrimSpace(rule.LocalIP)).To4()
		if localIP == nil {
			return fmt.Errorf("DNAT rule %d: local address must be IPv4", i+1)
		}
		var localAddress ip_types.IP4Address
		copy(localAddress[:], localIP)
		prepared = append(prepared, nat44StaticMappingRule{
			protocol:     protocol,
			localAddress: localAddress,
			localPort:    uint16(rule.LocalPort),
			externalPort: uint16(rule.ExternalPort),
			tag:          nat44StaticMappingTag(vnfID, i),
		})
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()
	outsideIndex, err := findInterface(ctx, conn, outsideName)
	if err != nil {
		return fmt.Errorf("find NAT44 outside interface %s: %w", outsideName, err)
	}
	client := nat44_ed.NewServiceClient(conn)
	if err := removeNAT44StaticMappings(ctx, client, outsideIndex, vnfID); err != nil {
		return err
	}
	for _, rule := range prepared {
		reply, err := client.Nat44AddDelStaticMappingV2(ctx, &nat44_ed.Nat44AddDelStaticMappingV2{
			IsAdd:             true,
			Flags:             nat_types.NAT_IS_NONE,
			LocalIPAddress:    rule.localAddress,
			Protocol:          rule.protocol,
			LocalPort:         rule.localPort,
			ExternalPort:      rule.externalPort,
			ExternalSwIfIndex: interface_types.InterfaceIndex(outsideIndex),
			Tag:               rule.tag,
		})
		if err != nil {
			_ = removeNAT44StaticMappings(ctx, client, outsideIndex, vnfID)
			return fmt.Errorf("add NAT44 static mapping %s/%d: %w", nat44ProtocolName(rule.protocol), rule.externalPort, err)
		}
		if err := checkVPPRetval("add NAT44 static mapping", reply.Retval); err != nil {
			_ = removeNAT44StaticMappings(ctx, client, outsideIndex, vnfID)
			return fmt.Errorf("add NAT44 static mapping %s/%d: %w", nat44ProtocolName(rule.protocol), rule.externalPort, err)
		}
	}
	return nil
}

type nat44StaticMappingRule struct {
	protocol     uint8
	localAddress ip_types.IP4Address
	localPort    uint16
	externalPort uint16
	tag          string
}

// RemoveNAT44StaticMappings deletes the static mappings owned by one VNF.
func (v *VPPNetwork) RemoveNAT44StaticMappings(ctx context.Context, outsideName string, vnfID int64) error {
	if vnfID < 1 {
		return errors.New("invalid VNF id")
	}
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()
	var outsideIndex uint32
	if name := strings.TrimSpace(outsideName); name != "" {
		index, err := findInterface(ctx, conn, name)
		switch {
		case err == nil:
			outsideIndex = index
		case errors.Is(err, errVPPInterfaceNotFound):
			// The outside interface may already be gone; fall back to the
			// address recorded in each static mapping.
		case err != nil:
			return fmt.Errorf("find NAT44 outside interface %s: %w", name, err)
		}
	}
	return removeNAT44StaticMappings(ctx, nat44_ed.NewServiceClient(conn), outsideIndex, vnfID)
}

type nat44StaticMappingClient interface {
	Nat44StaticMappingDump(context.Context, *nat44_ed.Nat44StaticMappingDump) (nat44_ed.RPCService_Nat44StaticMappingDumpClient, error)
	Nat44AddDelStaticMappingV2(context.Context, *nat44_ed.Nat44AddDelStaticMappingV2) (*nat44_ed.Nat44AddDelStaticMappingV2Reply, error)
}

func removeNAT44StaticMappings(ctx context.Context, client nat44StaticMappingClient, outsideIndex uint32, vnfID int64) error {
	stream, err := client.Nat44StaticMappingDump(ctx, &nat44_ed.Nat44StaticMappingDump{})
	if err != nil {
		if nat44Unavailable(err) {
			return nil
		}
		return fmt.Errorf("dump NAT44 static mappings: %w", err)
	}
	prefix := nat44StaticMappingTagPrefix(vnfID)
	var errs []error
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if nat44Unavailable(err) {
				return errors.Join(errs...)
			}
			return errors.Join(append(errs, fmt.Errorf("read NAT44 static mapping dump: %w", err))...)
		}
		if !strings.HasPrefix(details.Tag, prefix) {
			continue
		}
		if err := deleteNAT44StaticMapping(ctx, client, outsideIndex, details); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func deleteNAT44StaticMapping(ctx context.Context, client nat44StaticMappingClient, outsideIndex uint32, details *nat44_ed.Nat44StaticMappingDetails) error {
	// Prefer deleting through the outside interface. Interface-bound mappings
	// keep an internal resolve record after the address is known, and deleting
	// by address alone leaves that record behind.
	swIfIndex := outsideIndex
	if swIfIndex == 0 && details.ExternalSwIfIndex != interface_types.InterfaceIndex(^uint32(0)) {
		swIfIndex = uint32(details.ExternalSwIfIndex)
	}
	var retval int32
	if swIfIndex != 0 {
		ret, err := nat44DeleteStaticMapping(ctx, client, swIfIndex, ip_types.IP4Address{}, details)
		if err != nil {
			return err
		}
		if ret == 0 {
			return nil
		}
		retval = ret
	}
	ret, err := nat44DeleteStaticMapping(ctx, client, ^uint32(0), details.ExternalIPAddress, details)
	if err != nil {
		return err
	}
	if ret == 0 || vppRetvalNoSuchEntry(ret) || vppRetvalNoSuchEntry(retval) {
		return nil
	}
	return fmt.Errorf("delete NAT44 static mapping %s/%d: VPP returned retval %d", nat44ProtocolName(details.Protocol), details.ExternalPort, ret)
}

func vppRetvalNoSuchEntry(retval int32) bool {
	return govppapi.VPPApiError(retval) == govppapi.NO_SUCH_ENTRY
}

func nat44DeleteStaticMapping(ctx context.Context, client nat44StaticMappingClient, externalSwIfIndex uint32, externalAddress ip_types.IP4Address, details *nat44_ed.Nat44StaticMappingDetails) (int32, error) {
	reply, err := client.Nat44AddDelStaticMappingV2(ctx, &nat44_ed.Nat44AddDelStaticMappingV2{
		IsAdd:             false,
		Flags:             nat_types.NAT_IS_NONE,
		LocalIPAddress:    details.LocalIPAddress,
		ExternalIPAddress: externalAddress,
		Protocol:          details.Protocol,
		LocalPort:         details.LocalPort,
		ExternalPort:      details.ExternalPort,
		ExternalSwIfIndex: interface_types.InterfaceIndex(externalSwIfIndex),
		VrfID:             details.VrfID,
	})
	if err != nil {
		// The generated service client turns a non-zero VPP retval into an
		// error. Preserve the retval so callers can distinguish "already
		// gone" from a real failure.
		var vppErr govppapi.VPPApiError
		if errors.As(err, &vppErr) {
			return int32(vppErr), nil
		}
		return 0, err
	}
	return reply.Retval, nil
}

func nat44StaticMappingTagPrefix(vnfID int64) string {
	return fmt.Sprintf("nerds-vms:%d:", vnfID)
}

func nat44StaticMappingTag(vnfID int64, ruleIndex int) string {
	return fmt.Sprintf("%s%d", nat44StaticMappingTagPrefix(vnfID), ruleIndex)
}

func nat44ProtocolNumber(protocol string) (uint8, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "tcp":
		return 6, nil
	case "udp":
		return 17, nil
	default:
		return 0, fmt.Errorf("protocol must be tcp or udp")
	}
}

func nat44ProtocolName(protocol uint8) string {
	switch protocol {
	case 6:
		return "tcp"
	case 17:
		return "udp"
	default:
		return fmt.Sprintf("protocol %d", protocol)
	}
}

func nat44Unavailable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return isVPPNotFound(err) ||
		strings.Contains(message, "not enabled") ||
		strings.Contains(message, "feature disabled") ||
		strings.Contains(message, "plugin disabled")
}

// NAT44Sessions returns the VPP NAT44 session table text.
func (v *VPPNetwork) NAT44Sessions(ctx context.Context) ([]string, error) {
	out, err := v.runCLI(ctx, "show", "nat44", "sessions")
	if err != nil {
		return nil, fmt.Errorf("show NAT44 sessions: %w: %s", err, out)
	}
	out = strings.ReplaceAll(out, "\r", "")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "NAT44 ED sessions") || strings.HasPrefix(trimmed, "-------- thread") {
			continue
		}
		result = append(result, line)
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// setNAT44Interfaces adds or removes the inside/outside feature flags using
// the binary API. This avoids the VPP CLI's non-idempotent handling of
// duplicate `set interface nat44` commands.
func (v *VPPNetwork) setNAT44Interfaces(ctx context.Context, bviName, outsideName string, add bool) error {
	conn, err := v.connect()
	if err != nil {
		return err
	}
	defer conn.Disconnect()
	bviIndex, err := findInterface(ctx, conn, bviName)
	if err != nil {
		return fmt.Errorf("find BVI %s: %w", bviName, err)
	}
	outsideIndex, err := findInterface(ctx, conn, outsideName)
	if err != nil {
		return fmt.Errorf("find outside interface %s: %w", outsideName, err)
	}

	client := nat44_ed.NewServiceClient(conn)
	inside := make(map[uint32]bool)
	stream, err := client.Nat44InterfaceDump(ctx, &nat44_ed.Nat44InterfaceDump{})
	if err != nil {
		if !add {
			return nil // NAT44 may not be enabled at all.
		}
		return fmt.Errorf("dump NAT44 interfaces: %w", err)
	}
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if !add {
				return nil
			}
			return fmt.Errorf("read NAT44 interface dump: %w", err)
		}
		inside[uint32(details.SwIfIndex)] = details.Flags&nat_types.NAT_IS_INSIDE != 0
	}

	apply := func(index uint32, isInside bool) error {
		configured, exists := inside[index]
		if add {
			if exists && configured == isInside {
				return nil
			}
			if exists {
				// Remove a stale opposite-direction flag before adding.
				reply, err := client.Nat44InterfaceAddDelFeature(ctx, &nat44_ed.Nat44InterfaceAddDelFeature{
					IsAdd:     false,
					Flags:     natFlags(configured),
					SwIfIndex: interface_types.InterfaceIndex(index),
				})
				if err != nil {
					return err
				}
				if err := checkVPPRetval("remove stale NAT44 interface", reply.Retval); err != nil {
					return err
				}
			}
			reply, err := client.Nat44InterfaceAddDelFeature(ctx, &nat44_ed.Nat44InterfaceAddDelFeature{
				IsAdd:     true,
				Flags:     natFlags(isInside),
				SwIfIndex: interface_types.InterfaceIndex(index),
			})
			if err != nil {
				return err
			}
			return checkVPPRetval("add NAT44 interface", reply.Retval)
		}
		if !exists {
			return nil
		}
		reply, err := client.Nat44InterfaceAddDelFeature(ctx, &nat44_ed.Nat44InterfaceAddDelFeature{
			IsAdd:     false,
			Flags:     natFlags(configured),
			SwIfIndex: interface_types.InterfaceIndex(index),
		})
		if err != nil {
			return err
		}
		return checkVPPRetval("delete NAT44 interface", reply.Retval)
	}

	if err := apply(bviIndex, true); err != nil {
		return err
	}
	if err := apply(outsideIndex, false); err != nil {
		return err
	}
	return nil
}

func natFlags(inside bool) nat_types.NatConfigFlags {
	if inside {
		return nat_types.NAT_IS_INSIDE
	}
	return 0
}

func (v *VPPNetwork) runCLI(ctx context.Context, args ...string) (string, error) {
	binary := strings.TrimSpace(v.CLI)
	if binary == "" {
		binary = "vppctl"
	}
	// VPP's CLI socket can disappear while VPP is restarting. Without a
	// per-command deadline vppctl can block the reconciler indefinitely.
	cmdCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	out, err := exec.CommandContext(cmdCtx, binary, args...).CombinedOutput()
	output := strings.TrimSpace(string(out))
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
		return output, errors.New("vppctl timed out")
	}
	if err == nil && cliOutputFailed(output) {
		err = errors.New("vppctl rejected the command")
	}
	return output, err
}

func cliOutputFailed(output string) bool {
	output = strings.ToLower(output)
	for _, marker := range []string{
		"unknown input",
		"invalid",
		"failed",
		"no such",
		"not found",
		"permission denied",
		"cannot ",
		"error:",
	} {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

func cliNATAddressExists(output, outsideName string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "add "+strings.ToLower(outsideName)+" address failed")
}

func waitForVPPIPv4(ctx context.Context, v *VPPNetwork, interfaceName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		out, err := v.runCLI(ctx, "show", "interface", "address", interfaceName)
		if err == nil {
			for _, field := range strings.Fields(out) {
				ip, _, parseErr := net.ParseCIDR(field)
				if parseErr == nil && ip.To4() != nil {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("VPP outside interface %s did not obtain an IPv4 address", interfaceName)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func cliAlreadyExists(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "already") ||
		strings.Contains(output, "exists") ||
		strings.Contains(output, "conflicts with") ||
		strings.Contains(output, "address in use")
}

var errVPPInterfaceNotFound = errors.New("VPP interface not found")

func findInterface(ctx context.Context, conn *core.Connection, name string) (uint32, error) {
	client := interfaces.NewServiceClient(conn)
	stream, err := client.SwInterfaceDump(ctx, &interfaces.SwInterfaceDump{})
	if err != nil {
		return 0, fmt.Errorf("dump VPP interfaces: %w", err)
	}
	for {
		details, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return 0, errVPPInterfaceNotFound
		}
		if err != nil {
			return 0, fmt.Errorf("read VPP interface dump: %w", err)
		}
		if details.InterfaceName == name {
			return uint32(details.SwIfIndex), nil
		}
	}
}
