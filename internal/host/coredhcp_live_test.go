package host

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"

	"github.com/voidstarit/rookery/internal/store"
)

// cleanupLivePlugin removes a live handler and lease runtime registered under a
// lease path so one test cannot leak into the next.
func cleanupLivePlugin(t *testing.T, leasePath string) {
	t.Helper()
	t.Cleanup(func() {
		liveDHCPv4.Delete(leasePath)
		liveDHCPv6.Delete(leasePath)
		unregisterLeaseRuntime(leasePath)
	})
}

func TestSetupCoreDHCPv4LiveRejectsBadArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no arguments"},
		{name: "missing setup arguments", args: []string{"key"}},
		{name: "invalid setup arguments", args: []string{"key", "not-enough"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := setupCoreDHCPv4Live(test.args...); err == nil {
				t.Fatalf("setupCoreDHCPv4Live(%q) succeeded, want an error", test.args)
			}
		})
	}
}

func TestSetupCoreDHCPv4LivePublishesHandler(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "leases.json")
	cleanupLivePlugin(t, leasePath)
	vnf := store.VNF{
		ID:   101,
		Type: store.VNFTypeDHCPv4,
		Config: store.VNFConfig{
			Gateway:    "10.30.0.1/24",
			RangeStart: "10.30.0.100",
			RangeEnd:   "10.30.0.110",
			LeaseTime:  "1h",
		},
	}
	cfg, err := buildDHCPv4Config(vnf, "lo", leasePath, 0)
	if err != nil {
		t.Fatalf("build DHCPv4 config: %v", err)
	}
	if cfg.Server4.Plugins[0].Name != coreDHCPv4LivePlugin {
		t.Fatalf("plugin = %q, want %q", cfg.Server4.Plugins[0].Name, coreDHCPv4LivePlugin)
	}
	handler, err := setupCoreDHCPv4Live(cfg.Server4.Plugins[0].Args...)
	if err != nil {
		t.Fatalf("setup live plugin: %v", err)
	}
	if _, ok := liveDHCPv4.Load(leasePath); !ok {
		t.Fatal("live handler was not published")
	}

	mac, err := net.ParseMAC("02:00:00:00:00:71")
	if err != nil {
		t.Fatal(err)
	}
	req, err := dhcpv4.FromBytes(testDHCPDiscover(t, mac, dhcpv4.TransactionID{0x71}))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := dhcpv4.NewReplyFromRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	offer, stop := handler(req, resp)
	if stop || offer == nil {
		t.Fatalf("proxy handler = %v, stop = %v", offer, stop)
	}
	if offer.YourIPAddr.String() != "10.30.0.100" && offer.YourIPAddr.String() != "10.30.0.110" {
		t.Fatalf("offered address = %s, want one from the range", offer.YourIPAddr)
	}

	// When the registry entry disappears the stable proxy drops the packet
	// instead of panicking, so the listener survives a failed republish.
	liveDHCPv4.Delete(leasePath)
	if out, stop := handler(req, resp); out != nil || !stop {
		t.Fatalf("unpublished proxy = %v, stop = %v; want nil, true", out, stop)
	}
}

func TestSetupCoreDHCPv6LiveRejectsBadArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no arguments"},
		{name: "missing setup arguments", args: []string{"key"}},
		{name: "invalid setup arguments", args: []string{"key", "not-a-prefix"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := setupCoreDHCPv6Live(test.args...); err == nil {
				t.Fatalf("setupCoreDHCPv6Live(%q) succeeded, want an error", test.args)
			}
		})
	}
}

func TestSetupCoreDHCPv6LivePublishesHandler(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "leases-v6.json")
	cleanupLivePlugin(t, leasePath)
	vnf := store.VNF{
		ID:   102,
		Type: store.VNFTypeDHCPv6,
		Config: store.VNFConfig{
			Gateway:    "fd00:30::1/64",
			RangeStart: "fd00:30::100",
			RangeEnd:   "fd00:30::110",
			LeaseTime:  "1h",
		},
	}
	cfg, err := buildDHCPv6Config(vnf, "memif", leasePath)
	if err != nil {
		t.Fatalf("build DHCPv6 config: %v", err)
	}
	args := cfg.Server6.Plugins[0].Args
	if len(args) < 2 || args[0] != leasePath {
		t.Fatalf("plugin args = %q, want the lease path first", args)
	}
	handler, err := setupCoreDHCPv6Live(args...)
	if err != nil {
		t.Fatalf("setup live plugin: %v", err)
	}
	if _, ok := liveDHCPv6.Load(leasePath); !ok {
		t.Fatal("live DHCPv6 handler was not published")
	}

	mac, err := net.ParseMAC("02:00:00:00:00:72")
	if err != nil {
		t.Fatal(err)
	}
	solicit, err := dhcpv6.NewSolicit(mac)
	if err != nil {
		t.Fatal(err)
	}
	advertise, err := dhcpv6.NewAdvertiseFromSolicit(solicit)
	if err != nil {
		t.Fatal(err)
	}
	reply, stop := handler(solicit, advertise)
	if stop || reply == nil {
		t.Fatalf("proxy handler = %v, stop = %v", reply, stop)
	}

	liveDHCPv6.Delete(leasePath)
	if out, stop := handler(solicit, advertise); out != nil || !stop {
		t.Fatalf("unpublished proxy = %v, stop = %v; want nil, true", out, stop)
	}
}

