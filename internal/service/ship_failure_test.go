package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/steam"
)

func TestShipStopsBeforeUploadWhenBuildPipelineFails(t *testing.T) {
	buildErr := errors.New("engine failed")
	for _, tc := range []struct {
		name, failingCommand, wantErr string
		writeOutput                   bool
		postBuild                     []config.CopyRule
		required                      []string
		wantCommands                  int
	}{
		{name: "import fails", failingCommand: "Godot import", wantErr: "engine failed", wantCommands: 1},
		{name: "export fails", failingCommand: "target", wantErr: "engine failed", wantCommands: 2},
		{name: "output missing", wantErr: "missing build output", wantCommands: 2},
		{name: "copy fails", writeOutput: true, postBuild: []config.CopyRule{{From: "missing/*.txt"}}, wantErr: "matched no files", wantCommands: 2},
		{name: "required artifact missing", writeOutput: true, required: []string{"data/*.pak"}, wantErr: "missing required artifact", wantCommands: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("GODOT_BIN", filepath.Join(root, "godot"))
			steamCMD := filepath.Join(root, "steamcmd")
			t.Setenv("STEAMCMD_BIN", steamCMD)
			if err := os.WriteFile(steamCMD, []byte("fake"), 0o700); err != nil {
				t.Fatal(err)
			}
			document := testDocument(root, config.Engine{Type: "godot"}, config.Target{
				DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Preset: "Linux"}, Required: tc.required,
			})
			profile := document.Config.Profiles["release"]
			profile.PostBuild = tc.postBuild
			document.Config.Profiles["release"] = profile
			runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
				if command.Label == tc.failingCommand {
					return release.RunResult{}, buildErr
				}
				if command.Label == "target" && tc.writeOutput {
					return release.RunResult{}, os.WriteFile(document.ResolveProject("build/Game"), []byte("game"), 0o755)
				}
				return release.RunResult{}, nil
			}}
			service := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
			_, err := service.Ship(context.Background(), "", RunOptions{}, steam.UploadOptions{Username: "builder"})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Ship = %v, want %q", err, tc.wantErr)
			}
			if tc.failingCommand != "" && !errors.Is(err, buildErr) {
				t.Fatalf("Ship lost engine error: %v", err)
			}
			if len(runner.commands) != tc.wantCommands || runner.commands[0].Label != "Godot import" {
				t.Fatalf("unexpected pipeline commands: %#v", runner.commands)
			}
			for _, command := range runner.commands {
				if command.Label == "SteamCMD upload" {
					t.Fatal("failed build was uploaded")
				}
			}
			if _, err := os.Stat(document.ResolveProject(".steam-releaser/generated")); !os.IsNotExist(err) {
				t.Fatalf("failed pipeline generated upload scripts: %v", err)
			}
		})
	}
}
