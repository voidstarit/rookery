package host

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coredhcp/coredhcp/handler"
	"github.com/coredhcp/coredhcp/plugins"
	"github.com/coredhcp/coredhcp/plugins/allocators/bitmap"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/rs/zerolog/log"

	"github.com/voidstarit/rookery/internal/store"
)

const coreDHCPLeaseTime = 12 * time.Hour

var registerCoreDHCP sync.Once

// ensureCoreDHCPPlugin registers the per-VNF DHCPv4 plugin once. The plugin
// setup function receives all VNF-specific state as arguments, so multiple
// VNFs can run independent servers in one process without shared state.
func ensureCoreDHCPPlugin() {
	registerCoreDHCP.Do(func() {
		if err := plugins.RegisterPlugin(&plugins.Plugin{
			Name:   coreDHCPv4Plugin,
			Setup4: setupCoreDHCP,
		}); err != nil {
			panic(fmt.Sprintf("register CoreDHCP plugin: %v", err))
		}
	})
}

type coreDHCPState struct {
	mu        sync.Mutex
	gateway   net.IP
	serverID  net.IP
	mask      net.IPMask
	broadcast net.IP
	dns       []net.IP
	leaseTime time.Duration
	leasePath string
	options   []parsedDHCPOption

	alloc  *bitmap.IPv4Allocator
	start  uint32
	end    uint32
	leases map[string]*coreDHCPLease
	byIP   map[string]string

	static       map[string]net.IP
	reservedByIP map[string]string
}

type coreDHCPLease struct {
	ClientID string
	Address  net.IP
	Expires  time.Time
	Hostname string
}

