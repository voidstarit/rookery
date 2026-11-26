package host

import (
	"context"
	"net"
	"strings"
	"testing"

	api "github.com/osrg/gobgp/v3/api"

	"github.com/voidstarit/rookery/internal/store"
)

func mustCIDR(t *testing.T, value string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return network
}

// startBGPTestInstance starts a loopback-only GoBGP server for reload tests.
func startBGPTestInstance(t *testing.T, port int32, advertised ...*net.IPNet) *bgpInstance {
	t.Helper()
	instance, err := startBGPServer(context.Background(), bgpStartConfig{
		VNF:     store.VNF{ID: 41, Type: store.VNFTypeBGP, Config: store.VNFConfig{Transport: "tap"}},
		LocalIP: net.ParseIP("127.0.0.1").To4(),
		PeerIP:  net.ParseIP("127.0.0.2").To4(),
		LocalAS: 64512, PeerAS: 64513, RouterID: "10.0.0.1",
		Passive: true, Neighbor: "127.0.0.2", LocalAddress: "127.0.0.1",
		ListenAddress: []string{"127.0.0.1"}, ListenPort: port,
		Advertise: advertised,
	})
	if err != nil {
		t.Fatalf("start BGP server: %v", err)
	}
	t.Cleanup(func() { instance.Close(context.Background()) })
	return instance
}

func TestBGPWithdrawUnknownPathFails(t *testing.T) {
	instance := startBGPTestInstance(t, 11831)
	if err := instance.withdrawPaths(context.Background(), [][]byte{{0x01}}); err == nil ||
		!strings.Contains(err.Error(), "withdraw advertised BGP prefix") {
		t.Fatalf("withdrawPaths for an unknown path = %v", err)
	}
}

func TestBGPReloadReportsWithdrawFailure(t *testing.T) {
	initial := mustCIDR(t, "10.95.0.0/24")
	instance := startBGPTestInstance(t, 11832, initial)

	// A path UUID that the running server does not know makes the withdraw
	// step fail before the proposed set is advertised.
	instance.advertisedMu.Lock()
	instance.advertisedIDs = [][]byte{{0x01}}
	instance.advertisedMu.Unlock()

	err := instance.reload(context.Background(), store.VNF{
		ID:     41,
		Type:   store.VNFTypeBGP,
		Config: store.VNFConfig{LocalIP: "127.0.0.1/32"},
	})
	if err == nil || !strings.Contains(err.Error(), "withdraw advertised BGP prefix") {
		t.Fatalf("reload withdraw failure = %v", err)
	}
}

