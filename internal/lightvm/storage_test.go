package lightvm_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/lightvm"
)

func TestStorageSaveAndRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lightvm")
	storage, err := lightvm.NewStorage(dir, 16)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	if storage.Dir() != dir {
		t.Fatalf("storage dir = %q, want %q", storage.Dir(), dir)
	}

	stored, err := storage.Save(lightvm.KindKernel, "vmlinuz 6.12", strings.NewReader("kernel-data"))
	if err != nil {
		t.Fatalf("save kernel: %v", err)
	}
	if stored.FileName != "vmlinuz 6.12" || stored.SizeBytes != int64(len("kernel-data")) {
		t.Fatalf("stored = %+v", stored)
	}
	if !strings.HasPrefix(stored.Path, filepath.Join(dir, "kernels")+string(filepath.Separator)) {
		t.Fatalf("stored path = %q", stored.Path)
	}
	if !strings.Contains(filepath.Base(stored.Path), "vmlinuz_6.12") {
		t.Fatalf("stored filename = %q", filepath.Base(stored.Path))
	}
	content, err := os.ReadFile(stored.Path)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if string(content) != "kernel-data" {
		t.Fatalf("stored content = %q", content)
	}
	if !storage.Managed(stored.Path) {
		t.Fatalf("stored path should be managed")
	}
	if err := storage.Remove(stored.Path); err != nil {
		t.Fatalf("remove stored file: %v", err)
	}
	if _, err := os.Stat(stored.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat removed file = %v, want not exist", err)
	}
}

func TestStorageRejectsEmptyLargeAndUnknown(t *testing.T) {
	storage, err := lightvm.NewStorage(t.TempDir(), 4)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	if _, err := storage.Save(lightvm.KindKernel, "empty", bytes.NewReader(nil)); err == nil {
		t.Fatal("save empty file succeeded")
	}
	if _, err := storage.Save(lightvm.KindKernel, "large", strings.NewReader("too-large")); !errors.Is(err, lightvm.ErrTooLarge) {
		t.Fatalf("save large file error = %v, want ErrTooLarge", err)
	}
	if _, err := storage.Save("disk", "file", strings.NewReader("data")); !errors.Is(err, lightvm.ErrUnsupportedKind) {
		t.Fatalf("save unknown kind error = %v, want ErrUnsupportedKind", err)
	}
}

func TestStorageDoesNotRemoveHostPaths(t *testing.T) {
	dir := t.TempDir()
	hostFile := filepath.Join(dir, "host-kernel")
	if err := os.WriteFile(hostFile, []byte("host"), 0o644); err != nil {
		t.Fatal(err)
	}
	storage, err := lightvm.NewStorage(filepath.Join(dir, "managed"), 16)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	if storage.Managed(hostFile) {
		t.Fatalf("host file should not be managed")
	}
	if err := storage.Remove(hostFile); err != nil {
		t.Fatalf("remove host file: %v", err)
	}
	if _, err := os.Stat(hostFile); err != nil {
		t.Fatalf("host file should remain: %v", err)
	}
}

func TestStorageDoesNotRemoveFilesThroughSymlinks(t *testing.T) {
	dir := t.TempDir()
	storage, err := lightvm.NewStorage(filepath.Join(dir, "managed"), 16)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	kindDir := filepath.Join(storage.Dir(), "kernels")
	if err := os.MkdirAll(kindDir, 0o755); err != nil {
		t.Fatalf("create managed directory: %v", err)
	}
	outsideDir := filepath.Join(dir, "outside")
	if err := os.Mkdir(outsideDir, 0o755); err != nil {
		t.Fatalf("create outside directory: %v", err)
	}
	hostFile := filepath.Join(outsideDir, "host-kernel")
	if err := os.WriteFile(hostFile, []byte("host"), 0o644); err != nil {
		t.Fatalf("create host file: %v", err)
	}
	link := filepath.Join(kindDir, "outside")
	if err := os.Symlink(outsideDir, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	if err := storage.Remove(filepath.Join(link, "host-kernel")); err == nil {
		t.Fatal("remove through symlink succeeded")
	}
	if _, err := os.Stat(hostFile); err != nil {
		t.Fatalf("host file should remain: %v", err)
	}
}

func TestIsManagedRejectsSiblingPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lightvm")
	if lightvm.IsManaged(root, root+"-other/file") {
		t.Fatal("sibling with shared prefix must not be managed")
	}
	if !lightvm.IsManaged(root, filepath.Join(root, "kernels", "file")) {
		t.Fatal("nested file should be managed")
	}
}
