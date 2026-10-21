package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/voidstarit/rookery/internal/host"
)

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	var hostCfg hostFlags
	hostCfg.register(fs)

	size := fs.String("size", host.DefaultThinPoolSize, "thin pool size for creation (LVM --size or --extents syntax)")

	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: rookery init [flags]\n\n"+
			"Idempotently create the LVM thin pool if needed and verify the VPP\n"+
			"binary API socket with a GoVPP control ping. Re-running is safe.\n\nFlags:\n")
		fs.PrintDefaults()
	}

	if err := parseFlags(fs, args); err != nil {
		return err
	}

	vg, thinPool, vppSocket, err := hostCfg.resolve()
	if err != nil {
		return err
	}
	if vg == "" {
		return fmt.Errorf("volume group is required; pass -vg or -thin-pool <vg>/<name>")
	}
	if thinPool == "" {
		return fmt.Errorf("thin pool name is required; pass -thin-pool")
	}
	if err := host.ValidateLVMName("volume group", vg); err != nil {
		return err
	}
	if err := host.ValidateLVMName("thin pool", thinPool); err != nil {
		return err
	}
	if strings.TrimSpace(vppSocket) == "" {
		return fmt.Errorf("VPP API socket is required; pass -vpp-socket")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	lvm := host.NewLVM()
	created, err := lvm.EnsureThinPool(ctx, vg, thinPool, *size)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("thin pool %s/%s: created\n", vg, thinPool)
	} else {
		fmt.Printf("thin pool %s/%s: already exists\n", vg, thinPool)
	}

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()

	if err := host.PingVPP(pingCtx, vppSocket); err != nil {
		return fmt.Errorf("FD.io VPP check failed: %w", err)
	}
	fmt.Printf("FD.io VPP: control ping succeeded via %s\n", vppSocket)

	fmt.Printf("\ninitialization complete\nstart the server with:\n  rookery serve -vg %s -thin-pool %s -vpp-socket %s\n",
		vg, thinPool, vppSocket)
	return nil
}
