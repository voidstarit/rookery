package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/voidstarit/rookery/internal/store"
)

func TestStartupScriptRunsAndRecordsResult(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "startup-script.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.SetStartupScript(context.Background(), true, "printf 'socket=%s' \"$ROOKERY_VPP_SOCKET\""); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: st, vppSocket: "/run/vpp/api.sock", logger: zerolog.Nop()}
	server.runRookeryStartupScript()

	got, err := st.GetStartupScript(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "success" || got.ExitCode != 0 {
		t.Fatalf("startup script result = status %q, exit %d, want success/0", got.Status, got.ExitCode)
	}
	if !strings.Contains(got.Output, "socket=/run/vpp/api.sock") {
		t.Fatalf("startup script output = %q", got.Output)
	}
}

func TestStartupScriptRecordsNonZeroExit(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "startup-script-failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.SetStartupScript(context.Background(), true, "echo failed; exit 7"); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: st, logger: zerolog.Nop()}
	server.runRookeryStartupScript()

	got, err := st.GetStartupScript(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.ExitCode != 7 {
		t.Fatalf("startup script result = status %q, exit %d, want failed/7", got.Status, got.ExitCode)
	}
	if !strings.Contains(got.Output, "failed") {
		t.Fatalf("startup script output = %q", got.Output)
	}
}

func TestVPPStartupScriptCallsVPPCTL(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "startup-script-default.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const defaultScript = "# Assign NAT44 workers\nvppctl set nat workers 0\n"
	if _, err := st.SetVPPStartupScript(context.Background(), true, defaultScript); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	capture := filepath.Join(t.TempDir(), "vppctl-args")
	fakeVPPCTL := filepath.Join(binDir, "vppctl")
	if err := os.WriteFile(fakeVPPCTL, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$FAKE_VPPCTL_CAPTURE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_VPPCTL_CAPTURE", capture)

	server := &Server{store: st, logger: zerolog.Nop()}
	server.runVPPStartupScript()

	got, err := st.GetVPPStartupScript(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "success" || got.ExitCode != 0 {
		t.Fatalf("startup script result = status %q, exit %d, want success/0", got.Status, got.ExitCode)
	}
	args, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "set nat workers 0\n" {
		t.Fatalf("vppctl args = %q, want set nat workers 0", string(args))
	}
}