// setupCoreDHCP is a CoreDHCP plugin setup function. Arguments are, in order:
// gateway IPv4, netmask, range start, range end, comma-separated DNS servers,
// lease file path, lease duration, broadcast address, server identifier, an
// optional JSON array of static MAC-to-IPv4 assignments, and an optional JSON
// array of extra DHCP options.
func setupCoreDHCP(args ...string) (handler.Handler4, error) {
	if len(args) != 9 && len(args) != 10 && len(args) != 11 {
		return nil, fmt.Errorf("CoreDHCP setup: want 9, 10, or 11 arguments, got %d", len(args))
	}

	gateway := net.ParseIP(args[0]).To4()
	if gateway == nil {
		return nil, fmt.Errorf("invalid DHCP gateway %q", args[0])
	}
	maskIP := net.ParseIP(args[1]).To4()
	if maskIP == nil {
		return nil, fmt.Errorf("invalid DHCP netmask %q", args[1])
	}
	mask := net.IPv4Mask(maskIP[0], maskIP[1], maskIP[2], maskIP[3])
	ones, bits := mask.Size()
	if bits != 32 || ones > 30 {
		return nil, errors.New("DHCP netmask must be /30 or larger")
	}
	subnet := &net.IPNet{IP: gateway.Mask(mask), Mask: mask}

	startIP := net.ParseIP(args[2]).To4()
	endIP := net.ParseIP(args[3]).To4()
	if startIP == nil || endIP == nil {
		return nil, errors.New("DHCP range must contain valid IPv4 addresses")
	}
	for _, ip := range []net.IP{startIP, endIP} {
		if !subnet.Contains(ip) {
			return nil, fmt.Errorf("DHCP range address %s is outside %s", ip, subnet)
		}
	}
	if bytesCompare(startIP, endIP) > 0 {
		return nil, errors.New("DHCP range start must not be after range end")
	}
	if bytesCompare(startIP, gateway) <= 0 && bytesCompare(endIP, gateway) >= 0 {
		return nil, errors.New("DHCP range cannot include the gateway address")
	}

	dns, err := parseCoreDHCPDNS(args[4])
	if err != nil {
		return nil, err
	}
	leaseTime, err := time.ParseDuration(args[6])
	if err != nil || leaseTime <= 0 {
		return nil, fmt.Errorf("invalid DHCP lease duration %q", args[6])
	}
	broadcast := net.ParseIP(args[7]).To4()
	if broadcast == nil {
		return nil, fmt.Errorf("invalid DHCP broadcast address %q", args[7])
	}
	serverID := gateway
	if strings.TrimSpace(args[8]) != "" {
		serverID = net.ParseIP(args[8]).To4()
		if serverID == nil {
			return nil, fmt.Errorf("invalid DHCP server ID %q", args[8])
		}
	}

	alloc, err := bitmap.NewIPv4Allocator(startIP, endIP)
	if err != nil {
		return nil, fmt.Errorf("create DHCP address allocator: %w", err)
	}
	static := make(map[string]net.IP)
	reservedByIP := make(map[string]string)
	if len(args) == 10 && strings.TrimSpace(args[9]) != "" {
		var assignments []store.DHCPStaticAssignment
		if err := json.Unmarshal([]byte(args[9]), &assignments); err != nil {
			return nil, fmt.Errorf("parse DHCP static assignments: %w", err)
		}
		network := subnet.IP.To4()
		for _, assignment := range assignments {
			mac, err := net.ParseMAC(strings.TrimSpace(assignment.MAC))
			if err != nil || len(mac) != 6 {
				return nil, fmt.Errorf("invalid static assignment MAC %q", assignment.MAC)
			}
			ip := net.ParseIP(strings.TrimSpace(assignment.IP)).To4()
			if ip == nil || !subnet.Contains(ip) {
				return nil, fmt.Errorf("static assignment IP %q is outside %s", assignment.IP, subnet)
			}
			if ip.Equal(network) || ip.Equal(broadcast) || ip.Equal(gateway) || ip.Equal(serverID) {
				return nil, fmt.Errorf("static assignment IP %s is not a usable host address", ip)
			}
			key := strings.ToLower(mac.String())
			if previous, exists := reservedByIP[ip.String()]; exists && previous != key {
				return nil, fmt.Errorf("static assignment IP %s is used by multiple MAC addresses", ip)
			}
			if previous, exists := static[key]; exists && !previous.Equal(ip) {
				return nil, fmt.Errorf("static assignment MAC %s has multiple IP addresses", key)
			}
			static[key] = ip
			reservedByIP[ip.String()] = key
		}
	}

	options, err := parseDHCPOptionsJSON("", false)
	if err != nil {
		return nil, err
	}
	if len(args) >= 11 {
		options, err = parseDHCPOptionsJSON(args[10], false)
		if err != nil {
			return nil, err
		}
	}

	state := &coreDHCPState{
		gateway:      gateway,
		serverID:     serverID,
		mask:         mask,
		broadcast:    broadcast,
		dns:          dns,
		leaseTime:    leaseTime,
		leasePath:    args[5],
		options:      options,
		alloc:        alloc,
		start:        binary.BigEndian.Uint32(startIP),
		end:          binary.BigEndian.Uint32(endIP),
		leases:       make(map[string]*coreDHCPLease),
		byIP:         make(map[string]string),
		static:       static,
		reservedByIP: reservedByIP,
	}
	for ip := range reservedByIP {
		addr := net.ParseIP(ip).To4()
		if !state.inRange(addr) {
			continue
		}
		if _, err := alloc.Allocate(net.IPNet{IP: addr, Mask: net.CIDRMask(32, 32)}); err != nil {
			return nil, fmt.Errorf("reserve static DHCP address %s: %w", ip, err)
		}
	}
	if err := state.loadLeases(); err != nil {
		return nil, err
	}
	return state.handle, nil
}

func parseCoreDHCPDNS(value string) ([]net.IP, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var dns []net.IP
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ip := net.ParseIP(part).To4()
		if ip == nil {
			return nil, fmt.Errorf("invalid DHCP DNS address %q", part)
		}
		dns = append(dns, ip)
	}
	return dns, nil
}

