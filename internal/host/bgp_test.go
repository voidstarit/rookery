package host

import (
	"context"
	"net"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestStartBGPServer(t *testing.T) {
	ctx := context.Background()
	instance, err := startBGPServer(ctx, bgpStartConfig{
		VNF:     store.VNF{ID: 1, Type: store.VNFTypeBGP, Config: store.VNFConfig{Transport: "tap"}},
		LocalIP: net.ParseIP("127.0.0.1").To4(),
		PeerIP:  net.ParseIP("127.0.0.2").To4(),
		LocalAS: 64512, PeerAS: 64513, RouterID: "10.0.0.1",
		Passive: true, Neighbor: "127.0.0.2", LocalAddress: "127.0.0.1",
		ListenAddress: []string{"127.0.0.1"}, ListenPort: 1179,
	})
	if err != nil {
		t.Fatalf("start BGP server: %v", err)
	}
	instance.Close(ctx)
}

func TestStartBGPServerActiveWithoutListener(t *testing.T) {
	ctx := context.Background()
	instance, err := startBGPServer(ctx, bgpStartConfig{
		VNF:     store.VNF{ID: 2, Type: store.VNFTypeBGP, Config: store.VNFConfig{Transport: "memif"}},
		LocalIP: net.ParseIP("127.0.0.1").To4(),
		PeerIP:  net.ParseIP("127.0.0.2").To4(),
		LocalAS: 64512, PeerAS: 64513, RouterID: "10.0.0.1",
		Neighbor: "127.0.0.2", LocalAddress: "127.0.0.1",
		RemoteAddress: "127.0.0.1", RemotePort: 1179, ListenPort: -1,
	})
	if err != nil {
		t.Fatalf("start active BGP server: %v", err)
	}
	instance.Close(ctx)
}

// The memif slave (gVisor) MAC must never equal the VPP memif master MAC,
// otherwise both ends of the link claim the same address and L2 learning on
// the bridge domain breaks.
func TestVNFMemifMACsDiffer(t *testing.T) {
	const id = int64(42)
	master := VNFMemifMAC(id)
	bgp := vnfMemifBGPMAC(id)
	dhcp := vnfMemifDHCPMAC(id)
	if master.String() == bgp.String() {
		t.Fatalf("BGP memif slave MAC %s equals VPP master MAC %s", bgp, master)
	}
	if bgp.String() == dhcp.String() {
		t.Fatalf("BGP memif MAC %s equals DHCP memif MAC %s", bgp, dhcp)
	}
}

func TestBGPStatusAdvertisedPrefix(t *testing.T) {
	ctx := context.Background()
	_, network, err := net.ParseCIDR("10.99.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	instance, err := startBGPServer(ctx, bgpStartConfig{
		VNF:     store.VNF{ID: 3, Type: store.VNFTypeBGP, Config: store.VNFConfig{Transport: "tap"}},
		LocalIP: net.ParseIP("127.0.0.1").To4(),
		PeerIP:  net.ParseIP("127.0.0.2").To4(),
		LocalAS: 64512, PeerAS: 64513, RouterID: "10.0.0.1",
		Passive: true, Neighbor: "127.0.0.2", LocalAddress: "127.0.0.1",
		ListenAddress: []string{"127.0.0.1"}, ListenPort: 1179,
		Advertise: []*net.IPNet{network},
	})
	if err != nil {
		t.Fatalf("start BGP server: %v", err)
	}
	defer instance.Close(ctx)

	status := instance.Status(ctx)
	if !status.Running {
		t.Fatalf("status = %+v", status)
	}
	if status.PeerAddress != "127.0.0.2" || status.LocalAS != 64512 || status.PeerAS != 64513 {
		t.Fatalf("status = %+v", status)
	}
	if len(status.Advertised) != 1 || status.Advertised[0] != "10.99.0.0/24" {
		t.Fatalf("advertised = %+v", status.Advertised)
	}
	if len(status.Received) != 0 {
		t.Fatalf("received = %+v", status.Received)
	}
}
