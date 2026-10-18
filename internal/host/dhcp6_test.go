package host

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/coredhcp/coredhcp/handler"
	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/iana"
)

const (
	testDHCPv6Gateway = "fd00:20::1/64"
	testDHCPv6Start   = "fd00:20::100"
	testDHCPv6End     = "fd00:20::101"
	testDHCPv6DNS     = "2001:4860:4860::8888"
	testDHCPv6DUID    = "02:00:00:00:00:01"
)

func newDHCPv6TestHandler(t *testing.T, leasePath string, end string) handler.Handler6 {
	t.Helper()
	h, err := setupCoreDHCPv6(
		testDHCPv6Gateway,
		testDHCPv6Start,
		end,
		testDHCPv6DNS,
		leasePath,
		coreDHCPLeaseTime.String(),
		testDHCPv6DUID,
	)
	if err != nil {
		t.Fatalf("setup CoreDHCPv6: %v", err)
	}
	return h
}

func dhcpv6Solicit(t *testing.T, mac net.HardwareAddr) *dhcpv6.Message {
	t.Helper()
	sol, err := dhcpv6.NewSolicit(mac)
	if err != nil {
		t.Fatalf("build DHCPv6 solicit: %v", err)
	}
	return sol
}

func dhcpv6Advertise(t *testing.T, sol *dhcpv6.Message) *dhcpv6.Message {
	t.Helper()
	adv, err := dhcpv6.NewAdvertiseFromSolicit(sol)
	if err != nil {
		t.Fatalf("build DHCPv6 advertise: %v", err)
	}
	return adv
}

func dhcpv6Request(t *testing.T, adv *dhcpv6.Message) *dhcpv6.Message {
	t.Helper()
	req, err := dhcpv6.NewRequestFromAdvertise(adv)
	if err != nil {
		t.Fatalf("build DHCPv6 request: %v", err)
	}
	return req
}

func dhcpv6Reply(t *testing.T, msg *dhcpv6.Message) *dhcpv6.Message {
	t.Helper()
	reply, err := dhcpv6.NewReplyFromMessage(msg)
	if err != nil {
		t.Fatalf("build DHCPv6 reply: %v", err)
	}
	return reply
}

func dhcpv6Address(t *testing.T, msg dhcpv6.DHCPv6) net.IP {
	t.Helper()
	m, ok := msg.(*dhcpv6.Message)
	if !ok {
		t.Fatalf("response type = %T, want *dhcpv6.Message", msg)
	}
	iana := m.Options.OneIANA()
	if iana == nil {
		t.Fatal("response has no IA_NA")
	}
	addr := iana.Options.OneAddress()
	if addr == nil {
		t.Fatal("response IA_NA has no address")
	}
	return addr.IPv6Addr
}

func TestCoreDHCPv6AllocationAndPersistence(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "1.json")
	h := newDHCPv6TestHandler(t, leasePath, testDHCPv6End)
	mac := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x10}

	sol := dhcpv6Solicit(t, mac)
	adv := dhcpv6Advertise(t, sol)
	out, stop := h(sol, adv)
	if stop || out == nil {
		t.Fatalf("solicit returned stop=%v response=%v", stop, out)
	}
	adv = out.(*dhcpv6.Message)
	if got := dhcpv6Address(t, adv).String(); got != testDHCPv6Start {
		t.Fatalf("advertised address = %s, want %s", got, testDHCPv6Start)
	}
	if adv.Options.ServerID() == nil {
		t.Fatal("advertise has no server ID")
	}
	if dns := adv.Options.DNS(); len(dns) != 1 || dns[0].String() != testDHCPv6DNS {
		t.Fatalf("advertise DNS = %v", dns)
	}

	req := dhcpv6Request(t, adv)
	reply := dhcpv6Reply(t, req)
	out, stop = h(req, reply)
	if stop || out == nil {
		t.Fatalf("request returned stop=%v response=%v", stop, out)
	}
	reply = out.(*dhcpv6.Message)
	if got := dhcpv6Address(t, reply).String(); got != testDHCPv6Start {
		t.Fatalf("requested address = %s, want %s", got, testDHCPv6Start)
	}

	// A fresh handler must load the persisted lease for the same client.
	second := newDHCPv6TestHandler(t, leasePath, testDHCPv6End)
	sol2 := dhcpv6Solicit(t, mac)
	adv2 := dhcpv6Advertise(t, sol2)
	out, _ = second(sol2, adv2)
	if got := dhcpv6Address(t, out).String(); got != testDHCPv6Start {
		t.Fatalf("persisted address = %s, want %s", got, testDHCPv6Start)
	}
}