func (s *coreDHCPState) handle(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
	if req.OpCode != dhcpv4.OpcodeBootRequest {
		return resp, false
	}
	if sid := req.ServerIdentifier(); sid != nil && !sid.IsUnspecified() && !sid.Equal(s.serverID) {
		return nil, true
	}

	// CoreDHCP replies with a raw Ethernet frame when a client without an
	// address does not set the broadcast flag. Force the UDP broadcast path so
	// replies do not need an extra raw socket per packet.
	if req.ClientIPAddr.IsUnspecified() && req.GatewayIPAddr.IsUnspecified() {
		req.SetBroadcast()
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(now)

	clientID := req.ClientHWAddr.String()
	requested := req.RequestedIPAddress()
	if requested != nil && requested.IsUnspecified() {
		requested = nil
	}
	log.Trace().
		Str("leases", s.leasePath).
		Str("client_id", clientID).
		Str("hostname", req.HostName()).
		Str("message_type", req.MessageType().String()).
		Str("requested_ip", ipText(requested)).
		Str("client_ip", req.ClientIPAddr.String()).
		Str("options", req.Options.String()).
		Msg("DHCPv4 request")
	switch req.MessageType() {
	case dhcpv4.MessageTypeDiscover, dhcpv4.MessageTypeRequest:
		hint := req.RequestedIPAddress()
		if hint != nil && hint.IsUnspecified() {
			hint = nil
		}
		// Renewing clients may not send option 50; their current address is
		// still a useful hint if the lease file was lost or moved.
		if hint == nil && req.MessageType() == dhcpv4.MessageTypeRequest && !req.ClientIPAddr.IsUnspecified() {
			hint = req.ClientIPAddr
		}
		record := s.leaseForLocked(clientID, now)
		staticIP, hasStatic := s.static[clientID]

		if req.MessageType() == dhcpv4.MessageTypeRequest && hint != nil {
			if hasStatic && !hint.Equal(staticIP) {
				return s.nak(resp)
			}
			if !hasStatic && !s.inRange(hint) {
				return s.nak(resp)
			}
			if record != nil && !hint.Equal(record.Address) {
				return s.nak(resp)
			}
			if owner, ok := s.byIP[hint.String()]; ok && owner != clientID {
				return s.nak(resp)
			}
			if owner, reserved := s.reservedByIP[hint.String()]; reserved && owner != clientID {
				return s.nak(resp)
			}
		}

		if record == nil {
			var err error
			record, err = s.allocateLocked(clientID, hint, req.HostName(), now)
			if err != nil {
				if req.MessageType() == dhcpv4.MessageTypeRequest {
					return s.nak(resp)
				}
				return nil, true
			}
		} else {
			s.renewLocked(record, req.HostName(), now)
		}

		resp.YourIPAddr = record.Address
		s.applyOptions(resp, false)
		return resp, false

	case dhcpv4.MessageTypeRelease:
		if record, ok := s.leases[clientID]; ok {
			s.freeLocked(record)
			s.saveLocked()
		}
		return nil, true

	case dhcpv4.MessageTypeInform:
		s.applyOptions(resp, true)
		return resp, false
	}

	return resp, false
}

func (s *coreDHCPState) nak(resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
	log.Debug().
		Str("leases", s.leasePath).
		Str("client_id", resp.ClientHWAddr.String()).
		Str("current_ip", resp.ClientIPAddr.String()).
		Msg("DHCPv4 request denied")
	resp.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeNak))
	resp.YourIPAddr = net.IPv4zero
	resp.Options.Update(dhcpv4.OptServerIdentifier(s.serverID))
	return resp, true
}

