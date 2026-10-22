package host

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coredhcp/coredhcp/handler"
	"github.com/insomniacslk/dhcp/dhcpv4"
)

const (
	testDHCPGateway = "10.20.0.1"
	testDHCPMask    = "255.255.255.0"
	testDHCPStart   = "10.20.0.100"
	testDHCPEnd     = "10.20.0.200"
	testDHCPDNS     = "1.1.1.1,8.8.8.8"
	testDHCPBroad   = "10.20.0.255"
)

func newDHCPTestHandler(t *testing.T, leasePath string) handler.Handler4 {
	t.Helper()
	h, err := setupCoreDHCP(
		testDHCPGateway,
		testDHCPMask,
		testDHCPStart,
		testDHCPEnd,
		testDHCPDNS,
		leasePath,
		coreDHCPLeaseTime.String(),
		testDHCPBroad,
		testDHCPGateway,
	)
	if err != nil {
		t.Fatalf("setup CoreDHCP: %v", err)
	}
	return h
}

func dhcpTestMAC(t *testing.T, value string) net.HardwareAddr {
	t.Helper()
	mac, err := net.ParseMAC(value)
	if err != nil {
		t.Fatalf("parse MAC %q: %v", value, err)
	}
	return mac
}

func dhcpTestRequest(t *testing.T, messageType dhcpv4.MessageType, mac net.HardwareAddr, modifiers ...dhcpv4.Modifier) *dhcpv4.DHCPv4 {
	t.Helper()
	all := []dhcpv4.Modifier{
		dhcpv4.WithMessageType(messageType),
		dhcpv4.WithHwAddr(mac),
		dhcpv4.WithBroadcast(true),
	}
	all = append(all, modifiers...)
	req, err := dhcpv4.New(all...)
	if err != nil {
		t.Fatalf("build DHCP request: %v", err)
	}
	return req
}

func dhcpTestReply(t *testing.T, req *dhcpv4.DHCPv4) *dhcpv4.DHCPv4 {
	t.Helper()
	resp, err := dhcpv4.NewReplyFromRequest(req)
	if err != nil {
		t.Fatalf("build DHCP reply: %v", err)
	}
	switch req.MessageType() {
	case dhcpv4.MessageTypeDiscover:
		resp.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeOffer))
	case dhcpv4.MessageTypeRequest, dhcpv4.MessageTypeInform:
		resp.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeAck))
	}
	return resp
}

func dhcpTestExchange(t *testing.T, h handler.Handler4, req *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
	t.Helper()
	resp, stop := h(req, dhcpTestReply(t, req))
	return resp, stop
}

