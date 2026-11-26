package vnfd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/store"
)

func TestValidRuntimeSpecs(t *testing.T) {
	if _, err := validDHCPRuntime(nil); err == nil {
		t.Fatal("validDHCPRuntime(nil) succeeded")
	}
	if _, err := validDHCPRuntime(&rookeryv1.DHCPRuntimeSpec{}); err == nil {
		t.Fatal("validDHCPRuntime without a runtime succeeded")
	}
	invalidTransport := &rookeryv1.DHCPRuntimeSpec{Runtime: testRuntime(1, "tap"), Family: "dhcpv4"}
	invalidTransport.Runtime.Transport = "bogus"
	if _, err := validDHCPRuntime(invalidTransport); err == nil ||
		!strings.Contains(err.Error(), "invalid DHCP runtime spec") {
		t.Fatalf("validDHCPRuntime with an invalid transport = %v", err)
	}
	v4 := &rookeryv1.DHCPRuntimeSpec{Runtime: testRuntime(1, "tap"), Family: "dhcpv4"}
	if _, err := validDHCPRuntime(v4); err == nil ||
		!strings.Contains(err.Error(), "DHCPv4 runtime config is required") {
		t.Fatalf("validDHCPRuntime without a DHCPv4 config = %v", err)
	}
	v6 := &rookeryv1.DHCPRuntimeSpec{Runtime: testRuntime(1, "tap"), Family: "dhcpv6"}
	if _, err := validDHCPRuntime(v6); err == nil ||
		!strings.Contains(err.Error(), "DHCPv6 runtime config is required") {
		t.Fatalf("validDHCPRuntime without a DHCPv6 config = %v", err)
	}
	if _, err := validDHCPRuntime(dhcpSpec(1, "dhcpv4", "tap")); err != nil {
		t.Fatalf("validDHCPRuntime(valid) = %v", err)
	}

	if _, err := validDNSRuntime(nil); err == nil {
		t.Fatal("validDNSRuntime(nil) succeeded")
	}
	dnsInvalid := dnsSpec(1, "shared")
	dnsInvalid.Runtime.Transport = "bogus"
	if _, err := validDNSRuntime(dnsInvalid); err == nil ||
		!strings.Contains(err.Error(), "invalid DNS runtime spec") {
		t.Fatalf("validDNSRuntime with an invalid transport = %v", err)
	}
	if _, err := validDNSRuntime(dnsSpec(1, "shared")); err != nil {
		t.Fatalf("validDNSRuntime(valid) = %v", err)
	}

	if _, err := validBGPRuntime(nil); err == nil {
		t.Fatal("validBGPRuntime(nil) succeeded")
	}
	bgpInvalid := bgpSpec(1, "tap")
	bgpInvalid.Runtime.Transport = "bogus"
	if _, err := validBGPRuntime(bgpInvalid); err == nil ||
		!strings.Contains(err.Error(), "invalid BGP runtime spec") {
		t.Fatalf("validBGPRuntime with an invalid transport = %v", err)
	}
	if _, err := validBGPRuntime(bgpSpec(1, "tap")); err != nil {
		t.Fatalf("validBGPRuntime(valid) = %v", err)
	}
}

func TestEnsureRejectsInvalidRequests(t *testing.T) {
	ctx := context.Background()
	server := NewServer(newFakeManager(), t.TempDir(), zerolog.Nop())

	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("EnsureDHCP without a spec = %v", err)
	}
	if _, err := server.EnsureDNS(ctx, &rookeryv1.EnsureDNSRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("EnsureDNS without a spec = %v", err)
	}
	if _, err := server.EnsureBGP(ctx, &rookeryv1.EnsureBGPRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("EnsureBGP without a spec = %v", err)
	}
	if _, err := server.ReloadDHCP(ctx, &rookeryv1.ReloadDHCPRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ReloadDHCP without a spec = %v", err)
	}
	if _, err := server.ReloadDNS(ctx, &rookeryv1.ReloadDNSRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ReloadDNS without a spec = %v", err)
	}
	if _, err := server.ReloadBGP(ctx, &rookeryv1.ReloadBGPRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ReloadBGP without a spec = %v", err)
	}
}