func (s *coreDHCPState) applyOptions(resp *dhcpv4.DHCPv4, inform bool) {
	resp.ServerIPAddr = append(net.IP(nil), s.serverID...)
	resp.Options.Update(dhcpv4.OptServerIdentifier(s.serverID))
	resp.Options.Update(dhcpv4.OptRouter(s.gateway))
	resp.Options.Update(dhcpv4.OptSubnetMask(s.mask))
	resp.Options.Update(dhcpv4.OptBroadcastAddress(s.broadcast))
	if len(s.dns) > 0 {
		resp.Options.Update(dhcpv4.OptDNS(s.dns...))
	}
	for _, option := range s.options {
		resp.Options.Update(dhcpv4.OptGeneric(dhcpv4.GenericOptionCode(option.code), option.value))
	}
	if !inform {
		resp.Options.Update(dhcpv4.OptIPAddressLeaseTime(s.leaseTime))
	}
	log.Trace().
		Str("leases", s.leasePath).
		Str("client_id", resp.ClientHWAddr.String()).
		Str("message_type", resp.MessageType().String()).
		Str("address", resp.YourIPAddr.String()).
		Int("option_count", len(resp.Options)).
		Str("options", resp.Options.String()).
		Msg("DHCPv4 reply options")
}

func (s *coreDHCPState) leaseForLocked(clientID string, now time.Time) *coreDHCPLease {
	record, ok := s.leases[clientID]
	if !ok {
		return nil
	}
	if !record.Expires.After(now) {
		s.freeLocked(record)
		return nil
	}
	return record
}

func (s *coreDHCPState) renewLocked(record *coreDHCPLease, hostname string, now time.Time) {
	record.Expires = now.Add(s.leaseTime)
	if hostname != "" {
		record.Hostname = hostname
	}
	log.Debug().
		Str("leases", s.leasePath).
		Str("client_id", record.ClientID).
		Str("address", record.Address.String()).
		Str("hostname", record.Hostname).
		Msg("DHCPv4 lease renewed")
	s.saveLocked()
}

func (s *coreDHCPState) allocateLocked(clientID string, hint net.IP, hostname string, now time.Time) (*coreDHCPLease, error) {
	if staticIP, ok := s.static[clientID]; ok {
		if owner, used := s.byIP[staticIP.String()]; used && owner != clientID {
			return nil, fmt.Errorf("static DHCP address %s is already in use", staticIP)
		}
		record := &coreDHCPLease{
			ClientID: clientID,
			Address:  staticIP,
			Expires:  now.Add(s.leaseTime),
			Hostname: hostname,
		}
		s.leases[clientID] = record
		s.byIP[staticIP.String()] = clientID
		log.Info().
			Str("leases", s.leasePath).
			Str("client_id", clientID).
			Str("address", record.Address.String()).
			Str("hostname", hostname).
			Bool("static", true).
			Msg("DHCPv4 lease allocated")
		s.saveLocked()
		return record, nil
	}

	hintNet := net.IPNet{}
	if hint4 := hint.To4(); hint4 != nil && s.inRange(hint4) {
		if _, taken := s.byIP[hint4.String()]; !taken {
			if owner, reserved := s.reservedByIP[hint4.String()]; !reserved || owner == clientID {
				hintNet = net.IPNet{IP: hint4, Mask: net.CIDRMask(32, 32)}
			}
		}
	}

	allocated, err := s.alloc.Allocate(hintNet)
	if err != nil {
		return nil, err
	}
	ip := allocated.IP.To4()
	if ip == nil {
		return nil, errors.New("DHCP allocator returned a non-IPv4 address")
	}

	record := &coreDHCPLease{
		ClientID: clientID,
		Address:  ip,
		Expires:  now.Add(s.leaseTime),
		Hostname: hostname,
	}
	s.leases[clientID] = record
	s.byIP[ip.String()] = clientID
	log.Info().
		Str("leases", s.leasePath).
		Str("client_id", clientID).
		Str("address", record.Address.String()).
		Str("hostname", hostname).
		Bool("static", false).
		Msg("DHCPv4 lease allocated")
	s.saveLocked()
	return record, nil
}

func (s *coreDHCPState) purgeExpiredLocked(now time.Time) {
	for _, record := range s.leases {
		if !record.Expires.After(now) {
			s.freeLocked(record)
		}
	}
}

