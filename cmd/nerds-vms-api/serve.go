package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/voidstarit/rookery/internal/api"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var hostCfg hostFlags
	hostCfg.register(fs)

	addr := fs.String("addr", envDefault("NERDS_VMS_ADDR", "127.0.0.1:8080"), "HTTP listen address")
	dbPath := fs.String("db", envDefault("NERDS_VMS_DB", "nerds-vms.db"), "SQLite database path")
	checkInterval := fs.Duration("check-interval", 10*time.Second, "readiness check interval")
	qemuBin := fs.String("qemu-bin", envDefault("NERDS_VMS_QEMU_BIN", qemu.DefaultBinary), "QEMU system emulator binary")
	runDir := fs.String("run-dir", envDefault("NERDS_VMS_RUN_DIR", qemu.DefaultRunDir), "directory for QEMU pid files and VNC sockets")
	vhostDir := fs.String("vhost-dir", envDefault("NERDS_VMS_VHOST_DIR", host.DefaultVhostDir), "directory for VPP vhost-user sockets")

	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: nerds-vms-api serve [flags]\n\n"+
			"Start the VM management API and embedded UI. The server starts even\n"+
			"when prerequisites are unavailable; creation is blocked until ready.\n\nFlags:\n")
		fs.PrintDefaults()
	}

	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *checkInterval <= 0 {
		return errors.New("-check-interval must be positive")
	}

	vg, thinPool, vppSocket, err := hostCfg.resolve()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	if vg != "" && thinPool != "" {
		if _, err := st.EnsureDefaultStorageBackend(context.Background(), vg, thinPool); err != nil {
			return fmt.Errorf("bootstrap storage backend: %w", err)
		}
	} else {
		backends, err := st.ListStorageBackends(context.Background())
		if err != nil {
			return fmt.Errorf("list storage backends: %w", err)
		}
		for _, backend := range backends {
			if backend.Driver == "lvm-thin" && backend.Config.VG != "" && backend.Config.ThinPool != "" {
				vg = backend.Config.VG
				thinPool = backend.Config.ThinPool
				break
			}
		}
		if vg == "" {
			thinPool = ""
		}
	}

	qemuRunner, err := qemu.New(*qemuBin, *runDir)
	if err != nil {
		return fmt.Errorf("prepare qemu: %w", err)
	}
	networkRunner, err := host.NewVPPNetwork(vppSocket, *vhostDir)
	if err != nil {
		return fmt.Errorf("prepare VPP networking: %w", err)
	}
	vnfManager, err := host.NewVNFManager(*runDir)
	if err != nil {
		return fmt.Errorf("prepare network functions: %w", err)
	}
	defer func() {
		if err := vnfManager.Close(); err != nil {
			logger.Error("stop network functions", "error", err)
		}
	}()

	manager := readiness.NewManager(
		readiness.Config{
			VG:        vg,
			ThinPool:  thinPool,
			VPPSocket: vppSocket,
		},
		host.NewChecker(),
		logger,
		*checkInterval,
	)

	apiServer := api.New(st, manager, host.NewLVM(), qemuRunner, networkRunner, vnfManager, networkRunner, logger)
	srv := &http.Server{
		Addr:              *addr,
		Handler:           apiServer,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go manager.Run(ctx)
	go apiServer.RunInfrastructureReconciler(ctx)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	logger.Info("nerds-vms API listening",
		"addr", httpURL(*addr),
		"database", *dbPath,
		"vg", vg,
		"thin_pool", thinPool,
		"vpp_socket", vppSocket,
		"qemu_bin", *qemuBin,
		"run_dir", *runDir,
		"vhost_dir", *vhostDir,
		"vnf_dir", filepath.Join(*runDir, "vnfs"),
		"vnf_backend", "coredhcp",
	)

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown requested")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen: %w", err)
	}

	logger.Info("stopped")
	return nil
}

func httpURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}
