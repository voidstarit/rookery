package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

// lifecycleVNFKinds lists the typed parent routes exercised by the error matrix.
var lifecycleVNFKinds = []string{
	store.VNFTypeDHCPv4,
	store.VNFTypeDHCPv6,
	store.VNFTypeDNS,
	store.VNFTypeNAT44,
	store.VNFTypeBGP,
}

// createLifecycleVNF creates one typed parent through the lifecycle service.
func createLifecycleVNF(t *testing.T, service *Service, networkID int64, kind, name string, enabled bool) store.VNF {
	t.Helper()
	ctx := context.Background()
	var (
		vnf store.VNF
		err error
	)
	switch kind {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
		vnf, err = service.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{
			Name: name, NetworkId: networkID, Family: kind, Enabled: boolPtr(enabled),
		})
	case store.VNFTypeDNS:
		vnf, err = service.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{
			Name: name, NetworkId: networkID, Enabled: boolPtr(enabled),
		})
	case store.VNFTypeNAT44:
		vnf, err = service.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{
			Name: name, NetworkId: networkID, Enabled: boolPtr(enabled),
		})
	case store.VNFTypeBGP:
		vnf, err = service.CreateBGPVNF(ctx, &rookeryv1.CreateBGPVNFRequest{
			Name: name, NetworkId: networkID, Enabled: boolPtr(enabled),
		})
	default:
		t.Fatalf("unknown vnf kind %q", kind)
	}
	if err != nil {
		t.Fatalf("create %s vnf: %v", kind, err)
	}
	return vnf
}

// updateLifecycleVNF calls the typed update route for kind.
func updateLifecycleVNF(service *Service, kind string, id int64, name *string, enabled *bool, allFields bool, dnsVNFID int64) error {
	ctx := context.Background()
	switch kind {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
		request := &rookeryv1.UpdateDHCPVNFRequest{Id: id}
		if allFields {
			request.Gateway = strPtr("10.0.0.1/24")
			request.RangeStart = strPtr("10.0.0.10")
			request.RangeEnd = strPtr("10.0.0.30")
			request.LeaseTime = strPtr("6h")
			request.Dns = strPtr("10.0.0.2")
			request.BindIp = strPtr("10.0.0.2/24")
			request.Transport = strPtr("shared")
			request.DnsVnfId = i64Ptr(dnsVNFID)
			request.DnsDomain = strPtr("lab.test")
			request.DnsTtl = u32Ptr(600)
		}
		request.Name = name
		request.Enabled = enabled
		_, err := service.UpdateDHCPVNF(ctx, request)
		return err
	case store.VNFTypeDNS:
		_, err := service.UpdateDNSVNF(ctx, &rookeryv1.UpdateDNSVNFRequest{
			Id: id, Name: name, Enabled: enabled,
		})
		return err
	case store.VNFTypeNAT44:
		_, err := service.UpdateNAT44VNF(ctx, &rookeryv1.UpdateNAT44VNFRequest{
			Id: id, Name: name, Enabled: enabled,
		})
		return err
	case store.VNFTypeBGP:
		_, err := service.UpdateBGPVNF(ctx, &rookeryv1.UpdateBGPVNFRequest{
			Id: id, Name: name, Enabled: enabled,
		})
		return err
	default:
		panic("unknown vnf kind " + kind)
	}
}

// setKindNormalizeError makes the normalizer of one parent type fail.
func setKindNormalizeError(backend *fakeBackend, kind string) {
	err := errors.New("invalid configuration")
	switch kind {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
		backend.normalizeDHCPErr = err
	case store.VNFTypeDNS:
		// DNS has no normalizer; an invalid name is the equivalent rejection.
	case store.VNFTypeNAT44:
		backend.normalizeNAT44Err = err
	case store.VNFTypeBGP:
		backend.normalizeBGPErr = err
	}
}

// setKindStopError makes the stop call of one parent type fail.
func setKindStopError(backend *fakeBackend, kind string) {
	if kind == store.VNFTypeNAT44 {
		backend.stopNATErr = errors.New("stop failed")
		return
	}
	backend.stopErr = errors.New("stop failed")
}

