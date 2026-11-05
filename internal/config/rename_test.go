//go:build linux || darwin || windows

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExclusiveConfigurationRename(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "temporary")
	destination := filepath.Join(dir, "steam-release.yaml")
	if err := os.WriteFile(source, []byte("new configuration"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("existing configuration"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameConfigurationNoReplace(source, destination); !errors.Is(err, os.ErrExist) {
		t.Fatalf("rename over existing file = %v, want an existence error", err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "existing configuration" {
		t.Fatalf("existing configuration changed: %q, %v", got, err)
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err := renameConfigurationNoReplace(source, destination); err != nil {
		t.Fatalf("rename to unused destination: %v", err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "new configuration" {
		t.Fatalf("new configuration = %q, %v", got, err)
	}
	if _, err := os.Lstat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists after rename: %v", err)
	}
}

func TestExclusiveConfigurationRenameRejectsDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "temporary")
	destination := filepath.Join(dir, "steam-release.yaml")
	if err := os.WriteFile(source, []byte("new configuration"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "missing.yaml"), destination); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := renameConfigurationNoReplace(source, destination); !errors.Is(err, os.ErrExist) {
		t.Fatalf("rename over dangling symlink = %v, want an existence error", err)
	}
	if info, err := os.Lstat(destination); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("dangling symlink changed: %v", err)
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != "new configuration" {
		t.Fatalf("source changed: %q, %v", got, err)
	}
}
