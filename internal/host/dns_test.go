package host

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/voidstarit/rookery/internal/store"
)

type dnsTestWriter struct {
	msg *dns.Msg
}

func (w *dnsTestWriter) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(10, 0, 0, 2), Port: 53}
}
func (w *dnsTestWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(10, 0, 0, 100), Port: 5353}
}
func (w *dnsTestWriter) WriteMsg(msg *dns.Msg) error {
	w.msg = msg.Copy()
	return nil
}
func (w *dnsTestWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *dnsTestWriter) Close() error                { return nil }
func (w *dnsTestWriter) TsigStatus() error           { return nil }
func (w *dnsTestWriter) TsigTimersOnly(bool)         {}
func (w *dnsTestWriter) Hijack()                     {}

func dnsQuery(t *testing.T, handler *dnsHandler, name string, qtype uint16) *dns.Msg {
	t.Helper()
	request := new(dns.Msg)
	request.SetQuestion(dns.Fqdn(name), qtype)
	request.RecursionDesired = true
	writer := &dnsTestWriter{}
	handler.ServeDNS(writer, request)
	if writer.msg == nil {
		t.Fatalf("no DNS response for %s", name)
	}
	return writer.msg
}

func TestDNSHandlerLocalRecords(t *testing.T) {
	handler := newDNSHandler(store.VNFConfig{
		DNSRecords: []store.DNSRecord{
			{Name: "vm1.lab1.test.", Type: "A", Value: "10.20.0.50", TTL: 120},
			{Name: "vm1.lab1.test.", Type: "AAAA", Value: "fd00::50", TTL: 120},
			{Name: "alias.lab1.test.", Type: "CNAME", Value: "vm1.lab1.test.", TTL: 120},
			{Name: "*.wild.lab1.test.", Type: "A", Value: "10.20.0.99", TTL: 60},
			{Name: "txt.lab1.test.", Type: "TXT", Value: "hello", TTL: 60},
		},
	})

	response := dnsQuery(t, handler, "vm1.lab1.test", dns.TypeA)
	if response.Rcode != dns.RcodeSuccess || !response.Authoritative || len(response.Answer) != 1 {
		t.Fatalf("A response = %+v", response)
	}
	if a, ok := response.Answer[0].(*dns.A); !ok || a.A.String() != "10.20.0.50" || a.Hdr.Ttl != 120 {
		t.Fatalf("A answer = %+v", response.Answer[0])
	}

	response = dnsQuery(t, handler, "VM1.LAB1.TEST", dns.TypeA)
	if len(response.Answer) != 1 {
		t.Fatalf("case-insensitive response = %+v", response)
	}

	response = dnsQuery(t, handler, "vm1.lab1.test", dns.TypeAAAA)
	if len(response.Answer) != 1 || response.Answer[0].(*dns.AAAA).AAAA.String() != "fd00::50" {
		t.Fatalf("AAAA response = %+v", response)
	}

	response = dnsQuery(t, handler, "alias.lab1.test", dns.TypeA)
	if len(response.Answer) != 2 {
		t.Fatalf("CNAME response = %+v", response)
	}
	if _, ok := response.Answer[0].(*dns.CNAME); !ok {
		t.Fatalf("first CNAME answer = %+v", response.Answer[0])
	}
	if _, ok := response.Answer[1].(*dns.A); !ok {
		t.Fatalf("second CNAME answer = %+v", response.Answer[1])
	}

	response = dnsQuery(t, handler, "host.wild.lab1.test", dns.TypeA)
	if len(response.Answer) != 1 || response.Answer[0].(*dns.A).A.String() != "10.20.0.99" {
		t.Fatalf("wildcard response = %+v", response)
	}
	if response.Answer[0].Header().Name != "host.wild.lab1.test." {
		t.Fatalf("wildcard owner = %q", response.Answer[0].Header().Name)
	}

	response = dnsQuery(t, handler, "txt.lab1.test", dns.TypeTXT)
	if len(response.Answer) != 1 || response.Answer[0].(*dns.TXT).Txt[0] != "hello" {
		t.Fatalf("TXT response = %+v", response)
	}

	response = dnsQuery(t, handler, "vm1.lab1.test", dns.TypeMX)
	if response.Rcode != dns.RcodeSuccess || len(response.Answer) != 0 {
		t.Fatalf("NODATA response = %+v", response)
	}

	response = dnsQuery(t, handler, "missing.lab1.test", dns.TypeA)
	if response.Rcode != dns.RcodeNameError {
		t.Fatalf("NXDOMAIN response = %+v", response)
	}
}

func TestDNSHandlerForwarding(t *testing.T) {
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upstream := &dns.Server{
		PacketConn: packetConn,
		Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, request *dns.Msg) {
			response := new(dns.Msg)
			response.SetReply(request)
			response.Answer = append(response.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30},
				A:   net.IPv4(203, 0, 113, 10),
			})
			_ = writer.WriteMsg(response)
		}),
	}
	started := make(chan struct{})
	upstream.NotifyStartedFunc = func() { close(started) }
	go func() { _ = upstream.ActivateAndServe() }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream DNS server did not start")
	}
	t.Cleanup(func() { _ = upstream.Shutdown() })

	handler := newDNSHandler(store.VNFConfig{
		DNSForward:    true,
		DNSForwarders: []string{packetConn.LocalAddr().String()},
	})
	response := dnsQuery(t, handler, "example.com", dns.TypeA)
	if response.Rcode != dns.RcodeSuccess || len(response.Answer) != 1 ||
		response.Answer[0].(*dns.A).A.String() != "203.0.113.10" {
		t.Fatalf("forwarded response = %+v", response)
	}

	authoritative := newDNSHandler(store.VNFConfig{})
	response = dnsQuery(t, authoritative, "example.com", dns.TypeA)
	if response.Rcode != dns.RcodeNameError {
		t.Fatalf("non-forwarding response = %+v", response)
	}
}
