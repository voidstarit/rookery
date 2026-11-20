package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestServeRejectsInvalidPKIMode(t *testing.T) {
	t.Setenv("ROOKERY_CONFIG", "")
	root := t.TempDir()
	err := runServe([]string{
		"-db", filepath.Join(root, "rookery.db"),
		"-addr", "127.0.0.1:0",
		"-pki-dir", filepath.Join(root, "pki"),
		"-pki-mode", "invalid",
		"-mtls-addr", "127.0.0.1:0",
		"-grpc-mtls-addr", "",
		"-run-dir", filepath.Join(root, "run"),
		"-lightvm-dir", filepath.Join(root, "lightvm"),
		"-memif-dir", filepath.Join(root, "memif"),
		"-vhost-dir", filepath.Join(root, "vhost"),
		"-cgroup-root", filepath.Join(root, "cgroup"),
		"-vpp-socket", filepath.Join(root, "vpp.sock"),
		"-vnf-addr", "unix://" + filepath.Join(root, "vnfs.sock"),
		"-qemu-bin", "/bin/true",
		"-buildah-bin", "/bin/true",
		"-virtiofsd-bin", "/bin/true",
		"-log-level", "error",
	})
	if err == nil || !strings.Contains(err.Error(), "prepare mTLS certificates") {
		t.Fatalf("error = %v, want mTLS certificate error", err)
	}
	if !strings.Contains(err.Error(), "PKI mode") {
		t.Fatalf("error = %v, want invalid PKI mode", err)
	}
}