func TestStartDispatchValidation(t *testing.T) {
	ctx := context.Background()
	server := NewServer(newFakeManager(), t.TempDir(), zerolog.Nop())

	dhcpTests := []struct {
		name string
		spec *rookeryv1.DHCPRuntimeSpec
		want string
	}{
		{
			name: "missing DHCPv4 config",
			spec: &rookeryv1.DHCPRuntimeSpec{Runtime: testRuntime(1, "tap"), Family: "dhcpv4"},
			want: "DHCPv4 runtime config is required",
		},
		{
			name: "shared without socket",
			spec: func() *rookeryv1.DHCPRuntimeSpec {
				spec := dhcpSpec(1, "dhcpv4", "shared")
				spec.Runtime.MemifSocket = ""
				return spec
			}(),
			want: "shared memif socket path is required",
		},
		{
			name: "shared without address",
			spec: func() *rookeryv1.DHCPRuntimeSpec {
				spec := dhcpSpec(1, "dhcpv4", "shared")
				spec.Runtime.SharedAddress = ""
				return spec
			}(),
			want: "shared memif address is required",
		},
		{
			name: "memif without socket",
			spec: func() *rookeryv1.DHCPRuntimeSpec {
				spec := dhcpSpec(1, "dhcpv4", "memif")
				spec.Runtime.MemifSocket = ""
				return spec
			}(),
			want: "memif socket path is required",
		},
		{
			name: "tap without interface",
			spec: func() *rookeryv1.DHCPRuntimeSpec {
				spec := dhcpSpec(1, "dhcpv4", "tap")
				spec.Runtime.TapHostName = ""
				return spec
			}(),
			want: "tap interface name is required",
		},
		{
			name: "unsupported transport",
			spec: func() *rookeryv1.DHCPRuntimeSpec {
				spec := dhcpSpec(1, "dhcpv4", "tap")
				spec.Runtime.Transport = "vlan"
				return spec
			}(),
			want: "unsupported DHCP transport",
		},
	}
	for _, test := range dhcpTests {
		t.Run("dhcp/"+test.name, func(t *testing.T) {
			err := server.startDHCP(ctx, test.spec)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("startDHCP() error = %v, want %q", err, test.want)
			}
		})
	}

	dnsTests := []struct {
		name string
		spec *rookeryv1.DNSRuntimeSpec
		want string
	}{
		{
			name: "missing config",
			spec: &rookeryv1.DNSRuntimeSpec{Runtime: testRuntime(2, "shared")},
			want: "DNS runtime config is required",
		},
		{
			name: "shared without socket",
			spec: func() *rookeryv1.DNSRuntimeSpec {
				spec := dnsSpec(2, "shared")
				spec.Runtime.MemifSocket = ""
				return spec
			}(),
			want: "shared memif socket path is required",
		},
		{
			name: "shared without address",
			spec: func() *rookeryv1.DNSRuntimeSpec {
				spec := dnsSpec(2, "shared")
				spec.Runtime.SharedAddress = ""
				return spec
			}(),
			want: "shared memif address is required",
		},
	}
	for _, test := range dnsTests {
		t.Run("dns/"+test.name, func(t *testing.T) {
			err := server.startDNS(ctx, test.spec)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("startDNS() error = %v, want %q", err, test.want)
			}
		})
	}

	bgpTests := []struct {
		name string
		spec *rookeryv1.BGPRuntimeSpec
		want string
	}{
		{
			name: "missing config",
			spec: &rookeryv1.BGPRuntimeSpec{Runtime: testRuntime(3, "memif")},
			want: "BGP runtime config is required",
		},
		{
			name: "memif without socket",
			spec: func() *rookeryv1.BGPRuntimeSpec {
				spec := bgpSpec(3, "memif")
				spec.Runtime.MemifSocket = ""
				return spec
			}(),
			want: "memif socket path is required",
		},
		{
			name: "tap without interface",
			spec: func() *rookeryv1.BGPRuntimeSpec {
				spec := bgpSpec(3, "tap")
				spec.Runtime.TapHostName = ""
				return spec
			}(),
			want: "tap interface name is required",
		},
		{
			name: "unsupported transport",
			spec: func() *rookeryv1.BGPRuntimeSpec {
				spec := bgpSpec(3, "shared")
				return spec
			}(),
			want: "unsupported BGP transport",
		},
	}
	for _, test := range bgpTests {
		t.Run("bgp/"+test.name, func(t *testing.T) {
			err := server.startBGP(ctx, test.spec)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("startBGP() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestStoreVNFConversionHelpers(t *testing.T) {
	if _, err := storeVNFFromDHCP(&rookeryv1.DHCPRuntimeSpec{Runtime: testRuntime(1, "tap"), Family: "dhcpv4"}); err == nil ||
		!strings.Contains(err.Error(), "DHCPv4 runtime config is required") {
		t.Fatalf("storeVNFFromDHCP without a DHCPv4 config = %v", err)
	}
	if _, err := storeVNFFromDHCP(&rookeryv1.DHCPRuntimeSpec{Runtime: testRuntime(1, "tap"), Family: "dhcpv6"}); err == nil ||
		!strings.Contains(err.Error(), "DHCPv6 runtime config is required") {
		t.Fatalf("storeVNFFromDHCP without a DHCPv6 config = %v", err)
	}
	if _, err := storeVNFFromDHCP(&rookeryv1.DHCPRuntimeSpec{Runtime: testRuntime(1, "tap"), Family: "bogus"}); err == nil ||
		!strings.Contains(err.Error(), "unsupported DHCP family") {
		t.Fatalf("storeVNFFromDHCP with an unsupported family = %v", err)
	}
	if _, err := storeVNFFromDNS(&rookeryv1.DNSRuntimeSpec{Runtime: testRuntime(1, "shared")}); err == nil ||
		!strings.Contains(err.Error(), "DNS runtime config is required") {
		t.Fatalf("storeVNFFromDNS without a config = %v", err)
	}
	if _, err := storeVNFFromBGP(&rookeryv1.BGPRuntimeSpec{Runtime: testRuntime(1, "memif")}); err == nil ||
		!strings.Contains(err.Error(), "BGP runtime config is required") {
		t.Fatalf("storeVNFFromBGP without a config = %v", err)
	}

	dns, err := storeVNFFromDNS(dnsSpec(1, "shared"))
	if err != nil {
		t.Fatalf("storeVNFFromDNS(valid) = %v", err)
	}
	dnsSpecValue := &rookeryv1.DNSRuntimeSpec{
		Runtime: testRuntime(1, "shared"),
		Config: &rookeryv1.DNSRuntimeConfig{
			Forward:    true,
			Records:    []*rookeryv1.DNSRecordSpec{{Name: "vm1.lab1.test.", Type: "A", Value: "10.20.0.50", Ttl: 60}},
			Forwarders: []*rookeryv1.DNSForwarderSpec{{Address: "1.1.1.1"}, {Address: "8.8.8.8"}},
		},
	}
	dns, err = storeVNFFromDNS(dnsSpecValue)
	if err != nil {
		t.Fatalf("storeVNFFromDNS(forwarders) = %v", err)
	}
	if len(dns.Config.DNSForwarders) != 2 || dns.Config.DNSForwarders[1] != "8.8.8.8" {
		t.Fatalf("forwarders = %+v", dns.Config.DNSForwarders)
	}

	assignments := staticAssignmentsFromProto([]*rookeryv1.DHCPStaticAssignmentSpec{{Mac: "02:00:00:00:00:01", Ip: "10.20.0.50"}})
	if len(assignments) != 1 || assignments[0].MAC != "02:00:00:00:00:01" {
		t.Fatalf("static assignments = %+v", assignments)
	}
	options := dhcpOptionsFromProto([]*rookeryv1.DHCPOptionSpec{{Code: 26, Format: "int", Value: "1500"}})
	if len(options) != 1 || options[0].Code != 26 {
		t.Fatalf("options = %+v", options)
	}
}

func TestPersistRejectsRuntimeWithoutID(t *testing.T) {
	server := NewServer(newFakeManager(), t.TempDir(), zerolog.Nop())
	if err := server.persist(&rookeryv1.DesiredRuntime{}); err == nil {
		t.Fatal("persist accepted a runtime without an id")
	}
	if specHash(nil) != "" {
		t.Fatal("specHash(nil) is not empty")
	}
}

func TestDesiredRuntimeHelpers(t *testing.T) {
	empty := &rookeryv1.DesiredRuntime{}
	if desiredID(nil) != 0 || desiredID(empty) != 0 || desiredID(wrapDHCP(&rookeryv1.DHCPRuntimeSpec{})) != 0 {
		t.Fatal("desiredID did not return zero")
	}
	if desiredKind(nil) != "" || desiredKind(empty) != "" {
		t.Fatal("desiredKind did not return an empty kind")
	}
	if desiredTransport(nil) != "" || desiredTransport(empty) != "" {
		t.Fatal("desiredTransport did not return an empty transport")
	}
	if desiredMessage(nil) != nil || desiredMessage(empty) != nil {
		t.Fatal("desiredMessage did not return nil")
	}
	if desiredSpec(nil) != nil {
		t.Fatal("desiredSpec(nil) is not nil")
	}
	spec := wrapDNS(dnsSpec(1, "shared"))
	if desiredSpec(&desiredRuntime{spec: spec}) != spec {
		t.Fatal("desiredSpec did not return the wrapper")
	}
	if desiredKind(spec) != runtimeDNS || desiredTransport(spec) != "shared" {
		t.Fatal("DNS wrapper helpers returned the wrong kind or transport")
	}
}

func TestMergeRuntimeHelpers(t *testing.T) {
	dhcpRuntime := testRuntime(1, "tap")
	dnsRuntime := testRuntime(2, "shared")
	bgpRuntime := testRuntime(3, "memif")
	dhcp := &rookeryv1.DHCPRuntimeSpec{Runtime: dhcpRuntime, Family: "dhcpv4", Config: &rookeryv1.DHCPRuntimeSpec_Dhcpv4{Dhcpv4: &rookeryv1.DHCPV4RuntimeConfig{Gateway: "10.20.0.1/24"}}}
	dns := &rookeryv1.DNSRuntimeSpec{Runtime: dnsRuntime, Config: &rookeryv1.DNSRuntimeConfig{}}
	bgp := &rookeryv1.BGPRuntimeSpec{Runtime: bgpRuntime, Config: &rookeryv1.BGPRuntimeConfig{}}

	if mergeDHCP(nil, dhcp) != dhcp || mergeDHCP(wrapDHCP(&rookeryv1.DHCPRuntimeSpec{}), dhcp) != dhcp {
		t.Fatal("mergeDHCP without a current runtime did not return the proposal")
	}
	if mergeDNS(nil, dns) != dns || mergeDNS(&rookeryv1.DesiredRuntime{}, dns) != dns {
		t.Fatal("mergeDNS without a current runtime did not return the proposal")
	}
	if mergeBGP(nil, bgp) != bgp || mergeBGP(wrapBGP(&rookeryv1.BGPRuntimeSpec{}), bgp) != bgp {
		t.Fatal("mergeBGP without a current runtime did not return the proposal")
	}

	merged := mergeRuntimeSpec(dhcpRuntime, nil)
	if merged.GetTransport() != "tap" || merged.GetTapHostName() != dhcpRuntime.GetTapHostName() {
		t.Fatalf("mergeRuntimeSpec(nil incoming) = %+v", merged)
	}
}

func TestLoadIgnoresUnreadableAndInvalidSpecs(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "desired-5.json")); err != nil {
		t.Fatal(err)
	}
	invalid := []byte(`{"dhcp":{"runtime":{"id":6,"transport":"bogus"},"family":"dhcpv4"}}`)
	if err := os.WriteFile(filepath.Join(dir, "desired-6.json"), invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServer(newFakeManager(), dir, zerolog.Nop())
	if err := server.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	server.mu.Lock()
	restored := len(server.desired)
	server.mu.Unlock()
	if restored != 0 {
		t.Fatalf("Load restored %d invalid runtimes", restored)
	}
}

func TestLoadRestoresBGPRuntime(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	server := NewServer(newFakeManager(), dir, zerolog.Nop())
	if _, err := server.EnsureBGP(ctx, &rookeryv1.EnsureBGPRequest{Spec: bgpSpec(7, "tap")}); err != nil {
		t.Fatal(err)
	}

	restartedManager := newFakeManager()
	restarted := NewServer(restartedManager, dir, zerolog.Nop())
	if err := restarted.Load(); err != nil {
		t.Fatal(err)
	}
	if state, err := restarted.GetBGPState(ctx, &rookeryv1.GetBGPStateRequest{Id: 7}); err != nil || state.GetRunning() {
		t.Fatalf("restored BGP state = %+v, err = %v", state, err)
	}
	if len(restartedManager.starts) != 0 {
		t.Fatalf("Load started a runtime: %+v", restartedManager.starts)
	}
}

func TestEnsureAndReloadPersistFailuresAreLogged(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager, filepath.Join(blocker, "vnfs"), zerolog.Nop())

	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatalf("ensure with an unwritable state directory: %v", err)
	}
	changed := dhcpSpec(1, "dhcpv4", "tap")
	changed.GetDhcpv4().RangeEnd = "10.20.0.30"
	if _, err := server.ReloadDHCP(ctx, &rookeryv1.ReloadDHCPRequest{Spec: changed}); err != nil {
		t.Fatalf("reload with an unwritable state directory: %v", err)
	}
	server.mu.Lock()
	hash := server.desired[1].hash
	server.mu.Unlock()
	if hash != specHash(desiredMessage(wrapDHCP(changed))) {
		t.Fatal("reload did not update the in-memory desired spec")
	}
}

