package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/rs/zerolog/log"

	"github.com/voidstarit/rookery/internal/store"
)

const defaultDynamicDNSTTL = 300

type dnsLeaseEntry struct {
	source   int64
	clientID string
	hostname string
	address  string
	domain   string
	ttl      uint32
}

type dnsTarget struct {
	handler *dnsHandler
	sources map[int64]struct{}
	entries []dnsLeaseEntry
}

func (m *VNFManager) registerDHCPConfig(vnf store.VNF) {
	if vnf.Type != store.VNFTypeDHCPv4 && vnf.Type != store.VNFTypeDHCPv6 {
		return
	}
	m.dnsMu.Lock()
	m.dhcpConfigs[vnf.ID] = vnf
	m.dnsMu.Unlock()
	m.ensureDNSSync()
	m.syncDNSRecords()
}

func (m *VNFManager) registerDNSRuntime(vnfID int64, handler *dnsHandler) {
	if handler == nil {
		return
	}
	m.dnsMu.Lock()
	m.dnsRuntimes[vnfID] = handler
	m.dnsMu.Unlock()
	m.ensureDNSSync()
	m.syncDNSRecords()
}

func (m *VNFManager) unregisterVNF(vnfID int64, vnfType string) {
	m.dnsMu.Lock()
	delete(m.dhcpConfigs, vnfID)
	delete(m.dnsRuntimes, vnfID)
	handlers := make([]*dnsHandler, 0, len(m.dnsRuntimes))
	for _, handler := range m.dnsRuntimes {
		handlers = append(handlers, handler)
	}
	m.dnsMu.Unlock()
	if vnfType == store.VNFTypeDHCPv4 || vnfType == store.VNFTypeDHCPv6 {
		source := dnsDynamicSource(vnfID)
		for _, handler := range handlers {
			handler.RemoveDynamic(source)
		}
	}
}

func (m *VNFManager) ensureDNSSync() {
	m.dnsSyncStart.Do(func() {
		go m.dnsSyncLoop()
	})
}

func (m *VNFManager) stopDNSSync() {
	m.dnsSyncStopOnce.Do(func() {
		close(m.dnsSyncStop)
	})
}

func (m *VNFManager) dnsSyncLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.dnsSyncStop:
			return
		case <-ticker.C:
			m.syncDNSRecords()
		}
	}
}

