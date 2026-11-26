package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/voidstarit/rookery/internal/config"
)

type hostFlags struct {
	vg        string
	thinPool  string
	vppSocket string
}

// register defines -vg, -thin-pool, and -vpp-socket on fs, seeding defaults
// from cfg and the matching ROOKERY_* environment variables.
func (h *hostFlags) register(fs *flag.FlagSet, cfg config.API) {
	fs.StringVar(&h.vg, "vg",
		config.EnvString(cfg.VG, "ROOKERY_VG", ""),
		"LVM volume group (or pass -thin-pool <vg>/<name>)")
	fs.StringVar(&h.thinPool, "thin-pool",
		config.EnvString(cfg.ThinPool, "ROOKERY_THIN_POOL", "rookery"),
		"LVM thin pool name, optionally as <vg>/<name>")
	fs.StringVar(&h.vppSocket, "vpp-socket",
		config.EnvString(cfg.VPPSocket, "ROOKERY_VPP_SOCKET", "/run/vpp/api.sock"),
		"VPP binary API socket (not the CLI or vhost-user socket)")
}

// resolve trims the host flags and accepts -thin-pool as either a name or
// <vg>/<name>, rejecting a path-like -vg, a malformed thin pool, or a volume
// group that conflicts with the thin-pool prefix.
func (h *hostFlags) resolve() (vg, thinPool, socket string, err error) {
	vg = strings.TrimSpace(h.vg)
	thinPool = strings.TrimSpace(h.thinPool)
	socket = strings.TrimSpace(h.vppSocket)

	if strings.Contains(vg, "/") {
		return "", "", "", fmt.Errorf("-vg must be a volume group name, not a path")
	}

	if strings.Contains(thinPool, "/") {
		parts := strings.Split(thinPool, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", "", fmt.Errorf("-thin-pool must be <name> or <vg>/<name>")
		}
		if vg != "" && vg != parts[0] {
			return "", "", "", fmt.Errorf("-vg %q conflicts with -thin-pool %q", vg, thinPool)
		}
		vg = parts[0]
		thinPool = parts[1]
	}

	return vg, thinPool, socket, nil
}
