package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.fd.io/govpp"
	"go.fd.io/govpp/binapi/memclnt"
)

// VPP checks connectivity to the FD.io VPP binary API.
type VPP struct {
	// PingFunc can be replaced in tests.
	PingFunc func(context.Context, string) error
}

// NewVPP returns a VPP checker using GoVPP.
func NewVPP() *VPP {
	return &VPP{PingFunc: PingVPP}
}

// Ping checks that socket points at a reachable VPP API.
func (v *VPP) Ping(ctx context.Context, socket string) error {
	if v != nil && v.PingFunc != nil {
		return v.PingFunc(ctx, socket)
	}
	return PingVPP(ctx, socket)
}

// PingVPP checks the VPP API socket and performs a control ping over GoVPP.
// The socket is the VPP binary API socket (socksvr), normally
// /run/vpp/api.sock, not the CLI or vhost-user socket.
func PingVPP(ctx context.Context, socket string) error {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		return errors.New("VPP API socket is not configured")
	}

	info, err := os.Stat(socket)
	if err != nil {
		return fmt.Errorf("VPP API socket %s: %w", socket, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("VPP API socket %s is not a unix socket", socket)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("VPP ping: %w", err)
	}

	// govpp.Connect has no context variant and can block for a few seconds.
	// Keep the check bounded and let the goroutine clean up its connection.
	result := make(chan error, 1)
	go func() {
		conn, err := govpp.Connect(socket)
		if err != nil {
			result <- fmt.Errorf("connect to VPP API %s: %w", socket, err)
			return
		}
		defer conn.Disconnect()

		client := memclnt.NewServiceClient(conn)
		if _, err := client.ControlPing(ctx, &memclnt.ControlPing{}); err != nil {
			result <- fmt.Errorf("VPP control ping: %w", err)
			return
		}
		result <- nil
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return fmt.Errorf("VPP ping: %w", ctx.Err())
	}
}