func TestEnsureStopFailureRecordsDetail(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	manager.removeErr = errors.New("cannot stop")
	changed := dhcpSpec(1, "dhcpv4", "tap")
	changed.GetDhcpv4().RangeEnd = "10.20.0.30"
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: changed}); status.Code(err) != codes.Internal {
		t.Fatalf("ensure after a stop failure = %v", err)
	}
	server.mu.Lock()
	detail := server.details[1]
	server.mu.Unlock()
	if !strings.Contains(detail, "cannot stop") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestRemoveBranches(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())

	if _, err := server.RemoveDHCP(ctx, &rookeryv1.RemoveDHCPRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("remove without an id = %v", err)
	}
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	manager.removeErr = errors.New("cannot stop")
	if _, err := server.RemoveDHCP(ctx, &rookeryv1.RemoveDHCPRequest{Id: 1}); status.Code(err) != codes.Internal {
		t.Fatalf("remove after a stop failure = %v", err)
	}
	manager.removeErr = nil
	if _, err := server.RemoveDNS(ctx, &rookeryv1.RemoveDNSRequest{Id: 1}); status.Code(err) != codes.NotFound {
		t.Fatalf("remove of a DHCP runtime through the DNS service = %v", err)
	}
	if _, err := server.RemoveDHCPLeases(ctx, &rookeryv1.RemoveDHCPLeasesRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("lease removal without an id = %v", err)
	}
}

