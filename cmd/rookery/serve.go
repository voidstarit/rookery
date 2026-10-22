package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
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
	"github.com/voidstarit/rookery/internal/logging"
	"github.com/voidstarit/rookery/internal/pki"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/readiness"
	"github.com/voidstarit/rookery/internal/store"
	"github.com/voidstarit/rookery/internal/vnfd"
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	var hostCfg hostFlags
	hostCfg.register(fs)

	addr := fs.String("addr", envDefault("ROOKERY_ADDR", "127.0.0.1:8080"), "HTTP listen address")
	gatewayAddr := fs.String("gateway-addr", envDefault("ROOKERY_GATEWAY_ADDR", ""), "optional grpc-gateway HTTP listen address")
	grpcAddr := fs.String("grpc-addr", envDefault("ROOKERY_GRPC_ADDR", ""), "optional gRPC listen address")
	dbPath := fs.String("db", envDefault("ROOKERY_DB", "nerds-vms.db"), "SQLite database path")
	rateLimitValue := fs.String("rate-limit", envDefault("ROOKERY_RATE_LIMIT", "3"), "global HTTP request rate limit in requests per second (0 disables)")
	dhcpTransport := fs.String("dhcp-transport", envDefault("ROOKERY_DHCP_TRANSPORT", "memif"), "DHCP VNF transport: memif or tap")
	vnfAddr := fs.String("vnf-addr", envDefault("ROOKERY_VNF_ADDR", "unix:///run/rookery/vnfs.sock"), "VNF daemon control address (unix:///path or tcp://host:port)")
	checkInterval := fs.Duration("check-interval", 10*time.Second, "readiness check interval")
	qemuBin := fs.String("qemu-bin", envDefault("ROOKERY_QEMU_BIN", qemu.DefaultBinary), "QEMU system emulator binary")
	runDir := fs.String("run-dir", envDefault("ROOKERY_RUN_DIR", qemu.DefaultRunDir), "directory for QEMU pid files and VNC sockets")
	memifDir := fs.String("memif-dir", envDefault("ROOKERY_MEMIF_DIR", "/run/rookery/memif"), "directory for VNF memif sockets")
	vhostDir := fs.String("vhost-dir", envDefault("ROOKERY_VHOST_DIR", host.DefaultVhostDir), "directory for VPP vhost-user sockets")
	logLevel := fs.String("log-level", envDefault("ROOKERY_LOG_LEVEL", "info"), "log level: trace, debug, info, warn, error")
	logFormat := fs.String("log-format", envDefault("ROOKERY_LOG_FORMAT", "console"), "log format: console or json")
	mtlsAddr := fs.String("mtls-addr", envDefault("ROOKERY_MTLS_ADDR", ":8443"), "public mTLS API listen address (empty disables)")
	pkiDir := fs.String("pki-dir", envDefault("ROOKERY_PKI_DIR", "/var/lib/rookery/pki"), "directory for the mTLS CA and server certificate")
	mtlsSANs := fs.String("mtls-sans", envDefault("ROOKERY_MTLS_SANS", ""), "comma-separated extra DNS/IP SANs for the mTLS server certificate")
	grpcMTLSAddr := fs.String("grpc-mtls-addr", envDefault("ROOKERY_GRPC_MTLS_ADDR", ":9443"), "public mTLS gRPC listen address (empty disables)")

	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: rookery serve [flags]\n\n"+
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

	logger, err := logging.Setup(logging.Options{Level: *logLevel, Format: *logFormat, Service: "rookery"})
	if err != nil {
		return err
	}

	if rateLimit <= 0 {
		logger.Warn().Float64("rate_limit_rps", rateLimit).Msg("HTTP rate limiting disabled")
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
	vnfClient, err := vnfd.NewClient(*vnfAddr, logger)
	if err != nil {
		return fmt.Errorf("prepare VNF daemon client: %w", err)
	}
	defer vnfClient.Close()

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
	apiServer := api.New(st, manager, diskRunner, qemuRunner, networkRunner, vnfClient, networkRunner, logger)
	apiServer.ConfigureMemifDHCP(transport == "memif", *memifDir)
	var pkiManager *pki.Manager
	if strings.TrimSpace(*mtlsAddr) != "" || strings.TrimSpace(*grpcMTLSAddr) != "" {
		extraSANs := make([]string, 0)
		for _, san := range strings.Split(*mtlsSANs, ",") {
			if san = strings.TrimSpace(san); san != "" {
				extraSANs = append(extraSANs, san)
			}
		}
		pkiManager, err = pki.New(*pkiDir, st, extraSANs, logger)
		if err != nil {
			return fmt.Errorf("prepare mTLS certificates: %w", err)
		}
		apiServer.ConfigurePKI(pkiManager)
	}
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
		gateway.WithHostWriter(networkRunner),
		gateway.WithHostTapWriter(lifecycleService),
		gateway.WithDiagnosticsRuntime(networkRunner),
		gateway.WithNetworkRuntime(networkRunner), gateway.WithNetworkWriter(lifecycleService),
		gateway.WithVNFRuntime(vnfClient),
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
	var grpcMTLSListener net.Listener
	if pkiManager != nil && strings.TrimSpace(*grpcMTLSAddr) != "" {
		listener, err := net.Listen("tcp", *grpcMTLSAddr)
		if err != nil {
			if grpcListener != nil {
				_ = grpcListener.Close()
			}
			return fmt.Errorf("listen mTLS gRPC: %w", err)
		}
		grpcMTLSListener = tls.NewListener(listener, pkiManager.TLSConfig())
		gatewayOptions = append(gatewayOptions, gateway.WithExtraGRPCListener(grpcMTLSListener))
	}
	gatewayRuntime, err := gateway.New(st, manager, gatewayOptions...)
	if err != nil {
		if grpcListener != nil {
			_ = grpcListener.Close()
		}
		if grpcMTLSListener != nil {
			_ = grpcMTLSListener.Close()
		}
		return fmt.Errorf("prepare grpc-gateway: %w", err)
	}
	defer gatewayRuntime.Close()
	gatewayHandler := apiServer.LogRequests(api.SecurityHeaders(gatewayRuntime.Handler))
	rateLimiter := api.NewPathRateLimiter(rateLimit)

	var mtlsHTTP *http.Server
	if pkiManager != nil && strings.TrimSpace(*mtlsAddr) != "" {
		mtlsHTTP = &http.Server{
			Addr:              *mtlsAddr,
			Handler:           rateLimiter.Middleware(gatewayHandler),
			TLSConfig:         pkiManager.TLSConfig(),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
	}

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
	if mtlsHTTP != nil {
		servers = append(servers, mtlsHTTP)
	}
	serveErr := make(chan error, len(servers))
	for _, server := range servers {
		go func(server *http.Server) {
			if server == mtlsHTTP {
				serveErr <- server.ListenAndServeTLS("", "")
				return
			}
			serveErr <- server.ListenAndServe()
		}(server)
	}

	grpcAddress := ""
	if grpcListener != nil {
		grpcAddress = gatewayRuntime.GRPCAddress()
	}
	logger.Info().
		Str("addr", httpURL(*addr)).
		Str("gateway_addr", *gatewayAddr).
		Str("grpc_addr", grpcAddress).
		Str("mtls_addr", *mtlsAddr).
		Str("grpc_mtls_addr", *grpcMTLSAddr).
		Str("pki_dir", *pkiDir).
		Str("database", *dbPath).
		Str("vg", vg).
		Str("thin_pool", thinPool).
		Str("vpp_socket", vppSocket).
		Str("qemu_bin", *qemuBin).
		Str("run_dir", *runDir).
		Str("memif_dir", *memifDir).
		Str("vhost_dir", *vhostDir).
		Str("vnf_dir", filepath.Join(*runDir, "vnfs")).
		Str("vnf_backend", "coredhcp").
		Float64("rate_limit_rps", rateLimit).
		Str("rate_limit_scope", "path").
		Str("dhcp_transport", transport).
		Msg("rookery API listening")

	var runErr error
	select {
	case err := <-serveErr:
		runErr = err
	case <-ctx.Done():
		logger.Info().Msg("shutdown requested")
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
	logger.Info().Msg("stopped")
	return nil
}

func httpURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}