// createNameConflict creates a differently typed VNF whose name is taken.
func createNameConflict(t *testing.T, service *Service, networkID int64, kind string) {
	t.Helper()
	switch kind {
	case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
		createLifecycleVNF(t, service, networkID, store.VNFTypeDNS, "taken", false)
	case store.VNFTypeDNS:
		createLifecycleVNF(t, service, networkID, store.VNFTypeDHCPv4, "taken", false)
	default:
		createLifecycleVNF(t, service, networkID, store.VNFTypeDNS, "taken", false)
	}
}

// TestCreateVNFValidationErrors covers create validation and backend failures for
// every parent type.
func TestCreateVNFValidationErrors(t *testing.T) {
	ctx := context.Background()
	type createCase struct {
		name  string
		kind  string
		setup func(*fakeBackend, store.Network, store.Network)
		call  func(*Service, store.Network, store.Network) error
		want  int
	}
	cases := []createCase{
		{"dhcp name required", store.VNFTypeDHCPv4, nil, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: " ", NetworkId: network.ID, Family: store.VNFTypeDHCPv4})
			return err
		}, http.StatusBadRequest},
		{"dhcp name too long", store.VNFTypeDHCPv4, nil, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: strings.Repeat("a", 65), NetworkId: network.ID, Family: store.VNFTypeDHCPv4})
			return err
		}, http.StatusBadRequest},
		{"dhcp name pattern", store.VNFTypeDHCPv4, nil, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "-bad", NetworkId: network.ID, Family: store.VNFTypeDHCPv4})
			return err
		}, http.StatusBadRequest},
		{"dhcp family", store.VNFTypeDHCPv4, nil, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: network.ID, Family: "dhcpv5"})
			return err
		}, http.StatusBadRequest},
		{"dhcp network id", store.VNFTypeDHCPv4, nil, func(s *Service, _, _ store.Network) error {
			_, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: 0, Family: store.VNFTypeDHCPv4})
			return err
		}, http.StatusBadRequest},
		{"dhcp missing network", store.VNFTypeDHCPv4, nil, func(s *Service, _, _ store.Network) error {
			_, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: 9999, Family: store.VNFTypeDHCPv4})
			return err
		}, http.StatusBadRequest},
		{"dhcp normalizer", store.VNFTypeDHCPv4, func(b *fakeBackend, _, _ store.Network) {
			b.normalizeDHCPErr = errors.New("bad range")
		}, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4})
			return err
		}, http.StatusBadRequest},
		{"dhcp nat gateway", store.VNFTypeDHCPv4, func(b *fakeBackend, _, _ store.Network) {
			b.natGatewayErr = errors.New("range outside network")
		}, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4})
			return err
		}, http.StatusBadRequest},
		{"dhcp missing dns reference", store.VNFTypeDHCPv4, nil, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4, DnsVnfId: 9999})
			return err
		}, http.StatusBadRequest},
		{"dhcp dns reference not dns", store.VNFTypeDHCPv4, func(_ *fakeBackend, _, other store.Network) {
		}, func(s *Service, network, other store.Network) error {
			nat, err := s.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{Name: "nat", NetworkId: other.ID})
			if err != nil {
				return err
			}
			_, err = s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4, DnsVnfId: nat.ID})
			return err
		}, http.StatusBadRequest},
		{"dhcp dns reference another network", store.VNFTypeDHCPv4, nil, func(s *Service, network, other store.Network) error {
			dns, err := s.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{Name: "dns", NetworkId: other.ID})
			if err != nil {
				return err
			}
			_, err = s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4, DnsVnfId: dns.ID})
			return err
		}, http.StatusBadRequest},
		{"dhcp not ready", store.VNFTypeDHCPv4, func(b *fakeBackend, _, _ store.Network) {
			b.readyErr = errors.New("daemon unavailable")
		}, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4, Enabled: boolPtr(true)})
			return err
		}, http.StatusServiceUnavailable},
		{"dhcp duplicate name", store.VNFTypeDHCPv4, nil, func(s *Service, network, other store.Network) error {
			if _, err := s.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{Name: "dhcp", NetworkId: other.ID}); err != nil {
				return err
			}
			_, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "dhcp", NetworkId: network.ID, Family: store.VNFTypeDHCPv4})
			return err
		}, http.StatusConflict},
		{"dns name", store.VNFTypeDNS, nil, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{Name: "", NetworkId: network.ID})
			return err
		}, http.StatusBadRequest},
		{"dns network id", store.VNFTypeDNS, nil, func(s *Service, _, _ store.Network) error {
			_, err := s.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{Name: "dns"})
			return err
		}, http.StatusBadRequest},
		{"dns missing network", store.VNFTypeDNS, nil, func(s *Service, _, _ store.Network) error {
			_, err := s.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{Name: "dns", NetworkId: 9999})
			return err
		}, http.StatusBadRequest},
		{"dns not ready", store.VNFTypeDNS, func(b *fakeBackend, _, _ store.Network) {
			b.readyErr = errors.New("daemon unavailable")
		}, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{Name: "dns", NetworkId: network.ID, Enabled: boolPtr(true)})
			return err
		}, http.StatusServiceUnavailable},
		{"dns duplicate name", store.VNFTypeDNS, nil, func(s *Service, network, other store.Network) error {
			if _, err := s.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{Name: "dns", NetworkId: other.ID, Family: store.VNFTypeDHCPv4}); err != nil {
				return err
			}
			_, err := s.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{Name: "dns", NetworkId: network.ID})
			return err
		}, http.StatusConflict},
		{"nat44 name", store.VNFTypeNAT44, nil, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{Name: "", NetworkId: network.ID})
			return err
		}, http.StatusBadRequest},
		{"nat44 network id", store.VNFTypeNAT44, nil, func(s *Service, _, _ store.Network) error {
			_, err := s.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{Name: "nat"})
			return err
		}, http.StatusBadRequest},
		{"nat44 normalizer", store.VNFTypeNAT44, func(b *fakeBackend, _, _ store.Network) {
			b.normalizeNAT44Err = errors.New("bad gateway")
		}, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{Name: "nat", NetworkId: network.ID})
			return err
		}, http.StatusBadRequest},
		{"nat44 missing network", store.VNFTypeNAT44, nil, func(s *Service, _, _ store.Network) error {
			_, err := s.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{Name: "nat", NetworkId: 9999})
			return err
		}, http.StatusBadRequest},
		{"nat44 not ready", store.VNFTypeNAT44, func(b *fakeBackend, _, _ store.Network) {
			b.readyErr = errors.New("daemon unavailable")
		}, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{Name: "nat", NetworkId: network.ID, Enabled: boolPtr(true)})
			return err
		}, http.StatusServiceUnavailable},
		{"nat44 duplicate name", store.VNFTypeNAT44, nil, func(s *Service, network, other store.Network) error {
			if _, err := s.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{Name: "nat", NetworkId: other.ID}); err != nil {
				return err
			}
			_, err := s.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{Name: "nat", NetworkId: network.ID})
			return err
		}, http.StatusConflict},
		{"bgp name", store.VNFTypeBGP, nil, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateBGPVNF(ctx, &rookeryv1.CreateBGPVNFRequest{Name: "", NetworkId: network.ID})
			return err
		}, http.StatusBadRequest},
		{"bgp network id", store.VNFTypeBGP, nil, func(s *Service, _, _ store.Network) error {
			_, err := s.CreateBGPVNF(ctx, &rookeryv1.CreateBGPVNFRequest{Name: "bgp"})
			return err
		}, http.StatusBadRequest},
		{"bgp normalizer", store.VNFTypeBGP, func(b *fakeBackend, _, _ store.Network) {
			b.normalizeBGPErr = errors.New("bad peer")
		}, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateBGPVNF(ctx, &rookeryv1.CreateBGPVNFRequest{Name: "bgp", NetworkId: network.ID})
			return err
		}, http.StatusBadRequest},
		{"bgp missing network", store.VNFTypeBGP, nil, func(s *Service, _, _ store.Network) error {
			_, err := s.CreateBGPVNF(ctx, &rookeryv1.CreateBGPVNFRequest{Name: "bgp", NetworkId: 9999})
			return err
		}, http.StatusBadRequest},
		{"bgp not ready", store.VNFTypeBGP, func(b *fakeBackend, _, _ store.Network) {
			b.readyErr = errors.New("daemon unavailable")
		}, func(s *Service, network, _ store.Network) error {
			_, err := s.CreateBGPVNF(ctx, &rookeryv1.CreateBGPVNFRequest{Name: "bgp", NetworkId: network.ID, Enabled: boolPtr(true)})
			return err
		}, http.StatusServiceUnavailable},
		{"bgp duplicate name", store.VNFTypeBGP, nil, func(s *Service, network, other store.Network) error {
			if _, err := s.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{Name: "bgp", NetworkId: other.ID}); err != nil {
				return err
			}
			_, err := s.CreateBGPVNF(ctx, &rookeryv1.CreateBGPVNFRequest{Name: "bgp", NetworkId: network.ID})
			return err
		}, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, service, backend, network, other := newVNFService(t)
			if tc.setup != nil {
				tc.setup(backend, network, other)
			}
			err := tc.call(service, network, other)
			if err == nil {
				t.Fatalf("expected status %d, got no error", tc.want)
			}
			expectVNFStatus(t, err, tc.want)
		})
	}
}

