package main

import (
	"strings"
	"testing"
)

func TestHostFlagsResolve(t *testing.T) {
	tests := []struct {
		name     string
		flags    hostFlags
		wantVG   string
		wantPool string
		wantErr  string
	}{
		{
			name:     "separate vg and pool",
			flags:    hostFlags{vg: "vg0", thinPool: "rookery", vppSocket: "/run/vpp/api.sock"},
			wantVG:   "vg0",
			wantPool: "rookery",
		},
		{
			name:     "pool includes vg",
			flags:    hostFlags{thinPool: "vg0/rookery", vppSocket: "/run/vpp/api.sock"},
			wantVG:   "vg0",
			wantPool: "rookery",
		},
		{
			name:    "conflicting vg",
			flags:   hostFlags{vg: "other", thinPool: "vg0/rookery"},
			wantErr: "conflicts",
		},
		{
			name:    "too many path parts",
			flags:   hostFlags{thinPool: "vg0/pool/extra"},
			wantErr: "must be <name> or <vg>/<name>",
		},
		{
			name:    "vg is a path",
			flags:   hostFlags{vg: "vg0/path", thinPool: "rookery"},
			wantErr: "not a path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vg, pool, socket, err := test.flags.resolve()
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if vg != test.wantVG || pool != test.wantPool {
				t.Fatalf("resolved = %q/%q, want %q/%q", vg, pool, test.wantVG, test.wantPool)
			}
			if socket != "/run/vpp/api.sock" {
				t.Fatalf("socket = %q", socket)
			}
		})
	}
}