func TestCoreDHCPv6ExhaustionAndRelease(t *testing.T) {
	h := newDHCPv6TestHandler(t, filepath.Join(t.TempDir(), "1.json"), testDHCPv6End)
	firstMAC := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x20}
	secondMAC := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x21}
	thirdMAC := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x22}

	firstSol := dhcpv6Solicit(t, firstMAC)
	firstAdv := dhcpv6Advertise(t, firstSol)
	firstOut, _ := h(firstSol, firstAdv)
	firstAdv = firstOut.(*dhcpv6.Message)

	secondSol := dhcpv6Solicit(t, secondMAC)
	secondAdv := dhcpv6Advertise(t, secondSol)
	secondOut, _ := h(secondSol, secondAdv)
	if got := dhcpv6Address(t, secondOut).String(); got != testDHCPv6End {
		t.Fatalf("second address = %s, want %s", got, testDHCPv6End)
	}

	thirdSol := dhcpv6Solicit(t, thirdMAC)
	thirdAdv := dhcpv6Advertise(t, thirdSol)
	thirdOut, stop := h(thirdSol, thirdAdv)
	if stop || thirdOut == nil {
		t.Fatalf("exhausted solicit returned stop=%v response=%v", stop, thirdOut)
	}
	thirdMsg := thirdOut.(*dhcpv6.Message)
	iana := thirdMsg.Options.OneIANA()
	if iana == nil || iana.Options.Status() == nil {
		t.Fatalf("exhausted response lacks status: %v", thirdMsg)
	}

	release, err := dhcpv6.NewMessage(
		dhcpv6.WithClientID(firstAdv.Options.ClientID()),
		dhcpv6.WithServerID(firstAdv.Options.ServerID()),
	)
	if err != nil {
		t.Fatalf("build release: %v", err)
	}
	release.MessageType = dhcpv6.MessageTypeRelease
	release.AddOption(firstAdv.Options.OneIANA())
	releaseReply := dhcpv6Reply(t, release)
	if out, stop := h(release, releaseReply); !stop || out != nil {
		t.Fatalf("release returned stop=%v response=%v", stop, out)
	}

	fourthMAC := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x23}
	fourthSol := dhcpv6Solicit(t, fourthMAC)
	fourthAdv := dhcpv6Advertise(t, fourthSol)
	fourthOut, _ := h(fourthSol, fourthAdv)
	if got := dhcpv6Address(t, fourthOut).String(); got != testDHCPv6Start {
		t.Fatalf("released address was not reused: got %s", got)
	}
}

func TestCoreDHCPv6RejectsWrongServer(t *testing.T) {
	h := newDHCPv6TestHandler(t, filepath.Join(t.TempDir(), "1.json"), testDHCPv6End)
	mac := net.HardwareAddr{0x02, 0, 0, 0, 0, 0x30}
	sol := dhcpv6Solicit(t, mac)

	other, err := dhcpv6.NewMessage(
		dhcpv6.WithClientID(sol.Options.ClientID()),
		dhcpv6.WithServerID(&dhcpv6.DUIDLL{
			HWType:        iana.HWTypeEthernet,
			LinkLayerAddr: net.HardwareAddr{0x02, 0, 0, 0, 0, 0x99},
		}),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	other.MessageType = dhcpv6.MessageTypeRequest
	other.AddOption(sol.Options.OneIANA())
	reply := dhcpv6Reply(t, other)
	if out, stop := h(other, reply); !stop || out != nil {
		t.Fatalf("wrong-server request returned stop=%v response=%v", stop, out)
	}
}

func TestCoreDHCPv6ConfigValidation(t *testing.T) {
	valid := []string{
		testDHCPv6Gateway,
		testDHCPv6Start,
		testDHCPv6End,
		testDHCPv6DNS,
		filepath.Join(t.TempDir(), "1.json"),
		coreDHCPLeaseTime.String(),
		testDHCPv6DUID,
	}
	tests := []struct {
		name   string
		mutate func([]string) []string
	}{
		{name: "missing args", mutate: func([]string) []string { return valid[:6] }},
		{name: "bad gateway", mutate: func(args []string) []string { args[0] = "nope"; return args }},
		{name: "range outside", mutate: func(args []string) []string { args[1] = "fd00:21::100"; return args }},
		{name: "start after end", mutate: func(args []string) []string { args[1], args[2] = args[2], args[1]; return args }},
		{name: "bad DNS", mutate: func(args []string) []string { args[3] = "1.1.1.1"; return args }},
		{name: "bad lease time", mutate: func(args []string) []string { args[5] = "forever"; return args }},
		{name: "bad DUID", mutate: func(args []string) []string { args[6] = "nope"; return args }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string(nil), valid...)
			args = test.mutate(args)
			if _, err := setupCoreDHCPv6(args...); err == nil {
				t.Fatalf("setup succeeded for args %v", args)
			}
		})
	}
}
