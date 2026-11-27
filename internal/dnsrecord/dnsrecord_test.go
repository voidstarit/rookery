package dnsrecord

import "testing"

// TestNormalizeName covers the canonical owner/target forms and the wildcard
// rules the API enforces.
func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "adds the trailing dot and lowercases", input: "VM1.Lab.Test", want: "vm1.lab.test."},
		{name: "keeps a canonical name", input: "vm1.lab.test.", want: "vm1.lab.test."},
		{name: "trims whitespace", input: "  vm1.lab.test  ", want: "vm1.lab.test."},
		{name: "keeps one leading wildcard", input: "*.lab.test", want: "*.lab.test."},
		{name: "rejects an inner wildcard", input: "web.*.test", wantErr: true},
		{name: "rejects a bare wildcard", input: "*", wantErr: true},
		{name: "rejects the root", input: ".", wantErr: true},
		{name: "rejects whitespace", input: "vm 1.test", wantErr: true},
		{name: "rejects empty", input: "   ", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeName(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("NormalizeName(%q) = %q, want an error", test.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeName(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeName(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

// TestNormalizeType covers the case normalization and the accepted set.
func TestNormalizeType(t *testing.T) {
	for _, want := range []string{"A", "AAAA", "CNAME", "TXT", "PTR"} {
		got, err := NormalizeType(" " + want + " ")
		if err != nil || got != want {
			t.Fatalf("NormalizeType(%q) = %q, %v; want %q", want, got, err, want)
		}
	}
	if got, err := NormalizeType("cname"); err != nil || got != "CNAME" {
		t.Fatalf("NormalizeType(cname) = %q, %v; want CNAME", got, err)
	}
	if _, err := NormalizeType("SRV"); err == nil {
		t.Fatal("NormalizeType(SRV) accepted an unsupported type")
	}
}

// TestNormalizeValue covers each type's canonical value and the wildcard rules
// for CNAME and PTR targets.
func TestNormalizeValue(t *testing.T) {
	tests := []struct {
		name       string
		recordType string
		input      string
		want       string
		wantErr    bool
	}{
		{name: "A canonicalizes the address", recordType: "A", input: " 10.20.0.50 ", want: "10.20.0.50"},
		{name: "A rejects IPv6", recordType: "A", input: "fd00::50", wantErr: true},
		{name: "AAAA compresses the address", recordType: "aaaa", input: "FD00:0000::0050", want: "fd00::50"},
		{name: "AAAA rejects IPv4", recordType: "AAAA", input: "10.20.0.50", wantErr: true},
		{name: "CNAME adds the trailing dot", recordType: "CNAME", input: "VM1.Lab.Test", want: "vm1.lab.test."},
		{name: "CNAME rejects a wildcard target", recordType: "CNAME", input: "*.lab.test", wantErr: true},
		{name: "PTR adds the trailing dot", recordType: "PTR", input: "vm1.lab.test", want: "vm1.lab.test."},
		{name: "PTR rejects a wildcard target", recordType: "PTR", input: "*.lab.test", wantErr: true},
		{name: "TXT trims but keeps the text", recordType: "TXT", input: "  v=spf1 -all  ", want: "v=spf1 -all"},
		{name: "unsupported type is rejected", recordType: "SRV", input: "x", wantErr: true},
		{name: "empty value is rejected", recordType: "TXT", input: "   ", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeValue(test.recordType, test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("NormalizeValue(%q, %q) = %q, want an error", test.recordType, test.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeValue(%q, %q): %v", test.recordType, test.input, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeValue(%q, %q) = %q, want %q", test.recordType, test.input, got, test.want)
			}
		})
	}
}

// TestNormalizeValueTXTLength checks the 255-byte TXT limit at its boundary.
func TestNormalizeValueTXTLength(t *testing.T) {
	value := make([]byte, 255)
	for i := range value {
		value[i] = 'a'
	}
	if _, err := NormalizeValue("TXT", string(value)); err != nil {
		t.Fatalf("255-byte TXT value rejected: %v", err)
	}
	if _, err := NormalizeValue("TXT", string(value)+"a"); err == nil {
		t.Fatal("256-byte TXT value accepted")
	}
}

// TestNormalizeTTL covers the API default and the accepted range.
func TestNormalizeTTL(t *testing.T) {
	tests := []struct {
		name    string
		ttl     uint32
		want    uint32
		wantErr bool
	}{
		{name: "zero uses the default", ttl: 0, want: DefaultTTL},
		{name: "default passes through", ttl: DefaultTTL, want: DefaultTTL},
		{name: "minimum passes through", ttl: 1, want: 1},
		{name: "maximum passes through", ttl: MaxTTL, want: MaxTTL},
		{name: "above the maximum is rejected", ttl: MaxTTL + 1, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeTTL(test.ttl)
			if test.wantErr {
				if err == nil {
					t.Fatalf("NormalizeTTL(%d) = %d, want an error", test.ttl, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeTTL(%d): %v", test.ttl, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeTTL(%d) = %d, want %d", test.ttl, got, test.want)
			}
		})
	}
}
