package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coredhcp/coredhcp/handler"
	"github.com/coredhcp/coredhcp/plugins"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/iana"
)

var registerCoreDHCPv6 sync.Once

// ensureCoreDHCPv6Plugin registers the per-VNF DHCPv6 plugin once.
func ensureCoreDHCPv6Plugin() {
	registerCoreDHCPv6.Do(func() {
		if err := plugins.RegisterPlugin(&plugins.Plugin{
			Name:   coreDHCPv6Plugin,
			Setup6: setupCoreDHCPv6,
		}); err != nil {
			panic(fmt.Sprintf("register CoreDHCP v6 plugin: %v", err))
		}
	})
}

type coreDHCPv6State struct {
	mu        sync.Mutex
	prefix    netip.Prefix
	gateway   netip.Addr
	start     netip.Addr
	end       netip.Addr
	dns       []net.IP
	leaseTime time.Duration
	leasePath string
	duid      *dhcpv6.DUIDLL

	leases map[string]*coreDHCPv6Lease
	byIP   map[string]string
}

type coreDHCPv6Lease struct {
	ClientID string
	Address  netip.Addr
	Expires  time.Time
	Hostname string
}

// setupCoreDHCPv6 is a CoreDHCP plugin setup function. Arguments are, in order:
// gateway prefix, range start, range end, comma-separated DNS servers, lease
// file path, lease duration, and server DUID link-layer address.
func setupCoreDHCPv6(args ...string) (handler.Handler6, error) {
	if len(args) != 7 {
		return nil, fmt.Errorf("CoreDHCPv6 setup: want 7 arguments, got %d", len(args))
	}

	prefix, err := netip.ParsePrefix(strings.TrimSpace(args[0]))
	if err != nil || !prefix.Addr().Is6() {
		return nil, fmt.Errorf("invalid DHCPv6 gateway prefix %q", args[0])
	}
	gateway := prefix.Addr()
	if gateway.IsUnspecified() {
		return nil, errors.New("DHCPv6 gateway must be a usable address")
	}
	start, err := netip.ParseAddr(strings.TrimSpace(args[1]))
	if err != nil || !start.Is6() {
		return nil, fmt.Errorf("invalid DHCPv6 range start %q", args[1])
	}
	end, err := netip.ParseAddr(strings.TrimSpace(args[2]))
	if err != nil || !end.Is6() {
		return nil, fmt.Errorf("invalid DHCPv6 range end %q", args[2])
	}
	if !prefix.Contains(start) || !prefix.Contains(end) {
		return nil, errors.New("DHCPv6 range is outside the gateway prefix")
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

	dns, err := parseCoreDHCPv6DNS(args[3])
	if err != nil {
		return nil, err
	}
	leaseTime, err := time.ParseDuration(args[5])
	if err != nil || leaseTime <= 0 {
		return nil, fmt.Errorf("invalid DHCPv6 lease duration %q", args[5])
	}
	duidMAC, err := net.ParseMAC(args[6])
	if err != nil || len(duidMAC) != 6 {
		return nil, fmt.Errorf("invalid DHCPv6 server DUID MAC %q", args[6])
	}

	state := &coreDHCPv6State{
		prefix:    prefix,
		gateway:   gateway,
		start:     start,
		end:       end,
		dns:       dns,
		leaseTime: leaseTime,
		leasePath: args[4],
		duid: &dhcpv6.DUIDLL{
			HWType:        iana.HWTypeEthernet,
			LinkLayerAddr: duidMAC,
		},
		leases: make(map[string]*coreDHCPv6Lease),
		byIP:   make(map[string]string),
	}
	if err := state.loadLeases(); err != nil {
		return nil, err
	}
	return state.handle, nil
}

func parseCoreDHCPv6DNS(value string) ([]net.IP, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var dns []net.IP
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ip := net.ParseIP(part)
		if ip == nil || ip.To4() != nil || ip.To16() == nil {
			return nil, fmt.Errorf("invalid DHCPv6 DNS address %q", part)
		}
		dns = append(dns, ip.To16())
	}
	return dns, nil
}