func TestResetRuntimeFailure(t *testing.T) {
	manager := newFakeManager()
	manager.resetErr = errors.New("boom")
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	if _, err := server.ResetRuntime(context.Background(), &rookeryv1.ResetRuntimeRequest{}); status.Code(err) != codes.Internal {
		t.Fatalf("ResetRuntime with a failing manager = %v", err)
	}
}

func TestCleanupLegacyNetworkDelegates(t *testing.T) {
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	if _, err := server.CleanupLegacyNetwork(context.Background(), &rookeryv1.CleanupLegacyNetworkRequest{NetworkId: 42}); err != nil {
		t.Fatalf("CleanupLegacyNetwork: %v", err)
	}
	if len(manager.cleaned) != 1 || manager.cleaned[0] != 42 {
		t.Fatalf("cleaned networks = %+v", manager.cleaned)
	}
}

func TestReloadUnknownOrWrongType(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	if _, err := server.ReloadDHCP(ctx, &rookeryv1.ReloadDHCPRequest{Spec: dhcpSpec(9, "dhcpv4", "tap")}); status.Code(err) != codes.NotFound {
		t.Fatalf("reload of an unknown VNF = %v", err)
	}
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ReloadDNS(ctx, &rookeryv1.ReloadDNSRequest{Spec: dnsSpec(1, "shared")}); status.Code(err) != codes.NotFound {
		t.Fatalf("DNS reload of a DHCP VNF = %v", err)
	}
}

