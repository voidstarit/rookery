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
	liveDHCPv4           sync.Map // lease-file path -> *liveDHCPHandler4
	liveDHCPv6           sync.Map // lease-file path -> *liveDHCPHandler6
)

// liveDHCPHandler4 serializes a handler swap with in-flight requests. The proxy
// holds RLock for the whole handler call, so a reload waits for requests that
// loaded the previous handler before it reads the lease file and publishes a new
// one. Without this an old handler could save its lease map over a newer one.
type liveDHCPHandler4 struct {
	mu      sync.RWMutex
	handler handler.Handler4
}

// liveDHCPHandler6 is the DHCPv6 equivalent of liveDHCPHandler4.
type liveDHCPHandler6 struct {
	mu      sync.RWMutex
	handler handler.Handler6
}

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
	liveDHCPv4.Store(key, &liveDHCPHandler4{handler: current})
	return coreDHCPv4Proxy(key), nil
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
	liveDHCPv6.Store(key, &liveDHCPHandler6{handler: current})
	return coreDHCPv6Proxy(key), nil
}

// newDHCPv6Handlers loads the DHCPv6 plugin chain (which publishes the new
// handler) without starting CoreDHCP's kernel listeners.
func newDHCPv6Handlers(vnf store.VNF, hostIf, leasePath string) ([]handler.Handler6, error) {
	ensureCoreDHCPv6Plugin()
	ensureCoreDHCPLivePlugins()
	unlock := lockLiveDHCPv6(leasePath)
	defer unlock()
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

// coreDHCPv4Proxy returns the stable handler installed in the plugin chain. It
// holds the current handler's read lock for the whole call so a reload cannot
// swap the lease state underneath an in-flight request.
func coreDHCPv4Proxy(key string) handler.Handler4 {
	return func(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
		for {
			value, ok := liveDHCPv4.Load(key)
			if !ok {
				return nil, true
			}
			active, ok := value.(*liveDHCPHandler4)
			if !ok || active.handler == nil {
				return nil, true
			}
			active.mu.RLock()
			// The reload publishes a new wrapper while holding the old wrapper's
			// write lock. If the wrapper changed before this reader acquired the
			// lock, run the new handler instead of the stale one.
			if current, ok := liveDHCPv4.Load(key); !ok || current != value {
				active.mu.RUnlock()
				continue
			}
			defer active.mu.RUnlock()
			return active.handler(req, resp)
		}
	}
}

// coreDHCPv6Proxy is the DHCPv6 equivalent of coreDHCPv4Proxy.
func coreDHCPv6Proxy(key string) handler.Handler6 {
	return func(req, resp dhcpv6.DHCPv6) (dhcpv6.DHCPv6, bool) {
		for {
			value, ok := liveDHCPv6.Load(key)
			if !ok {
				return nil, true
			}
			active, ok := value.(*liveDHCPHandler6)
			if !ok || active.handler == nil {
				return nil, true
			}
			active.mu.RLock()
			if current, ok := liveDHCPv6.Load(key); !ok || current != value {
				active.mu.RUnlock()
				continue
			}
			defer active.mu.RUnlock()
			return active.handler(req, resp)
		}
	}
}

// lockLiveDHCPv4 blocks new packets from entering the currently published
// DHCPv4 handler and waits for in-flight handlers to finish. It returns the
// unlock function; with no published handler it is a no-op.
func lockLiveDHCPv4(key string) func() {
	value, ok := liveDHCPv4.Load(key)
	if !ok {
		return func() {}
	}
	active, ok := value.(*liveDHCPHandler4)
	if !ok {
		return func() {}
	}
	active.mu.Lock()
	return active.mu.Unlock
}

// lockLiveDHCPv6 is the DHCPv6 equivalent of lockLiveDHCPv4.
func lockLiveDHCPv6(key string) func() {
	value, ok := liveDHCPv6.Load(key)
	if !ok {
		return func() {}
	}
	active, ok := value.(*liveDHCPHandler6)
	if !ok {
		return func() {}
	}
	active.mu.Lock()
	return active.mu.Unlock
}
