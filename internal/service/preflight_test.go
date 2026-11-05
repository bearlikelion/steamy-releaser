package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/auth"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/steam"
)

func TestShipChecksUploadPrerequisitesBeforeBuilding(t *testing.T) {
	for _, tc := range []struct {
		name, environmentUser, cachedUser, wantError string
		options                                      steam.UploadOptions
		missingSteamCMD, buildOnly                   bool
	}{
		{name: "missing account", wantError: "no Steam username"},
		{name: "invalid account", options: steam.UploadOptions{Username: "bad account"}, wantError: "Steam username must contain"},
		{name: "missing SteamCMD", options: steam.UploadOptions{Username: "builder"}, missingSteamCMD: true, wantError: "SteamCMD was not found"},
		{name: "preview needs SteamCMD", options: steam.UploadOptions{Username: "builder", Preview: true}, missingSteamCMD: true, wantError: "SteamCMD was not found"},
		{name: "provided account", options: steam.UploadOptions{Username: "builder"}},
		{name: "environment account", environmentUser: "builder"},
		{name: "cached account", cachedUser: "builder"},
		{name: "standalone build needs no upload prerequisites", missingSteamCMD: true, buildOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "cache"))
			t.Setenv("STEAM_USERNAME", tc.environmentUser)
			steamCMD := filepath.Join(root, "steamcmd")
			t.Setenv("STEAMCMD_BIN", steamCMD)
			if !tc.missingSteamCMD {
				if err := os.WriteFile(steamCMD, []byte("fake"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if tc.cachedUser != "" {
				if err := auth.CacheUsername(tc.cachedUser); err != nil {
					t.Fatal(err)
				}
			}
			builder := filepath.Join(root, "builder")
			if err := os.WriteFile(builder, []byte("fake"), 0o700); err != nil {
				t.Fatal(err)
			}
			document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
				DepotID: 101, Output: "build/Game", Content: "build",
				Build: config.Build{Command: []string{builder}},
			})
			runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
				if command.Label == "target" {
					return release.RunResult{}, os.WriteFile(document.ResolveProject("build/Game"), []byte("game"), 0o755)
				}
				return release.RunResult{Output: "Successfully finished AppID 100 build (BuildID 12345)."}, nil
			}}
			service := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
			var err error
			if tc.buildOnly {
				err = service.Build(context.Background(), "", RunOptions{})
			} else {
				_, err = service.Ship(context.Background(), "", RunOptions{}, tc.options)
			}
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("Ship = %v, want %q", err, tc.wantError)
				}
				if len(runner.commands) != 0 {
					t.Fatalf("failed preflight started commands: %#v", runner.commands)
				}
				for _, path := range []string{"build", ".steam-releaser"} {
					if _, err := os.Stat(document.ResolveProject(path)); !os.IsNotExist(err) {
						t.Fatalf("failed preflight created %s: %v", path, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantCommands := 2
			if tc.buildOnly {
				wantCommands = 1
			}
			if len(runner.commands) != wantCommands || runner.commands[0].Label != "target" {
				t.Fatalf("unexpected pipeline commands: %#v", runner.commands)
			}
			if !tc.buildOnly {
				upload := runner.commands[1]
				if upload.Label != "SteamCMD upload" || upload.Program != steamCMD || !strings.Contains(strings.Join(upload.Args, " "), "+login builder") {
					t.Fatalf("unexpected upload command: %#v", upload)
				}
			}
		})
	}
}

func TestUnsafeGeneratedScriptStopsBeforeBuildOrUpload(t *testing.T) {
	root := t.TempDir()
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.Description = `release" Preview 0 //`
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{}
	service := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	options := steam.UploadOptions{Username: "builder", Preview: true}
	if _, err := service.Plan("release", RunOptions{}, options); err == nil || !strings.Contains(err.Error(), "Steam build script") {
		t.Fatalf("planning accepted unsafe script input: %v", err)
	}
	if _, err := service.Ship(context.Background(), "release", RunOptions{}, options); err == nil || !strings.Contains(err.Error(), "Steam build script") {
		t.Fatalf("ship accepted unsafe script input: %v", err)
	}
	if _, err := service.Upload(context.Background(), "release", RunOptions{}, options); err == nil || !strings.Contains(err.Error(), "Steam build script") {
		t.Fatalf("upload accepted unsafe script input: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unsafe script input started commands: %#v", runner.commands)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("unsafe script input created files: %v, %v", entries, err)
	}
}

func TestEscapingGeneratedStateStopsBeforeBuild(t *testing.T) {
	root, external := t.TempDir(), t.TempDir()
	if err := os.Symlink(external, filepath.Join(root, ".steam-releaser")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	runner := &fakeRunner{}
	service := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if _, err := service.Ship(context.Background(), "release", RunOptions{}, steam.UploadOptions{Username: "builder"}); err == nil || !strings.Contains(err.Error(), "generated Steam state") {
		t.Fatalf("ship accepted escaping generated state: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("escaping generated state started commands: %#v", runner.commands)
	}
	if entries, err := os.ReadDir(external); err != nil || len(entries) != 0 {
		t.Fatalf("escaping generated state changed external directory: %v, %v", entries, err)
	}
}

func TestGeneratedStateInsideContentStopsBeforeBuild(t *testing.T) {
	root := t.TempDir()
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "Game", Content: ".", Build: config.Build{Command: []string{"builder"}},
	})
	runner := &fakeRunner{}
	service := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	options := steam.UploadOptions{Username: "builder"}
	if _, err := service.Plan("release", RunOptions{}, options); err == nil || !strings.Contains(err.Error(), "overlaps target") {
		t.Fatalf("plan accepted generated state inside content: %v", err)
	}
	if _, err := service.Ship(context.Background(), "release", RunOptions{}, options); err == nil || !strings.Contains(err.Error(), "overlaps target") {
		t.Fatalf("ship accepted generated state inside content: %v", err)
	}
	if _, err := service.Upload(context.Background(), "release", RunOptions{}, options); err == nil || !strings.Contains(err.Error(), "overlaps target") {
		t.Fatalf("upload accepted generated state inside content: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unsafe content started commands: %#v", runner.commands)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("unsafe content created files: %v, %v", entries, err)
	}
}