func (s *coreDHCPState) freeLocked(record *coreDHCPLease) {
	log.Info().
		Str("leases", s.leasePath).
		Str("client_id", record.ClientID).
		Str("address", record.Address.String()).
		Str("hostname", record.Hostname).
		Msg("DHCPv4 lease freed")
	delete(s.leases, record.ClientID)
	delete(s.byIP, record.Address.String())
	if _, reserved := s.reservedByIP[record.Address.String()]; reserved {
		return
	}
	_ = s.alloc.Free(net.IPNet{IP: record.Address, Mask: net.CIDRMask(32, 32)})
}

func (s *coreDHCPState) inRange(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	value := binary.BigEndian.Uint32(ip4)
	return value >= s.start && value <= s.end
}

func (s *coreDHCPState) loadLeases() error {
	if s.leasePath == "" {
		return nil
	}
	data, err := os.ReadFile(s.leasePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read DHCP leases: %w", err)
	}

	var file leaseFile
	if err := json.Unmarshal(data, &file); err != nil {
		backup := s.leasePath + ".corrupt"
		_ = os.Rename(s.leasePath, backup)
		log.Warn().Str("path", s.leasePath).Str("backup", backup).Err(err).Msg("ignoring corrupt DHCP lease file")
		return nil
	}

	now := time.Now()
	for _, lease := range file.Leases {
		mac, err := net.ParseMAC(lease.ClientID)
		if err != nil {
			continue
		}
		ip := net.ParseIP(lease.Address).To4()
		if ip == nil || !lease.Expires.After(now) {
			continue
		}
		key := strings.ToLower(mac.String())
		staticIP, hasStatic := s.static[key]
		if hasStatic {
			if !ip.Equal(staticIP) {
				continue
			}
		} else if !s.inRange(ip) {
			continue
		}
		if owner, reserved := s.reservedByIP[ip.String()]; reserved && owner != key {
			continue
		}
		if _, exists := s.leases[key]; exists {
			continue
		}
		if _, exists := s.byIP[ip.String()]; exists {
			continue
		}
		if !hasStatic {
			if _, err := s.alloc.Allocate(net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)}); err != nil {
				continue
			}
		}
		s.leases[key] = &coreDHCPLease{
			ClientID: key,
			Address:  ip,
			Expires:  lease.Expires,
			Hostname: lease.Hostname,
		}
		s.byIP[ip.String()] = key
		log.Trace().
			Str("leases", s.leasePath).
			Str("client_id", key).
			Str("address", ip.String()).
			Str("hostname", lease.Hostname).
			Msg("loaded DHCPv4 lease")
	}
	if len(s.leases) > 0 {
		log.Info().Str("leases", s.leasePath).Int("count", len(s.leases)).Msg("loaded DHCPv4 leases")
	}
	return nil
}

func (s *coreDHCPState) saveLocked() {
	if s.leasePath == "" {
		return
	}
	leases := make([]leaseRecord, 0, len(s.leases))
	for _, record := range s.leases {
		leases = append(leases, leaseRecord{
			ClientID: record.ClientID,
			Address:  record.Address.String(),
			Expires:  record.Expires,
			Hostname: record.Hostname,
		})
	}
	sort.Slice(leases, func(i, j int) bool { return leases[i].ClientID < leases[j].ClientID })
	writeLeaseFile(s.leasePath, leases)
}

func writeLeaseFile(path string, leases []leaseRecord) {
	data, err := json.MarshalIndent(leaseFile{Leases: leases}, "", "  ")
	if err != nil {
		log.Error().Str("path", path).Err(err).Msg("encode DHCP leases")
		return
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Error().Str("path", tmp).Err(err).Msg("write DHCP leases")
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Error().Str("path", path).Err(err).Msg("replace DHCP leases")
		_ = os.Remove(tmp)
	}
}

func ipText(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}

func bytesCompare(a, b net.IP) int {
	a4 := a.To4()
	b4 := b.To4()
	if a4 == nil || b4 == nil {
		return 0
	}
	av := binary.BigEndian.Uint32(a4)
	bv := binary.BigEndian.Uint32(b4)
	switch {
	case av < bv:
		return -1
	case av > bv:
		return 1
	default:
		return 0
	}
}