// syncDNSRecords reconciles dynamic DNS records from the persisted DHCP lease
// files. Lease files are the durable source of truth, so this also rebuilds
// records after a DHCP or DNS VNF restart.
func (m *VNFManager) syncDNSRecords() {
	m.dnsMu.Lock()
	configs := make([]store.VNF, 0, len(m.dhcpConfigs))
	for _, vnf := range m.dhcpConfigs {
		configs = append(configs, vnf)
	}
	handlers := make(map[int64]*dnsHandler, len(m.dnsRuntimes))
	for id, handler := range m.dnsRuntimes {
		handlers[id] = handler
	}
	m.dnsMu.Unlock()

	targets := make(map[int64]*dnsTarget)
	for _, vnf := range configs {
		if vnf.Config.DNSVNFID == 0 {
			continue
		}
		handler := handlers[vnf.Config.DNSVNFID]
		if handler == nil {
			continue
		}
		target := targets[vnf.Config.DNSVNFID]
		if target == nil {
			target = &dnsTarget{handler: handler, sources: make(map[int64]struct{})}
			targets[vnf.Config.DNSVNFID] = target
		}
		target.sources[vnf.ID] = struct{}{}
		leases, err := m.readLeaseRecords(vnf.ID)
		if err != nil {
			log.Warn().Int64("vnf_id", vnf.ID).Err(err).Msg("could not read DHCP leases for DNS integration")
			continue
		}
		now := time.Now()
		for _, lease := range leases {
			if !lease.Expires.After(now) {
				continue
			}
			target.entries = append(target.entries, dnsLeaseEntry{
				source:   vnf.ID,
				clientID: lease.ClientID,
				hostname: lease.Hostname,
				address:  lease.Address,
				domain:   vnf.Config.DNSDomain,
				ttl:      dynamicDNSTTL(vnf.Config.DNSTTL),
			})
		}
	}

	for _, target := range targets {
		sort.Slice(target.entries, func(i, j int) bool {
			if target.entries[i].source == target.entries[j].source {
				return target.entries[i].clientID < target.entries[j].clientID
			}
			return target.entries[i].source < target.entries[j].source
		})
		claimed := make(map[string]struct{})
		records := make(map[int64][]store.DNSRecord, len(target.sources))
		for _, entry := range target.entries {
			forward, ok := leaseForwardName(entry.hostname, entry.domain)
			if !ok || dnsNameClaimed(target.handler, claimed, forward) {
				forward, ok = fallbackLeaseName(entry.clientID, entry.domain)
			}
			if !ok || dnsNameClaimed(target.handler, claimed, forward) {
				log.Warn().
					Int64("vnf_id", entry.source).
					Str("client_id", entry.clientID).
					Str("address", entry.address).
					Msg("could not allocate a unique DNS name for DHCP lease")
				continue
			}
			address, err := netip.ParseAddr(strings.TrimSpace(entry.address))
			if err != nil {
				continue
			}
			recordType := "AAAA"
			if address.Is4() {
				recordType = "A"
			}
			record := store.DNSRecord{
				Name: forward, Type: recordType, Value: address.String(), TTL: entry.ttl,
			}
			records[entry.source] = append(records[entry.source], record)
			claimed[forward] = struct{}{}
			if reverse, err := dns.ReverseAddr(address.String()); err == nil {
				if !dnsNameClaimed(target.handler, claimed, reverse) {
					records[entry.source] = append(records[entry.source], store.DNSRecord{
						Name: reverse, Type: "PTR", Value: dns.Fqdn(forward), TTL: entry.ttl,
					})
					claimed[reverse] = struct{}{}
				}
			}
		}
		for source := range target.sources {
			target.handler.ReplaceDynamic(dnsDynamicSource(source), records[source])
		}
	}
}

func (m *VNFManager) readLeaseRecords(vnfID int64) ([]leaseRecord, error) {
	data, err := os.ReadFile(m.leasePath(vnfID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read DHCP leases: %w", err)
	}
	var file leaseFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse DHCP leases: %w", err)
	}
	return file.Leases, nil
}

func dynamicDNSTTL(ttl uint32) uint32 {
	if ttl == 0 {
		return defaultDynamicDNSTTL
	}
	return ttl
}

func dnsDynamicSource(vnfID int64) string {
	return "dhcp-" + strconv.FormatInt(vnfID, 10)
}

func dnsNameClaimed(handler *dnsHandler, claimed map[string]struct{}, name string) bool {
	if _, ok := claimed[name]; ok {
		return true
	}
	return handler != nil && handler.StaticNameTaken(name)
}

func leaseForwardName(hostname, domain string) (string, bool) {
	name := strings.TrimSpace(strings.TrimSuffix(hostname, "."))
	domain = strings.TrimSpace(strings.TrimSuffix(domain, "."))
	if name == "" || domain == "" || strings.ContainsAny(name, " \t\r\n*") {
		return "", false
	}
	if !strings.Contains(name, ".") {
		name = name + "." + domain
	}
	name = strings.ToLower(name)
	if _, ok := dns.IsDomainName(dns.Fqdn(name)); !ok {
		return "", false
	}
	return strings.TrimSuffix(dns.CanonicalName(name), "."), true
}

func fallbackLeaseName(clientID, domain string) (string, bool) {
	domain = strings.TrimSpace(strings.TrimSuffix(domain, "."))
	if domain == "" {
		return "", false
	}
	var label strings.Builder
	for _, r := range strings.ToLower(clientID) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			label.WriteRune(r)
		default:
			if label.Len() > 0 && !strings.HasSuffix(label.String(), "-") {
				label.WriteByte('-')
			}
		}
	}
	name := strings.Trim(label.String(), "-")
	if name == "" {
		name = "client"
	}
	if len(name) > 40 {
		name = name[:40]
	}
	name = "host-" + name + "." + domain
	if _, ok := dns.IsDomainName(dns.Fqdn(name)); !ok {
		return "", false
	}
	return strings.TrimSuffix(dns.CanonicalName(name), "."), true
}