// TestCreateVNFActivationRollback proves a failed activation removes the new
// record for every parent type.
func TestCreateVNFActivationRollback(t *testing.T) {
	for _, kind := range lifecycleVNFKinds {
		t.Run(kind, func(t *testing.T) {
			st, service, backend, network, _ := newVNFService(t)
			backend.activateErr = errors.New("start failed")
			err := func() error {
				ctx := context.Background()
				switch kind {
				case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
					_, err := service.CreateDHCPVNF(ctx, &rookeryv1.CreateDHCPVNFRequest{
						Name: "vnf", NetworkId: network.ID, Family: kind, Enabled: boolPtr(true),
					})
					return err
				case store.VNFTypeDNS:
					_, err := service.CreateDNSVNF(ctx, &rookeryv1.CreateDNSVNFRequest{
						Name: "vnf", NetworkId: network.ID, Enabled: boolPtr(true),
					})
					return err
				case store.VNFTypeNAT44:
					_, err := service.CreateNAT44VNF(ctx, &rookeryv1.CreateNAT44VNFRequest{
						Name: "vnf", NetworkId: network.ID, Enabled: boolPtr(true),
					})
					return err
				default:
					_, err := service.CreateBGPVNF(ctx, &rookeryv1.CreateBGPVNFRequest{
						Name: "vnf", NetworkId: network.ID, Enabled: boolPtr(true),
					})
					return err
				}
			}()
			if err == nil {
				t.Fatal("create succeeded despite an activation failure")
			}
			expectVNFStatus(t, err, http.StatusInternalServerError)
			vnfs, listErr := st.ListVNFs(context.Background(), nil)
			if listErr != nil {
				t.Fatalf("list vnfs: %v", listErr)
			}
			if len(vnfs) != 0 {
				t.Fatalf("failed activation kept the record: %+v", vnfs)
			}
		})
	}
}

