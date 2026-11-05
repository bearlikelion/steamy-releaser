package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/localstore"
)

func TestValidateUsername(t *testing.T) {
	for _, username := range []string{"builder_123", "A", strings.Repeat("a", 64)} {
		if err := ValidateUsername(username); err != nil {
			t.Errorf("ValidateUsername(%q): %v", username, err)
		}
	}
	for _, username := range []string{"", " builder", "builder ", "two words", "a\nb", "a/b", "+quit", "é", strings.Repeat("a", 65)} {
		if err := ValidateUsername(username); err == nil {
			t.Errorf("ValidateUsername(%q) accepted invalid username", username)
		}
	}
}

func TestResolveUsernamePrecedence(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	if err := CacheUsername("cached_builder"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, provided, environment, want string
		wantErr                           bool
	}{
		{"explicit", " explicit_builder ", "env_builder", "explicit_builder", false},
		{"environment", " \t", " env_builder ", "env_builder", false},
		{"cache", "", "", "cached_builder", false},
		{"invalid explicit does not fall back", "+quit", "env_builder", "", true},
		{"invalid environment does not fall back", "", "invalid user", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STEAM_USERNAME", tc.environment)
			got, err := ResolveUsername(tc.provided)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("ResolveUsername = %q, %v; want %q, error=%t", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestUsernameCacheLifecycle(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "config")
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", directory)
	t.Setenv("STEAM_USERNAME", "")
	if err := ClearUsername(); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveUsername(""); err == nil {
		t.Fatal("resolved a username without any credentials")
	}
	for _, username := range []string{"first_builder", "replacement_builder"} {
		if err := CacheUsername(username); err != nil {
			t.Fatal(err)
		}
		if got, err := LoadUsername(); err != nil || got != username {
			t.Fatalf("LoadUsername = %q, %v; want %q", got, err, username)
		}
	}
	if err := CacheUsername("invalid user"); err == nil {
		t.Fatal("cached invalid username")
	}
	if got, err := LoadUsername(); err != nil || got != "replacement_builder" {
		t.Fatalf("invalid write changed cache: %q, %v", got, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != localstore.Filename {
		t.Fatalf("unexpected cache files: %v, %v", entries, err)
	}
	if runtime.GOOS != "windows" {
		for path, mode := range map[string]os.FileMode{directory: 0o700, filepath.Join(directory, localstore.Filename): 0o600} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != mode {
				t.Errorf("%s permissions = %o, want %o", path, info.Mode().Perm(), mode)
			}
		}
	}
	if err := ClearUsername(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUsername(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache survived logout: %v", err)
	}
}

func TestUsernameMigratesLegacyCacheAndDoesNotReturnAfterLogout(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", directory)
	path := filepath.Join(directory, "username")
	if err := os.WriteFile(path, []byte("legacy_builder\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadUsername(); err != nil || got != "legacy_builder" {
		t.Fatalf("legacy username = %q, %v", got, err)
	}
	if err := CacheUsername("new_builder"); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadUsername(); err != nil || got != "new_builder" {
		t.Fatalf("legacy username replaced SQLite data: %q, %v", got, err)
	}
	if err := ClearUsername(); err != nil {
		t.Fatal(err)
	}
	// Even a restored legacy backup must not resurrect the cleared account.
	if err := os.WriteFile(path, []byte("legacy_builder\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUsername(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy username returned after logout: %v", err)
	}
}

func TestLoadUsernameRejectsCorruptCache(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", directory)
	t.Setenv("STEAM_USERNAME", "")
	if err := os.WriteFile(filepath.Join(directory, "username"), []byte("invalid user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUsername(); err == nil {
		t.Fatal("loaded invalid cached username")
	}
	if _, err := ResolveUsername(""); err == nil {
		t.Fatal("resolved invalid cached username")
	}
}
