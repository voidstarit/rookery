package lightvm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidstarit/rookery/internal/store"
)

func TestWriteLightVMEnvFileRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name   string
		values []store.LightVMEnvVar
		want   string
	}{
		{
			name:   "invalid name",
			values: []store.LightVMEnvVar{{Name: "1INVALID", Value: "x"}},
			want:   "must match [A-Za-z_][A-Za-z0-9_]*",
		},
		{
			name:   "duplicate name",
			values: []store.LightVMEnvVar{{Name: "DUP", Value: "1"}, {Name: "DUP", Value: "2"}},
			want:   "is duplicated",
		},
		{
			name:   "multi-line value",
			values: []store.LightVMEnvVar{{Name: "MULTI", Value: "one\ntwo"}},
			want:   "must be a single line",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rootfs := t.TempDir()
			err := writeLightVMEnvFile(rootfs, tc.values)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("writeLightVMEnvFile error = %v, want %q", err, tc.want)
			}
			if _, statErr := os.Stat(filepath.Join(rootfs, ".env")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf(".env stat error = %v, want not exist", statErr)
			}
		})
	}
}

func TestRenderLightVMEntrypointRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name       string
		executable string
		args       []string
		want       string
	}{
		{
			name:       "missing executable",
			executable: "   ",
			want:       "executable path is required",
		},
		{
			name:       "multi-line executable",
			executable: "/bin/sh\n/bin/bash",
			want:       "must be a single line",
		},
		{
			name:       "NUL argument",
			executable: "/bin/sh",
			args:       []string{"ok", "bad\x00arg"},
			want:       "cannot contain NUL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content, err := renderLightVMEntrypoint(tc.executable, tc.args, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("renderLightVMEntrypoint error = %v, want %q", err, tc.want)
			}
			if content != nil {
				t.Fatalf("renderLightVMEntrypoint content = %q, want nil", content)
			}
		})
	}
}

func TestWriteLightVMConfigArchiveRejectsInvalidContent(t *testing.T) {
	cases := []struct {
		name string
		vm   store.LightVM
		want string
	}{
		{
			name: "invalid env",
			vm:   store.LightVM{Env: []store.LightVMEnvVar{{Name: "1BAD", Value: "x"}}},
			want: "must match [A-Za-z_][A-Za-z0-9_]*",
		},
		{
			name: "invalid executable",
			vm:   store.LightVM{ExecutablePath: "/bin/sh\n/bin/bash"},
			want: "must be a single line",
		},
		{
			name: "invalid config file name",
			vm:   store.LightVM{ConfigFiles: []store.LightVMConfigFile{{Name: "../../escape.conf", Content: "x"}}},
			want: "invalid path component",
		},
		{
			name: "empty definition",
			vm:   store.LightVM{},
			want: "requires env, a startup command, or a config file",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rookery-config.tar")
			err := writeLightVMConfigArchive(path, tc.vm)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("writeLightVMConfigArchive error = %v, want %q", err, tc.want)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("config archive stat error = %v, want not exist", statErr)
			}
		})
	}
}

func TestWriteLightVMConfigArchiveRefusesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.tar")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	vm := store.LightVM{Env: []store.LightVMEnvVar{{Name: "APP_ENV", Value: "test"}}}
	if err := writeLightVMConfigArchive(path, vm); err == nil {
		t.Fatal("writeLightVMConfigArchive overwrote an existing config drive")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing config drive: %v", err)
	}
	if string(content) != "keep" {
		t.Fatalf("existing config drive content = %q, want %q", content, "keep")
	}
}

func TestLightVMNeedsConfigDrive(t *testing.T) {
	if lightVMNeedsConfigDrive(store.LightVM{
		Image: "alpine",
		Env:   []store.LightVMEnvVar{{Name: "APP_ENV", Value: "test"}},
	}) {
		t.Fatal("image-backed LightVM should not need a config drive")
	}
	if !lightVMNeedsConfigDrive(store.LightVM{
		ConfigFiles: []store.LightVMConfigFile{{Name: "app.conf", Content: "x"}},
	}) {
		t.Fatal("disk-backed LightVM with a config file should need a config drive")
	}
}
