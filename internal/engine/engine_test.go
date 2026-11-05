package engine

import (
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestBuildEnvironmentIsLiteralIsolatedAndTargetScoped(t *testing.T) {
	for _, kind := range []string{"godot", "unity", "custom"} {
		t.Run(kind, func(t *testing.T) {
			document := planDocument(t, config.Engine{Type: kind})
			profile := document.Config.Profiles["release"]
			for _, name := range []string{"windows", "linux"} {
				target := profile.Targets[name]
				target.Build.Env = map[string]string{"EDITION": name, "LITERAL": "$VALUE={output}", "EMPTY": ""}
				profile.Targets[name] = target
			}
			plan, err := Plan(document, "release", nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, preparation := range plan.Prepare {
				if len(preparation.Environment) != 0 {
					t.Fatal("target env leaked into shared import")
				}
			}
			for _, target := range plan.Targets {
				want := profile.Targets[target.Name].Build.Env
				if !maps.Equal(target.Command.Environment, want) {
					t.Fatalf("env changed: %#v", target.Command.Environment)
				}
				target.Command.Environment["EDITION"] = "changed"
				if want["EDITION"] != target.Name {
					t.Fatal("planning aliased configuration env")
				}
			}
		})
	}
}

func TestUnityPlansOneProcessPerTargetSerially(t *testing.T) {
	document := planDocument(t, config.Engine{Type: "unity", Executable: "Unity"})
	plan, err := Plan(document, "release", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Parallelism != 1 || len(plan.Targets) != 2 {
		t.Fatalf("unexpected Unity plan: %#v", plan)
	}
	for _, target := range plan.Targets {
		if target.Command == nil {
			t.Fatalf("target %s has no command", target.Name)
		}
	}
}

func TestPlanParallelismAndTargetSelection(t *testing.T) {
	for _, test := range []struct {
		name     string
		engine   string
		limit    int
		selected []string
		want     int
	}{
		{name: "godot defaults to all targets", engine: "godot", want: 2},
		{name: "godot honors limit", engine: "godot", limit: 1, want: 1},
		{name: "godot caps limit", engine: "godot", limit: 10, want: 2},
		{name: "custom defaults to serial", engine: "custom", want: 1},
		{name: "custom honors limit", engine: "custom", limit: 2, want: 2},
		{name: "custom caps limit", engine: "custom", limit: 10, want: 2},
		{name: "unity stays serial", engine: "unity", limit: 10, want: 1},
		{name: "prebuilt stays serial", engine: "prebuilt", limit: 10, want: 1},
		{name: "selected targets set the limit", engine: "godot", selected: []string{"windows", "windows"}, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := planDocument(t, config.Engine{Type: test.engine, MaxParallel: test.limit})
			plan, err := Plan(document, "release", test.selected)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Parallelism != test.want {
				t.Fatalf("parallelism = %d, want %d", plan.Parallelism, test.want)
			}
			wantNames, wantDepots := []string{"linux", "windows"}, []uint64{102, 101}
			if len(test.selected) > 0 {
				wantNames, wantDepots = []string{"windows"}, []uint64{101}
			}
			var names []string
			for _, target := range plan.Targets {
				names = append(names, target.Name)
				if (target.Command == nil) != (test.engine == "prebuilt") {
					t.Fatalf("unexpected command for %s: %#v", test.engine, target.Command)
				}
			}
			if !slices.Equal(names, wantNames) || !slices.Equal(plan.Upload.DepotIDs, wantDepots) {
				t.Fatalf("targets/depots = %v/%v, want %v/%v", names, plan.Upload.DepotIDs, wantNames, wantDepots)
			}
		})
	}
}

func TestGodotPlanImportAndExport(t *testing.T) {
	t.Setenv("GODOT_BIN", "")
	for _, importBeforeBuild := range []bool{true, false} {
		document := planDocument(t, config.Engine{Type: "godot", Executable: "./tools/godot", ImportBeforeBuild: &importBeforeBuild})
		plan, err := Plan(document, "release", []string{"windows"})
		if err != nil {
			t.Fatal(err)
		}
		if (len(plan.Prepare) == 1) != importBeforeBuild {
			t.Fatalf("import enabled = %v, preparation = %#v", importBeforeBuild, plan.Prepare)
		}
		root := document.ProjectRoot()
		program := document.ResolveProject("tools/godot")
		if importBeforeBuild {
			wantArgs := []string{"--headless", "--path", root, "--import"}
			if !slices.Equal(plan.Prepare[0].Args, wantArgs) || plan.Prepare[0].Program != program {
				t.Fatalf("unexpected import command: %#v", plan.Prepare[0])
			}
		}
		command := plan.Targets[0].Command
		wantArgs := []string{"--headless", "--path", root, "--export-release", "Windows Desktop", document.ResolveProject("build/windows/game.exe"), "--verbose"}
		if command.Program != program || command.WorkingDir != root || !slices.Equal(command.Args, wantArgs) {
			t.Fatalf("unexpected export command: %#v", command)
		}
	}
}

func TestGodotPlanBuildsSharedDepotTargetsSeparately(t *testing.T) {
	document := planDocument(t, config.Engine{Type: "godot"})
	profile := document.Config.Profiles["release"]
	linux := profile.Targets["linux"]
	linux.DepotID = profile.Targets["windows"].DepotID
	profile.Targets["linux"] = linux
	document.Config.Profiles["release"] = profile
	plan, err := Plan(document, "release", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(plan.Upload.DepotIDs, []uint64{101}) {
		t.Fatalf("upload depots = %v", plan.Upload.DepotIDs)
	}
	if len(plan.Targets) != 2 || plan.Targets[0].Command.Args[3] != "--export-release" || plan.Targets[1].Command.Args[3] != "--export-release" {
		t.Fatalf("Godot export commands = %#v", plan.Targets)
	}
}

func TestUnityBuildModes(t *testing.T) {
	for _, test := range []struct {
		name         string
		profile      string
		target       string
		engineMethod string
		targetMethod string
		wantMethod   string
		wantBuildArg string
		noGraphics   *bool
	}{
		{name: "windows target", wantBuildArg: "-buildWindows64Player"},
		{name: "linux target", target: "StandaloneLinux64", wantBuildArg: "-buildLinux64Player"},
		{name: "macos target", target: "StandaloneOSX", wantBuildArg: "-buildOSXUniversalPlayer"},
		{name: "windows 32-bit target", target: "StandaloneWindows", wantBuildArg: "-buildWindowsPlayer"},
		{name: "build profile", profile: "Assets/Release.asset"},
		{name: "engine method", engineMethod: "Project.Build", wantMethod: "Project.Build"},
		{name: "target method overrides engine", engineMethod: "Project.Build", targetMethod: "Target.Build", wantMethod: "Target.Build"},
		{name: "profile with method", profile: "Assets/Release.asset", targetMethod: "Target.Build", wantMethod: "Target.Build"},
		{name: "graphics required", noGraphics: new(false)},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := planDocument(t, config.Engine{Type: "unity", ExecuteMethod: test.engineMethod})
			target := document.Config.Profiles["release"].Targets["windows"]
			if test.target != "" {
				target.Build.Target = test.target
			}
			target.Build.Profile = test.profile
			target.Build.ExecuteMethod = test.targetMethod
			target.Build.NoGraphics = test.noGraphics
			document.Config.Profiles["release"].Targets["windows"] = target
			plan, err := Plan(document, "release", []string{"windows"})
			if err != nil {
				t.Fatal(err)
			}
			wantArgs := []string{"-batchmode", "-quit", "-projectPath", document.ProjectRoot(), "-logFile", document.ResolveProject(".steam-releaser/logs/release/windows.log")}
			if test.noGraphics == nil || *test.noGraphics {
				wantArgs = append(wantArgs, "-nographics")
			}
			if test.profile != "" {
				wantArgs = append(wantArgs, "-activeBuildProfile", test.profile)
			} else {
				wantArgs = append(wantArgs, "-buildTarget", target.Build.Target)
			}
			if test.wantMethod == "" {
				buildArg := test.wantBuildArg
				if buildArg == "" {
					if test.profile != "" {
						buildArg = "-build"
					} else {
						buildArg = "-buildWindows64Player"
					}
				}
				wantArgs = append(wantArgs, buildArg, document.ResolveProject(target.Output))
			} else {
				wantArgs = append(wantArgs, "-executeMethod", test.wantMethod, "-steamReleaserOutput", document.ResolveProject(target.Output), "-steamReleaserProfile", "release", "-steamReleaserTarget", "windows")
			}
			wantArgs = append(wantArgs, "--verbose")
			if !slices.Equal(plan.Targets[0].Command.Args, wantArgs) {
				t.Fatalf("args = %q, want %q", plan.Targets[0].Command.Args, wantArgs)
			}
		})
	}
}

func TestCustomPlaceholdersPreserveLiteralPaths(t *testing.T) {
	document := planDocument(t, config.Engine{Type: "custom"})
	document.Config.Project.Root = "project with {target}"
	target := document.Config.Profiles["release"].Targets["windows"]
	target.Build.Command = []string{"{project}/tools/builder", "{output}"}
	target.Build.Args = []string{"{content}", "{profile}/{target}", "{unknown}", "{project}"}
	document.Config.Profiles["release"].Targets["windows"] = target
	originalCommand, originalArgs := slices.Clone(target.Build.Command), slices.Clone(target.Build.Args)
	plan, err := Plan(document, "release", []string{"windows"})
	if err != nil {
		t.Fatal(err)
	}
	command := plan.Targets[0].Command
	wantArgs := []string{document.ResolveProject(target.Output), document.ResolveProject(target.Content), "release/windows", "{unknown}", document.ProjectRoot()}
	if command.Program != document.ResolveProject("tools/builder") || !slices.Equal(command.Args, wantArgs) {
		t.Fatalf("unexpected command: %#v; want arguments %q", command, wantArgs)
	}
	build := document.Config.Profiles["release"].Targets["windows"].Build
	if !slices.Equal(build.Command, originalCommand) || !slices.Equal(build.Args, originalArgs) {
		t.Fatal("planning modified the configured command")
	}
}

func planDocument(t *testing.T, engine config.Engine) *config.Document {
	t.Helper()
	return &config.Document{
		Path: filepath.Join(t.TempDir(), "steam-release.yaml"),
		Config: config.Config{
			Version: 1,
			Project: config.Project{Name: "Example", Engine: engine},
			Profiles: map[string]config.Profile{"release": {
				AppID: 100,
				Targets: map[string]config.Target{
					"windows": {Platform: "windows", DepotID: 101, Output: "build/windows/game.exe", Content: "build/windows", Build: config.Build{Preset: "Windows Desktop", Target: "StandaloneWindows64", Command: []string{"builder"}, Args: []string{"--verbose"}}},
					"linux":   {Platform: "linux", DepotID: 102, Output: "build/linux/game", Content: "build/linux", Build: config.Build{Preset: "Linux", Target: "StandaloneLinux64", Command: []string{"builder"}}},
				},
			}},
		},
	}
}
