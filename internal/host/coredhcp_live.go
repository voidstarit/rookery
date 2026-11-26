package host

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/coredhcp/coredhcp/handler"
	"github.com/coredhcp/coredhcp/plugins"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv6"

	"github.com/voidstarit/rookery/internal/store"
)

// The live plugins keep CoreDHCP's kernel listener open across a runtime
// reload. The listener's handler chain is a stable proxy that reads the current
// vnf handler from a process-wide registry on every packet, so a reload only
// replaces the registry entry instead of rebinding the socket.
const (
	coreDHCPv4LivePlugin = "rookery-coredhcp-live"
	coreDHCPv6LivePlugin = "rookery-coredhcp6-live"
)

var (
	registerCoreDHCPLive sync.Once
	liveDHCPv4           sync.Map // lease-file path -> handler.Handler4
	liveDHCPv6           sync.Map // lease-file path -> handler.Handler6
)

// ensureCoreDHCPLivePlugins registers the stable proxy plugins once.
func ensureCoreDHCPLivePlugins() {
	registerCoreDHCPLive.Do(func() {
		if err := plugins.RegisterPlugin(&plugins.Plugin{
			Name:   coreDHCPv4LivePlugin,
			Setup4: setupCoreDHCPv4Live,
		}); err != nil {
			panic(fmt.Sprintf("register live CoreDHCP plugin: %v", err))
		}
		if err := plugins.RegisterPlugin(&plugins.Plugin{
			Name:   coreDHCPv6LivePlugin,
			Setup6: setupCoreDHCPv6Live,
		}); err != nil {
			panic(fmt.Sprintf("register live CoreDHCPv6 plugin: %v", err))
		}
	})
}

// setupCoreDHCPv4Live builds the real DHCPv4 handler from the arguments after
// the lease-file key, publishes it, and returns the stable proxy handler.
func setupCoreDHCPv4Live(args ...string) (handler.Handler4, error) {
	if len(args) < 2 || strings.TrimSpace(args[0]) == "" {
		return nil, errors.New("live CoreDHCP plugin requires a lease-file key and setup arguments")
	}
	key := args[0]
	current, err := setupCoreDHCP(args[1:]...)
	if err != nil {
		return nil, err
	}
	liveDHCPv4.Store(key, current)
	return func(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
		if value, ok := liveDHCPv4.Load(key); ok {
			if active, ok := value.(handler.Handler4); ok {
				return active(req, resp)
			}
		}
		return nil, true
	}, nil
}

// setupCoreDHCPv6Live builds the real DHCPv6 handler from the arguments after
// the lease-file key, publishes it, and returns the stable proxy handler.
func setupCoreDHCPv6Live(args ...string) (handler.Handler6, error) {
	if len(args) < 2 || strings.TrimSpace(args[0]) == "" {
		return nil, errors.New("live CoreDHCPv6 plugin requires a lease-file key and setup arguments")
	}
	key := args[0]
	current, err := setupCoreDHCPv6(args[1:]...)
	if err != nil {
		return nil, err
	}
	liveDHCPv6.Store(key, current)
	return func(req, resp dhcpv6.DHCPv6) (dhcpv6.DHCPv6, bool) {
		if value, ok := liveDHCPv6.Load(key); ok {
			if active, ok := value.(handler.Handler6); ok {
				return active(req, resp)
			}
		}
		return nil, true
	}, nil
}

// newDHCPv6Handlers loads the DHCPv6 plugin chain (which publishes the new
// handler) without starting CoreDHCP's kernel listeners.
func newDHCPv6Handlers(vnf store.VNF, hostIf, leasePath string) ([]handler.Handler6, error) {
	ensureCoreDHCPv6Plugin()
	ensureCoreDHCPLivePlugins()
	cfg, err := buildDHCPv6Config(vnf, hostIf, leasePath)
	if err != nil {
		return nil, err
	}
	_, handlers6, err := plugins.LoadPlugins(cfg)
	if err != nil {
		return nil, fmt.Errorf("load DHCPv6 plugin: %w", err)
	}
	if len(handlers6) == 0 {
		return nil, errors.New("no DHCPv6 handler configured")
	}
	return handlers6, nil
}