// TestUpdateVNFErrorMatrix covers validation, backend, conflict, and activation
// failures for every typed update route.
func TestUpdateVNFErrorMatrix(t *testing.T) {
	for _, kind := range lifecycleVNFKinds {
		kind := kind
		t.Run(kind+"/missing", func(t *testing.T) {
			_, service, _, _, _ := newVNFService(t)
			if err := updateLifecycleVNF(service, kind, 9999, nil, boolPtr(false), false, 0); err == nil {
				t.Fatal("update of a missing VNF succeeded")
			} else {
				expectVNFStatus(t, err, http.StatusNotFound)
			}
		})
		t.Run(kind+"/bad name", func(t *testing.T) {
			_, service, _, network, _ := newVNFService(t)
			vnf := createLifecycleVNF(t, service, network.ID, kind, "vnf", false)
			if err := updateLifecycleVNF(service, kind, vnf.ID, strPtr("bad name"), nil, false, 0); err == nil {
				t.Fatal("update accepted a bad name")
			} else {
				expectVNFStatus(t, err, http.StatusBadRequest)
			}
		})
		t.Run(kind+"/normalizer", func(t *testing.T) {
			_, service, backend, network, _ := newVNFService(t)
			vnf := createLifecycleVNF(t, service, network.ID, kind, "vnf", false)
			setKindNormalizeError(backend, kind)
			var err error
			if kind == store.VNFTypeDNS {
				// DNS has no normalizer; exercise the name rule instead.
				err = updateLifecycleVNF(service, kind, vnf.ID, strPtr("-bad"), nil, false, 0)
			} else {
				err = updateLifecycleVNF(service, kind, vnf.ID, nil, nil, false, 0)
			}
			if err == nil {
				t.Fatal("update succeeded despite the validation failure")
			}
			expectVNFStatus(t, err, http.StatusBadRequest)
		})
		t.Run(kind+"/not ready", func(t *testing.T) {
			_, service, backend, network, _ := newVNFService(t)
			vnf := createLifecycleVNF(t, service, network.ID, kind, "vnf", false)
			backend.readyErr = errors.New("daemon unavailable")
			if err := updateLifecycleVNF(service, kind, vnf.ID, nil, boolPtr(true), false, 0); err == nil {
				t.Fatal("update succeeded while the backend was unavailable")
			} else {
				expectVNFStatus(t, err, http.StatusServiceUnavailable)
			}
		})
		t.Run(kind+"/stop failure", func(t *testing.T) {
			_, service, backend, network, _ := newVNFService(t)
			vnf := createLifecycleVNF(t, service, network.ID, kind, "vnf", true)
			setKindStopError(backend, kind)
			if err := updateLifecycleVNF(service, kind, vnf.ID, nil, nil, false, 0); err == nil {
				t.Fatal("update succeeded despite a stop failure")
			} else {
				expectVNFStatus(t, err, http.StatusInternalServerError)
			}
		})
		t.Run(kind+"/activation failure", func(t *testing.T) {
			st, service, backend, network, _ := newVNFService(t)
			vnf := createLifecycleVNF(t, service, network.ID, kind, "vnf", true)
			backend.activateErr = errors.New("start failed")
			if err := updateLifecycleVNF(service, kind, vnf.ID, nil, nil, false, 0); err == nil {
				t.Fatal("update succeeded despite an activation failure")
			} else {
				expectVNFStatus(t, err, http.StatusInternalServerError)
			}
			saved, err := st.GetVNF(context.Background(), vnf.ID)
			if err != nil {
				t.Fatalf("read updated vnf: %v", err)
			}
			if saved.Enabled {
				t.Fatalf("failed activation left the VNF enabled: %+v", saved)
			}
		})
		t.Run(kind+"/name conflict", func(t *testing.T) {
			_, service, _, network, _ := newVNFService(t)
			vnf := createLifecycleVNF(t, service, network.ID, kind, "vnf", false)
			createNameConflict(t, service, network.ID, kind)
			if err := updateLifecycleVNF(service, kind, vnf.ID, strPtr("taken"), nil, false, 0); err == nil {
				t.Fatal("update accepted a duplicate name")
			} else {
				expectVNFStatus(t, err, http.StatusConflict)
			}
		})
	}
}

