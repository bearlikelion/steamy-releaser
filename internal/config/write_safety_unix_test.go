//go:build linux || darwin

package config

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestInitializeWriteFailureLeavesNoConfiguration(t *testing.T) {
	if path := os.Getenv("STEAM_RELEASER_TEST_LIMITED_CONFIG"); path != "" {
		signal.Ignore(syscall.SIGXFSZ)
		var original unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &original); err != nil {
			t.Fatal(err)
		}
		limited := original
		limited.Cur = min(limited.Cur, 128)
		if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &limited); err != nil {
			t.Fatal(err)
		}
		// Coverage-instrumented test binaries write their reports after this
		// test returns, so restore the limit before the test process exits.
		defer func() {
			if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &original); err != nil {
				t.Errorf("restore file size limit: %v", err)
			}
		}()
		_, err := Initialize(InitOptions{Path: path, Engine: "prebuilt", AppID: 100,
			Targets: []InitTarget{{Name: "linux", DepotID: 101, Content: "build/linux"}}})
		if err == nil {
			t.Fatal("initialization succeeded despite file size limit")
		}
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "steam-release.yaml")
	cmd := exec.Command(os.Args[0], "-test.run=^TestInitializeWriteFailureLeavesNoConfiguration$")
	cmd.Env = append(os.Environ(), "STEAM_RELEASER_TEST_LIMITED_CONFIG="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("limited child: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed initialization left files behind: %v, %v", entries, err)
	}
}

func TestInitializeRespectsUmask(t *testing.T) {
	if path := os.Getenv("STEAM_RELEASER_TEST_UMASK_CONFIG"); path != "" {
		unix.Umask(0o077)
		if _, err := Initialize(InitOptions{Path: path, Engine: "prebuilt", AppID: 100,
			Targets: []InitTarget{{Name: "linux", DepotID: 101, Content: "build/linux"}}}); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "steam-release.yaml")
	cmd := exec.Command(os.Args[0], "-test.run=^TestInitializeRespectsUmask$")
	cmd.Env = append(os.Environ(), "STEAM_RELEASER_TEST_UMASK_CONFIG="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("umask child: %v\n%s", err, output)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("configuration mode = %v, %v; want 0600", info, err)
	}
}
