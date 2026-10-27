package host

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/rs/zerolog/log"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"github.com/voidstarit/rookery/internal/store"
)

const dnsListenPort = 53

type sharedDNS struct {
	vnfID        int64
	handler      *dnsHandler
	udpServer    *dns.Server
	tcpServer    *dns.Server
	udpConn      *gonet.UDPConn
	tcpListener  *gonet.TCPListener
	udpServer6   *dns.Server
	tcpServer6   *dns.Server
	udpConn6     *gonet.UDPConn
	tcpListener6 *gonet.TCPListener
	done         chan struct{}
	closeOnce    sync.Once
}

// addDNS starts a DNS server on the shared network stack. The endpoint is
// reachable at the network service address (.2) on UDP and TCP port 53.
func (n *sharedNetwork) addDNS(vnf store.VNF) error {
	handler := newDNSHandler(vnf.Config)
	if handler == nil {
		return errors.New("DNS configuration is invalid")
	}
	handler.vnfID = vnf.ID
	handler.networkID = n.networkID
	for _, record := range vnf.Config.DNSRecords {
		log.Trace().
			Int64("vnf_id", vnf.ID).
			Int64("network_id", n.networkID).
			Str("name", record.Name).
			Str("type", record.Type).
			Str("value", record.Value).
			Uint32("ttl", record.TTL).
			Msg("DNS record loaded")
	}

	var wq waiter.Queue
	endpoint, tcpipErr := n.stack.NewEndpoint(udp.ProtocolNumber, header.IPv4ProtocolNumber, &wq)
	if tcpipErr != nil {
		return fmt.Errorf("create shared DNS UDP endpoint: %v", tcpipErr)
	}
	if err := endpoint.Bind(tcpip.FullAddress{NIC: 1, Port: dnsListenPort}); err != nil {
		endpoint.Close()
		return fmt.Errorf("bind shared DNS UDP endpoint: %v", err)
	}
	udpConn := gonet.NewUDPConn(&wq, endpoint)

	tcpListener, err := gonet.ListenTCP(n.stack, tcpip.FullAddress{NIC: 1, Port: dnsListenPort}, header.IPv4ProtocolNumber)
	if err != nil {
		_ = udpConn.Close()
		return fmt.Errorf("listen for shared DNS TCP: %v", err)
	}

	var udpConn6 *gonet.UDPConn
	var tcpListener6 *gonet.TCPListener
	if strings.TrimSpace(n.ipv6Address) != "" {
		ipv6IP, _, err := net.ParseCIDR(strings.TrimSpace(n.ipv6Address))
		if err != nil || ipv6IP.To4() != nil {
			_ = udpConn.Close()
			_ = tcpListener.Close()
			return fmt.Errorf("parse shared DNS IPv6 address %q: %w", n.ipv6Address, err)
		}
		listenAddr := tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFromSlice(ipv6IP.To16()), Port: dnsListenPort}
		var wq6 waiter.Queue
		endpoint6, tcpipErr := n.stack.NewEndpoint(udp.ProtocolNumber, header.IPv6ProtocolNumber, &wq6)
		if tcpipErr != nil {
			_ = udpConn.Close()
			_ = tcpListener.Close()
			return fmt.Errorf("create shared DNS IPv6 UDP endpoint: %v", tcpipErr)
		}
		if err := endpoint6.Bind(listenAddr); err != nil {
			endpoint6.Close()
			_ = udpConn.Close()
			_ = tcpListener.Close()
			return fmt.Errorf("bind shared DNS IPv6 UDP endpoint: %v", err)
		}
		udpConn6 = gonet.NewUDPConn(&wq6, endpoint6)
		tcpListener6, err = gonet.ListenTCP(n.stack, listenAddr, header.IPv6ProtocolNumber)
		if err != nil {
			_ = udpConn6.Close()
			_ = udpConn.Close()
			_ = tcpListener.Close()
			return fmt.Errorf("listen for shared DNS IPv6 TCP: %v", err)
		}
	}

	component := &sharedDNS{
		vnfID:        vnf.ID,
		handler:      handler,
		udpConn:      udpConn,
		tcpListener:  tcpListener,
		udpConn6:     udpConn6,
		tcpListener6: tcpListener6,
		done:         make(chan struct{}),
	}
	component.udpServer = &dns.Server{PacketConn: udpConn, Handler: handler, UDPSize: 4096}
	component.tcpServer = &dns.Server{Listener: tcpListener, Handler: handler}
	if udpConn6 != nil {
		component.udpServer6 = &dns.Server{PacketConn: udpConn6, Handler: handler, UDPSize: 4096}
		component.tcpServer6 = &dns.Server{Listener: tcpListener6, Handler: handler}
	}

	n.mu.Lock()
	if n.dns != nil {
		n.mu.Unlock()
		component.close()
		return errors.New("a DNS VNF is already registered on this network")
	}
	n.dns = component
	n.vnfs[vnf.ID] = vnf.Type
	n.mu.Unlock()

	go component.serve()
	log.Info().
		Int64("vnf_id", vnf.ID).
		Int64("network_id", n.networkID).
		Str("address", n.address).
		Str("ipv6_address", n.ipv6Address).
		Str("socket", n.socket).
		Int("records", len(vnf.Config.DNSRecords)).
		Bool("forward", vnf.Config.DNSForward).
		Strs("forwarders", handler.forwarders).
		Msg("DNS started on shared VNF memif")
	return nil
}