func TestExistingVDFIsCheckedBeforePlanningOrBuilding(t *testing.T) {
	for _, tc := range []struct {
		name, script, wantError string
		targets                 []string
		upload                  steam.UploadOptions
	}{
		{name: "missing script", script: "missing.vdf", wantError: "existing VDF does not exist"},
		{name: "directory instead of script", script: ".", wantError: "existing VDF is not a regular file"},
		{name: "preview", script: "custom.vdf", upload: steam.UploadOptions{Preview: true}, wantError: "cannot rewrite profile existing_vdf"},
		{name: "set live", script: "custom.vdf", upload: steam.UploadOptions{SetLive: "beta"}, wantError: "cannot rewrite profile existing_vdf"},
		{name: "filtered targets", script: "custom.vdf", targets: []string{"linux"}, wantError: "cannot rewrite profile existing_vdf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"custom.vdf", "builder", "steamcmd"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("STEAMCMD_BIN", filepath.Join(root, "steamcmd"))
			t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "cache"))
			target := func(name string, depot uint64) config.Target {
				return config.Target{DepotID: depot, Output: "build/" + name + "/Game", Content: "build/" + name, Build: config.Build{Command: []string{filepath.Join(root, "builder")}}}
			}
			document := testDocument(root, config.Engine{Type: "custom"}, target("linux", 101))
			profile := document.Config.Profiles["release"]
			profile.ExistingVDF = tc.script
			profile.Targets = map[string]config.Target{"linux": target("linux", 101), "windows": target("windows", 102)}
			document.Config.Profiles["release"] = profile
			runner := &fakeRunner{}
			svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
			run := RunOptions{Targets: tc.targets}
			tc.upload.Username = "builder"
			for _, action := range []string{"plan", "ship", "upload"} {
				var err error
				switch action {
				case "plan":
					_, err = svc.Plan("release", run, tc.upload)
				case "ship":
					_, err = svc.Ship(context.Background(), "release", run, tc.upload)
				case "upload":
					_, err = svc.Upload(context.Background(), "release", run, tc.upload)
				}
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Errorf("%s error = %v, want %q", action, err, tc.wantError)
				}
			}
			if len(runner.commands) != 0 {
				t.Fatalf("invalid script started a command: %#v", runner.commands)
			}
			for _, path := range []string{"build", ".steam-releaser"} {
				if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
					t.Fatalf("invalid script created %s: %v", path, err)
				}
			}
		})
	}
}

func TestUnityBuildRejectsRepositoryRootBeforeStartingEditor(t *testing.T) {
	for _, action := range []string{"build", "ship"} {
		for _, correctRoot := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/correctRoot=%t", action, correctRoot), func(t *testing.T) {
				root := t.TempDir()
				t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "cache"))
				repository := filepath.Join(root, "fletch")
				project := filepath.Join(repository, "Fletch")
				for _, folder := range []string{"Assets", "ProjectSettings"} {
					if err := os.MkdirAll(filepath.Join(project, folder), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(project, "ProjectSettings", "ProjectVersion.txt"), []byte("m_EditorVersion: 6000.3.21f1\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				tool := filepath.Join(root, "fake-editor")
				if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("UNITY_BIN", tool)
				t.Setenv("STEAMCMD_BIN", tool)
				configPath := filepath.Join(root, "steam-release.yaml")
				source := "project:\n  name: Reign Of Arrows\n  engine:\n    type: unity\napp_id: 100\ntargets:\n  linux:\n    depot_id: 101\n    output: builds/steam/linux/Game.x86_64\n    build:\n      target: StandaloneLinux64\n"
				if err := os.WriteFile(configPath, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
				selectedRoot := repository
				if correctRoot {
					selectedRoot = project
				}
				svc, err := LoadWithProjectRoot(configPath, selectedRoot, nil)
				if err != nil {
					t.Fatal(err)
				}
				runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
					if command.Label == "linux" {
						if command.WorkingDir != project || command.Args[3] != project {
							t.Errorf("wrong Unity project: %#v", command)
						}
						return release.RunResult{}, os.WriteFile(svc.Document.ResolveProject("builds/steam/linux/Game.x86_64"), []byte("game"), 0o755)
					}
					return release.RunResult{Output: "Successfully finished AppID 100 build (BuildID 12345)."}, nil
				}}
				svc.Runner = runner
				if action == "build" {
					err = svc.Build(context.Background(), "", RunOptions{})
				} else {
					_, err = svc.Ship(context.Background(), "", RunOptions{}, steam.UploadOptions{Username: "builder"})
				}
				if correctRoot {
					if err != nil || len(runner.commands) == 0 {
						t.Fatalf("valid nested project failed: %v; commands: %#v", err, runner.commands)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), strconv.Quote(project)) || !strings.Contains(err.Error(), "game project folder") {
					t.Fatalf("parent folder must fail with the child project suggestion: %v", err)
				}
				if len(runner.commands) != 0 {
					t.Fatalf("invalid Unity project started commands: %#v", runner.commands)
				}
				for _, path := range []string{"builds", ".steam-releaser"} {
					if _, err := os.Stat(filepath.Join(repository, path)); !os.IsNotExist(err) {
						t.Fatalf("invalid Unity project created %s: %v", path, err)
					}
				}
			})
		}
	}
}