func TestRestartBranches(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())

	// A desired runtime that is already gone is a no-op.
	if err := server.restart(ctx, 99, &desiredRuntime{kind: runtimeDHCP}); err != nil {
		t.Fatalf("restart without a desired runtime = %v", err)
	}

	dhcpWrapped := wrapDHCP(dhcpSpec(1, "dhcpv4", "tap"))
	dhcpDesired := &desiredRuntime{kind: runtimeDHCP, hash: specHash(desiredMessage(dhcpWrapped)), spec: dhcpWrapped}
	server.mu.Lock()
	server.desired[1] = dhcpDesired
	server.mu.Unlock()

	// A healthy running runtime is left alone.
	manager.running[1] = true
	if err := server.restart(ctx, 1, dhcpDesired); err != nil {
		t.Fatalf("restart of a healthy runtime = %v", err)
	}

	// A half-dead runtime is stopped; a stop failure is reported.
	manager.healthy = false
	manager.removeErr = errors.New("cannot stop")
	if err := server.restart(ctx, 1, dhcpDesired); err == nil ||
		!strings.Contains(err.Error(), "cannot stop") {
		t.Fatalf("restart with a stop failure = %v", err)
	}

	// A start failure is recorded in the detail.
	manager.removeErr = nil
	manager.running[1] = false
	manager.startErr = errors.New("cannot start")
	if err := server.restart(ctx, 1, dhcpDesired); err == nil {
		t.Fatal("restart succeeded despite a start failure")
	}
	server.mu.Lock()
	startDetail := server.details[1]
	server.mu.Unlock()
	if !strings.Contains(startDetail, "cannot start") {
		t.Fatalf("detail after a start failure = %q", startDetail)
	}

	// BGP and DNS desired runtimes dispatch to their start paths.
	manager.startErr = nil
	bgpWrapped := wrapBGP(bgpSpec(2, "tap"))
	bgpDesired := &desiredRuntime{kind: runtimeBGP, hash: specHash(desiredMessage(bgpWrapped)), spec: bgpWrapped}
	server.mu.Lock()
	server.desired[2] = bgpDesired
	server.mu.Unlock()
	if err := server.restart(ctx, 2, bgpDesired); err != nil {
		t.Fatalf("restart BGP: %v", err)
	}
	if !manager.running[2] {
		t.Fatal("restart did not start the BGP runtime")
	}

	// An unknown desired kind is rejected.
	unknown := &desiredRuntime{kind: "bogus"}
	server.mu.Lock()
	server.desired[3] = unknown
	server.mu.Unlock()
	if err := server.restart(ctx, 3, unknown); err == nil ||
		!strings.Contains(err.Error(), "unsupported runtime type") {
		t.Fatalf("restart of an unknown runtime kind = %v", err)
	}
}

