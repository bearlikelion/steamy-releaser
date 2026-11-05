package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestMinimalConfigurations(t *testing.T) {
	for _, test := range []struct {
		name, fields, engine, output, content string
	}{
		{"prebuilt", "content: dist/windows", "prebuilt", "dist/windows", "dist/windows"},
		{"godot", "output: builds/windows/Game.exe\nbuild:\n  preset: Windows Desktop", "godot", "builds/windows/Game.exe", "builds/windows"},
		{"unity", "output: builds/windows/Game.exe\nbuild:\n  target: StandaloneWindows64", "unity", "builds/windows/Game.exe", "builds/windows"},
		{"unity profile", "output: builds/windows/Game.exe\nbuild:\n  profile: Assets/Windows.asset", "unity", "builds/windows/Game.exe", "builds/windows"},
		{"custom", "output: builds/windows/Game.exe\nbuild:\n  command: [make, windows]", "custom", "builds/windows/Game.exe", "builds/windows"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "app_id: 100\ntargets:\n  windows-x64:\n    depot_id: 101\n    " + strings.ReplaceAll(test.fields, "\n", "\n    ") + "\n"
			document := loadSource(t, source)
			if document.Config.Version != CurrentVersion || document.Config.Project.Name != filepath.Base(document.BaseDir()) || document.Config.Project.Engine.Type != test.engine {
				t.Fatalf("unexpected defaults: %#v", document.Config)
			}
			selection, err := document.Select("", nil)
			if err != nil {
				t.Fatal(err)
			}
			if selection.Name != DefaultProfile || selection.Profile.AppID != 100 || !slices.Equal(selection.TargetNames, []string{"windows-x64"}) {
				t.Fatalf("unexpected selection: %#v", selection)
			}
			target := selection.Profile.Targets["windows-x64"]
			if target.DepotID != 101 || target.Platform != "windows" || target.Output != test.output || target.Content != test.content {
				t.Fatalf("unexpected target defaults: %#v", target)
			}
		})
	}
}

func TestSimpleAndNamedProfileFormsResolveIdentically(t *testing.T) {
	simple := `app_id: 100
targets:
  linux:
    depot_id: 101
    output: builds/linux/Game
    build:
      preset: Linux
`
	document := loadSource(t, simple)
	// Write the explicit form at the same path to compare resolved paths and names.
	explicit := "version: 1\nproject:\n  name: " + document.Config.Project.Name + "\n  engine:\n    type: godot\nprofiles:\n  release:\n    " + strings.ReplaceAll(strings.TrimSpace(simple), "\n", "\n    ")
	explicit = strings.Replace(explicit, "depot_id: 101", "platform: linux\n        content: builds/linux\n        depot_id: 101", 1)
	if err := os.WriteFile(document.Path, []byte(explicit), 0o600); err != nil {
		t.Fatal(err)
	}
	other, err := Load(document.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document.Config, other.Config) {
		t.Fatalf("short and explicit YAML differ:\n%#v\n%#v", document.Config, other.Config)
	}
}

func TestExplicitOverridesAndDirectoryOutputs(t *testing.T) {
	document := loadSource(t, `project:
  name: Custom name
  root: ../game
  engine:
    type: prebuilt
app_id: 100
targets:
  dedicated:
    platform: linux
    architecture: arm64
    depot_id: 101
    output: release/Game.app
    content: release
  assets:
    depot_id: 102
    content: data
`)
	if document.Config.Project.Name != "Custom name" || document.ProjectRoot() != filepath.Clean(filepath.Join(document.BaseDir(), "../game")) {
		t.Fatalf("project overrides were lost: %#v", document.Config.Project)
	}
	selection, _ := document.Select("", nil)
	target := selection.Profile.Targets["dedicated"]
	if target.Platform != "linux" || target.Architecture != "arm64" || target.Output != "release/Game.app" || target.Content != "release" {
		t.Fatalf("target overrides were lost: %#v", target)
	}
	if selection.Profile.Targets["assets"].Platform != "" {
		t.Fatal("arbitrary target names must not invent a platform")
	}
}

