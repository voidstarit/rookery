// Command vnfs runs the userspace network function daemon.
//
// It owns the long-running DHCP and BGP runtimes (CoreDHCP, gVisor memif
// stacks, GoBGP) and is controlled by rookery over an internal gRPC
// channel, so API restarts do not interrupt running VNFs. VPP provisioning
// (memif/tap interfaces, BVIs, NAT44) stays in the API; this daemon only opens
// the VPP binary API socket to install/withdraw BGP-learned routes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/voidstarit/rookery/internal/logging"
	"google.golang.org/grpc"

	rookeryv1 "github.com/voidstarit/rookery/design/gen/rookery/v1"
	"github.com/voidstarit/rookery/internal/host"
	"github.com/voidstarit/rookery/internal/qemu"
	"github.com/voidstarit/rookery/internal/vnfd"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("vnfs", flag.ContinueOnError)
	vppSocket := fs.String("vpp-socket", envDefault("ROOKERY_VPP_SOCKET", "/run/vpp/api.sock"), "VPP binary API socket used for BGP route installs")
	listen := fs.String("listen", envDefault("ROOKERY_VNF_LISTEN", "unix:///run/rookery/vnfs.sock"), "control listen address (unix:///path or tcp://host:port)")
	runDir := fs.String("run-dir", envDefault("ROOKERY_RUN_DIR", qemu.DefaultRunDir), "directory for VNF lease and state files")
	vhostDir := fs.String("vhost-dir", envDefault("ROOKERY_VHOST_DIR", host.DefaultVhostDir), "directory for VPP vhost-user sockets")
	logLevel := fs.String("log-level", envDefault("ROOKERY_LOG_LEVEL", "info"), "log level: trace, debug, info, warn, error")
	logFormat := fs.String("log-format", envDefault("ROOKERY_LOG_FORMAT", "console"), "log format: console or json")

	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: vnfs [flags]\n\n"+
			"Run the DHCP/BGP userspace daemon. The API server owns the database\n"+
			"and VPP provisioning and drives this daemon over gRPC.\n\nFlags:\n")
		fs.PrintDefaults()
	}

	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(os.Stdout)
			fs.Usage()
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	logger, err := logging.Setup(logging.Options{Level: *logLevel, Format: *logFormat, Service: "vnfs"})
	if err != nil {
		return err
	}

	manager, err := host.NewVNFManager(*runDir)
	if err != nil {
		return fmt.Errorf("prepare VNF runtime: %w", err)
	}
	networkRunner, err := host.NewVPPNetwork(*vppSocket, *vhostDir)
	if err != nil {
		return fmt.Errorf("prepare VPP route writer: %w", err)
	}
	manager.SetBGPRouteWriter(networkRunner)

	server := vnfd.NewServer(manager, manager.Dir, logger)
	if err := server.Load(); err != nil {
		logger.Warn().Err(err).Msg("could not restore persisted VNF specs")
	}

	listener, err := vnfd.Listen(*listen)
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(vnfd.TraceInterceptor(logger)))
	rookeryv1.RegisterVNFDaemonServer(grpcServer, server)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Supervise(ctx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- grpcServer.Serve(listener)
	}()
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signalCh)

	logger.Info().
		Str("addr", *listen).
		Str("vpp_socket", *vppSocket).
		Str("state_dir", manager.Dir).
		Msg("VNF daemon listening")

	select {
	case err := <-errCh:
		cancel()
		_ = server.Close()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("serve VNF daemon: %w", err)
		}
		return nil
	case sig := <-signalCh:
		logger.Info().Str("signal", sig.String()).Msg("shutting down VNF daemon")
	}
	cancel()
	grpcServer.GracefulStop()
	_ = listener.Close()
	if err := server.Close(); err != nil {
		logger.Warn().Err(err).Msg("stopping VNF runtimes")
	}
	return nil
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
