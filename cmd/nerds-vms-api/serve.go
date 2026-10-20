package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/voidstarit/rookery/internal/api"
	"github.com/voidstarit/rookery/internal/gateway"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/lifecycle"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var hostCfg hostFlags
	hostCfg.register(fs)

	addr := fs.String("addr", envDefault("NERDS_VMS_ADDR", "127.0.0.1:8080"), "HTTP listen address")
	gatewayAddr := fs.String("gateway-addr", envDefault("NERDS_VMS_GATEWAY_ADDR", ""), "optional grpc-gateway HTTP listen address")
	grpcAddr := fs.String("grpc-addr", envDefault("NERDS_VMS_GRPC_ADDR", ""), "optional gRPC listen address")
	dbPath := fs.String("db", envDefault("NERDS_VMS_DB", "nerds-vms.db"), "SQLite database path")
	rateLimitValue := fs.String("rate-limit", envDefault("NERDS_VMS_RATE_LIMIT", "3"), "global HTTP request rate limit in requests per second (0 disables)")
	dhcpTransport := fs.String("dhcp-transport", envDefault("NERDS_VMS_DHCP_TRANSPORT", "memif"), "DHCP VNF transport: memif or tap")
	checkInterval := fs.Duration("check-interval", 10*time.Second, "readiness check interval")
	qemuBin := fs.String("qemu-bin", envDefault("NERDS_VMS_QEMU_BIN", qemu.DefaultBinary), "QEMU system emulator binary")
	runDir := fs.String("run-dir", envDefault("NERDS_VMS_RUN_DIR", qemu.DefaultRunDir), "directory for QEMU pid files and VNC sockets")
	memifDir := fs.String("memif-dir", envDefault("ROOKERY_MEMIF_DIR", envDefault("NERDS_VMS_MEMIF_DIR", "/run/rookery/memif")), "directory for VNF memif sockets")
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

	rateLimit, err := strconv.ParseFloat(strings.TrimSpace(*rateLimitValue), 64)
	if err != nil || math.IsNaN(rateLimit) || math.IsInf(rateLimit, 0) || rateLimit < 0 {
		return fmt.Errorf("-rate-limit must be a non-negative number, got %q", *rateLimitValue)
	}
	transport := strings.ToLower(strings.TrimSpace(*dhcpTransport))
	if transport != "tap" && transport != "memif" {
		return fmt.Errorf("-dhcp-transport must be tap or memif, got %q", *dhcpTransport)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if rateLimit <= 0 {
		logger.Warn("HTTP rate limiting disabled", "rate_limit_rps", rateLimit)
	}

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

	diskRunner := host.NewLVM()
	apiServer := api.New(st, manager, diskRunner, qemuRunner, networkRunner, vnfManager, networkRunner, logger)
	apiServer.ConfigureMemifDHCP(transport == "memif", *memifDir)
	lifecycleService := lifecycle.New(st, apiServer, apiServer.ResourceMutex())

	gatewayOptions := []gateway.Option{
		gateway.WithLogger(logger),
		gateway.WithVPPInterfaceSource(networkRunner),
		gateway.WithVMController(qemuRunner),
		gateway.WithInterfaceCounters(networkRunner),
		gateway.WithACLRuntime(networkRunner),
		gateway.WithDiskProvisioner(diskRunner),
		gateway.WithDiskMutex(apiServer.ResourceMutex()),
		gateway.WithVMMutex(apiServer.RunMutex()),
		gateway.WithInterfaceRuntime(networkRunner),
		gateway.WithDiagnosticsRuntime(networkRunner),
		gateway.WithNetworkRuntime(networkRunner), gateway.WithNetworkWriter(lifecycleService),
		gateway.WithVNFRuntime(vnfManager),
		gateway.WithVNFStatusProvider(apiServer),
		gateway.WithVNFWriter(lifecycleService),
	}
	var grpcListener net.Listener
	if *grpcAddr != "" {
		listener, err := net.Listen("tcp", *grpcAddr)
		if err != nil {
			return fmt.Errorf("listen gRPC: %w", err)
		}
		grpcListener = listener
		gatewayOptions = append(gatewayOptions, gateway.WithGRPCListener(listener))
	}
	gatewayRuntime, err := gateway.New(st, manager, gatewayOptions...)
	if err != nil {
		if grpcListener != nil {
			_ = grpcListener.Close()
		}
		return fmt.Errorf("prepare grpc-gateway: %w", err)
	}
	defer gatewayRuntime.Close()
	gatewayHandler := apiServer.LogRequests(api.SecurityHeaders(gatewayRuntime.Handler))
	rateLimiter := api.NewPathRateLimiter(rateLimit)

	var gatewayHTTP *http.Server
	if *gatewayAddr != "" {
		gatewayHTTP = &http.Server{
			Addr:              *gatewayAddr,
			Handler:           rateLimiter.Middleware(gatewayHandler),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           rateLimiter.Middleware(api.CutoverHandler(gatewayHandler, apiServer.NativeHandler())),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go manager.Run(ctx)
	go apiServer.RunInfrastructureReconciler(ctx)

	servers := []*http.Server{srv}
	if gatewayHTTP != nil {
		servers = append(servers, gatewayHTTP)
	}
	serveErr := make(chan error, len(servers))
	for _, server := range servers {
		go func(server *http.Server) {
			serveErr <- server.ListenAndServe()
		}(server)
	}

	grpcAddress := ""
	if grpcListener != nil {
		grpcAddress = gatewayRuntime.GRPCAddress()
	}
	logger.Info("nerds-vms API listening",
		"addr", httpURL(*addr),
		"gateway_addr", *gatewayAddr,
		"grpc_addr", grpcAddress,
		"database", *dbPath,
		"vg", vg,
		"thin_pool", thinPool,
		"vpp_socket", vppSocket,
		"qemu_bin", *qemuBin,
		"run_dir", *runDir,
		"memif_dir", *memifDir,
		"vhost_dir", *vhostDir,
		"vnf_dir", filepath.Join(*runDir, "vnfs"),
		"vnf_backend", "coredhcp",
		"rate_limit_rps", rateLimit,
		"rate_limit_scope", "path",
		"dhcp_transport", transport,
	)

	var runErr error
	select {
	case err := <-serveErr:
		runErr = err
	case <-ctx.Done():
		logger.Info("shutdown requested")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil && runErr == nil {
			runErr = fmt.Errorf("graceful shutdown: %w", err)
		}
	}
	for range servers {
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) && runErr == nil {
			runErr = err
		}
	}

	if runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
		return fmt.Errorf("listen: %w", runErr)
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
