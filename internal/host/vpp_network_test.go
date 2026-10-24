package host

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckVPPRetval(t *testing.T) {
	if err := checkVPPRetval("op", 0); err != nil {
		t.Fatalf("retval 0 = %v, want nil", err)
	}
	err := checkVPPRetval("create bridge domain", 7)
	if err == nil || !strings.Contains(err.Error(), "retval 7") {
		t.Fatalf("retval 7 error = %v", err)
	}
}

func TestNewVPPNetworkCreatesVhostDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vhost")
	provisioner, err := NewVPPNetwork("/run/vpp/api.sock", dir)
	if err != nil {
		t.Fatalf("NewVPPNetwork: %v", err)
	}
	if provisioner.VhostDir != dir {
		t.Fatalf("VhostDir = %q, want %q", provisioner.VhostDir, dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat vhost dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("vhost path is not a directory")
	}
}

func TestNewVPPNetworkRequiresSocket(t *testing.T) {
	if _, err := NewVPPNetwork("", t.TempDir()); err == nil {
		t.Fatal("NewVPPNetwork accepted an empty socket")
	}
}

func TestIsVPPNotFound(t *testing.T) {
	for _, message := range []string{
		"VPPApiError: Bridge domain not found (-2)",
		"VPPApiError: unknown interface (-1)",
		"VPPApiError: unknown bridge domain",
		"unknown message: delete_vhost_user_if",
	} {
		if !isVPPNotFound(errors.New(message)) {
			t.Errorf("isVPPNotFound(%q) = false", message)
		}
	}
	if isVPPNotFound(errors.New("permission denied")) {
		t.Error("isVPPNotFound(permission denied) = true")
	}
	if isVPPNotFound(nil) {
		t.Error("isVPPNotFound(nil) = true")
	}
}
