package host

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	coredhcpconfig "github.com/coredhcp/coredhcp/config"
	coredhcpserver "github.com/coredhcp/coredhcp/server"
	"github.com/insomniacslk/dhcp/dhcpv6"

	"github.com/voidstarit/rookery/internal/store"
)

const (
	vnfStopTimeout   = 3 * time.Second
	defaultDHCPPort  = 67
	maxDHCPRangeSize = 65536
	coreDHCPv6Port   = 547
	coreDHCPv4Plugin = "nerds-coredhcp"
	coreDHCPv6Plugin = "nerds-coredhcp6"
)

// VNFManager runs one CoreDHCP server instance per network function.
type VNFManager struct {
	Dir       string
	LegacyDir string
	Port      int

	mu      sync.Mutex
	servers map[int64]*managedVNF
}

type managedVNF struct {
	vnfType string
	servers *coredhcpserver.Servers
	done    chan struct{}
}

type leaseRecord struct {
	ClientID string    `json:"client_id"`
	Address  string    `json:"address"`
	Expires  time.Time `json:"expires"`
	Hostname string    `json:"hostname,omitempty"`
}

type leaseFile struct {
	Leases []leaseRecord `json:"leases"`
}

// NewVNFManager returns a manager storing VNF leases under runDir/vnfs.
func NewVNFManager(runDir string) (*VNFManager, error) {
	if strings.TrimSpace(runDir) == "" {
		runDir = "/run/nerds-vms"
	}
	abs, err := filepath.Abs(runDir)
	if err != nil {
		return nil, fmt.Errorf("resolve VNF run directory: %w", err)
	}
	dir := filepath.Join(abs, "vnfs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create VNF directory: %w", err)
	}

	ensureCoreDHCPPlugin()
	ensureCoreDHCPv6Plugin()

	return &VNFManager{
		Dir:       dir,
		LegacyDir: filepath.Join(abs, "dhcp"),
		Port:      defaultDHCPPort,
		servers:   make(map[int64]*managedVNF),
	}, nil
}

// Start configures the VNF tap and starts the type-specific DHCP server.
func (m *VNFManager) Start(ctx context.Context, vnf store.VNF, hostIf string) error {
	if vnf.ID < 1 {
		return errors.New("invalid VNF id")
	}
	if !store.ValidVNFType(vnf.Type) {
		return fmt.Errorf("unsupported VNF type %q", vnf.Type)
	}
	hostIf = strings.TrimSpace(hostIf)
	if hostIf == "" {
		return errors.New("tap interface name is required")
	}

	leasePath := m.leasePath(vnf.ID)
	var cfg *coredhcpconfig.Config
	var err error
	switch vnf.Type {
	case store.VNFTypeDHCPv4:
		cfg, err = buildDHCPv4Config(vnf, hostIf, leasePath, m.port())
	case store.VNFTypeDHCPv6:
		cfg, err = buildDHCPv6Config(vnf, hostIf, leasePath)
	}
	if err != nil {
		return err
	}

	// Stop an old instance after validating the new configuration but keep the
	// lease file so a configuration change can reuse still-valid leases.
	if err := m.stop(ctx, vnf.ID, false); err != nil {
		return err
	}
	m.CleanupLegacyNetwork(vnf.NetworkID)

	if err := waitForLink(ctx, hostIf, 3*time.Second); err != nil {
		return err
	}
	tapCIDR := strings.TrimSpace(vnf.Config.Gateway)
	if strings.TrimSpace(vnf.Config.BindIP) != "" {
		tapCIDR = strings.TrimSpace(vnf.Config.BindIP)
	}
	if out, err := runIP(ctx, "addr", "replace", tapCIDR, "dev", hostIf); err != nil {
		return fmt.Errorf("configure VNF tap address: %w: %s", err, out)
	}
	if out, err := runIP(ctx, "link", "set", "up", "dev", hostIf); err != nil {
		return fmt.Errorf("bring VNF tap up: %w: %s", err, out)
	}

	servers, err := coredhcpserver.Start(cfg)
	if err != nil {
		return fmt.Errorf("start %s on %s: %w", vnf.Type, hostIf, err)
	}

	slog.Info("VNF started",
		"vnf_id", vnf.ID,
		"type", vnf.Type,
		"interface", hostIf,
		"gateway", vnf.Config.Gateway,
		"range_start", vnf.Config.RangeStart,
		"range_end", vnf.Config.RangeEnd,
		"leases", leasePath,
	)

	managed := &managedVNF{vnfType: vnf.Type, servers: servers, done: make(chan struct{})}
	go func() {
		err := servers.Wait()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			slog.Error("VNF stopped", "vnf_id", vnf.ID, "type", vnf.Type, "interface", hostIf, "error", err)
		}
		close(managed.done)
	}()

	m.mu.Lock()
	if m.servers == nil {
		m.servers = make(map[int64]*managedVNF)
	}
	m.servers[vnf.ID] = managed
	m.mu.Unlock()
	return nil
}

