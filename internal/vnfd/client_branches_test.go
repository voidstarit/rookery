package vnfd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/store"
)

func TestClientEnsureErrorsPerType(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	manager.startErr = errors.New("runtime unavailable")
	client := startTestDaemon(t, manager, zerolog.Nop())

	calls := []struct {
		name string
		run  func() error
	}{
		{name: "DHCP tap", run: func() error { return client.Start(ctx, dhcpVNF(1), "vnf1") }},
		{name: "DHCP shared", run: func() error {
			return client.StartShared(ctx, dhcpVNF(1), "/run/rookery/memif/net-7.sock", 1, "10.20.0.2/24", "fd00:20::2/64")
		}},
		{name: "DHCP memif", run: func() error {
			return client.StartMemif(ctx, dhcpVNF(1), "/run/rookery/memif/1.sock", 1)
		}},
		{name: "DNS shared", run: func() error {
			return client.StartShared(ctx, dnsVNF(2), "/run/rookery/memif/net-7.sock", 1, "10.20.0.2/24", "fd00:20::2/64")
		}},
		{name: "BGP memif", run: func() error {
			return client.StartMemif(ctx, bgpVNF(3), "/run/rookery/memif/3.sock", 3)
		}},
		{name: "BGP tap", run: func() error { return client.Start(ctx, bgpVNF(3), "vnf3") }},
	}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			err := call.run()
			if err == nil || !strings.HasPrefix(err.Error(), "start VNF: ") ||
				!strings.Contains(err.Error(), "runtime unavailable") {
				t.Fatalf("ensure error = %v", err)
			}
		})
	}
}

func TestClientStopErrorsAndUnsupportedType(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	manager.removeErr = errors.New("runtime busy")
	client := startTestDaemon(t, manager, zerolog.Nop())

	err := client.Stop(ctx, dhcpVNF(1), false)
	if err == nil || !strings.HasPrefix(err.Error(), "stop VNF: ") || !strings.Contains(err.Error(), "runtime busy") {
		t.Fatalf("stop error = %v", err)
	}
	err = client.Stop(ctx, store.VNF{ID: 1, Type: "bogus"}, false)
	if err == nil || !strings.HasPrefix(err.Error(), "stop VNF: ") || !strings.Contains(err.Error(), "not served by the daemon") {
		t.Fatalf("stop of an unsupported type = %v", err)
	}
}

func TestClientReloadOfUnsupportedType(t *testing.T) {
	client := startTestDaemon(t, newFakeManager(), zerolog.Nop())
	err := client.ReloadVNF(context.Background(), store.VNF{ID: 1, Type: "bogus"})
	if err == nil || !strings.HasPrefix(err.Error(), "reload VNF: ") || !strings.Contains(err.Error(), "not served by the daemon") {
		t.Fatalf("reload of an unsupported type = %v", err)
	}
}

func TestClientStateForUnsupportedType(t *testing.T) {
	client := startTestDaemon(t, newFakeManager(), zerolog.Nop())
	if _, err := client.state(context.Background(), store.VNF{ID: 1, Type: store.VNFTypeNAT44}); err == nil ||
		!strings.Contains(err.Error(), "not served by the daemon") {
		t.Fatalf("state for an unsupported type = %v", err)
	}
}

func TestClientAdminRPCFailures(t *testing.T) {
	client, err := NewClient("tcp://127.0.0.1:1", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)

	// CleanupLegacyNetwork only logs a warning; the call must not panic or block.
	client.CleanupLegacyNetwork(7)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.ResetRuntime(ctx); err == nil ||
		!strings.HasPrefix(err.Error(), "reset VNF runtime: ") {
		t.Fatalf("ResetRuntime without a daemon = %v", err)
	}
}