func TestSuperviseFailureRecordsDetail(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	server.restartBackoff = 0
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	delete(manager.running, 1)
	manager.startErr = errors.New("supervision start failed")
	server.superviseOnce(ctx)
	server.mu.Lock()
	detail := server.details[1]
	server.mu.Unlock()
	if !strings.Contains(detail, "supervision start failed") {
		t.Fatalf("detail after a failed supervised restart = %q", detail)
	}
}

func TestReloadPrepareFailure(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	_, err := server.reload(ctx, 1, runtimeDHCP, func(*rookeryv1.DesiredRuntime) (store.VNF, *rookeryv1.DesiredRuntime, error) {
		return store.VNF{}, nil, errors.New("prepare failed")
	})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "prepare failed") {
		t.Fatalf("reload prepare failure = %v", err)
	}
}

// TestRemoveUnknownRuntimeIsIdempotent proves delete/restart of a disabled or
// never-started VNF no longer fails just because the daemon has no desired spec.
func TestRemoveUnknownRuntimeIsIdempotent(t *testing.T) {
	ctx := context.Background()
	manager := newFakeManager()
	server := NewServer(manager, t.TempDir(), zerolog.Nop())
	if _, err := server.RemoveDHCP(ctx, &rookeryv1.RemoveDHCPRequest{Id: 99}); err != nil {
		t.Fatalf("remove of an unstarted VNF: %v", err)
	}
	if _, err := server.EnsureDHCP(ctx, &rookeryv1.EnsureDHCPRequest{Spec: dhcpSpec(1, "dhcpv4", "tap")}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.RemoveDNS(ctx, &rookeryv1.RemoveDNSRequest{Id: 1}); status.Code(err) != codes.NotFound {
		t.Fatalf("remove through the wrong service = %v", err)
	}
}