// Stop terminates the VNF server. Lease files are removed only when
// removeLeases is true (VNF deletion).
func (m *VNFManager) Stop(ctx context.Context, vnfID int64, removeLeases bool) error {
	return m.stop(ctx, vnfID, removeLeases)
}

func (m *VNFManager) stop(ctx context.Context, vnfID int64, removeLeases bool) error {
	m.mu.Lock()
	managed := m.servers[vnfID]
	m.mu.Unlock()

	if managed != nil {
		managed.servers.Close()
		select {
		case <-managed.done:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(vnfStopTimeout):
			return errors.New("VNF server did not stop in time")
		}

		m.mu.Lock()
		if m.servers[vnfID] == managed {
			delete(m.servers, vnfID)
		}
		m.mu.Unlock()
	}

	if removeLeases {
		_ = os.Remove(m.leasePath(vnfID))
	}
	return nil
}

// Running reports whether a VNF server is currently running.
func (m *VNFManager) Running(vnfID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.servers[vnfID]
	return ok
}

// Leases returns the persisted leases for a VNF and marks whether they are active.
func (m *VNFManager) Leases(_ context.Context, vnfID int64) ([]store.VNFLease, error) {
	path := m.leasePath(vnfID)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []store.VNFLease{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read VNF leases: %w", err)
	}

	var file leaseFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse VNF leases: %w", err)
	}
	now := time.Now()
	leases := make([]store.VNFLease, 0, len(file.Leases))
	for _, lease := range file.Leases {
		leases = append(leases, store.VNFLease{
			ClientID:  lease.ClientID,
			Address:   lease.Address,
			Hostname:  lease.Hostname,
			ExpiresAt: lease.Expires,
			Active:    lease.Expires.After(now),
		})
	}
	sort.Slice(leases, func(i, j int) bool {
		if leases[i].Address == leases[j].Address {
			return leases[i].ClientID < leases[j].ClientID
		}
		return leases[i].Address < leases[j].Address
	})
	return leases, nil
}

// CleanupLegacyNetwork removes DHCP runtime state left by the old
// per-network DHCP implementation.
func (m *VNFManager) CleanupLegacyNetwork(networkID int64) {
	if networkID < 1 {
		return
	}
	removeLegacyDHCP(m.LegacyDir, networkID)
	_ = os.Remove(filepath.Join(m.LegacyDir, strconv.FormatInt(networkID, 10)+".coredhcp.json"))
}

// ResetRuntime stops every DHCP server but keeps lease files. It is used when
// VPP restarts and the tap interfaces backing those servers disappear.
func (m *VNFManager) ResetRuntime(_ context.Context) error {
	return m.Close()
}

