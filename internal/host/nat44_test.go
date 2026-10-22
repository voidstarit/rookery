package host

import (
	"context"
	"errors"
	"testing"

	govppapi "go.fd.io/govpp/api"
	"go.fd.io/govpp/binapi/nat44_ed"
)

type fakeNAT44StaticMappingClient struct {
	deleteCalls  int
	deleteErrors []error
}

func (f *fakeNAT44StaticMappingClient) Nat44StaticMappingDump(context.Context, *nat44_ed.Nat44StaticMappingDump) (nat44_ed.RPCService_Nat44StaticMappingDumpClient, error) {
	return nil, nil
}

func (f *fakeNAT44StaticMappingClient) Nat44AddDelStaticMappingV2(context.Context, *nat44_ed.Nat44AddDelStaticMappingV2) (*nat44_ed.Nat44AddDelStaticMappingV2Reply, error) {
	call := f.deleteCalls
	f.deleteCalls++
	if call < len(f.deleteErrors) {
		return &nat44_ed.Nat44AddDelStaticMappingV2Reply{}, f.deleteErrors[call]
	}
	return &nat44_ed.Nat44AddDelStaticMappingV2Reply{}, nil
}

func TestDeleteNAT44StaticMappingToleratesMissingEntry(t *testing.T) {
	details := &nat44_ed.Nat44StaticMappingDetails{}
	for _, test := range []struct {
		name   string
		errors []error
	}{
		{name: "interface delete missing, address delete succeeds", errors: []error{govppapi.NO_SUCH_ENTRY}},
		{name: "both attempts missing", errors: []error{govppapi.NO_SUCH_ENTRY, govppapi.NO_SUCH_ENTRY}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeNAT44StaticMappingClient{deleteErrors: test.errors}
			if err := deleteNAT44StaticMapping(context.Background(), client, 5, details); err != nil {
				t.Fatalf("deleteNAT44StaticMapping returned %v", err)
			}
		})
	}
}

func TestDeleteNAT44StaticMappingReturnsTransportError(t *testing.T) {
	client := &fakeNAT44StaticMappingClient{deleteErrors: []error{errors.New("connection lost")}}
	if err := deleteNAT44StaticMapping(context.Background(), client, 5, &nat44_ed.Nat44StaticMappingDetails{}); err == nil {
		t.Fatal("deleteNAT44StaticMapping returned nil for a transport error")
	}
}

func TestNAT44ProtocolNumber(t *testing.T) {
	for input, want := range map[string]uint8{
		"tcp":   6,
		" TCP ": 6,
		"udp":   17,
		"Udp":   17,
	} {
		got, err := nat44ProtocolNumber(input)
		if err != nil || got != want {
			t.Errorf("nat44ProtocolNumber(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	if _, err := nat44ProtocolNumber("icmp"); err == nil {
		t.Error("nat44ProtocolNumber(icmp) succeeded; want error")
	}
}

func TestNAT44StaticMappingTags(t *testing.T) {
	if got := nat44StaticMappingTag(12, 3); got != "rookery:12:3" {
		t.Fatalf("tag = %q", got)
	}
	if got := nat44StaticMappingTagPrefix(12); got != "rookery:12:" {
		t.Fatalf("tag prefix = %q", got)
	}
}

func TestNAT44Unavailable(t *testing.T) {
	for _, message := range []string{
		"VPPApiError: No such entry (-6)",
		"NAT44 is not enabled",
		"VPPApiError: Feature disabled (-30)",
	} {
		if !nat44Unavailable(errors.New(message)) {
			t.Errorf("nat44Unavailable(%q) = false", message)
		}
	}
	if nat44Unavailable(errors.New("connection refused")) {
		t.Error("nat44Unavailable(connection refused) = true")
	}
	if !vppRetvalNoSuchEntry(int32(govppapi.NO_SUCH_ENTRY)) {
		t.Error("vppRetvalNoSuchEntry(NO_SUCH_ENTRY) = false")
	}
	if vppRetvalNoSuchEntry(0) {
		t.Error("vppRetvalNoSuchEntry(0) = true")
	}
}

func TestManagedHostInterface(t *testing.T) {
	for _, name := range []string{"vnf1", "tap0", "host-eth0", "bvi1", "vpp0", "ndn1"} {
		if !isManagedHostInterface(name) {
			t.Errorf("%s should be managed", name)
		}
	}
	for _, name := range []string{"eth0", "enp1s0", "docker0", "br0"} {
		if isManagedHostInterface(name) {
			t.Errorf("%s should not be managed", name)
		}
	}
}
