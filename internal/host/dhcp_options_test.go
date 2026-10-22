package host

import (
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestNormalizeDHCPOptions(t *testing.T) {
	tests := []struct {
		name   string
		ipv6   bool
		input  store.DHCPOption
		want   store.DHCPOption
		errSub string
	}{
		{
			name:  "domain name string",
			input: store.DHCPOption{Code: 15, Value: " lab.test "},
			want:  store.DHCPOption{Code: 15, Format: "string", Value: "lab.test"},
		},
		{
			name:  "ntp addresses",
			input: store.DHCPOption{Code: 42, Format: "ipv4", Value: "10.20.0.1, 10.20.0.2"},
			want:  store.DHCPOption{Code: 42, Format: "ipv4", Value: "10.20.0.1,10.20.0.2"},
		},
		{
			name:  "domain search list",
			input: store.DHCPOption{Code: 119, Format: "domain-list", Value: "lab.test, apps.lab.test"},
			want:  store.DHCPOption{Code: 119, Format: "domain-list", Value: "lab.test.,apps.lab.test."},
		},
		{
			name:  "hex",
			input: store.DHCPOption{Code: 43, Format: "hex", Value: "01:02:ff"},
			want:  store.DHCPOption{Code: 43, Format: "hex", Value: "0102ff"},
		},
		{
			name:  "uint16",
			input: store.DHCPOption{Code: 150, Format: "uint16", Value: "0x1234"},
			want:  store.DHCPOption{Code: 150, Format: "uint16", Value: "4660"},
		},
		{
			name:  "dhcpv6 domain search",
			ipv6:  true,
			input: store.DHCPOption{Code: 24, Format: "domain-list", Value: "lab.test"},
			want:  store.DHCPOption{Code: 24, Format: "domain-list", Value: "lab.test."},
		},
		{name: "reserved dns", input: store.DHCPOption{Code: 6, Format: "ipv4", Value: "1.1.1.1"}, errSub: "managed by the server"},
		{name: "reserved v6 dns", ipv6: true, input: store.DHCPOption{Code: 23, Format: "ipv6", Value: "fd00::1"}, errSub: "managed by the server"},
		{name: "v4 code too high", input: store.DHCPOption{Code: 300, Format: "string", Value: "x"}, errSub: "between 1 and 254"},
		{name: "bad format", input: store.DHCPOption{Code: 15, Format: "bogus", Value: "x"}, errSub: "format must be"},
		{name: "bad ipv4", input: store.DHCPOption{Code: 42, Format: "ipv4", Value: "not-an-ip"}, errSub: "invalid IP address"},
		{name: "oversized", input: store.DHCPOption{Code: 15, Format: "string", Value: strings.Repeat("x", 300)}, errSub: "value is too long"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options, err := NormalizeDHCPOptions([]store.DHCPOption{test.input}, test.ipv6)
			if test.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), test.errSub) {
					t.Fatalf("error = %v, want containing %q", err, test.errSub)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(options) != 1 || options[0] != test.want {
				t.Fatalf("options = %+v, want %+v", options, test.want)
			}
			if _, err := dhcpOptionData(options[0], test.ipv6); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNormalizeDHCPOptionsRejectsDuplicates(t *testing.T) {
	_, err := NormalizeDHCPOptions([]store.DHCPOption{
		{Code: 15, Value: "lab.test"},
		{Code: 15, Value: "other.test"},
	}, false)
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("error = %v, want duplicate rejection", err)
	}
}
