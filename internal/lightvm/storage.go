// Package lightvm stores uploaded LightVM artifacts on the host.
package lightvm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const (
	// KindKernel identifies kernel uploads.
	KindKernel = "kernel"
	// KindInitramfs identifies initramfs uploads.
	KindInitramfs = "initramfs"

	// DefaultDir is the default directory for managed LightVM files.
	DefaultDir = "/var/lib/rookery/lightvm"
	// DefaultMaxUploadBytes is the default maximum size of one uploaded file.
	DefaultMaxUploadBytes int64 = 1 << 30
)

// ErrTooLarge is returned when an upload exceeds the configured limit.
var (
	ErrTooLarge        = errors.New("upload exceeds the maximum size")
	ErrUnsupportedKind = errors.New("unsupported lightvm file kind")
)

// StoredFile describes a file written to managed storage.
type StoredFile struct {
	Path      string `json:"path"`
	FileName  string `json:"file_name"`
	SizeBytes int64  `json:"size_bytes"`
}

// Storage writes uploaded kernels and initramfs images below a managed
// directory. Construction never touches the filesystem; Save creates the
// per-kind directory on demand.
type Storage struct {
	dir       string
	maxUpload int64
}

// NewStorage validates the managed directory and upload limit.
func NewStorage(dir string, maxUpload int64) (*Storage, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("lightvm storage directory is required")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve lightvm storage directory: %w", err)
	}
	if maxUpload <= 0 {
		maxUpload = DefaultMaxUploadBytes
	}
	return &Storage{dir: abs, maxUpload: maxUpload}, nil
}

// Dir returns the absolute managed directory.
func (s *Storage) Dir() string {
	return s.dir
}

// MaxUploadBytes returns the per-file upload limit.
func (s *Storage) MaxUploadBytes() int64 {
	return s.maxUpload
}

// Save streams one uploaded file into managed storage and returns its final
// path. The original file name is preserved for display; the stored name is a
// UUID prefix plus a sanitized basename so records never collide.
func (s *Storage) Save(kind, name string, reader io.Reader) (StoredFile, error) {
	kindDir, err := s.kindDir(kind)
	if err != nil {
		return StoredFile{}, err
	}
	if err := os.MkdirAll(kindDir, 0o755); err != nil {
		return StoredFile{}, fmt.Errorf("create lightvm storage: %w", err)
	}

	displayName := filepath.Base(strings.TrimSpace(name))
	if displayName == "." || displayName == string(filepath.Separator) || displayName == "" {
		displayName = kind
	}
	storedName := uuid.NewString() + "-" + sanitizeFileName(displayName)

	tmp, err := os.CreateTemp(kindDir, ".upload-*")
	if err != nil {
		return StoredFile{}, fmt.Errorf("create upload file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	written, err := io.Copy(tmp, io.LimitReader(reader, s.maxUpload+1))
	if err != nil {
		cleanup()
		return StoredFile{}, fmt.Errorf("write upload file: %w", err)
	}
	if written > s.maxUpload {
		cleanup()
		return StoredFile{}, ErrTooLarge
	}
	if written == 0 {
		cleanup()
		return StoredFile{}, errors.New("uploaded file is empty")
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return StoredFile{}, fmt.Errorf("sync upload file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return StoredFile{}, fmt.Errorf("close upload file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return StoredFile{}, fmt.Errorf("set upload permissions: %w", err)
	}

	finalPath := filepath.Join(kindDir, storedName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return StoredFile{}, fmt.Errorf("store upload file: %w", err)
	}
	return StoredFile{Path: finalPath, FileName: displayName, SizeBytes: written}, nil
}

// Remove deletes a managed file. Paths outside the managed directory are
// ignored so records created from host paths never remove host files.
func (s *Storage) Remove(path string) error {
	if !s.Managed(path) {
		return nil
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve lightvm file path: %w", err)
	}
	rel, err := filepath.Rel(s.dir, target)
	if err != nil {
		return fmt.Errorf("resolve lightvm file path: %w", err)
	}
	root, err := os.OpenRoot(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open lightvm storage: %w", err)
	}
	defer root.Close()
	if err := root.Remove(rel); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove lightvm file: %w", err)
	}
	return nil
}

// Managed reports whether path is inside the managed directory.
func (s *Storage) Managed(path string) bool {
	return IsManaged(s.dir, path)
}

// IsManaged reports whether path is inside dir. Both paths are compared after
// converting them to absolute paths.
func IsManaged(dir, path string) bool {
	dir = strings.TrimSpace(dir)
	path = strings.TrimSpace(path)
	if dir == "" || path == "" {
		return false
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Storage) kindDir(kind string) (string, error) {
	switch strings.TrimSpace(kind) {
	case KindKernel:
		return filepath.Join(s.dir, "kernels"), nil
	case KindInitramfs:
		return filepath.Join(s.dir, "initramfs"), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedKind, kind)
	}
}

func sanitizeFileName(name string) string {
	var builder strings.Builder
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z',
			char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9',
			char == '.', char == '_', char == '-':
			builder.WriteRune(char)
		default:
			builder.WriteByte('_')
		}
	}
	value := strings.Trim(builder.String(), "._-")
	if value == "" {
		return "file"
	}
	return value
}
