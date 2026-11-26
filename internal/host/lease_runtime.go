package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// leaseRuntime is implemented by the in-process DHCPv4/DHCPv6 state so the
// manager can remove individual leases while the server keeps running.
type leaseRuntime interface {
	removeLeases(clientIDs []string) int
}

var (
	leaseRuntimeMu sync.Mutex
	leaseRuntimes  = make(map[string]leaseRuntime)
)

// leaseRuntimeKey canonicalizes a lease file path so equivalent paths resolve to
// the same registered runtime.
func leaseRuntimeKey(path string) string {
	return filepath.Clean(path)
}

// registerLeaseRuntime records the live DHCP state for a lease file. Plugin
// setup runs during CoreDHCP startup, before the VNF manager records the VNF.
func registerLeaseRuntime(path string, runtime leaseRuntime) {
	if path == "" || runtime == nil {
		return
	}
	leaseRuntimeMu.Lock()
	leaseRuntimes[leaseRuntimeKey(path)] = runtime
	leaseRuntimeMu.Unlock()
}

// unregisterLeaseRuntime drops the live DHCP state registered for a lease file,
// ignoring an empty path.
func unregisterLeaseRuntime(path string) {
	if path == "" {
		return
	}
	leaseRuntimeMu.Lock()
	delete(leaseRuntimes, leaseRuntimeKey(path))
	leaseRuntimeMu.Unlock()
}

// removeRuntimeLeases removes leases from the live DHCP state. The boolean
// return reports whether a runtime was registered for the lease file.
func removeRuntimeLeases(path string, clientIDs []string) (int, bool) {
	if path == "" {
		return 0, false
	}
	leaseRuntimeMu.Lock()
	runtime, ok := leaseRuntimes[leaseRuntimeKey(path)]
	leaseRuntimeMu.Unlock()
	if !ok {
		return 0, false
	}
	return runtime.removeLeases(clientIDs), true
}

// removeLeasesFromFile updates a lease file when no live runtime owns it. It is
// used for VNFs that are enabled but not currently running.
func removeLeasesFromFile(path string, clientIDs []string) (int, error) {
	if path == "" || len(clientIDs) == 0 {
		return 0, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read DHCP leases: %w", err)
	}
	var file leaseFile
	if err := json.Unmarshal(data, &file); err != nil {
		return 0, fmt.Errorf("parse DHCP leases: %w", err)
	}
	remove := make(map[string]struct{}, len(clientIDs))
	for _, clientID := range clientIDs {
		remove[clientID] = struct{}{}
	}
	kept := make([]leaseRecord, 0, len(file.Leases))
	for _, lease := range file.Leases {
		if _, ok := remove[lease.ClientID]; ok {
			continue
		}
		kept = append(kept, lease)
	}
	removed := len(file.Leases) - len(kept)
	if removed == 0 {
		return 0, nil
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ClientID < kept[j].ClientID })
	writeLeaseFile(path, kept)
	return removed, nil
}
