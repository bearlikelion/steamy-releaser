package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/steam"
)

func TestMinimalConfigShipsSelectedTarget(t *testing.T) {
	for _, engine := range []string{"prebuilt", "custom"} {
		t.Run(engine, func(t *testing.T) {
			directory := t.TempDir()
			t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(directory, "cache"))
			steamCMD := filepath.Join(directory, "steamcmd")
			builder := filepath.Join(directory, "builder")
			t.Setenv("STEAMCMD_BIN", steamCMD)
			for _, tool := range []string{steamCMD, builder} {
				if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			content := filepath.Join(directory, "dist", "windows")
			source := "app_id: 100\ntargets:\n"
			for i, name := range []string{"windows", "linux"} {
				depot := []string{"101", "102"}[i]
				source += "  " + name + ":\n    depot_id: " + depot + "\n"
				if engine == "custom" {
					source += "    output: dist/" + name + "/Game\n    build:\n      command: ['{project}/builder', '{output}']\n"
				} else {
					source += "    content: dist/" + name + "\n"
				}
			}
			if engine == "prebuilt" {
				if err := os.MkdirAll(content, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(content, "Game"), []byte("game"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(directory, "steam-release.yaml")
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
				if command.Program == builder {
					return release.RunResult{}, os.WriteFile(command.Args[0], []byte("game"), 0o700)
				}
				return release.RunResult{Output: "Successfully finished AppID 100 build preview."}, nil
			}}
			loaded.Runner = runner
			run := RunOptions{Targets: []string{"windows"}}
			plan, err := loaded.Plan("", run, steam.UploadOptions{})
			if err != nil || plan.Profile != "release" || plan.Engine != engine || len(plan.Targets) != 1 {
				t.Fatalf("unexpected plan: %#v; error: %v", plan, err)
			}
			result, err := loaded.Ship(context.Background(), "", run, steam.UploadOptions{Username: "builder_account", Preview: true})
			if err != nil {
				t.Fatal(err)
			}
			if !matchesPlannedScriptTemplate(result.BuildScript, plan.Upload.BuildScript) || len(result.Scripts.DepotPaths) != 1 {
				t.Fatalf("unexpected upload result: %#v", result)
			}
			depotVDF, err := os.ReadFile(result.Scripts.DepotPaths[101])
			if err != nil || !strings.Contains(string(depotVDF), `"ContentRoot" "`+content+`"`) {
				t.Fatalf("incorrect upload root: %s; error: %v", depotVDF, err)
			}
			wantCommands := 1
			if engine == "custom" {
				wantCommands++
			}
			if len(runner.commands) != wantCommands || runner.commands[len(runner.commands)-1].Program != steamCMD {
				t.Fatalf("unexpected commands: %#v", runner.commands)
			}
			if _, err := os.Stat(filepath.Join(directory, "dist", "linux")); !os.IsNotExist(err) {
				t.Fatalf("unselected target was created: %v", err)
			}
		})
	}
}

func matchesPlannedScriptTemplate(path, template string) bool {
	return filepath.Base(filepath.Dir(template)) == "<run-id>" &&
		filepath.Base(path) == filepath.Base(template) &&
		filepath.Dir(filepath.Dir(path)) == filepath.Dir(filepath.Dir(template)) &&
		strings.HasPrefix(filepath.Base(filepath.Dir(path)), "run-")
}

func TestPrebuiltBuildDoesNotCreateMissingContent(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "steam-release.yaml")
	source := "app_id: 100\ntargets:\n  linux:\n    depot_id: 101\n    content: missing\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Build(context.Background(), "", RunOptions{}); err == nil {
		t.Fatal("missing prebuilt artifacts passed validation")
	}
	for _, name := range []string{"missing", ".steam-releaser"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatalf("prebuilt build created %s: %v", name, err)
		}
	}
}