func TestDHCPRuntimeSpecConversion(t *testing.T) {
	v6 := store.VNF{
		ID: 5, Name: "dhcp6", NetworkID: 2, Type: store.VNFTypeDHCPv6,
		Config: store.VNFConfig{
			Gateway:    "fd00:20::1/64",
			RangeStart: "fd00:20::100",
			RangeEnd:   "fd00:20::110",
			LeaseTime:  "12h",
			DNS:        "2001:4860:4860::8888",
			BindIP:     "fd00:20::2/64",
			DNSVNFID:   9,
			DNSDomain:  "lab.test",
			DNSTTL:     300,
			DHCPOptions: []store.DHCPOption{
				{Code: 24, Format: "domain-list", Value: "lab.test"},
			},
		},
	}
	spec := dhcpRuntimeSpec(v6, runtimeSpecFor(v6, "", "", "", 0, "", ""))
	if spec.GetFamily() != store.VNFTypeDHCPv6 || spec.GetDhcpv6() == nil {
		t.Fatalf("DHCPv6 spec = %+v", spec)
	}
	cfg6 := spec.GetDhcpv6()
	if cfg6.GetGateway() != v6.Config.Gateway || cfg6.GetDnsVnfId() != 9 || cfg6.GetDnsTtl() != 300 {
		t.Fatalf("DHCPv6 config = %+v", cfg6)
	}
	if len(cfg6.GetOptions()) != 1 || cfg6.GetOptions()[0].GetCode() != 24 {
		t.Fatalf("DHCPv6 options = %+v", cfg6.GetOptions())
	}

	v4 := store.VNF{
		ID: 4, Name: "dhcp4", NetworkID: 2, Type: store.VNFTypeDHCPv4,
		Config: store.VNFConfig{
			Gateway:    "10.20.0.1/24",
			RangeStart: "10.20.0.100",
			RangeEnd:   "10.20.0.110",
			StaticAssignments: []store.DHCPStaticAssignment{
				{MAC: "02:00:00:00:00:01", IP: "10.20.0.50"},
				{MAC: "02:00:00:00:00:02", IP: "10.20.0.51"},
			},
			DHCPOptions: []store.DHCPOption{{Code: 26, Format: "int", Value: "1500"}},
		},
	}
	spec4 := dhcpRuntimeSpec(v4, runtimeSpecFor(v4, "", "", "", 0, "", ""))
	if spec4.GetFamily() != store.VNFTypeDHCPv4 || spec4.GetDhcpv4() == nil {
		t.Fatalf("DHCPv4 spec = %+v", spec4)
	}
	if len(spec4.GetDhcpv4().GetStaticAssignments()) != 2 || len(spec4.GetDhcpv4().GetOptions()) != 1 {
		t.Fatalf("DHCPv4 config = %+v", spec4.GetDhcpv4())
	}
}

func TestDNSRuntimeSpecConversion(t *testing.T) {
	vnf := store.VNF{
		ID: 6, Name: "dns", NetworkID: 2, Type: store.VNFTypeDNS,
		Config: store.VNFConfig{
			DNSForward:    true,
			DNSRecords:    []store.DNSRecord{{Name: "vm1.lab1.test.", Type: "A", Value: "10.20.0.50", TTL: 60}},
			DNSForwarders: []string{"1.1.1.1", "8.8.8.8"},
		},
	}
	spec := dnsRuntimeSpec(vnf, runtimeSpecFor(vnf, "shared", "", "/run/rookery/memif/net-2.sock", 2, "10.20.0.2/24", "fd00:20::2/64"))
	if spec.GetConfig().GetForward() != true || len(spec.GetConfig().GetRecords()) != 1 {
		t.Fatalf("DNS spec = %+v", spec)
	}
	forwarders := spec.GetConfig().GetForwarders()
	if len(forwarders) != 2 || forwarders[0].GetAddress() != "1.1.1.1" ||
		forwarders[1].GetAddress() != "8.8.8.8" || forwarders[1].GetPosition() != 1 {
		t.Fatalf("DNS forwarders = %+v", forwarders)
	}
}
