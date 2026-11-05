package config

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildProcessOptionsLoadAndValidation(t *testing.T) {
	base := "app_id: 100\ntargets:\n  windows:\n    depot_id: 101\n    output: build/windows/Game.exe\n    build:\n"
	for _, tc := range []struct{ name, yaml, want string }{
		{"literal options", "      preset: Windows Demo\n      args: ['--verbose', '', 'a b', '$UNEXPANDED']\n      env: {EDITION: demo, EMPTY: '', LITERAL: '$UNEXPANDED={output}'}\n", ""},
		{"env without selector", "      env: {EDITION: demo}\n", "build.env needs"},
		{"empty env name", "      preset: Windows Demo\n      env: {'': value}\n", "name"},
		{"equals env name", "      preset: Windows Demo\n      env: {'A=B': value}\n", "name"},
		{"NUL env name", "      preset: Windows Demo\n      env: {\"A\\0B\": value}\n", "name"},
		{"NUL env value", "      preset: Windows Demo\n      env: {A: \"bad\\0value\"}\n", "value for"},
		{"NUL arg", "      preset: Windows Demo\n      args: [\"bad\\0arg\"]\n", "args[0]"},
		{"explicit prebuilt env", "      env: {EDITION: demo}\n", "prebuilt targets"},
		{"explicit prebuilt args", "      args: [--demo]\n", "prebuilt targets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := base + tc.yaml
			if strings.HasPrefix(tc.name, "explicit prebuilt") {
				source = "project: {engine: {type: prebuilt}}\n" + source
			}
			document, err := decode(filepath.Join(t.TempDir(), "release.yaml"), strings.NewReader(source))
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error = %v, want %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			build := document.Config.Profiles[DefaultProfile].Targets["windows"].Build
			if !slices.Equal(build.Args, []string{"--verbose", "", "a b", "$UNEXPANDED"}) || !maps.Equal(build.Env, map[string]string{"EDITION": "demo", "EMPTY": "", "LITERAL": "$UNEXPANDED={output}"}) {
				t.Fatalf("options changed during load: %#v", build)
			}
		})
	}
}

func TestUpdateBuildOptionsPreservesConfigurationAndFailedEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.yaml")
	source := `# keep project notes
app_id: 100
description: My release
targets:
  windows:
    depot_id: 101
    output: build/windows/Game.exe
    required: [Game.exe]
    build:
      preset: Windows Demo # keep preset notes
      args: [--verbose]
      env: {EDITION: demo}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"", "--label", "path with spaces", "$(literal)"}
	document, err := UpdateTargetBuildOptions(path, "", "windows", BuildOptionsPatch{Args: &args})
	if err != nil {
		t.Fatal(err)
	}
	target := document.Config.Profiles[DefaultProfile].Targets["windows"]
	if !slices.Equal(target.Build.Args, args) || target.Build.Env["EDITION"] != "demo" || target.Build.Preset != "Windows Demo" || !slices.Equal(target.Required, []string{"Game.exe"}) {
		t.Fatalf("unrelated settings lost: %#v", target)
	}
	contents, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(contents), "# keep project notes") || !strings.Contains(string(contents), "# keep preset notes") {
		t.Fatalf("comments lost: %s, %v", contents, err)
	}
	badEnv := map[string]string{"A=B": "bad"}
	for _, tc := range []struct {
		profile, target string
		patch           BuildOptionsPatch
	}{
		{"release", "windows", BuildOptionsPatch{Env: &badEnv}},
		{"missing", "windows", BuildOptionsPatch{Args: &args}},
		{"release", "missing", BuildOptionsPatch{Args: &args}},
		{"release", "windows", BuildOptionsPatch{}},
	} {
		if _, err := UpdateTargetBuildOptions(path, tc.profile, tc.target, tc.patch); err == nil {
			t.Fatal("invalid edit succeeded")
		}
		updated, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(contents, updated) {
			t.Fatal("failed edit changed file")
		}
	}
	// Converting the short form must preserve existing option groups.
	if _, err := AddProfile(path, "demo", Profile{AppID: 200, Targets: map[string]Target{"windows": {DepotID: 201, Output: "build/demo/Game.exe", Build: Build{Preset: "Windows Demo"}}}}); err != nil {
		t.Fatal(err)
	}
	empty := map[string]string{}
	document, err = UpdateTargetBuildOptions(path, "release", "windows", BuildOptionsPatch{Env: &empty})
	if err != nil {
		t.Fatal(err)
	}
	build := document.Config.Profiles["release"].Targets["windows"].Build
	if len(build.Env) != 0 || !slices.Equal(build.Args, args) || document.Config.Profiles["demo"].Targets["windows"].Build.Preset != "Windows Demo" {
		t.Fatalf("clear or conversion changed other settings: %#v", document.Config)
	}
}

func TestNativeArgumentWarningsRespectProjectDelimiters(t *testing.T) {
	for _, tc := range []struct {
		engine string
		args   []string
		count  int
	}{
		{"godot", []string{"--verbose", "--path=other", "--", "--export-debug"}, 1},
		{"godot", []string{"++", "--path", "other"}, 0},
		{"unity", []string{"-projectPath", "other", "-executeMethod=Project.Build", "-customPluginFlag"}, 2},
		{"custom", []string{"--path", "-projectPath", "--export-release"}, 0},
	} {
		if got := (Build{Args: tc.args}).ArgumentWarnings(tc.engine); len(got) != tc.count {
			t.Fatalf("%s warnings = %v, want %d", tc.engine, got, tc.count)
		}
	}
}

func TestBuildOptionEditsDoNotChangeTargetsSharingAYAMLAnchor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.yaml")
	source := []byte(`app_id: 100
targets:
  windows:
    depot_id: 101
    output: build/windows/Game.exe
    build: &shared
      preset: Shared Export
      args: [--verbose]
  linux:
    depot_id: 102
    output: build/linux/Game
    build: *shared
`)
	if err := os.WriteFile(path, source, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--quiet"}
	for _, name := range []string{"windows", "linux"} {
		if _, err := UpdateTargetBuildOptions(path, "release", name, BuildOptionsPatch{Args: &args}); err == nil {
			t.Fatalf("edited a shared build mapping for %s", name)
		}
		updated, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(updated, source) {
			t.Fatalf("shared mapping was changed: %s", updated)
		}
	}
}
