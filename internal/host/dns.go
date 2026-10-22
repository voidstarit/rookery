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
	vnfID       int64
	handler     *dnsHandler
	udpServer   *dns.Server
	tcpServer   *dns.Server
	udpConn     *gonet.UDPConn
	tcpListener *gonet.TCPListener
	done        chan struct{}
	closeOnce   sync.Once
}

// addDNS starts a DNS server on the shared network stack. The endpoint is
// reachable at the network service address (.2) on UDP and TCP port 53.
func (n *sharedNetwork) addDNS(vnf store.VNF) error {
	handler := newDNSHandler(vnf.Config)
	if handler == nil {
		return errors.New("DNS configuration is invalid")
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

	component := &sharedDNS{
		vnfID:       vnf.ID,
		handler:     handler,
		udpConn:     udpConn,
		tcpListener: tcpListener,
		done:        make(chan struct{}),
	}
	component.udpServer = &dns.Server{PacketConn: udpConn, Handler: handler, UDPSize: 4096}
	component.tcpServer = &dns.Server{Listener: tcpListener, Handler: handler}

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
		Str("socket", n.socket).
		Int("records", len(vnf.Config.DNSRecords)).
		Bool("forward", vnf.Config.DNSForward).
		Strs("forwarders", handler.forwarders).
		Msg("DNS started on shared VNF memif")
	return nil
}

func (s *sharedDNS) serve() {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := s.udpServer.ActivateAndServe(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Warn().Int64("vnf_id", s.vnfID).Err(err).Msg("shared DNS UDP server stopped")
		}
	}()
	go func() {
		defer wg.Done()
		if err := s.tcpServer.ActivateAndServe(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Warn().Int64("vnf_id", s.vnfID).Err(err).Msg("shared DNS TCP server stopped")
		}
	}()
	wg.Wait()
	close(s.done)
}

func (s *sharedDNS) close() {
	s.closeOnce.Do(func() {
		if s.udpServer != nil || s.tcpServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), vnfStopTimeout)
			defer cancel()
			if s.udpServer != nil {
				_ = s.udpServer.ShutdownContext(ctx)
			}
			if s.tcpServer != nil {
				_ = s.tcpServer.ShutdownContext(ctx)
			}
		}
		if s.udpConn != nil {
			_ = s.udpConn.Close()
		}
		if s.tcpListener != nil {
			_ = s.tcpListener.Close()
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

type dnsHandler struct {
	records    map[string][]store.DNSRecord
	wildcards  []dnsWildcardRecords
	forward    bool
	forwarders []string
}

func newDNSHandler(config store.VNFConfig) *dnsHandler {
	handler := &dnsHandler{
		records: make(map[string][]store.DNSRecord, len(config.DNSRecords)),
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
		_ = writer.WriteMsg(response)
		return
	}
	question := request.Question[0]
	if question.Qclass != dns.ClassINET {
		response.Rcode = dns.RcodeRefused
		_ = writer.WriteMsg(response)
		return
	}
	switch question.Qtype {
	case dns.TypeAXFR, dns.TypeIXFR:
		response.Rcode = dns.RcodeRefused
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
		_ = writer.WriteMsg(response)
		return
	}
	if !h.forward {
		response.Rcode = dns.RcodeNameError
		_ = writer.WriteMsg(response)
		return
	}
	forwarded, err := h.forwardQuery(request)
	if err != nil || forwarded == nil {
		response.Rcode = dns.RcodeServerFailure
		_ = writer.WriteMsg(response)
		return
	}
	forwarded.Id = request.Id
	_ = writer.WriteMsg(forwarded)
}

func (h *dnsHandler) lookup(name string) ([]store.DNSRecord, bool) {
	if records, ok := h.records[name]; ok {
		return records, false
	}
	for _, wildcard := range h.wildcards {
		if name == wildcard.suffix || !strings.HasSuffix(name, "."+wildcard.suffix) {
			continue
		}
		return wildcard.records, true
	}
	return nil, false
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
	for _, forwarder := range h.forwarders {
		client := &dns.Client{Net: "udp", Timeout: 3 * time.Second, UDPSize: 4096}
		response, _, err := client.Exchange(request, forwarder)
		if err != nil {
			lastErr = err
			continue
		}
		if response == nil {
			lastErr = errors.New("empty DNS response")
			continue
		}
		if response.Truncated {
			tcpClient := &dns.Client{Net: "tcp", Timeout: 5 * time.Second}
			if tcpResponse, _, tcpErr := tcpClient.Exchange(request, forwarder); tcpErr == nil && tcpResponse != nil {
				return tcpResponse, nil
			}
		}
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
	default:
		return dns.TypeTXT
	}
}