func (s *sharedDNS) serve() {
	var wg sync.WaitGroup
	start := func(server *dns.Server, network string) {
		if server == nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := server.ActivateAndServe(); err != nil && !errors.Is(err, net.ErrClosed) {
				log.Warn().Int64("vnf_id", s.vnfID).Str("network", network).Err(err).Msg("shared DNS server stopped")
			}
		}()
	}
	start(s.udpServer, "udp4")
	start(s.tcpServer, "tcp4")
	start(s.udpServer6, "udp6")
	start(s.tcpServer6, "tcp6")
	wg.Wait()
	close(s.done)
}

func (s *sharedDNS) close() {
	s.closeOnce.Do(func() {
		if s.udpServer != nil || s.tcpServer != nil || s.udpServer6 != nil || s.tcpServer6 != nil {
			ctx, cancel := context.WithTimeout(context.Background(), vnfStopTimeout)
			defer cancel()
			for _, server := range []*dns.Server{s.udpServer, s.tcpServer, s.udpServer6, s.tcpServer6} {
				if server != nil {
					_ = server.ShutdownContext(ctx)
				}
			}
		}
		for _, conn := range []*gonet.UDPConn{s.udpConn, s.udpConn6} {
			if conn != nil {
				_ = conn.Close()
			}
		}
		for _, listener := range []*gonet.TCPListener{s.tcpListener, s.tcpListener6} {
			if listener != nil {
				_ = listener.Close()
			}
		}
		select {
		case <-s.done:
		case <-time.After(vnfStopTimeout):
		}
	})
}

type dnsWildcardRecords struct {
	suffix  string
	records []store.DNSRecord
}

type dnsDynamicRecord struct {
	source string
	record store.DNSRecord
}

type dnsHandler struct {
	mu         sync.RWMutex
	vnfID      int64
	networkID  int64
	records    map[string][]store.DNSRecord
	dynamic    map[string][]dnsDynamicRecord
	wildcards  []dnsWildcardRecords
	forward    bool
	forwarders []string
}

func newDNSHandler(config store.VNFConfig) *dnsHandler {
	handler := &dnsHandler{
		records: make(map[string][]store.DNSRecord, len(config.DNSRecords)),
		dynamic: make(map[string][]dnsDynamicRecord),
		forward: config.DNSForward,
	}
	for _, record := range config.DNSRecords {
		name := dns.CanonicalName(strings.TrimSpace(record.Name))
		if strings.HasPrefix(name, "*.") {
			handler.wildcards = append(handler.wildcards, dnsWildcardRecords{
				suffix:  strings.TrimPrefix(name, "*."),
				records: append([]store.DNSRecord(nil), record),
			})
			continue
		}
		handler.records[name] = append(handler.records[name], record)
	}
	sort.Slice(handler.wildcards, func(i, j int) bool {
		return len(handler.wildcards[i].suffix) > len(handler.wildcards[j].suffix)
	})
	handler.forwarders = append([]string(nil), config.DNSForwarders...)
	if handler.forward && len(handler.forwarders) == 0 {
		if resolver, err := dns.ClientConfigFromFile("/etc/resolv.conf"); err == nil {
			port := resolver.Port
			if port == "" {
				port = "53"
			}
			for _, server := range resolver.Servers {
				handler.forwarders = append(handler.forwarders, net.JoinHostPort(server, port))
			}
		}
	}
	return handler
}

