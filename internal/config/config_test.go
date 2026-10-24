package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `
api:
  vg: vg0
  thin_pool: rookery
  vpp_socket: /run/vpp/api.sock
  addr: 127.0.0.1:8080
  rate_limit: 0
  check_interval: 15s
vnf:
  vpp_socket: /run/vpp/api.sock
  listen: unix:///run/rookery/vnfs.sock
  log_level: debug
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.API == nil || file.VNF == nil {
		t.Fatalf("missing sections: %+v", file)
	}
	if got := String(file.API.VG, ""); got != "vg0" {
		t.Fatalf("vg = %q", got)
	}
	if got := FloatString(file.API.RateLimit, ""); got != "0" {
		t.Fatalf("rate limit = %q", got)
	}
	duration, err := Duration(file.API.CheckInterval, 10*time.Second)
	if err != nil || duration != 15*time.Second {
		t.Fatalf("duration = %s, %v", duration, err)
	}
	if got := String(file.VNF.LogLevel, ""); got != "debug" {
		t.Fatalf("log level = %q", got)
	}
}

func TestLoadUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("api:\n  typo: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestLoadEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.API != nil || file.VNF != nil {
		t.Fatalf("unexpected config: %+v", file)
	}
}

func TestPath(t *testing.T) {
	t.Setenv("ROOKERY_CONFIG", "/env/config.yaml")
	if got := Path([]string{"serve", "-config", "/flag/config.yaml"}); got != "/flag/config.yaml" {
		t.Fatalf("path = %q", got)
	}
	if got := Path([]string{"serve", "--config=/flag/config.yaml"}); got != "/flag/config.yaml" {
		t.Fatalf("path = %q", got)
	}
	if got := Path([]string{"serve"}); got != "/env/config.yaml" {
		t.Fatalf("path = %q", got)
	}
}