// Close stops all VNF servers but keeps lease files.
func (m *VNFManager) Close() error {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*vnfStopTimeout)
	defer cancel()

	var errs []error
	for _, id := range ids {
		if err := m.stop(ctx, id, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *VNFManager) port() int {
	if m.Port > 0 {
		return m.Port
	}
	return defaultDHCPPort
}

func (m *VNFManager) leasePath(vnfID int64) string {
	return filepath.Join(m.Dir, strconv.FormatInt(vnfID, 10)+".json")
}

func buildDHCPv4Config(vnf store.VNF, hostIf, leasePath string, port int) (*coredhcpconfig.Config, error) {
	if port <= 0 {
		port = defaultDHCPPort
	}
	gateway, ipnet, err := net.ParseCIDR(strings.TrimSpace(vnf.Config.Gateway))
	if err != nil {
		return nil, fmt.Errorf("parse DHCPv4 gateway %q: %w", vnf.Config.Gateway, err)
	}
	gateway4 := gateway.To4()
	network4 := ipnet.IP.To4()
	if gateway4 == nil || network4 == nil {
		return nil, errors.New("DHCPv4 gateway must be an IPv4 CIDR")
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones > 30 {
		return nil, errors.New("DHCPv4 gateway prefix must be /30 or smaller")
	}
	broadcast := broadcastIPv4(ipnet)
	if gateway4.Equal(network4) || gateway4.Equal(broadcast) {
		return nil, errors.New("DHCPv4 gateway must be a usable host address")
	}

	start4 := net.ParseIP(strings.TrimSpace(vnf.Config.RangeStart)).To4()
	end4 := net.ParseIP(strings.TrimSpace(vnf.Config.RangeEnd)).To4()
	if start4 == nil || end4 == nil {
		return nil, errors.New("DHCPv4 range must contain valid IPv4 addresses")
	}
	for _, addr := range []net.IP{start4, end4} {
		if !ipnet.Contains(addr) {
			return nil, errors.New("DHCPv4 range must be inside the gateway subnet")
		}
		if addr.Equal(network4) || addr.Equal(broadcast) {
			return nil, errors.New("DHCPv4 range cannot include the network or broadcast address")
		}
		if addr.Equal(gateway4) {
			return nil, errors.New("DHCPv4 range cannot include the gateway address")
		}
	}
	if bytesCompare(start4, end4) > 0 {
		return nil, errors.New("DHCPv4 range start must not be after range end")
	}
	if bytesCompare(start4, gateway4) <= 0 && bytesCompare(end4, gateway4) >= 0 {
		return nil, errors.New("DHCPv4 range cannot include the gateway address")
	}
	rangeSize := uint64(binary.BigEndian.Uint32(end4)) - uint64(binary.BigEndian.Uint32(start4)) + 1
	if rangeSize > maxDHCPRangeSize {
		return nil, fmt.Errorf("DHCPv4 range must contain at most %d addresses", maxDHCPRangeSize)
	}
	dns, err := normalizeDNS(vnf.Config.DNS, false)
	if err != nil {
		return nil, err
	}
	leaseTime, err := vnfLeaseTime(vnf.Config.LeaseTime)
	if err != nil {
		return nil, err
	}

	return &coredhcpconfig.Config{
		Server4: &coredhcpconfig.ServerConfig{
			Addresses: []net.UDPAddr{{
				IP:   net.IPv4zero,
				Port: port,
				Zone: hostIf,
			}},
			Plugins: []coredhcpconfig.PluginConfig{{
				Name: coreDHCPv4Plugin,
				Args: []string{
					gateway4.String(),
					net.IP(ipnet.Mask).String(),
					start4.String(),
					end4.String(),
					strings.Join(dns, ","),
					leasePath,
					leaseTime.String(),
					broadcast.String(),
					dhcpServerIP(vnf.Config).String(),
				},
			}},
		},
	}, nil
}

func buildDHCPv6Config(vnf store.VNF, hostIf, leasePath string) (*coredhcpconfig.Config, error) {
	gatewayCIDR := strings.TrimSpace(vnf.Config.Gateway)
	prefix, err := netip.ParsePrefix(gatewayCIDR)
	if err != nil {
		return nil, fmt.Errorf("parse DHCPv6 gateway %q: %w", gatewayCIDR, err)
	}
	if !prefix.Addr().Is6() {
		return nil, errors.New("DHCPv6 gateway must be an IPv6 CIDR")
	}
	if prefix.Bits() > 128 {
		return nil, errors.New("DHCPv6 prefix is invalid")
	}
	gateway := prefix.Addr()
	if gateway.IsUnspecified() {
		return nil, errors.New("DHCPv6 gateway must be a usable address")
	}
	start, err := netip.ParseAddr(strings.TrimSpace(vnf.Config.RangeStart))
	if err != nil || !start.Is6() {
		return nil, errors.New("DHCPv6 range start must be a valid IPv6 address")
	}
	end, err := netip.ParseAddr(strings.TrimSpace(vnf.Config.RangeEnd))
	if err != nil || !end.Is6() {
		return nil, errors.New("DHCPv6 range end must be a valid IPv6 address")
	}
	if !prefix.Contains(start) || !prefix.Contains(end) {
		return nil, errors.New("DHCPv6 range must be inside the gateway prefix")
	}
	if start.Compare(end) > 0 {
		return nil, errors.New("DHCPv6 range start must not be after range end")
	}
	if start.Compare(gateway) <= 0 && end.Compare(gateway) >= 0 {
		return nil, errors.New("DHCPv6 range cannot include the gateway address")
	}
	anycast := prefix.Masked().Addr()
	if start.Compare(anycast) <= 0 && end.Compare(anycast) >= 0 {
		return nil, errors.New("DHCPv6 range cannot include the subnet-router anycast address")
	}
	if !v6RangeWithinLimit(start, end) {
		return nil, fmt.Errorf("DHCPv6 range must contain at most %d addresses", maxDHCPRangeSize)
	}
	dns, err := normalizeDNS(vnf.Config.DNS, true)
	if err != nil {
		return nil, err
	}
	leaseTime, err := vnfLeaseTime(vnf.Config.LeaseTime)
	if err != nil {
		return nil, err
	}
	duidMAC := vnfDUIDMAC(vnf.ID)

	return &coredhcpconfig.Config{
		Server6: &coredhcpconfig.ServerConfig{
			Addresses: []net.UDPAddr{{
				IP:   dhcpv6.AllDHCPRelayAgentsAndServers,
				Port: coreDHCPv6Port,
				Zone: hostIf,
			}},
			Plugins: []coredhcpconfig.PluginConfig{{
				Name: coreDHCPv6Plugin,
				Args: []string{
					gatewayCIDR,
					start.String(),
					end.String(),
					strings.Join(dns, ","),
					leasePath,
					leaseTime.String(),
					duidMAC.String(),
				},
			}},
		},
	}, nil
}

func dhcpServerIP(cfg store.VNFConfig) net.IP {
	if strings.TrimSpace(cfg.BindIP) != "" {
		if ip, _, err := net.ParseCIDR(strings.TrimSpace(cfg.BindIP)); err == nil {
			if ip4 := ip.To4(); ip4 != nil {
				return ip4
			}
		}
	}
	if ip, _, err := net.ParseCIDR(strings.TrimSpace(cfg.Gateway)); err == nil {
		return ip.To4()
	}
	return nil
}

func vnfLeaseTime(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return coreDHCPLeaseTime, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("invalid DHCP lease time %q", value)
	}
	return duration, nil
}

func normalizeDNS(value string, ipv6 bool) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ip := net.ParseIP(part)
		if ip == nil {
			return nil, fmt.Errorf("invalid DNS address %q", part)
		}
		if ipv6 {
			if ip.To4() != nil {
				return nil, fmt.Errorf("DNS address %q is not IPv6", part)
			}
			out = append(out, ip.To16().String())
		} else {
			if ip.To4() == nil {
				return nil, fmt.Errorf("DNS address %q is not IPv4", part)
			}
			out = append(out, ip.To4().String())
		}
	}
	return out, nil
}