// TestUpdateVNFAllFields covers every optional DHCP/NAT44/BGP update field.
func TestUpdateVNFAllFields(t *testing.T) {
	ctx := context.Background()

	t.Run("dhcpv4", func(t *testing.T) {
		_, service, _, network, _ := newVNFService(t)
		dns := createLifecycleVNF(t, service, network.ID, store.VNFTypeDNS, "dns", false)
		vnf := createLifecycleVNF(t, service, network.ID, store.VNFTypeDHCPv4, "dhcp", false)
		if err := updateLifecycleVNF(service, store.VNFTypeDHCPv4, vnf.ID, strPtr("dhcp-renamed"), boolPtr(true), true, dns.ID); err != nil {
			t.Fatalf("update dhcp: %v", err)
		}
		saved, err := service.store.GetVNF(ctx, vnf.ID)
		if err != nil {
			t.Fatalf("read dhcp: %v", err)
		}
		if !saved.Enabled || saved.Name != "dhcp-renamed" || saved.Config.Gateway != "10.0.0.1/24" ||
			saved.Config.RangeEnd != "10.0.0.30" || saved.Config.LeaseTime != "6h" ||
			saved.Config.DNS != "10.0.0.2" || saved.Config.BindIP != "10.0.0.2/24" ||
			saved.Config.Transport != "shared" || saved.Config.DNSVNFID != dns.ID ||
			saved.Config.DNSDomain != "lab.test" || saved.Config.DNSTTL != 600 {
			t.Fatalf("updated dhcp = %+v", saved)
		}
	})

	t.Run("dhcpv6", func(t *testing.T) {
		_, service, _, network, _ := newVNFService(t)
		vnf := createLifecycleVNF(t, service, network.ID, store.VNFTypeDHCPv6, "dhcp6", false)
		if _, err := service.UpdateDHCPVNF(ctx, &rookeryv1.UpdateDHCPVNFRequest{
			Id: vnf.ID, Gateway: strPtr("fd00:1::1/64"), RangeStart: strPtr("fd00:1::100"),
			RangeEnd: strPtr("fd00:1::200"), LeaseTime: strPtr("1h"), Transport: strPtr("tap"),
		}); err != nil {
			t.Fatalf("update dhcpv6: %v", err)
		}
		saved, err := service.store.GetDHCPVNF(ctx, vnf.ID)
		if err != nil {
			t.Fatalf("read dhcpv6: %v", err)
		}
		if saved.Config.Gateway != "fd00:1::1/64" || saved.Config.Transport != "tap" {
			t.Fatalf("updated dhcpv6 = %+v", saved.Config)
		}
	})

	t.Run("nat44", func(t *testing.T) {
		_, service, _, network, _ := newVNFService(t)
		vnf := createLifecycleVNF(t, service, network.ID, store.VNFTypeNAT44, "nat", false)
		if _, err := service.UpdateNAT44VNF(ctx, &rookeryv1.UpdateNAT44VNFRequest{
			Id: vnf.ID, Gateway: strPtr("10.31.0.1/24"), OutsideInterface: strPtr("eth1"),
			OutsideAddress: strPtr("192.0.2.20/24"), OutsideGateway: strPtr("192.0.2.1"),
			Nat44Pool: strPtr("192.0.2.100-192.0.2.200"), Enabled: boolPtr(true),
		}); err != nil {
			t.Fatalf("update nat44: %v", err)
		}
		saved, err := service.store.GetNAT44VNF(ctx, vnf.ID)
		if err != nil {
			t.Fatalf("read nat44: %v", err)
		}
		if !saved.Enabled || saved.Config.Gateway != "10.31.0.1/24" ||
			saved.Config.OutsideInterface != "eth1" || saved.Config.OutsideAddress != "192.0.2.20/24" ||
			saved.Config.OutsideGateway != "192.0.2.1" {
			t.Fatalf("updated nat44 = %+v", saved)
		}
	})

	t.Run("bgp", func(t *testing.T) {
		_, service, _, network, _ := newVNFService(t)
		vnf := createLifecycleVNF(t, service, network.ID, store.VNFTypeBGP, "bgp", false)
		if _, err := service.UpdateBGPVNF(ctx, &rookeryv1.UpdateBGPVNFRequest{
			Id: vnf.ID, Transport: strPtr("tap"), LocalAs: u32Ptr(65010), RouterId: strPtr("10.40.0.9"),
			PeerAs: u32Ptr(65011), PeerIp: strPtr("10.40.0.10"), LocalIp: strPtr("10.40.0.9/24"),
			PeerGateway: strPtr("10.40.0.1"), MultihopTtl: u32Ptr(10), Password: strPtr("s3cret"),
			Passive: boolPtr(true), TargetTableId: u32Ptr(200), NextHop: strPtr("10.40.0.9"),
		}); err != nil {
			t.Fatalf("update bgp: %v", err)
		}
		saved, err := service.store.GetVNF(ctx, vnf.ID)
		if err != nil {
			t.Fatalf("read bgp: %v", err)
		}
		if saved.Config.Transport != "tap" || saved.Config.LocalAS != 65010 ||
			saved.Config.PeerAS != 65011 || saved.Config.RouterID != "10.40.0.9" ||
			saved.Config.PeerIP != "10.40.0.10" || saved.Config.LocalIP != "10.40.0.9/24" ||
			saved.Config.PeerGateway != "10.40.0.1" || saved.Config.MultihopTTL != 10 ||
			saved.Config.Password != "s3cret" || !saved.Config.Passive ||
			saved.Config.TargetTableID != 200 || saved.Config.NextHop != "10.40.0.9" {
			t.Fatalf("updated bgp = %+v", saved)
		}
	})
}