func (h *dnsHandler) ServeDNS(writer dns.ResponseWriter, request *dns.Msg) {
	response := new(dns.Msg)
	response.SetReply(request)
	response.RecursionAvailable = h.forward
	if len(request.Question) != 1 {
		response.Rcode = dns.RcodeFormatError
		h.logResponse(writer, request, response, "format-error")
		_ = writer.WriteMsg(response)
		return
	}
	question := request.Question[0]
	log.Trace().
		Int64("vnf_id", h.vnfID).
		Int64("network_id", h.networkID).
		Str("client", writer.RemoteAddr().String()).
		Str("name", question.Name).
		Str("type", dnsTypeName(question.Qtype)).
		Str("class", dnsClassName(question.Qclass)).
		Msg("DNS query received")
	if question.Qclass != dns.ClassINET {
		response.Rcode = dns.RcodeRefused
		h.logResponse(writer, request, response, "refused-class")
		_ = writer.WriteMsg(response)
		return
	}
	switch question.Qtype {
	case dns.TypeAXFR, dns.TypeIXFR:
		response.Rcode = dns.RcodeRefused
		h.logResponse(writer, request, response, "refused-transfer")
		_ = writer.WriteMsg(response)
		return
	}
	if opt := request.IsEdns0(); opt != nil {
		response.SetEdns0(opt.UDPSize(), opt.Do())
	}

	name := dns.CanonicalName(question.Name)
	records, wildcard := h.lookup(name)
	if len(records) > 0 {
		response.Authoritative = true
		h.answer(response, question, name, records, wildcard)
		h.logResponse(writer, request, response, "local")
		_ = writer.WriteMsg(response)
		return
	}
	if !h.forward {
		response.Rcode = dns.RcodeNameError
		h.logResponse(writer, request, response, "nxdomain")
		_ = writer.WriteMsg(response)
		return
	}
	forwarded, err := h.forwardQuery(request)
	if err != nil || forwarded == nil {
		response.Rcode = dns.RcodeServerFailure
		h.logResponse(writer, request, response, "servfail")
		_ = writer.WriteMsg(response)
		return
	}
	forwarded.Id = request.Id
	h.logResponse(writer, request, forwarded, "forward")
	_ = writer.WriteMsg(forwarded)
}

func (h *dnsHandler) logResponse(writer dns.ResponseWriter, request, response *dns.Msg, source string) {
	event := log.Trace().
		Int64("vnf_id", h.vnfID).
		Int64("network_id", h.networkID).
		Str("client", writer.RemoteAddr().String()).
		Str("source", source).
		Str("rcode", dns.RcodeToString[response.Rcode]).
		Int("answers", len(response.Answer)).
		Bool("authoritative", response.Authoritative)
	if len(request.Question) == 1 {
		event = event.Str("name", request.Question[0].Name).Str("type", dnsTypeName(request.Question[0].Qtype))
	}
	event.Str("response", response.String()).Msg("DNS response")
}

func (h *dnsHandler) lookup(name string) ([]store.DNSRecord, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	records := h.records[name]
	dynamic := h.dynamic[name]
	if len(records) > 0 || len(dynamic) > 0 {
		combined := make([]store.DNSRecord, 0, len(records)+len(dynamic))
		combined = append(combined, records...)
		for _, record := range dynamic {
			combined = append(combined, record.record)
		}
		return combined, false
	}
	for _, wildcard := range h.wildcards {
		if name == wildcard.suffix || !strings.HasSuffix(name, "."+wildcard.suffix) {
			continue
		}
		return wildcard.records, true
	}
	return nil, false
}

// StaticNameTaken reports whether name already has a configured local record.
// Dynamic DHCP records use it to avoid shadowing administrator-managed data.
func (h *dnsHandler) StaticNameTaken(name string) bool {
	name = dns.CanonicalName(strings.TrimSpace(name))
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.records[name]) > 0
}

// ReplaceDynamic replaces every dynamic record owned by source. The caller is
// responsible for avoiding collisions with other lease owners.
func (h *dnsHandler) ReplaceDynamic(source string, records []store.DNSRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeDynamicLocked(source)
	for _, record := range records {
		name := dns.CanonicalName(strings.TrimSpace(record.Name))
		if name == "." || name == "" {
			continue
		}
		record.Name = name
		record.Type = strings.ToUpper(strings.TrimSpace(record.Type))
		h.dynamic[name] = append(h.dynamic[name], dnsDynamicRecord{source: source, record: record})
	}
}

// RemoveDynamic removes every dynamic record owned by source.
func (h *dnsHandler) RemoveDynamic(source string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeDynamicLocked(source)
}

func (h *dnsHandler) removeDynamicLocked(source string) {
	for name, records := range h.dynamic {
		filtered := records[:0]
		for _, record := range records {
			if record.source != source {
				filtered = append(filtered, record)
			}
		}
		if len(filtered) == 0 {
			delete(h.dynamic, name)
			continue
		}
		h.dynamic[name] = filtered
	}
}

