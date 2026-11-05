package service

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/steam"
)

// Exercise the shared CLI/desktop release pipeline with fake engine and Steam
// processes, while using real configuration, artifact, and VDF handling.
func TestShipEngineWorkflow(t *testing.T) {
	for _, tc := range []struct {
		engine, linuxBuild, windowsBuild string
		upload                           steam.UploadOptions
		wantBuildID                      string
	}{
		{"godot", "preset: Linux Desktop", "preset: Windows Desktop", steam.UploadOptions{Username: "builder", Preview: true}, ""},
		{"unity", "target: StandaloneLinux64", "target: StandaloneWindows64", steam.UploadOptions{Username: "builder", SetLive: "qa"}, "12345"},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "game project")
			for _, directory := range []string{"legal", "Assets", "ProjectSettings"} {
				if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for name, contents := range map[string]string{
				"legal/license.txt":                  "license",
				"ProjectSettings/ProjectVersion.txt": "m_EditorVersion: 6000.0\n",
			} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			enginePath := filepath.Join(root, "fake engine")
			steamPath := filepath.Join(root, "fake steamcmd")
			for _, path := range []string{enginePath, steamPath} {
				if err := os.WriteFile(path, []byte("fake"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("GODOT_BIN", enginePath)
			t.Setenv("UNITY_BIN", enginePath)
			t.Setenv("STEAMCMD_BIN", steamPath)
			t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(t.TempDir(), "account"))
			configuration := fmt.Sprintf(`project:
  name: Example
  engine:
    type: %s
profiles:
  public:
    app_id: 100
    targets:
      linux:
        depot_id: 101
        output: builds/linux/Game
        required: [license.txt]
        build:
          %s
          env:
            RELEASE_CHANNEL: '$literal {output}'
      windows:
        depot_id: 102
        output: builds/windows/Game.exe
        build:
          %s
    post_build:
      - from: legal/license.txt
`, tc.engine, tc.linuxBuild, tc.windowsBuild)
			path := filepath.Join(root, "steam-release.yaml")
			if err := os.WriteFile(path, []byte(configuration), 0o600); err != nil {
				t.Fatal(err)
			}

			var events []release.Event
			svc, err := Load(path, release.ReporterFunc(func(event release.Event) {
				events = append(events, event)
			}))
			if err != nil {
				t.Fatal(err)
			}
			selection := RunOptions{Targets: []string{"linux"}}
			plan, err := svc.Plan("public", selection, tc.upload)
			if err != nil || len(plan.Targets) != 1 || plan.Targets[0].Name != "linux" ||
				!slices.Equal(plan.Upload.DepotIDs, []uint64{101}) || plan.Upload.Preview != tc.upload.Preview || plan.Upload.SetLive != tc.upload.SetLive {
				t.Fatalf("incorrect release plan: %#v; %v", plan, err)
			}
			if _, err := os.Stat(filepath.Join(root, ".steam-releaser")); !os.IsNotExist(err) {
				t.Fatalf("planning wrote release files: %v", err)
			}

			output := filepath.Join(root, "builds", "linux", "Game")
			runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
				if command.WorkingDir != root {
					return release.RunResult{}, fmt.Errorf("unexpected working directory for %s: %s", command.Label, command.WorkingDir)
				}
				switch command.Label {
				case "Godot import":
					if tc.engine != "godot" || command.Program != enginePath || len(command.Environment) != 0 ||
						!slices.Equal(command.Args, []string{"--headless", "--path", root, "--import"}) {
						return release.RunResult{}, fmt.Errorf("unexpected Godot import: %#v", command)
					}
				case "linux":
					if command.Program != enginePath || !maps.Equal(command.Environment, map[string]string{"RELEASE_CHANNEL": "$literal {output}"}) {
						return release.RunResult{}, fmt.Errorf("incorrect build executable or environment: %#v", command)
					}
					if tc.engine == "godot" && !slices.Equal(command.Args, []string{"--headless", "--path", root, "--export-release", "Linux Desktop", output}) {
						return release.RunResult{}, fmt.Errorf("incorrect Godot export: %#v", command.Args)
					}
					if tc.engine == "unity" && (!slices.Contains(command.Args, "-batchmode") || !slices.Contains(command.Args, "-buildLinux64Player") || !slices.Contains(command.Args, output)) {
						return release.RunResult{}, fmt.Errorf("incorrect Unity build: %#v", command.Args)
					}
					return release.RunResult{}, os.WriteFile(output, []byte("game"), 0o700)
				case "SteamCMD upload":
					if len(command.Args) != 7 || !matchesPlannedScriptTemplate(command.Args[5], plan.Upload.BuildScript) {
						return release.RunResult{}, fmt.Errorf("unexpected generated script path: %#v", command.Args)
					}
					actualScript := command.Args[5]
					want := []string{"+@NoPromptForPassword", "1", "+login", "builder", "+run_app_build", actualScript, "+quit"}
					if command.Program != steamPath || len(command.Environment) != 0 || !slices.Equal(command.Args, want) {
						return release.RunResult{}, fmt.Errorf("incorrect SteamCMD invocation: %#v", command)
					}
					for _, artifact := range []string{output, filepath.Join(root, "builds", "linux", "license.txt"), actualScript} {
						if _, err := os.Stat(artifact); err != nil {
							return release.RunResult{}, fmt.Errorf("upload started before %s was ready: %w", artifact, err)
						}
					}
					if tc.upload.Preview {
						return release.RunResult{Output: "Successfully finished AppID 100 build preview."}, nil
					}
					return release.RunResult{Output: "Successfully finished AppID 100 build (BuildID 12345)."}, nil
				default:
					return release.RunResult{}, fmt.Errorf("unexpected process: %s", command.Label)
				}
				return release.RunResult{}, nil
			}}
			svc.Runner = runner
			result, err := svc.Ship(context.Background(), "public", selection, tc.upload)
			if err != nil {
				t.Fatalf("ship: %v", err)
			}
			wantLabels := []string{"linux", "SteamCMD upload"}
			if tc.engine == "godot" {
				wantLabels = append([]string{"Godot import"}, wantLabels...)
			}
			var labels []string
			for _, command := range runner.commands {
				labels = append(labels, command.Label)
			}
			if !slices.Equal(labels, wantLabels) || result.Username != "builder" || result.BuildID != tc.wantBuildID {
				t.Fatalf("incorrect release result: commands=%v result=%#v", labels, result)
			}
			if len(result.Scripts.DepotPaths) != 1 || result.Scripts.DepotPaths[101] == "" || !matchesPlannedScriptTemplate(result.BuildScript, plan.Upload.BuildScript) {
				t.Fatalf("incorrect scripts: %#v", result.Scripts)
			}
			appVDF, err := os.ReadFile(result.BuildScript)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(appVDF), `"AppID" "100"`) || !strings.Contains(string(appVDF), `"101"`) || strings.Contains(string(appVDF), `"102"`) {
				t.Fatalf("incorrect app or depot selection: %s", appVDF)
			}
			if tc.upload.Preview && (!strings.Contains(string(appVDF), `"Preview" "1"`) || strings.Contains(string(appVDF), `"SetLive"`)) ||
				!tc.upload.Preview && (!strings.Contains(string(appVDF), `"Preview" "0"`) || !strings.Contains(string(appVDF), `"SetLive" "qa"`)) {
				t.Fatalf("incorrect publishing options: %s", appVDF)
			}
			depotVDF, err := os.ReadFile(result.Scripts.DepotPaths[101])
			if err != nil || !strings.Contains(string(depotVDF), `"ContentRoot" "`+filepath.Join(root, "builds", "linux")+`"`) {
				t.Fatalf("incorrect depot root: %s; %v", depotVDF, err)
			}
			if _, err := os.Stat(filepath.Join(root, "builds", "windows")); !os.IsNotExist(err) {
				t.Fatalf("unselected target was built: %v", err)
			}
			var kinds []string
			for _, event := range events {
				kinds = append(kinds, event.Kind)
			}
			if !slices.Contains(kinds, "build-start") || !slices.Contains(kinds, "build-complete") || !slices.Contains(kinds, "build-ready") ||
				len(events) == 0 || events[len(events)-1].Level != release.LevelSuccess {
				t.Fatalf("missing release progress: %#v", events)
			}
		})
	}
}