func TestCoreDHCPAllocationAndRelease(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "1.coredhcp.json")
	h := newDHCPTestHandler(t, leasePath)

	mac1 := dhcpTestMAC(t, "02:00:00:00:00:01")
	offer, stop := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, mac1))
	if stop || offer == nil {
		t.Fatalf("discover returned stop=%v response=%v", stop, offer)
	}
	if got, want := offer.YourIPAddr.String(), "10.20.0.100"; got != want {
		t.Fatalf("offered IP = %s, want %s", got, want)
	}
	if got := offer.ServerIdentifier().String(); got != testDHCPGateway {
		t.Fatalf("server identifier = %s, want %s", got, testDHCPGateway)
	}
	if routers := offer.Router(); len(routers) != 1 || routers[0].String() != testDHCPGateway {
		t.Fatalf("routers = %v, want %s", routers, testDHCPGateway)
	}
	if mask := net.IP(offer.SubnetMask()).String(); mask != testDHCPMask {
		t.Fatalf("subnet mask = %s, want %s", mask, testDHCPMask)
	}
	if dns := offer.DNS(); len(dns) != 2 || dns[0].String() != "1.1.1.1" || dns[1].String() != "8.8.8.8" {
		t.Fatalf("DNS = %v", dns)
	}
	if lease := offer.IPAddressLeaseTime(0); lease != coreDHCPLeaseTime {
		t.Fatalf("lease time = %s, want %s", lease, coreDHCPLeaseTime)
	}

	request := dhcpTestRequest(t, dhcpv4.MessageTypeRequest, mac1,
		dhcpv4.WithOption(dhcpv4.OptRequestedIPAddress(offer.YourIPAddr)),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.ParseIP(testDHCPGateway))),
	)
	ack, stop := dhcpTestExchange(t, h, request)
	if stop || ack == nil || ack.MessageType() != dhcpv4.MessageTypeAck {
		t.Fatalf("request returned stop=%v response=%v", stop, ack)
	}
	if got, want := ack.YourIPAddr.String(), "10.20.0.100"; got != want {
		t.Fatalf("acked IP = %s, want %s", got, want)
	}

	mac2 := dhcpTestMAC(t, "02:00:00:00:00:02")
	second, _ := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, mac2))
	if second == nil || second.YourIPAddr.String() != "10.20.0.101" {
		t.Fatalf("second offer = %v", second)
	}

	mac3 := dhcpTestMAC(t, "02:00:00:00:00:03")

	release := dhcpTestRequest(t, dhcpv4.MessageTypeRelease, mac1,
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.ParseIP(testDHCPGateway))),
	)
	if resp, stop := dhcpTestExchange(t, h, release); !stop || resp != nil {
		t.Fatalf("release returned stop=%v response=%v", stop, resp)
	}

	reused, _ := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, mac3))
	if reused == nil || reused.YourIPAddr.String() != "10.20.0.100" {
		t.Fatalf("reused offer = %v, want 10.20.0.100", reused)
	}
}

func TestCoreDHCPStaticAssignments(t *testing.T) {
	staticArg := `[{"mac":"02:00:00:00:00:01","ip":"10.20.0.50"},{"mac":"02:00:00:00:00:02","ip":"10.20.0.100"}]`
	h, err := setupCoreDHCP(
		testDHCPGateway,
		testDHCPMask,
		testDHCPStart,
		testDHCPEnd,
		testDHCPDNS,
		filepath.Join(t.TempDir(), "1.coredhcp.json"),
		coreDHCPLeaseTime.String(),
		testDHCPBroad,
		testDHCPGateway,
		staticArg,
	)
	if err != nil {
		t.Fatalf("setup CoreDHCP: %v", err)
	}

	staticMAC := dhcpTestMAC(t, "02:00:00:00:00:01")
	offer, stop := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, staticMAC))
	if stop || offer == nil || offer.YourIPAddr.String() != "10.20.0.50" {
		t.Fatalf("static offer = %v, stop=%v", offer, stop)
	}

	request := dhcpTestRequest(t, dhcpv4.MessageTypeRequest, staticMAC,
		dhcpv4.WithOption(dhcpv4.OptRequestedIPAddress(net.ParseIP("10.20.0.50"))),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.ParseIP(testDHCPGateway))),
	)
	ack, stop := dhcpTestExchange(t, h, request)
	if stop || ack == nil || ack.MessageType() != dhcpv4.MessageTypeAck || ack.YourIPAddr.String() != "10.20.0.50" {
		t.Fatalf("static ack = %v, stop=%v", ack, stop)
	}

	reservedMAC := dhcpTestMAC(t, "02:00:00:00:00:02")
	reservedOffer, _ := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, reservedMAC))
	if reservedOffer == nil || reservedOffer.YourIPAddr.String() != "10.20.0.100" {
		t.Fatalf("reserved offer = %v, want 10.20.0.100", reservedOffer)
	}

	dynamicMAC := dhcpTestMAC(t, "02:00:00:00:00:03")
	reservedRequest := dhcpTestRequest(t, dhcpv4.MessageTypeRequest, dynamicMAC,
		dhcpv4.WithOption(dhcpv4.OptRequestedIPAddress(net.ParseIP("10.20.0.100"))),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.ParseIP(testDHCPGateway))),
	)
	nak, stop := dhcpTestExchange(t, h, reservedRequest)
	if !stop || nak == nil || nak.MessageType() != dhcpv4.MessageTypeNak {
		t.Fatalf("reserved-IP request returned stop=%v response=%v, want NAK", stop, nak)
	}

	dynamicOffer, _ := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, dynamicMAC))
	if dynamicOffer == nil || dynamicOffer.YourIPAddr.String() != "10.20.0.101" {
		t.Fatalf("dynamic offer = %v, want 10.20.0.101 (reserved .100 skipped)", dynamicOffer)
	}
}