func (h *dnsHandler) answer(response *dns.Msg, question dns.Question, owner string, records []store.DNSRecord, wildcard bool) {
	var cname *store.DNSRecord
	for index := range records {
		if records[index].Type == "CNAME" {
			cname = &records[index]
			break
		}
	}
	if cname != nil && question.Qtype != dns.TypeCNAME && question.Qtype != dns.TypeANY {
		response.Answer = append(response.Answer, dnsRecordRR(*cname, owner, wildcard))
		targetRecords, targetWildcard := h.lookup(dns.CanonicalName(cname.Value))
		for _, record := range targetRecords {
			if recordQType(record.Type) == question.Qtype {
				response.Answer = append(response.Answer, dnsRecordRR(record, cname.Value, targetWildcard))
			}
		}
		return
	}
	for _, record := range records {
		if question.Qtype == dns.TypeANY || recordQType(record.Type) == question.Qtype {
			response.Answer = append(response.Answer, dnsRecordRR(record, owner, wildcard))
		}
	}
}

func (h *dnsHandler) forwardQuery(request *dns.Msg) (*dns.Msg, error) {
	var lastErr error
	qname, qtype := "", uint16(0)
	if len(request.Question) > 0 {
		qname = request.Question[0].Name
		qtype = request.Question[0].Qtype
	}
	for _, forwarder := range h.forwarders {
		log.Trace().
			Int64("vnf_id", h.vnfID).
			Int64("network_id", h.networkID).
			Str("name", qname).
			Str("type", dnsTypeName(qtype)).
			Str("forwarder", forwarder).
			Msg("DNS forwarding query")
		client := &dns.Client{Net: "udp", Timeout: 3 * time.Second, UDPSize: 4096}
		response, rtt, err := client.Exchange(request, forwarder)
		if err != nil {
			lastErr = err
			log.Trace().
				Int64("vnf_id", h.vnfID).
				Int64("network_id", h.networkID).
				Str("forwarder", forwarder).
				Err(err).
				Msg("DNS forwarder failed")
			continue
		}
		if response == nil {
			lastErr = errors.New("empty DNS response")
			continue
		}
		if response.Truncated {
			tcpClient := &dns.Client{Net: "tcp", Timeout: 5 * time.Second}
			if tcpResponse, tcpRTT, tcpErr := tcpClient.Exchange(request, forwarder); tcpErr == nil && tcpResponse != nil {
				log.Trace().
					Int64("vnf_id", h.vnfID).
					Int64("network_id", h.networkID).
					Str("forwarder", forwarder).
					Dur("rtt", tcpRTT).
					Str("rcode", dns.RcodeToString[tcpResponse.Rcode]).
					Int("answers", len(tcpResponse.Answer)).
					Msg("DNS forwarded response (tcp fallback)")
				return tcpResponse, nil
			}
		}
		log.Trace().
			Int64("vnf_id", h.vnfID).
			Int64("network_id", h.networkID).
			Str("forwarder", forwarder).
			Dur("rtt", rtt).
			Str("rcode", dns.RcodeToString[response.Rcode]).
			Int("answers", len(response.Answer)).
			Msg("DNS forwarded response")
		return response, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no DNS forwarders configured")
	}
	return nil, lastErr
}

func dnsRecordRR(record store.DNSRecord, owner string, wildcard bool) dns.RR {
	name := owner
	if wildcard {
		name = dns.CanonicalName(owner)
	} else if name == "" {
		name = record.Name
	}
	header := dns.RR_Header{Name: dns.Fqdn(name), Rrtype: recordQType(record.Type), Class: dns.ClassINET, Ttl: record.TTL}
	switch record.Type {
	case "A":
		return &dns.A{Hdr: header, A: net.ParseIP(record.Value).To4()}
	case "AAAA":
		return &dns.AAAA{Hdr: header, AAAA: net.ParseIP(record.Value)}
	case "CNAME":
		return &dns.CNAME{Hdr: header, Target: dns.Fqdn(record.Value)}
	case "TXT":
		return &dns.TXT{Hdr: header, Txt: []string{record.Value}}
	case "PTR":
		return &dns.PTR{Hdr: header, Ptr: dns.Fqdn(record.Value)}
	default:
		return &dns.TXT{Hdr: header, Txt: []string{record.Value}}
	}
}

func recordQType(recordType string) uint16 {
	switch recordType {
	case "A":
		return dns.TypeA
	case "AAAA":
		return dns.TypeAAAA
	case "CNAME":
		return dns.TypeCNAME
	case "TXT":
		return dns.TypeTXT
	case "PTR":
		return dns.TypePTR
	default:
		return dns.TypeTXT
	}
}

func dnsTypeName(qtype uint16) string {
	if name, ok := dns.TypeToString[qtype]; ok {
		return name
	}
	return fmt.Sprintf("TYPE%d", qtype)
}

func dnsClassName(qclass uint16) string {
	if name, ok := dns.ClassToString[qclass]; ok {
		return name
	}
	return fmt.Sprintf("CLASS%d", qclass)
}
