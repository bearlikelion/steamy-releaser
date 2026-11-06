package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thornity/steam-release-tool/internal/release"
)

func TestStandaloneLoginWithoutConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	steamCMD := filepath.Join(home, "steamcmd")
	if err := os.WriteFile(steamCMD, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEAMCMD_BIN", steamCMD)
	svc, err := Standalone(release.NopReporter{})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	svc.Runner = runner
	if err := svc.LoginWithOptions(context.Background(), "builder", false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(runner.commands))
	}
	command := runner.commands[0]
	if command.Program != steamCMD || command.WorkingDir != home {
		t.Fatalf("command = %q in %q, want %q in %q", command.Program, command.WorkingDir, steamCMD, home)
	}
}
