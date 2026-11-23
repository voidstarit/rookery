package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeLeaseRemover struct {
	removed int
}

func (f *fakeLeaseRemover) removeLeases([]string) int {
	return f.removed
}

func TestVNFManagerRemoveLeases(t *testing.T) {
	ctx := context.Background()
	manager, err := NewVNFManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	if _, err := manager.RemoveLeases(ctx, 0, []string{"aa"}); err == nil {
		t.Fatal("expected an error for an invalid VNF id")
	}

	path := manager.leasePath(7)
	writeLeaseRecords(t, path, []leaseRecord{
		{ClientID: "02:00:00:00:00:01", Address: "10.20.0.10", Expires: time.Now().Add(time.Hour)},
		{ClientID: "02:00:00:00:00:02", Address: "10.20.0.11", Expires: time.Now().Add(time.Hour)},
	})

	// Blank ids are ignored, and duplicates are collapsed before removal.
	removed, err := manager.RemoveLeases(ctx, 7, []string{" ", "\t"})
	if err != nil || removed != 0 {
		t.Fatalf("blank ids: removed=%d err=%v", removed, err)
	}
	removed, err = manager.RemoveLeases(ctx, 7, []string{" 02:00:00:00:00:01 ", "02:00:00:00:00:01"})
	if err != nil || removed != 1 {
		t.Fatalf("file removal: removed=%d err=%v", removed, err)
	}

	// A registered live runtime is used instead of the lease file.
	registerLeaseRuntime(path, &fakeLeaseRemover{removed: 2})
	t.Cleanup(func() { unregisterLeaseRuntime(path) })
	removed, err = manager.RemoveLeases(ctx, 7, []string{"02:00:00:00:00:02"})
	if err != nil || removed != 2 {
		t.Fatalf("live removal: removed=%d err=%v", removed, err)
	}

	// Unparseable lease files surface the error.
	broken := manager.leasePath(8)
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RemoveLeases(ctx, 8, []string{"02:00:00:00:00:01"}); err == nil {
		t.Fatal("expected a parse error")
	}

	// Stopping a VNF also unregisters its live lease runtime.
	registerLeaseRuntime(path, &fakeLeaseRemover{removed: 1})
	if err := manager.Stop(ctx, 7, false); err != nil {
		t.Fatal(err)
	}
	if _, live := removeRuntimeLeases(path, []string{"02:00:00:00:00:01"}); live {
		t.Fatal("lease runtime still registered after stop")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stop without lease removal deleted the lease file: %v", err)
	}

	if err := manager.Stop(ctx, 7, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Clean(path)); !os.IsNotExist(err) {
		t.Fatalf("lease file survived removal: %v", err)
	}
}
