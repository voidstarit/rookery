package diskthrottle

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		limits  Limits
		wantErr string
	}{
		{
			name:   "total bps",
			limits: Limits{BPSTotal: 1_000_000},
		},
		{
			name:   "directional bps",
			limits: Limits{BPSRead: 1_000_000, BPSWrite: 500_000},
		},
		{
			name:   "total bps with directional iops",
			limits: Limits{BPSTotal: 1_000_000, IOPSRead: 100, IOPSWrite: 50},
		},
		{
			name:   "directional bps with total iops",
			limits: Limits{BPSRead: 1_000_000, IOPSTotal: 100},
		},
		{
			name:    "mixed bps modes",
			limits:  Limits{BPSTotal: 1_000_000, BPSRead: 500_000},
			wantErr: "total and read/write",
		},
		{
			name:    "mixed iops modes",
			limits:  Limits{IOPSTotal: 100, IOPSWrite: 10},
			wantErr: "total and read/write",
		},
		{
			name:    "no base",
			limits:  Limits{},
			wantErr: "at least one",
		},
		{
			name:    "negative",
			limits:  Limits{BPSRead: -1},
			wantErr: "must not be negative",
		},
		{
			name:    "max without base",
			limits:  Limits{BPSReadMax: 1_000_000},
			wantErr: "at least one",
		},
		{
			name:    "total max without total base",
			limits:  Limits{BPSRead: 1_000_000, BPSTotalMax: 2_000_000},
			wantErr: "requires a positive total base",
		},
		{
			name:    "max below base",
			limits:  Limits{BPSRead: 1_000_000, BPSReadMax: 500_000},
			wantErr: "at least its base",
		},
		{
			name:    "length without max",
			limits:  Limits{BPSRead: 1_000_000, BPSReadMaxLength: 1},
			wantErr: "requires a max",
		},
		{
			name:    "length above uint max",
			limits:  Limits{BPSRead: 1, BPSReadMax: 1, BPSReadMaxLength: Uint32Max + 1},
			wantErr: "must not exceed",
		},
		{
			name:    "burst overflow",
			limits:  Limits{BPSRead: ThrottleValueMax, BPSReadMax: ThrottleValueMax, BPSReadMaxLength: 2},
			wantErr: "burst limit",
		},
		{
			name:    "base above qemu max",
			limits:  Limits{BPSRead: ThrottleValueMax + 1},
			wantErr: "must not exceed",
		},
		{
			name:    "max above qemu max",
			limits:  Limits{BPSRead: 1, BPSReadMax: ThrottleValueMax + 1},
			wantErr: "must not exceed",
		},
		{
			name:    "iops size without iops",
			limits:  Limits{BPSTotal: 1_000_000, IOPSSize: 4096},
			wantErr: "iops_size requires",
		},
		{
			name:   "iops size with iops",
			limits: Limits{IOPSTotal: 100, IOPSSize: 4096},
		},
		{
			name:   "burst at bound",
			limits: Limits{BPSRead: 1_000_000, BPSReadMax: 2_000_000, BPSReadMaxLength: 500},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.limits.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestQEMUOptions(t *testing.T) {
	limits := Limits{
		BPSTotal:           1_000_000,
		BPSTotalMax:        2_000_000,
		BPSTotalMaxLength:  3,
		IOPSTotal:          500,
		IOPSTotalMax:       600,
		IOPSTotalMaxLength: 4,
		IOPSSize:           4096,
	}
	want := ",throttling.bps-total=1000000,throttling.bps-total-max=2000000,throttling.bps-total-max-length=3,throttling.iops-total=500,throttling.iops-total-max=600,throttling.iops-total-max-length=4,throttling.iops-size=4096"
	if got := limits.QEMUOptions(); got != want {
		t.Fatalf("QEMUOptions() = %q, want %q", got, want)
	}
	if got := (Limits{}).QEMUOptions(); got != "" {
		t.Fatalf("empty QEMUOptions() = %q", got)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	limits := Limits{BPSRead: 1_000_000, IOPSWrite: 50}
	encoded, err := json.Marshal(limits)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "bps_total") {
		t.Fatalf("zero values must be omitted: %s", encoded)
	}
	var decoded Limits
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != limits {
		t.Fatalf("decoded = %+v, want %+v", decoded, limits)
	}
	var zero Limits
	encoded, err = json.Marshal(zero)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "{}" {
		t.Fatalf("zero marshal = %s, want {}", encoded)
	}
}
