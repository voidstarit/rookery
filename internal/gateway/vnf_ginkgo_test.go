package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/store"
)

var _ = Describe("Typed VNF services", func() {
	var (
		st      *store.Store
		writer  *fakeVNFWriter
		server  *httptest.Server
		network store.Network
		dhcp    store.VNF
		dns     store.VNF
		nat44   store.VNF
		bgp     store.VNF
	)

	request := func(method, path, body string) *http.Response {
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, server.URL+path, reader)
		Expect(err).NotTo(HaveOccurred())
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		response, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		return response
	}
	decode := func(response *http.Response, target any) {
		defer response.Body.Close()
		Expect(json.NewDecoder(response.Body).Decode(target)).To(Succeed())
	}

	BeforeEach(func() {
		var err error
		st, err = store.Open(filepath.Join(GinkgoT().TempDir(), "gateway-vnfs.db"))
		Expect(err).NotTo(HaveOccurred())
		ctx := context.Background()
		network, err = st.CreateNetwork(ctx, store.Network{Name: "lab", BridgeDomainID: 100})
		Expect(err).NotTo(HaveOccurred())

		dhcp, err = st.CreateDHCPVNF(ctx, store.NewDHCPVNF{
			Name: "lab-dhcpv4", NetworkID: network.ID, Family: store.VNFTypeDHCPv4, Enabled: true,
			Config: store.DHCPVNFConfig{
				Gateway: "10.20.0.1/24", RangeStart: "10.20.0.100", RangeEnd: "10.20.0.200",
				LeaseTime: "12h", Transport: "shared",
			},
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = st.CreateDHCPStaticAssignment(ctx, dhcp.ID, store.DHCPStaticAssignment{
			MAC: "02:00:00:00:00:01", IP: "10.20.0.50",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = st.CreateDHCPOption(ctx, dhcp.ID, store.DHCPOption{
			Code: 15, Format: "string", Value: "lab.test",
		})
		Expect(err).NotTo(HaveOccurred())

		dns, err = st.CreateDNSVNF(ctx, store.NewDNSVNF{
			Name: "lab-dns", NetworkID: network.ID, Enabled: true,
			Config: store.DNSVNFConfig{Forward: true},
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = st.CreateDNSVNFRecord(ctx, dns.ID, store.DNSRecord{
			Name: "host.lab.test", Type: "A", Value: "10.20.0.50", TTL: 300,
		})
		Expect(err).NotTo(HaveOccurred())

		nat44, err = st.CreateNAT44VNF(ctx, store.NewNAT44VNF{
			Name: "lab-nat44", NetworkID: network.ID, Enabled: true,
			Config: store.NAT44VNFConfig{Gateway: "10.30.0.1/24", OutsideInterface: "eth0"},
		})
		Expect(err).NotTo(HaveOccurred())

		bgp, err = st.CreateBGPVNF(ctx, store.NewBGPVNF{
			Name: "lab-bgp", NetworkID: network.ID, Enabled: true,
			Config: store.BGPVNFConfig{
				Transport: "tap", LocalAS: 65000, RouterID: "10.40.0.1",
				PeerAS: 65001, PeerIP: "10.40.0.2", LocalIP: "10.40.0.1/24",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		writer = &fakeVNFWriter{}
		status := fakeVNFStatusProvider{
			sessions: []string{"tcp 10.30.0.10:8080 -> 192.0.2.10:80"},
			bgp: host.BGPStatus{
				Running: true, Established: true, SessionState: "established",
				PeerAddress: "10.40.0.2", PeerAS: 65001, LocalAS: 65000,
				Advertised: []string{"10.50.0.0/24"},
				Received:   []host.BGPReceivedPrefix{{Prefix: "10.60.0.0/24", NextHop: "10.40.0.2", ASPath: []uint32{65001}}},
			},
			dnsRecords: []host.DNSRecordView{{
				Name: "lease.lab.test", Type: "A", Value: "10.20.0.150", TTL: 300, SourceVNFID: dhcp.ID,
			}},
		}
		gw, err := gateway.New(st, fakeReadiness{status: readyStatus()},
			gateway.WithVNFRuntime(fakeVNFRuntime{}),
			gateway.WithVNFStatusProvider(status),
			gateway.WithVNFWriter(writer),
		)
		Expect(err).NotTo(HaveOccurred())
		server = httptest.NewServer(gw.Handler)
	})

	AfterEach(func() {
		server.Close()
		Expect(st.Close()).To(Succeed())
	})

	It("serves typed parent lists with status and child counts", func() {
		response := request(http.MethodGet, "/api/dhcp-vnfs", "")
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		var items []map[string]any
		decode(response, &items)
		Expect(items).To(HaveLen(1))
		Expect(items[0]["family"]).To(Equal("dhcpv4"))
		Expect(items[0]["status"]).To(Equal("running"))
		Expect(items[0]["lease_count"]).To(BeNumerically("==", 2))
		Expect(items[0]["static_assignment_count"]).To(BeNumerically("==", 1))
		Expect(items[0]["option_count"]).To(BeNumerically("==", 1))
		Expect(items[0]["gateway"]).To(Equal("10.20.0.1/24"))

		response = request(http.MethodGet, "/api/dhcp-vnfs?family=dhcpv6", "")
		var filtered []map[string]any
		decode(response, &filtered)
		Expect(filtered).To(BeEmpty())

		for _, path := range []string{"/api/dns-vnfs", "/api/nat44-vnfs", "/api/bgp-vnfs"} {
			response = request(http.MethodGet, path, "")
			Expect(response.StatusCode).To(Equal(http.StatusOK))
			var typed []map[string]any
			decode(response, &typed)
			Expect(typed).To(HaveLen(1))
		}
	})

	It("returns 404 for a wrong-type parent and for the retired common route", func() {
		response := request(http.MethodGet, "/api/dns-vnfs/"+jsonID(dhcp.ID), "")
		Expect(response.StatusCode).To(Equal(http.StatusNotFound))
		response.Body.Close()

		response = request(http.MethodGet, "/api/vnfs", "")
		Expect(response.StatusCode).To(Equal(http.StatusNotFound))
		response.Body.Close()
	})

	It("serves children, leases, record views, sessions, and BGP status", func() {
		response := request(http.MethodGet, "/api/dhcp-vnfs/"+jsonID(dhcp.ID)+"/static-assignments", "")
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		var assignments []map[string]any
		decode(response, &assignments)
		Expect(assignments).To(HaveLen(1))
		Expect(assignments[0]["mac"]).To(Equal("02:00:00:00:00:01"))

		response = request(http.MethodGet, "/api/dhcp-vnfs/"+jsonID(dhcp.ID)+"/options", "")
		var options []map[string]any
		decode(response, &options)
		Expect(options).To(HaveLen(1))
		Expect(options[0]["code"]).To(BeNumerically("==", 15))

		response = request(http.MethodGet, "/api/dhcp-vnfs/"+jsonID(dhcp.ID)+"/leases", "")
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		var leases map[string]any
		decode(response, &leases)
		Expect(leases["leases"]).To(HaveLen(1))

		response = request(http.MethodGet, "/api/dns-vnfs/"+jsonID(dns.ID)+"/record-views", "")
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		var views map[string]any
		decode(response, &views)
		records := views["records"].([]any)
		Expect(records).To(HaveLen(2))
		Expect(views["dynamic_error"]).To(BeNil())

		response = request(http.MethodGet, "/api/nat44-vnfs/"+jsonID(nat44.ID)+"/sessions", "")
		var sessions map[string]any
		decode(response, &sessions)
		Expect(sessions["sessions"]).To(HaveLen(1))

		response = request(http.MethodGet, "/api/bgp-vnfs/"+jsonID(bgp.ID)+"/status", "")
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		var status map[string]any
		decode(response, &status)
		Expect(status["established"]).To(BeTrue())
		Expect(status["advertised"]).To(HaveLen(1))
		Expect(status["received"]).To(HaveLen(1))
	})

	It("delegates parent create and delete and sets 201/204 with Location", func() {
		writer.id = dhcp.ID
		response := request(http.MethodPost, "/api/dhcp-vnfs", `{
			"name":"new-dhcp","network_id":`+jsonID(network.ID)+`,"family":"dhcpv4",
			"gateway":"10.50.0.1/24","range_start":"10.50.0.10","range_end":"10.50.0.20"}`)
		Expect(response.StatusCode).To(Equal(http.StatusCreated))
		Expect(response.Header.Get("Location")).To(Equal("/api/dhcp-vnfs/" + jsonID(dhcp.ID)))
		response.Body.Close()
		Expect(writer.createdDHCP).NotTo(BeNil())
		Expect(writer.createdDHCP.GetName()).To(Equal("new-dhcp"))

		response = request(http.MethodDelete, "/api/dhcp-vnfs/"+jsonID(dhcp.ID), "")
		Expect(response.StatusCode).To(Equal(http.StatusNoContent))
		response.Body.Close()
		Expect(writer.deleted).To(Equal(dhcp.ID))
	})

	It("maps writer errors to gRPC status codes", func() {
		writer.err = &testVNFError{status: http.StatusConflict, message: "network already has a VNF of that type"}
		response := request(http.MethodPost, "/api/dhcp-vnfs", `{
			"name":"new-dhcp","network_id":`+jsonID(network.ID)+`,"family":"dhcpv4",
			"gateway":"10.50.0.1/24","range_start":"10.50.0.10","range_end":"10.50.0.20"}`)
		Expect(response.StatusCode).To(Equal(http.StatusConflict))
		response.Body.Close()
	})
})

// testVNFError implements the VNFError interface the gateway maps to gRPC codes.
type testVNFError struct {
	status  int
	message string
}

func (e *testVNFError) Error() string            { return e.message }
func (e *testVNFError) VNFStatus() (int, string) { return e.status, e.message }