// TestDeleteVNFErrorMatrix covers delete stop failures and unknown IDs.
func TestDeleteVNFErrorMatrix(t *testing.T) {
	ctx := context.Background()
	for _, kind := range lifecycleVNFKinds {
		kind := kind
		deleteVNF := func(service *Service, id int64) error {
			switch kind {
			case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
				return service.DeleteDHCPVNF(ctx, &rookeryv1.DeleteDHCPVNFRequest{Id: id})
			case store.VNFTypeDNS:
				return service.DeleteDNSVNF(ctx, &rookeryv1.DeleteDNSVNFRequest{Id: id})
			case store.VNFTypeNAT44:
				return service.DeleteNAT44VNF(ctx, &rookeryv1.DeleteNAT44VNFRequest{Id: id})
			default:
				return service.DeleteBGPVNF(ctx, &rookeryv1.DeleteBGPVNFRequest{Id: id})
			}
		}
		t.Run(kind+"/missing", func(t *testing.T) {
			_, service, _, _, _ := newVNFService(t)
			if err := deleteVNF(service, 9999); err == nil {
				t.Fatal("delete of a missing VNF succeeded")
			} else {
				expectVNFStatus(t, err, http.StatusNotFound)
			}
		})
		t.Run(kind+"/stop failure", func(t *testing.T) {
			st, service, backend, network, _ := newVNFService(t)
			vnf := createLifecycleVNF(t, service, network.ID, kind, "vnf", false)
			setKindStopError(backend, kind)
			if err := deleteVNF(service, vnf.ID); err == nil {
				t.Fatal("delete succeeded despite a stop failure")
			} else {
				expectVNFStatus(t, err, http.StatusInternalServerError)
			}
			if _, err := st.GetVNF(ctx, vnf.ID); err != nil {
				t.Fatalf("VNF was removed despite the stop failure: %v", err)
			}
		})
	}
	t.Run("wrong type", func(t *testing.T) {
		_, service, _, network, _ := newVNFService(t)
		vnf := createLifecycleVNF(t, service, network.ID, store.VNFTypeDHCPv4, "dhcp", false)
		for _, call := range []func() error{
			func() error { return service.DeleteDNSVNF(ctx, &rookeryv1.DeleteDNSVNFRequest{Id: vnf.ID}) },
			func() error { return service.DeleteNAT44VNF(ctx, &rookeryv1.DeleteNAT44VNFRequest{Id: vnf.ID}) },
			func() error { return service.DeleteBGPVNF(ctx, &rookeryv1.DeleteBGPVNFRequest{Id: vnf.ID}) },
		} {
			if err := call(); err == nil {
				t.Fatal("typed delete accepted another VNF type")
			} else {
				expectVNFStatus(t, err, http.StatusNotFound)
			}
		}
	})
}

