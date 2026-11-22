package host

import (
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coredhcp/coredhcp/plugins/allocators/bitmap"
)

func TestRemoveLeasesFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	writeLeaseRecords(t, path, []leaseRecord{
		{ClientID: "02:00:00:00:00:01", Address: "10.20.0.10", Expires: time.Now().Add(time.Hour)},
		{ClientID: "02:00:00:00:00:02", Address: "10.20.0.11", Expires: time.Now().Add(time.Hour)},
	})

	removed, err := removeLeasesFromFile(path, []string{"02:00:00:00:00:01"})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file leaseFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Leases) != 1 || file.Leases[0].ClientID != "02:00:00:00:00:02" {
		t.Fatalf("leases after removal = %+v", file.Leases)
	}
}

func TestCoreDHCPStateRemoveLeases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	alloc, err := bitmap.NewIPv4Allocator(net.ParseIP("10.20.0.10"), net.ParseIP("10.20.0.20"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Hour)
	state := &coreDHCPState{
		leasePath: path,
		alloc:     alloc,
		leases: map[string]*coreDHCPLease{
			"02:00:00:00:00:01": {ClientID: "02:00:00:00:00:01", Address: net.ParseIP("10.20.0.10").To4(), Expires: now},
			"02:00:00:00:00:02": {ClientID: "02:00:00:00:00:02", Address: net.ParseIP("10.20.0.11").To4(), Expires: now},
		},
		byIP: map[string]string{
			"10.20.0.10": "02:00:00:00:00:01",
			"10.20.0.11": "02:00:00:00:00:02",
		},
		reservedByIP: map[string]string{},
	}
	registerLeaseRuntime(path, state)
	t.Cleanup(func() { unregisterLeaseRuntime(path) })

	removed, live := removeRuntimeLeases(path, []string{"02:00:00:00:00:01"})
	if !live || removed != 1 {
		t.Fatalf("live=%v removed=%d", live, removed)
	}
	if _, ok := state.leases["02:00:00:00:00:01"]; ok {
		t.Fatal("removed lease is still in memory")
	}
	if _, ok := state.leases["02:00:00:00:00:02"]; !ok {
		t.Fatal("unrelated lease was removed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file leaseFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Leases) != 1 || file.Leases[0].ClientID != "02:00:00:00:00:02" {
		t.Fatalf("leases after removal = %+v", file.Leases)
	}
}

func TestCoreDHCPv6StateRemoveLeases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases.json")
	now := time.Now().Add(time.Hour)
	state := &coreDHCPv6State{
		leasePath: path,
		leases: map[string]*coreDHCPv6Lease{
			"duid-a": {ClientID: "duid-a", Address: netip.MustParseAddr("fd00:20::10"), Expires: now},
			"duid-b": {ClientID: "duid-b", Address: netip.MustParseAddr("fd00:20::11"), Expires: now},
		},
		byIP: map[string]string{
			"fd00:20::10": "duid-a",
			"fd00:20::11": "duid-b",
		},
	}
	registerLeaseRuntime(path, state)
	t.Cleanup(func() { unregisterLeaseRuntime(path) })

	removed, live := removeRuntimeLeases(path, []string{"duid-a"})
	if !live || removed != 1 {
		t.Fatalf("live=%v removed=%d", live, removed)
	}
	if _, ok := state.leases["duid-a"]; ok {
		t.Fatal("removed lease is still in memory")
	}
	if _, ok := state.leases["duid-b"]; !ok {
		t.Fatal("unrelated lease was removed")
	}
}