func TestCoreDHCPExhaustion(t *testing.T) {
	h, err := setupCoreDHCP(
		testDHCPGateway,
		testDHCPMask,
		testDHCPStart,
		"10.20.0.101",
		testDHCPDNS,
		filepath.Join(t.TempDir(), "1.coredhcp.json"),
		coreDHCPLeaseTime.String(),
		testDHCPBroad,
		testDHCPGateway,
	)
	if err != nil {
		t.Fatalf("setup CoreDHCP: %v", err)
	}

	for _, value := range []string{"02:00:00:00:00:50", "02:00:00:00:00:51"} {
		offer, _ := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, dhcpTestMAC(t, value)))
		if offer == nil {
			t.Fatalf("discover for %s returned no offer", value)
		}
	}

	third := dhcpTestMAC(t, "02:00:00:00:00:52")
	resp, stop := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, third))
	if !stop || resp != nil {
		t.Fatalf("exhausted range returned stop=%v response=%v", stop, resp)
	}
}

func TestCoreDHCPForcesBroadcastForAddresslessClients(t *testing.T) {
	h := newDHCPTestHandler(t, filepath.Join(t.TempDir(), "1.coredhcp.json"))
	mac := dhcpTestMAC(t, "02:00:00:00:00:05")
	req, err := dhcpv4.New(
		dhcpv4.WithMessageType(dhcpv4.MessageTypeDiscover),
		dhcpv4.WithHwAddr(mac),
	)
	if err != nil {
		t.Fatalf("build DHCP request: %v", err)
	}
	if req.IsBroadcast() {
		t.Fatal("test request unexpectedly starts with the broadcast flag")
	}
	if resp, stop := dhcpTestExchange(t, h, req); stop || resp == nil {
		t.Fatalf("discover returned stop=%v response=%v", stop, resp)
	}
	if !req.IsBroadcast() {
		t.Fatal("addressless discover did not enable the broadcast flag")
	}
}

func TestCoreDHCPSeparateServerID(t *testing.T) {
	h, err := setupCoreDHCP(
		testDHCPGateway,
		testDHCPMask,
		testDHCPStart,
		testDHCPEnd,
		testDHCPDNS,
		filepath.Join(t.TempDir(), "1.coredhcp.json"),
		coreDHCPLeaseTime.String(),
		testDHCPBroad,
		"10.20.0.2",
	)
	if err != nil {
		t.Fatalf("setup CoreDHCP: %v", err)
	}
	mac := dhcpTestMAC(t, "02:00:00:00:00:70")
	offer, stop := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, mac))
	if stop || offer == nil {
		t.Fatalf("discover returned stop=%v response=%v", stop, offer)
	}
	if got := offer.ServerIdentifier().String(); got != "10.20.0.2" {
		t.Fatalf("server ID = %s, want 10.20.0.2", got)
	}
	if got := offer.ServerIPAddr.String(); got != "10.20.0.2" {
		t.Fatalf("server IP = %s, want 10.20.0.2", got)
	}
	if routers := offer.Router(); len(routers) != 1 || routers[0].String() != testDHCPGateway {
		t.Fatalf("routers = %v, want %s", routers, testDHCPGateway)
	}

	request := dhcpTestRequest(t, dhcpv4.MessageTypeRequest, mac,
		dhcpv4.WithOption(dhcpv4.OptRequestedIPAddress(offer.YourIPAddr)),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.ParseIP("10.20.0.2"))),
	)
	ack, stop := dhcpTestExchange(t, h, request)
	if stop || ack == nil || ack.MessageType() != dhcpv4.MessageTypeAck {
		t.Fatalf("request to separate server ID returned stop=%v response=%v", stop, ack)
	}
	if got := ack.YourIPAddr.String(); got != offer.YourIPAddr.String() {
		t.Fatalf("acked IP = %s, want %s", got, offer.YourIPAddr)
	}
}