// TestRestartVNFAllTypes covers restart success; restartVNF's readiness, stop,
// and activation failures; and unknown IDs.
func TestRestartVNFAllTypes(t *testing.T) {
	ctx := context.Background()
	restartVNF := func(service *Service, kind string, id int64) error {
		switch kind {
		case store.VNFTypeDHCPv4, store.VNFTypeDHCPv6:
			_, err := service.RestartDHCPVNF(ctx, &rookeryv1.RestartDHCPVNFRequest{Id: id})
			return err
		case store.VNFTypeDNS:
			_, err := service.RestartDNSVNF(ctx, &rookeryv1.RestartDNSVNFRequest{Id: id})
			return err
		case store.VNFTypeNAT44:
			_, err := service.RestartNAT44VNF(ctx, &rookeryv1.RestartNAT44VNFRequest{Id: id})
			return err
		default:
			_, err := service.RestartBGPVNF(ctx, &rookeryv1.RestartBGPVNFRequest{Id: id})
			return err
		}
	}
	for _, kind := range lifecycleVNFKinds {
		kind := kind
		t.Run(kind+"/success", func(t *testing.T) {
			st, service, backend, network, _ := newVNFService(t)
			vnf := createLifecycleVNF(t, service, network.ID, kind, "vnf", false)
			if err := restartVNF(service, kind, vnf.ID); err != nil {
				t.Fatalf("restart: %v", err)
			}
			if backend.stopped != 1 || backend.activated != 1 {
				t.Fatalf("restart stopped=%d activated=%d", backend.stopped, backend.activated)
			}
			saved, err := st.GetVNF(ctx, vnf.ID)
			if err != nil || !saved.Enabled {
				t.Fatalf("restarted vnf = %+v, err = %v", saved, err)
			}
		})
		t.Run(kind+"/missing", func(t *testing.T) {
			_, service, _, _, _ := newVNFService(t)
			if err := restartVNF(service, kind, 9999); err == nil {
				t.Fatal("restart of a missing VNF succeeded")
			} else {
				expectVNFStatus(t, err, http.StatusNotFound)
			}
		})
		t.Run(kind+"/not ready", func(t *testing.T) {
			_, service, backend, network, _ := newVNFService(t)
			vnf := createLifecycleVNF(t, service, network.ID, kind, "vnf", false)
			backend.readyErr = errors.New("daemon unavailable")
			if err := restartVNF(service, kind, vnf.ID); err == nil {
				t.Fatal("restart succeeded while the backend was unavailable")
			} else {
				expectVNFStatus(t, err, http.StatusServiceUnavailable)
			}
		})
		t.Run(kind+"/stop failure", func(t *testing.T) {
			_, service, backend, network, _ := newVNFService(t)
			vnf := createLifecycleVNF(t, service, network.ID, kind, "vnf", false)
			setKindStopError(backend, kind)
			if err := restartVNF(service, kind, vnf.ID); err == nil {
				t.Fatal("restart succeeded despite a stop failure")
			} else {
				expectVNFStatus(t, err, http.StatusInternalServerError)
			}
		})
		t.Run(kind+"/activation failure", func(t *testing.T) {
			_, service, backend, network, _ := newVNFService(t)
			vnf := createLifecycleVNF(t, service, network.ID, kind, "vnf", false)
			backend.activateErr = errors.New("start failed")
			if err := restartVNF(service, kind, vnf.ID); err == nil {
				t.Fatal("restart succeeded despite an activation failure")
			} else {
				expectVNFStatus(t, err, http.StatusInternalServerError)
			}
		})
	}
}