func TestSharedDepotShipRequiresEveryTargetBeforeBuilding(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"builder", "steamcmd"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fake"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("STEAMCMD_BIN", filepath.Join(root, "steamcmd"))
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(t.TempDir(), "account"))
	configuration := `app_id: 100
targets:
  linux:
    depot_id: 101
    output: builds/linux/Game
    content: builds
    mappings:
      - local_path: linux/*
        depot_path: linux
    build:
      command: ['{project}/builder', '{output}']
  windows:
    depot_id: 101
    output: builds/windows/Game.exe
    content: builds
    mappings:
      - local_path: windows/*
        depot_path: windows
    build:
      command: ['{project}/builder', '{output}']
`
	path := filepath.Join(root, "steam-release.yaml")
	if err := os.WriteFile(path, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := Load(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
		if command.Label == "SteamCMD upload" {
			return release.RunResult{Output: "Successfully finished AppID 100 build (BuildID 12345)."}, nil
		}
		return release.RunResult{}, os.WriteFile(command.Args[0], []byte("game"), 0o700)
	}}
	svc.Runner = runner
	upload := steam.UploadOptions{Username: "builder"}
	partial := RunOptions{Targets: []string{"linux"}}
	for _, action := range []string{"ship", "upload"} {
		if action == "ship" {
			_, err = svc.Ship(context.Background(), "", partial, upload)
		} else {
			_, err = svc.Upload(context.Background(), "", partial, upload)
		}
		if err == nil || !strings.Contains(err.Error(), "select all targets") {
			t.Fatalf("%s accepted part of a shared depot: %v", action, err)
		}
		if len(runner.commands) != 0 {
			t.Fatalf("%s started a process before rejecting the selection: %#v", action, runner.commands)
		}
		for _, name := range []string{"builds", ".steam-releaser"} {
			if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
				t.Fatalf("%s created %s before rejecting the selection: %v", action, name, err)
			}
		}
	}

	result, err := svc.Ship(context.Background(), "", RunOptions{}, upload)
	if err != nil {
		t.Fatalf("shipping the complete shared depot: %v", err)
	}
	if len(runner.commands) != 3 || runner.commands[0].Label != "linux" || runner.commands[1].Label != "windows" || runner.commands[2].Label != "SteamCMD upload" {
		t.Fatalf("unexpected complete shared-depot workflow: %#v", runner.commands)
	}
	if len(result.Scripts.DepotPaths) != 1 || result.Scripts.DepotPaths[101] == "" {
		t.Fatalf("expected one shared depot: %#v", result.Scripts)
	}
	depotVDF, err := os.ReadFile(result.Scripts.DepotPaths[101])
	if err != nil || !strings.Contains(string(depotVDF), `"LocalPath" "linux/*"`) || !strings.Contains(string(depotVDF), `"LocalPath" "windows/*"`) {
		t.Fatalf("shared depot is missing a target mapping: %s; %v", depotVDF, err)
	}
}