func TestMinimalConfigRemainsStrict(t *testing.T) {
	base := "app_id: 100\ntargets:\n  linux:\n    depot_id: 101\n    content: dist/linux\n"
	for _, test := range []struct{ name, source, want string }{
		{"missing app", strings.Replace(base, "app_id: 100\n", "", 1), "invalid app_id"},
		{"missing depot", strings.Replace(base, "    depot_id: 101\n", "", 1), "invalid depot_id"},
		{"missing artifact path", strings.Replace(base, "    content: dist/linux\n", "", 1), "content directory"},
		{"unknown field", base + "surprise: true\n", "field surprise not found"},
		{"unknown target field", base + "    typo: true\n", "field typo not found"},
		{"unknown build field", base + "    build: {commmand: [make]}\n", "field commmand not found"},
		{"unknown engine field", "project: {engine: {typo: true}}\n" + base, "field typo not found"},
		{"unsupported version", "version: 2\n" + base, "unsupported configuration version"},
		{"explicit zero version", "version: 0\n" + base, "unsupported configuration version"},
		{"mixed profile forms", base + "profiles: {}\n", "not both"},
		{"mixed forms with zero app", "app_id: 0\nprofiles: {}\n", "not both"},
		{"duplicate app field", base + "app_id: 200\n", "already defined"},
		{"multiple documents", base + "---\n" + base, "more than one YAML document"},
		{"no implicit project upload", strings.Replace(base, "content: dist/linux", "output: Game.exe", 1), "content explicitly"},
		{"no implicit project parent upload", strings.Replace(base, "content: dist/linux", "output: ../Game.exe", 1), "content explicitly"},
		{"conflicting build engines", base + "    build: {preset: Linux, command: [make]}\n", "mix engines"},
		{"args without build", base + "    build: {args: [--release]}\n", "build.args needs"},
		{"build without output", base + "    build: {preset: Linux}\n", "must define output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "steam-release.yaml")
			if err := os.WriteFile(path, []byte(test.source), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDefaultProfileSelection(t *testing.T) {
	profile := Profile{Targets: map[string]Target{"windows": {}, "linux": {}}}
	for _, test := range []struct {
		name, requested, want string
		profiles              map[string]Profile
	}{
		{"release is preferred", "", "release", map[string]Profile{"beta": profile, "release": profile}},
		{"only profile", "", "demo", map[string]Profile{"demo": profile}},
		{"explicit profile", "beta", "beta", map[string]Profile{"beta": profile, "release": profile}},
		{"ambiguous", "", "", map[string]Profile{"beta": profile, "demo": profile}},
		{"unknown", "missing", "", map[string]Profile{"release": profile}},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := &Document{Config: Config{Profiles: test.profiles}}
			selection, err := document.Select(test.requested, []string{"windows", "linux", "windows"})
			if test.want == "" {
				if err == nil {
					t.Fatal("expected a profile selection error")
				}
				return
			}
			if err != nil || selection.Name != test.want || !slices.Equal(selection.TargetNames, []string{"windows", "linux"}) {
				t.Fatalf("selection = %#v, error = %v", selection, err)
			}
			if _, err := document.Select(test.want, []string{"missing"}); err == nil {
				t.Fatal("unknown target was accepted")
			}
		})
	}
}

func TestInitializeWritesAnnotatedConfig(t *testing.T) {
	for _, test := range []struct{ engine, path, setting string }{
		{"prebuilt", "builds/windows", ""},
		{"godot", "builds/windows/Game.exe", "Windows Desktop"},
		{"unity", "builds/windows/Game.exe", "StandaloneWindows64"},
		{"unity", "builds/windows/Game.exe", "Assets/Settings/Windows.asset"},
		{"custom", "builds/windows/Game.exe", "make"},
	} {
		t.Run(test.engine, func(t *testing.T) {
			document, err := Initialize(InitOptions{
				Path: filepath.Join(t.TempDir(), "steam-release.yaml"), Engine: test.engine, AppID: 100,
				Targets: []InitTarget{{Name: "windows", DepotID: 101, Output: test.path, BuildSetting: test.setting}},
			})
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(document.Path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(contents), "profiles:\n  release:") || !strings.Contains(string(contents), "# demo:") {
				t.Fatalf("generated YAML lacks profile examples:\n%s", contents)
			}
			loaded, err := Load(document.Path)
			if err != nil || !reflect.DeepEqual(loaded, document) || document.Config.Project.Engine.Type != test.engine {
				t.Fatalf("generated config changed on reload: %v", err)
			}
		})
	}
}

func TestExamplesLoad(t *testing.T) {
	paths, err := filepath.Glob("../../examples/*.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("find examples: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if _, err := Load(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func loadSource(t *testing.T, source string) *Document {
	t.Helper()
	path := filepath.Join(t.TempDir(), "steam-release.yaml")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return document
}