// TestStoreErrorMapping covers every sentinel-to-HTTP mapping.
func TestStoreErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"name taken", store.ErrVNFNameTaken, http.StatusConflict},
		{"type taken", store.ErrVNFTypeTaken, http.StatusConflict},
		{"not found", store.ErrVNFNotFound, http.StatusNotFound},
		{"type mismatch", store.ErrVNFTypeMismatch, http.StatusNotFound},
		{"child taken", store.ErrVNFChildTaken, http.StatusConflict},
		{"child not found", store.ErrVNFChildNotFound, http.StatusNotFound},
		{"other", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectVNFStatus(t, storeError(tc.err), tc.want)
		})
	}
}

// TestVNFStoreHandleErrors covers the internal-error branches taken when the
// store itself fails.
func TestVNFStoreHandleErrors(t *testing.T) {
	ctx := context.Background()
	st, service, _, network, _ := newVNFService(t)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if err := service.validateDHCPDNSIntegration(ctx, network.ID, 123); err == nil {
		t.Fatal("DNS integration check succeeded on a closed store")
	} else if _, ok := err.(*VNFError); ok {
		t.Fatalf("DNS integration store error was wrapped as a VNFError: %v", err)
	}
	if err := service.DeleteDHCPVNF(ctx, &rookeryv1.DeleteDHCPVNFRequest{Id: 1}); err == nil {
		t.Fatal("delete succeeded on a closed store")
	} else {
		expectVNFStatus(t, err, http.StatusInternalServerError)
	}
}

// ptr helpers for the optional proto fields used above.
func i64Ptr(value int64) *int64   { return &value }
func u32Ptr(value uint32) *uint32 { return &value }