func TestCoreDHCPPersistsLeases(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "1.coredhcp.json")
	first := newDHCPTestHandler(t, leasePath)
	mac := dhcpTestMAC(t, "02:00:00:00:00:10")

	offer, _ := dhcpTestExchange(t, first, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, mac))
	if offer == nil {
		t.Fatal("first discover returned no offer")
	}

	second := newDHCPTestHandler(t, leasePath)
	again, _ := dhcpTestExchange(t, second, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, mac))
	if again == nil || !again.YourIPAddr.Equal(offer.YourIPAddr) {
		t.Fatalf("persisted offer = %v, want %s", again, offer.YourIPAddr)
	}
}

func TestCoreDHCPRejectsOtherServerAndTakenAddress(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "1.coredhcp.json")
	h := newDHCPTestHandler(t, leasePath)
	mac1 := dhcpTestMAC(t, "02:00:00:00:00:20")
	mac2 := dhcpTestMAC(t, "02:00:00:00:00:21")

	offer, _ := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, mac1))
	if offer == nil {
		t.Fatal("discover returned no offer")
	}

	otherServer := dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, mac2,
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.ParseIP("10.20.0.254"))),
	)
	if resp, stop := dhcpTestExchange(t, h, otherServer); !stop || resp != nil {
		t.Fatalf("other-server request returned stop=%v response=%v", stop, resp)
	}

	taken := dhcpTestRequest(t, dhcpv4.MessageTypeRequest, mac2,
		dhcpv4.WithOption(dhcpv4.OptRequestedIPAddress(offer.YourIPAddr)),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.ParseIP(testDHCPGateway))),
	)
	nak, stop := dhcpTestExchange(t, h, taken)
	if !stop || nak == nil || nak.MessageType() != dhcpv4.MessageTypeNak {
		t.Fatalf("taken address request returned stop=%v response=%v", stop, nak)
	}
}

func TestCoreDHCPRenewUsesClientAddressHint(t *testing.T) {
	h := newDHCPTestHandler(t, filepath.Join(t.TempDir(), "1.coredhcp.json"))
	mac := dhcpTestMAC(t, "02:00:00:00:00:35")

	renew := dhcpTestRequest(t, dhcpv4.MessageTypeRequest, mac,
		dhcpv4.WithClientIP(net.ParseIP("10.20.0.150")),
	)
	ack, stop := dhcpTestExchange(t, h, renew)
	if stop || ack == nil || ack.MessageType() != dhcpv4.MessageTypeAck {
		t.Fatalf("renew returned stop=%v response=%v", stop, ack)
	}
	if got := ack.YourIPAddr.String(); got != "10.20.0.150" {
		t.Fatalf("renewed IP = %s, want 10.20.0.150", got)
	}
}

func TestCoreDHCPInformDoesNotAllocate(t *testing.T) {
	h := newDHCPTestHandler(t, filepath.Join(t.TempDir(), "1.coredhcp.json"))
	informMAC := dhcpTestMAC(t, "02:00:00:00:00:30")
	discoverMAC := dhcpTestMAC(t, "02:00:00:00:00:31")

	inform, stop := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeInform, informMAC))
	if stop || inform == nil {
		t.Fatalf("inform returned stop=%v response=%v", stop, inform)
	}
	if !inform.YourIPAddr.IsUnspecified() {
		t.Fatalf("inform assigned %s", inform.YourIPAddr)
	}
	if inform.ServerIdentifier().String() != testDHCPGateway {
		t.Fatalf("inform server identifier = %s", inform.ServerIdentifier())
	}

	offer, _ := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, discoverMAC))
	if offer == nil || offer.YourIPAddr.String() != "10.20.0.100" {
		t.Fatalf("offer after inform = %v, want 10.20.0.100", offer)
	}
}

