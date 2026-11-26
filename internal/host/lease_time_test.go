package host

import (
	"testing"
	"time"
)

func TestDecodeLeaseTime(t *testing.T) {
	tests := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{value: "12h", want: 12 * time.Hour, ok: true},
		{value: "1h30m", want: 90 * time.Minute, ok: true},
		{value: "45m", want: 45 * time.Minute, ok: true},
		{value: "90s", want: 90 * time.Second, ok: true},
		// Legacy compact forms written by the pre-split API.
		{value: "1h3", want: 90 * time.Minute, ok: true},
		{value: "3", want: 30 * time.Minute, ok: true},
		{value: "1h1", want: 70 * time.Minute, ok: true},
		{value: "2h2", want: 140 * time.Minute, ok: true},
		{value: "1h4", want: 100 * time.Minute, ok: true},
		{value: "5", want: 50 * time.Minute, ok: true},
		{value: "40m1", want: 40*time.Minute + 10*time.Second, ok: true},
		{value: "1h0m1", want: time.Hour + 10*time.Second, ok: true},
		// Invalid values.
		{value: "", ok: false},
		{value: "soon", ok: false},
		{value: "0", ok: false},
		{value: "00", ok: false},
		{value: "-1h", ok: false},
		{value: "0s", ok: false},
	}
	for _, test := range tests {
		got, err := DecodeLeaseTime(test.value)
		if test.ok {
			if err != nil {
				t.Errorf("DecodeLeaseTime(%q) error = %v", test.value, err)
				continue
			}
			if got != test.want {
				t.Errorf("DecodeLeaseTime(%q) = %v, want %v", test.value, got, test.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("DecodeLeaseTime(%q) = %v, want an error", test.value, got)
		}
	}
}

func TestVNFLeaseTimeDefaults(t *testing.T) {
	if got, err := vnfLeaseTime(""); err != nil || got != coreDHCPLeaseTime {
		t.Fatalf("vnfLeaseTime(empty) = %v, err = %v", got, err)
	}
	if got, err := vnfLeaseTime("1h3"); err != nil || got != 90*time.Minute {
		t.Fatalf("vnfLeaseTime(1h3) = %v, err = %v", got, err)
	}
	if _, err := vnfLeaseTime("nope"); err == nil {
		t.Fatal("vnfLeaseTime accepted an invalid duration")
	}
}