func (s *coreDHCPv6State) handle(req, resp dhcpv6.DHCPv6) (dhcpv6.DHCPv6, bool) {
	msg, err := req.GetInnerMessage()
	if err != nil {
		return nil, true
	}
	if sid := msg.Options.ServerID(); sid != nil {
		switch msg.Type() {
		case dhcpv6.MessageTypeSolicit, dhcpv6.MessageTypeConfirm, dhcpv6.MessageTypeRebind:
			return nil, true
		}
		if !sid.Equal(s.duid) {
			return nil, true
		}
	} else {
		switch msg.Type() {
		case dhcpv6.MessageTypeRequest, dhcpv6.MessageTypeRenew,
			dhcpv6.MessageTypeDecline, dhcpv6.MessageTypeRelease:
			return nil, true
		}
	}

	clientID := msg.Options.ClientID()
	if clientID == nil {
		return nil, true
	}
	clientKey := clientID.String()

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredLocked(now)

	switch msg.Type() {
	case dhcpv6.MessageTypeSolicit, dhcpv6.MessageTypeRequest,
		dhcpv6.MessageTypeRenew, dhcpv6.MessageTypeRebind:
		ianaReq := msg.Options.OneIANA()
		if ianaReq == nil {
			return nil, true
		}

		record := s.leaseForLocked(clientKey, now)
		requested, hasRequested := requestedV6Address(ianaReq)
		if record == nil {
			var allocErr error
			record, allocErr = s.allocateLocked(clientKey, requested, hasRequested, now)
			if allocErr != nil {
				resp.UpdateOption(&dhcpv6.OptIANA{
					IaId: ianaReq.IaId,
					Options: dhcpv6.IdentityOptions{Options: dhcpv6.Options{
						&dhcpv6.OptStatusCode{
							StatusCode:    iana.StatusNoAddrsAvail,
							StatusMessage: "no addresses available",
						},
					}},
				})
				s.applyOptions(resp, msg)
				return resp, false
			}
		} else {
			if hasRequested && requested != record.Address {
				resp.UpdateOption(&dhcpv6.OptIANA{
					IaId: ianaReq.IaId,
					Options: dhcpv6.IdentityOptions{Options: dhcpv6.Options{
						&dhcpv6.OptStatusCode{
							StatusCode:    iana.StatusNotOnLink,
							StatusMessage: "requested address does not match the lease",
						},
					}},
				})
				s.applyOptions(resp, msg)
				return resp, false
			}
			s.renewLocked(record, now)
		}

		resp.UpdateOption(&dhcpv6.OptIANA{
			IaId: ianaReq.IaId,
			T1:   s.leaseTime / 2,
			T2:   s.leaseTime * 4 / 5,
			Options: dhcpv6.IdentityOptions{Options: dhcpv6.Options{
				&dhcpv6.OptIAAddress{
					IPv6Addr:          net.IP(record.Address.AsSlice()),
					PreferredLifetime: s.leaseTime,
					ValidLifetime:     2 * s.leaseTime,
				},
			}},
		})
		s.applyOptions(resp, msg)
		return resp, false

	case dhcpv6.MessageTypeRelease:
		if record, ok := s.leases[clientKey]; ok {
			s.freeLocked(record)
			s.saveLocked()
		}
		return nil, true

	case dhcpv6.MessageTypeInformationRequest:
		s.applyOptions(resp, msg)
		return resp, false

	case dhcpv6.MessageTypeConfirm:
		status := iana.StatusSuccess
		if ianaReq := msg.Options.OneIANA(); ianaReq != nil {
			if addr, ok := requestedV6Address(ianaReq); ok && !s.prefix.Contains(addr) {
				status = iana.StatusNotOnLink
			}
		}
		resp.UpdateOption(&dhcpv6.OptStatusCode{
			StatusCode:    status,
			StatusMessage: status.String(),
		})
		s.applyOptions(resp, msg)
		return resp, false
	}

	return nil, true
}