func TestCoreDHCPExpiresLeases(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "1.coredhcp.json")
	h, err := setupCoreDHCP(
		testDHCPGateway,
		testDHCPMask,
		testDHCPStart,
		"10.20.0.100",
		"",
		leasePath,
		"20ms",
		testDHCPBroad,
		testDHCPGateway,
	)
	if err != nil {
		t.Fatalf("setup CoreDHCP: %v", err)
	}

	firstMAC := dhcpTestMAC(t, "02:00:00:00:00:40")
	first, _ := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, firstMAC))
	if first == nil || first.YourIPAddr.String() != "10.20.0.100" {
		t.Fatalf("first offer = %v", first)
	}
	time.Sleep(50 * time.Millisecond)

	secondMAC := dhcpTestMAC(t, "02:00:00:00:00:41")
	second, _ := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, secondMAC))
	if second == nil || second.YourIPAddr.String() != "10.20.0.100" {
		t.Fatalf("offer after expiry = %v, want 10.20.0.100", second)
	}
}

func TestCoreDHCPInstancesAreIndependent(t *testing.T) {
	dir := t.TempDir()
	first := newDHCPTestHandler(t, filepath.Join(dir, "1.coredhcp.json"))
	second, err := setupCoreDHCP(
		"10.30.0.1",
		testDHCPMask,
		"10.30.0.150",
		"10.30.0.151",
		"9.9.9.9",
		filepath.Join(dir, "2.coredhcp.json"),
		coreDHCPLeaseTime.String(),
		"10.30.0.255",
		"10.30.0.1",
	)
	if err != nil {
		t.Fatalf("setup second CoreDHCP: %v", err)
	}

	mac := dhcpTestMAC(t, "02:00:00:00:00:60")
	firstOffer, _ := dhcpTestExchange(t, first, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, mac))
	if firstOffer == nil || firstOffer.YourIPAddr.String() != "10.20.0.100" {
		t.Fatalf("first offer = %v", firstOffer)
	}
	if dns := firstOffer.DNS(); len(dns) != 2 || dns[0].String() != "1.1.1.1" {
		t.Fatalf("first DNS = %v", dns)
	}

	secondOffer, _ := dhcpTestExchange(t, second, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, mac))
	if secondOffer == nil || secondOffer.YourIPAddr.String() != "10.30.0.150" {
		t.Fatalf("second offer = %v", secondOffer)
	}
	if dns := secondOffer.DNS(); len(dns) != 1 || dns[0].String() != "9.9.9.9" {
		t.Fatalf("second DNS = %v", dns)
	}
	if got := secondOffer.ServerIdentifier().String(); got != "10.30.0.1" {
		t.Fatalf("second server identifier = %s", got)
	}
}

func TestRemoveLegacyDHCPFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "7")
	for _, suffix := range []string{".conf", ".pid", ".leases"} {
		if err := os.WriteFile(base+suffix, []byte("stale"), 0o600); err != nil {
			t.Fatalf("write %s: %v", suffix, err)
		}
	}
	current := base + ".coredhcp.json"
	if err := os.WriteFile(current, []byte("current"), 0o600); err != nil {
		t.Fatalf("write current lease file: %v", err)
	}

	removeLegacyDHCP(dir, 7)

	for _, suffix := range []string{".conf", ".pid", ".leases"} {
		if _, err := os.Stat(base + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("legacy %s still exists: %v", suffix, err)
		}
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("CoreDHCP lease file was removed: %v", err)
	}
}