func v6RangeWithinLimit(start, end netip.Addr) bool {
	startInt := new(big.Int).SetBytes(start.AsSlice())
	endInt := new(big.Int).SetBytes(end.AsSlice())
	size := new(big.Int).Sub(endInt, startInt)
	size.Add(size, big.NewInt(1))
	return size.Cmp(big.NewInt(maxDHCPRangeSize)) <= 0
}

func vnfDUIDMAC(vnfID int64) net.HardwareAddr {
	return net.HardwareAddr{
		0x02, 0x4e, 0x56, 0x46,
		byte(uint64(vnfID) >> 8), byte(vnfID),
	}
}

func broadcastIPv4(ipnet *net.IPNet) net.IP {
	ip := ipnet.IP.To4()
	out := make(net.IP, net.IPv4len)
	for i := 0; i < 4; i++ {
		out[i] = ip[i] | ^ipnet.Mask[i]
	}
	return out
}

func waitForLink(ctx context.Context, hostIf string, timeout time.Duration) error {
	if _, err := exec.LookPath("ip"); err != nil {
		return fmt.Errorf("ip command is not available: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		if _, err := runIP(ctx, "link", "show", "dev", hostIf); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("tap interface %s did not appear", hostIf)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func runIP(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// removeLegacyDHCP terminates a dnsmasq instance left by an older server
// version and removes its pid/config/lease files.
func removeLegacyDHCP(dir string, networkID int64) {
	base := filepath.Join(dir, strconv.FormatInt(networkID, 10))
	pidPath := base + ".pid"
	configPath := base + ".conf"

	if pid, ok := readPIDFile(pidPath); ok && processIsLegacyDNSMasq(pid, configPath) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		deadline := time.Now().Add(vnfStopTimeout)
		for time.Now().Before(deadline) && processExists(pid) && processIsLegacyDNSMasq(pid, configPath) {
			time.Sleep(50 * time.Millisecond)
		}
		if processExists(pid) && processIsLegacyDNSMasq(pid, configPath) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}

	for _, path := range []string{configPath, pidPath, base + ".leases"} {
		_ = os.Remove(path)
	}
}

func readPIDFile(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid < 1 {
		return 0, false
	}
	return pid, true
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func processIsLegacyDNSMasq(pid int, configPath string) bool {
	comm, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil || strings.TrimSpace(string(comm)) != "dnsmasq" {
		return false
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	return strings.Contains(string(cmdline), configPath)
}