func (s *coreDHCPv6State) applyOptions(resp dhcpv6.DHCPv6, msg *dhcpv6.Message) {
	resp.UpdateOption(dhcpv6.OptServerID(s.duid))
	if len(s.dns) > 0 && msg.IsOptionRequested(dhcpv6.OptionDNSRecursiveNameServer) {
		resp.UpdateOption(dhcpv6.OptDNS(s.dns...))
	}
}

func requestedV6Address(ianaOpt *dhcpv6.OptIANA) (netip.Addr, bool) {
	if ianaOpt == nil {
		return netip.Addr{}, false
	}
	addrOpt := ianaOpt.Options.OneAddress()
	if addrOpt == nil {
		return netip.Addr{}, false
	}
	addr, ok := netip.AddrFromSlice(addrOpt.IPv6Addr)
	if !ok || !addr.Is6() {
		return netip.Addr{}, false
	}
	return addr, true
}

func (s *coreDHCPv6State) leaseForLocked(clientID string, now time.Time) *coreDHCPv6Lease {
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

func (s *coreDHCPv6State) renewLocked(record *coreDHCPv6Lease, now time.Time) {
	record.Expires = now.Add(s.leaseTime)
	s.saveLocked()
}

func (s *coreDHCPv6State) allocateLocked(clientID string, requested netip.Addr, hasRequested bool, now time.Time) (*coreDHCPv6Lease, error) {
	chosen := netip.Addr{}
	if hasRequested && s.inRange(requested) {
		if _, used := s.byIP[requested.String()]; !used {
			chosen = requested
		}
	}
	if !chosen.IsValid() {
		for addr := s.start; ; addr = addr.Next() {
			if addr != s.gateway {
				if _, used := s.byIP[addr.String()]; !used {
					chosen = addr
					break
				}
			}
			if addr == s.end {
				break
			}
		}
	}
	if !chosen.IsValid() {
		return nil, errors.New("no DHCPv6 addresses available")
	}

	record := &coreDHCPv6Lease{
		ClientID: clientID,
		Address:  chosen,
		Expires:  now.Add(s.leaseTime),
	}
	s.leases[clientID] = record
	s.byIP[chosen.String()] = clientID
	s.saveLocked()
	return record, nil
}

func (s *coreDHCPv6State) purgeExpiredLocked(now time.Time) {
	for _, record := range s.leases {
		if !record.Expires.After(now) {
			s.freeLocked(record)
		}
	}
}

func (s *coreDHCPv6State) freeLocked(record *coreDHCPv6Lease) {
	delete(s.leases, record.ClientID)
	delete(s.byIP, record.Address.String())
}

func (s *coreDHCPv6State) inRange(addr netip.Addr) bool {
	return addr.Compare(s.start) >= 0 && addr.Compare(s.end) <= 0
}

func (s *coreDHCPv6State) loadLeases() error {
	if s.leasePath == "" {
		return nil
	}
	data, err := os.ReadFile(s.leasePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read DHCPv6 leases: %w", err)
	}

	var file leaseFile
	if err := json.Unmarshal(data, &file); err != nil {
		backup := s.leasePath + ".corrupt"
		_ = os.Rename(s.leasePath, backup)
		slog.Warn("ignoring corrupt DHCPv6 lease file", "path", s.leasePath, "backup", backup, "error", err)
		return nil
	}

	now := time.Now()
	for _, lease := range file.Leases {
		addr, err := netip.ParseAddr(lease.Address)
		if err != nil || !addr.Is6() || !s.inRange(addr) || addr == s.gateway || !lease.Expires.After(now) {
			continue
		}
		if _, exists := s.leases[lease.ClientID]; exists {
			continue
		}
		if _, exists := s.byIP[addr.String()]; exists {
			continue
		}
		s.leases[lease.ClientID] = &coreDHCPv6Lease{
			ClientID: lease.ClientID,
			Address:  addr,
			Expires:  lease.Expires,
			Hostname: lease.Hostname,
		}
		s.byIP[addr.String()] = lease.ClientID
	}
	return nil
}

func (s *coreDHCPv6State) saveLocked() {
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
