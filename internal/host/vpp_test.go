package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPingVPPRequiresSocket(t *testing.T) {
	err := PingVPP(context.Background(), "  ")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v, want not configured", err)
	}
}

func TestPingVPPMissingSocket(t *testing.T) {
	err := PingVPP(context.Background(), filepath.Join(t.TempDir(), "missing.sock"))
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("error = %v, want missing socket error", err)
	}
}

func TestPingVPPRejectsRegularFile(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "not-a-socket")
	if err := os.WriteFile(socket, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	err := PingVPP(context.Background(), socket)
	if err == nil || !strings.Contains(err.Error(), "not a unix socket") {
		t.Fatalf("error = %v, want non-socket error", err)
	}
}