func TestBGPReloadConfigValidation(t *testing.T) {
	v4 := net.ParseIP("10.40.0.1").To4()
	v6 := net.ParseIP("fd00:40::1")
	tests := []struct {
		name        string
		localIP     net.IP
		vnf         store.VNF
		wantErr     string
		wantNextHop string
	}{
		{
			name:    "wrong VNF type",
			localIP: v4,
			vnf:     store.VNF{Type: store.VNFTypeDHCPv4},
			wantErr: "only supports BGP",
		},
		{
			name:    "missing local address",
			vnf:     store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{LocalIP: "10.40.0.1/24"}},
			wantErr: "no local address",
		},
		{
			name:    "invalid local address",
			localIP: v4,
			vnf:     store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{LocalIP: "not-a-cidr"}},
			wantErr: "local_ip must be",
		},
		{
			name:    "changed local address",
			localIP: v4,
			vnf:     store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{LocalIP: "10.40.0.2/24"}},
			wantErr: "local_ip changed",
		},
		{
			name:    "changed address family",
			localIP: v4,
			vnf:     store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{LocalIP: "fd00:40::1/64"}},
			wantErr: "local_ip changed",
		},
		{
			name:    "invalid next hop",
			localIP: v4,
			vnf: store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{
				LocalIP: "10.40.0.1/24", NextHop: "not-an-ip",
			}},
			wantErr: "next_hop must be",
		},
		{
			name:    "next hop family mismatch",
			localIP: v4,
			vnf: store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{
				LocalIP: "10.40.0.1/24", NextHop: "fd00:40::2",
			}},
			wantErr: "next_hop must match",
		},
		{
			name:    "invalid advertised prefix",
			localIP: v4,
			vnf: store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{
				LocalIP: "10.40.0.1/24", AdvertisePrefixes: []string{"not-a-prefix"},
			}},
			wantErr: "invalid BGP prefix",
		},
		{
			name:    "advertised prefix family mismatch",
			localIP: v4,
			vnf: store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{
				LocalIP: "10.40.0.1/24", AdvertisePrefixes: []string{"fd00:99::/64"},
			}},
			wantErr: "must match the local address family",
		},
		{
			name:        "IPv4 next hop",
			localIP:     v4,
			vnf:         store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{LocalIP: "10.40.0.1/24", NextHop: "10.40.0.254"}},
			wantNextHop: "10.40.0.254",
		},
		{
			name:        "IPv6 next hop and prefixes",
			localIP:     v6,
			vnf:         store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{LocalIP: "fd00:40::1/64", NextHop: "fd00:40::2", AdvertisePrefixes: []string{"fd00:99::/64"}}},
			wantNextHop: "fd00:40::2",
		},
		{
			name:        "default IPv4 next hop",
			localIP:     v4,
			vnf:         store.VNF{Type: store.VNFTypeBGP, Config: store.VNFConfig{LocalIP: "10.40.0.1/24", AdvertisePrefixes: []string{"10.99.0.0/24"}}},
			wantNextHop: "10.40.0.1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := &bgpInstance{localIP: test.localIP}
			prefixes, nextHop, err := instance.reloadConfig(test.vnf)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("reloadConfig() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("reloadConfig() error = %v", err)
			}
			if nextHop.String() != test.wantNextHop {
				t.Fatalf("reloadConfig() next hop = %v, want %s", nextHop, test.wantNextHop)
			}
			if len(prefixes) != len(test.vnf.Config.AdvertisePrefixes) {
				t.Fatalf("reloadConfig() prefixes = %v", prefixes)
			}
		})
	}
}

// stopBGPTestServer makes the GoBGP management loop reject new advertisements
// while keeping the in-process server (and its API goroutine) alive.
func stopBGPTestServer(t *testing.T, instance *bgpInstance) {
	t.Helper()
	if err := instance.server.StopBgp(context.Background(), &api.StopBgpRequest{}); err != nil {
		t.Fatalf("stop BGP: %v", err)
	}
}

func TestBGPAdvertiseFailsWhenServerStopped(t *testing.T) {
	instance := startBGPTestInstance(t, 11834)
	stopBGPTestServer(t, instance)
	if err := instance.advertise(context.Background(), bgpStartConfig{
		LocalIP:   net.ParseIP("127.0.0.1").To4(),
		Advertise: []*net.IPNet{mustCIDR(t, "10.94.0.0/24")},
	}); err == nil || !strings.Contains(err.Error(), "advertise BGP prefix") {
		t.Fatalf("advertise on a stopped server = %v", err)
	}
}

func TestBGPReloadRestoresAfterAdvertiseFailure(t *testing.T) {
	initial := mustCIDR(t, "10.93.0.0/24")
	instance := startBGPTestInstance(t, 11835, initial)
	stopBGPTestServer(t, instance)
	// Withdraw the tracked paths before stopping the server so the failed
	// advertise is the first error the reload hits.
	instance.advertisedMu.Lock()
	instance.advertisedIDs = nil
	instance.advertised = []*net.IPNet{initial}
	instance.advertisedMu.Unlock()

	err := instance.reload(context.Background(), store.VNF{
		ID:   41,
		Type: store.VNFTypeBGP,
		Config: store.VNFConfig{
			LocalIP:           "127.0.0.1/32",
			AdvertisePrefixes: []string{"10.92.0.0/24"},
		},
	})
	if err == nil {
		t.Fatal("reload with a stopped BGP server succeeded")
	}
	if !strings.Contains(err.Error(), "advertise BGP prefix") ||
		!strings.Contains(err.Error(), "restore advertised BGP prefixes") {
		t.Fatalf("reload error = %v, want advertisement and restore failures", err)
	}
}