func TestNewDHCPv6HandlersLoadsPluginChain(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "leases-v6.json")
	cleanupLivePlugin(t, leasePath)
	vnf := store.VNF{
		ID:   103,
		Type: store.VNFTypeDHCPv6,
		Config: store.VNFConfig{
			Gateway:     "fd00:31::1/64",
			RangeStart:  "fd00:31::100",
			RangeEnd:    "fd00:31::110",
			LeaseTime:   "2h",
			DNS:         "2001:4860:4860::8888",
			DHCPOptions: []store.DHCPOption{{Code: 24, Format: "domain-list", Value: "lab.test"}},
		},
	}
	handlers, err := newDHCPv6Handlers(vnf, "memif", leasePath)
	if err != nil {
		t.Fatalf("newDHCPv6Handlers: %v", err)
	}
	if len(handlers) == 0 {
		t.Fatal("newDHCPv6Handlers returned no handlers")
	}
	if _, ok := liveDHCPv6.Load(leasePath); !ok {
		t.Fatal("newDHCPv6Handlers did not publish its live handler")
	}

	invalid := vnf
	invalid.Config.Gateway = "10.0.0.1/24"
	if _, err := newDHCPv6Handlers(invalid, "memif", filepath.Join(t.TempDir(), "other.json")); err == nil {
		t.Fatal("newDHCPv6Handlers accepted an IPv4 gateway")
	}

	// buildDHCPv6Config accepts an unvalidated option list; the plugin setup
	// rejects it, so the load error must surface from newDHCPv6Handlers.
	badOptions := vnf
	badOptions.ID = 104
	badOptions.Config.DHCPOptions = []store.DHCPOption{{Code: 26, Format: "bogus", Value: "1500"}}
	if _, err := newDHCPv6Handlers(badOptions, "memif", filepath.Join(t.TempDir(), "bad-options.json")); err == nil ||
		!strings.Contains(err.Error(), "load DHCPv6 plugin") {
		t.Fatalf("newDHCPv6Handlers with invalid options = %v", err)
	}
}

// TestLiveDHCPv4SwapWaitsForInFlight proves a handler reload cannot publish a
// new lease state while a packet is still being handled by the old one.
func TestLiveDHCPv4SwapWaitsForInFlight(t *testing.T) {
	key := "in-flight-v4"
	liveDHCPv4.Delete(key)
	t.Cleanup(func() { liveDHCPv4.Delete(key) })
	started := make(chan struct{})
	release := make(chan struct{})
	liveDHCPv4.Store(key, &liveDHCPHandler4{handler: func(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
		close(started)
		<-release
		return resp, true
	}})
	proxy := coreDHCPv4Proxy(key)
	done := make(chan struct{})
	go func() {
		proxy(nil, nil)
		close(done)
	}()
	<-started

	locked := make(chan struct{})
	go func() {
		unlock := lockLiveDHCPv4(key)
		close(locked)
		unlock()
	}()
	select {
	case <-locked:
		t.Fatal("the reload lock was acquired while a handler call was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("the reload lock was never acquired after the handler returned")
	}
	<-done
}

// TestLiveDHCPv6SwapWaitsForInFlight is the DHCPv6 equivalent of the v4 test.
func TestLiveDHCPv6SwapWaitsForInFlight(t *testing.T) {
	key := "in-flight-v6"
	liveDHCPv6.Delete(key)
	t.Cleanup(func() { liveDHCPv6.Delete(key) })
	started := make(chan struct{})
	release := make(chan struct{})
	liveDHCPv6.Store(key, &liveDHCPHandler6{handler: func(req, resp dhcpv6.DHCPv6) (dhcpv6.DHCPv6, bool) {
		close(started)
		<-release
		return resp, true
	}})
	proxy := coreDHCPv6Proxy(key)
	done := make(chan struct{})
	go func() {
		proxy(nil, nil)
		close(done)
	}()
	<-started

	locked := make(chan struct{})
	go func() {
		unlock := lockLiveDHCPv6(key)
		close(locked)
		unlock()
	}()
	select {
	case <-locked:
		t.Fatal("the reload lock was acquired while a handler call was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("the reload lock was never acquired after the handler returned")
	}
	<-done
}
