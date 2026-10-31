package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// File is the optional YAML configuration shared by the API and VNF daemon.
// Both binaries accept -config (or ROOKERY_CONFIG) pointing at the same file.
type File struct {
	API *API `yaml:"api"`
	VNF *VNF `yaml:"vnf"`
}

// API configures rookery serve and rookery init. Pointer fields distinguish an
// omitted setting from an intentional empty string or zero value.
type API struct {
	VG            *string  `yaml:"vg"`
	ThinPool      *string  `yaml:"thin_pool"`
	VPPSocket     *string  `yaml:"vpp_socket"`
	Addr          *string  `yaml:"addr"`
	GatewayAddr   *string  `yaml:"gateway_addr"`
	GRPCAddr      *string  `yaml:"grpc_addr"`
	DB            *string  `yaml:"db"`
	RateLimit     *float64 `yaml:"rate_limit"`
	DHCPTransport *string  `yaml:"dhcp_transport"`
	VNFAddr       *string  `yaml:"vnf_addr"`
	CheckInterval *string  `yaml:"check_interval"`
	QEMUBin       *string  `yaml:"qemu_bin"`
	RunDir        *string  `yaml:"run_dir"`
	LightVMDir    *string  `yaml:"lightvm_dir"`
	BuildahBin    *string  `yaml:"buildah_bin"`
	VirtiofsdBin  *string  `yaml:"virtiofsd_bin"`
	CgroupRoot    *string  `yaml:"cgroup_root"`
	MemifDir      *string  `yaml:"memif_dir"`
	VhostDir      *string  `yaml:"vhost_dir"`
	LogLevel      *string  `yaml:"log_level"`
	LogFormat     *string  `yaml:"log_format"`
	MTLSAddr      *string  `yaml:"mtls_addr"`
	PKIDir        *string  `yaml:"pki_dir"`
	MTLSSANs      *string  `yaml:"mtls_sans"`
	GRPCMTLSAddr  *string  `yaml:"grpc_mtls_addr"`
	InitSize      *string  `yaml:"init_size"`
}

// VNF configures the vnfs daemon.
type VNF struct {
	VPPSocket *string `yaml:"vpp_socket"`
	Listen    *string `yaml:"listen"`
	RunDir    *string `yaml:"run_dir"`
	VhostDir  *string `yaml:"vhost_dir"`
	LogLevel  *string `yaml:"log_level"`
	LogFormat *string `yaml:"log_format"`
}

// Load reads a YAML configuration file. An empty path means no file and returns
// an empty configuration. Unknown fields are rejected.
func Load(path string) (File, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return File{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read config %s: %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var file File
	if err := decoder.Decode(&file); err != nil {
		if errors.Is(err, io.EOF) {
			return File{}, nil
		}
		return File{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return File{}, fmt.Errorf("parse config %s: multiple YAML documents are not supported", path)
	} else if !errors.Is(err, io.EOF) {
		return File{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return file, nil
}

// Path returns the configuration path from -config/--config or ROOKERY_CONFIG.
// A command-line flag takes precedence over the environment.
func Path(args []string) string {
	path := EnvDefault("ROOKERY_CONFIG", "")
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-config" || arg == "--config":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "-config="):
			path = strings.TrimPrefix(arg, "-config=")
		case strings.HasPrefix(arg, "--config="):
			path = strings.TrimPrefix(arg, "--config=")
		}
	}
	return strings.TrimSpace(path)
}

// EnvDefault returns the trimmed environment value when non-empty, otherwise
// fallback. Empty environment variables mean "not set".
func EnvDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// String returns the configured string when present, otherwise fallback.
func String(value *string, fallback string) string {
	if value != nil {
		return *value
	}
	return fallback
}

// EnvString applies environment precedence over a configured string.
func EnvString(value *string, envKey, fallback string) string {
	return EnvDefault(envKey, String(value, fallback))
}

// FloatString returns a configured number formatted for a string flag.
func FloatString(value *float64, fallback string) string {
	if value == nil {
		return fallback
	}
	return strconv.FormatFloat(*value, 'g', -1, 64)
}

// EnvFloat applies environment precedence over a configured number.
func EnvFloat(value *float64, envKey, fallback string) string {
	return EnvDefault(envKey, FloatString(value, fallback))
}

// Duration parses a configured duration, or returns fallback when unset.
func Duration(value *string, fallback time.Duration) (time.Duration, error) {
	if value == nil {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(*value))
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", *value, err)
	}
	return parsed, nil
}