func TestCoreDHCPConfigValidation(t *testing.T) {
	valid := []string{
		testDHCPGateway,
		testDHCPMask,
		testDHCPStart,
		testDHCPEnd,
		testDHCPDNS,
		filepath.Join(t.TempDir(), "1.coredhcp.json"),
		coreDHCPLeaseTime.String(),
		testDHCPBroad,
		testDHCPGateway,
		"",
	}

	tests := []struct {
		name   string
		mutate func([]string) []string
	}{
		{name: "missing args", mutate: func([]string) []string { return valid[:8] }},
		{name: "bad gateway", mutate: func(args []string) []string { args[0] = "nope"; return args }},
		{name: "bad mask", mutate: func(args []string) []string { args[1] = "nope"; return args }},
		{name: "range outside subnet", mutate: func(args []string) []string { args[2] = "10.21.0.10"; return args }},
		{name: "start after end", mutate: func(args []string) []string { args[2], args[3] = args[3], args[2]; return args }},
		{name: "bad DNS", mutate: func(args []string) []string { args[4] = "1.1.1.1,nope"; return args }},
		{name: "bad lease time", mutate: func(args []string) []string { args[6] = "forever"; return args }},
		{name: "bad broadcast", mutate: func(args []string) []string { args[7] = "nope"; return args }},
		{name: "bad server ID", mutate: func(args []string) []string { args[8] = "nope"; return args }},
		{name: "bad static JSON", mutate: func(args []string) []string { args[9] = "nope"; return args }},
		{name: "bad static MAC", mutate: func(args []string) []string {
			args[9] = `[{"mac":"nope","ip":"10.20.0.50"}]`
			return args
		}},
		{name: "static IP outside subnet", mutate: func(args []string) []string {
			args[9] = `[{"mac":"02:00:00:00:00:01","ip":"10.21.0.50"}]`
			return args
		}},
		{name: "duplicate static IP", mutate: func(args []string) []string {
			args[9] = `[{"mac":"02:00:00:00:00:01","ip":"10.20.0.50"},{"mac":"02:00:00:00:00:02","ip":"10.20.0.50"}]`
			return args
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string(nil), valid...)
			args = test.mutate(args)
			if _, err := setupCoreDHCP(args...); err == nil {
				t.Fatal("expected setup error")
			}
		})
	}
}

func TestCoreDHCPExtraOptions(t *testing.T) {
	h, err := setupCoreDHCP(
		testDHCPGateway,
		testDHCPMask,
		testDHCPStart,
		testDHCPEnd,
		testDHCPDNS,
		filepath.Join(t.TempDir(), "1.coredhcp.json"),
		coreDHCPLeaseTime.String(),
		testDHCPBroad,
		testDHCPGateway,
		"",
		`[{"code":3,"format":"ipv4","value":"10.20.0.254"},{"code":6,"format":"ipv4","value":"10.20.0.10"},{"code":15,"format":"string","value":"lab.test"},{"code":42,"format":"ipv4","value":"10.20.0.1,10.20.0.2"}]`,
	)
	if err != nil {
		t.Fatalf("setup CoreDHCP: %v", err)
	}
	mac := dhcpTestMAC(t, "02:00:00:00:00:90")
	offer, stop := dhcpTestExchange(t, h, dhcpTestRequest(t, dhcpv4.MessageTypeDiscover, mac))
	if stop || offer == nil {
		t.Fatalf("discover returned stop=%v response=%v", stop, offer)
	}
	if got := offer.DomainName(); got != "lab.test" {
		t.Fatalf("domain name = %q, want lab.test", got)
	}
	if got := offer.Router(); len(got) != 1 || got[0].String() != "10.20.0.254" {
		t.Fatalf("router = %v, want custom router", got)
	}
	if got := offer.DNS(); len(got) != 1 || got[0].String() != "10.20.0.10" {
		t.Fatalf("DNS = %v, want custom DNS", got)
	}
	ntp := offer.NTPServers()
	if len(ntp) != 2 || ntp[0].String() != "10.20.0.1" || ntp[1].String() != "10.20.0.2" {
		t.Fatalf("NTP servers = %v", ntp)
	}
}

func TestCoreDHCPOptionsRejectReservedCode(t *testing.T) {
	if _, err := setupCoreDHCP(
		testDHCPGateway,
		testDHCPMask,
		testDHCPStart,
		testDHCPEnd,
		testDHCPDNS,
		filepath.Join(t.TempDir(), "1.coredhcp.json"),
		coreDHCPLeaseTime.String(),
		testDHCPBroad,
		testDHCPGateway,
		"",
		`[{"code":1,"format":"ipv4","value":"255.255.255.0"}]`,
	); err == nil {
		t.Fatal("setup accepted a server-managed option code")
	}
}
