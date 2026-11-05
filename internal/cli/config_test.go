package cli

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestCLIAuthorsAndUpdatesLiteralBuildOptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.yaml")
	base := []string{"--config", path, "--json"}
	var out, errOut bytes.Buffer
	run := func(args ...string) error {
		return Execute(context.Background(), append(slices.Clone(base), args...), &out, &errOut)
	}
	if err := run("init", "--engine", "godot", "--app-id", "100", "--target", "windows,101,build/windows/Game.exe,Windows Demo", "--build-arg=--verbose", "--build-arg=", "--build-arg=path with spaces", "--build-env=EDITION=demo", "--build-env=EMPTY=", "--build-env=LITERAL=$VALUE=a b"); err != nil {
		t.Fatal(err)
	}
	check := func(profile, name string, args []string, env map[string]string) {
		t.Helper()
		document, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		build := document.Config.Profiles[profile].Targets[name].Build
		if !slices.Equal(build.Args, args) || !maps.Equal(build.Env, env) {
			t.Fatalf("wrong options: %#v", build)
		}
	}
	initialArgs := []string{"--verbose", "", "path with spaces"}
	initialEnv := map[string]string{"EDITION": "demo", "EMPTY": "", "LITERAL": "$VALUE=a b"}
	check("release", "windows", initialArgs, initialEnv)
	if err := run("config", "add-profile", "demo", "--app-id", "200", "--target", "windows,201,build/demo/Game.exe,Windows Demo", "--build-arg=--verbose", "--build-env=EDITION=demo"); err != nil {
		t.Fatal(err)
	}
	check("release", "windows", initialArgs, initialEnv)
	check("demo", "windows", []string{"--verbose"}, map[string]string{"EDITION": "demo"})
	if err := run("config", "add-target", "demo", "--target", "linux,202,build/demo/Game,Linux Demo", "--build-arg=--", "--build-arg=--demo", "--build-env=EDITION=demo"); err != nil {
		t.Fatal(err)
	}
	check("demo", "linux", []string{"--", "--demo"}, map[string]string{"EDITION": "demo"})
	if err := run("config", "set-build-options", "windows", "--build-env=EDITION=full"); err != nil {
		t.Fatal(err)
	}
	check("release", "windows", initialArgs, map[string]string{"EDITION": "full"})
	if err := run("config", "set-build-options", "windows", "--clear-build-args"); err != nil {
		t.Fatal(err)
	}
	check("release", "windows", nil, map[string]string{"EDITION": "full"})
	if err := run("config", "set-build-options", "windows", "--clear-build-env"); err != nil {
		t.Fatal(err)
	}
	check("release", "windows", nil, nil)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "set-build-options", "windows"},
		{"config", "set-build-options", "windows", "--build-env=BROKEN"},
		{"config", "set-build-options", "windows", "--build-env=A=one", "--build-env=A=two"},
		{"config", "set-build-options", "windows", "--build-arg=--verbose", "--clear-build-args"},
		{"config", "add-profile", "other", "--app-id", "300", "--target", "windows,301,build/other/Game,Windows", "--target", "linux,302,build/other/Linux,Linux", "--build-env=A=one"},
	} {
		if err := run(args...); err == nil {
			t.Fatalf("invalid options accepted: %v", args)
		}
		updated, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(updated, original) {
			t.Fatalf("failed edit modified YAML: %v", args)
		}
	}
}

func TestCLIConfigurationEditsMatchDesktopTargets(t *testing.T) {
	for _, tc := range []struct{ engine, setting string }{
		{"prebuilt", ""}, {"godot", "Linux Desktop"},
		{"unity", "Assets/Settings/Linux.asset"}, {"custom", "build-game"},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "steam-release.yaml")
			var out, errOut bytes.Buffer
			base := []string{"--config", path, "--json"}
			if err := Execute(context.Background(), append(base, "init", "--app-id", "100", "--engine", tc.engine,
				"--target", "windows,101,build/windows/Game,"+tc.setting), &out, &errOut); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			annotated := bytes.Replace(original, []byte("# description: Release build"), []byte("description: Preserve my release"), 1)
			if err := os.WriteFile(path, append([]byte("# Keep this comment\n"), annotated...), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Execute(context.Background(), append(base, "config", "add-profile", "demo", "--app-id", "200",
				"--target", "windows,201,build/demo/Game,"+tc.setting), &out, &errOut); err != nil {
				t.Fatal(err)
			}
			if err := Execute(context.Background(), append(base, "config", "add-target", "demo", "--architecture", "arm64",
				"--target", "linux,linux,202,build/linux/Game,build/linux,"+tc.setting), &out, &errOut); err != nil {
				t.Fatal(err)
			}
			document, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			release := document.Config.Profiles["release"]
			demo := document.Config.Profiles["demo"]
			target := demo.Targets["linux"]
			if release.AppID != 100 || release.Description != "Preserve my release" || len(release.Targets) != 1 || demo.AppID != 200 || len(demo.Targets) != 2 {
				t.Fatalf("profile settings changed: %#v", document.Config.Profiles)
			}
			if target.DepotID != 202 || target.Architecture != "arm64" || target.Content != "build/linux" {
				t.Fatalf("target settings changed: %#v", target)
			}
			switch tc.engine {
			case "godot":
				if target.Build.Preset != tc.setting {
					t.Fatalf("Godot preset lost: %#v", target)
				}
			case "unity":
				if target.Build.Profile != tc.setting || target.Build.Target != "" {
					t.Fatalf("Unity Build Profile lost: %#v", target)
				}
			case "custom":
				if len(target.Build.Command) != 1 || target.Build.Command[0] != tc.setting {
					t.Fatalf("custom command lost: %#v", target)
				}
			}
			contents, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(contents), "# Keep this comment") {
				t.Fatalf("existing comment lost: %s, %v", contents, err)
			}
			for _, args := range [][]string{
				{"config", "add-profile", "demo", "--app-id", "300", "--target", "linux,301,other," + tc.setting},
				{"config", "add-profile", "invalid", "--app-id", "0", "--target", "linux,301,other," + tc.setting},
				{"config", "add-profile", "invalid", "--app-id", "300", "--target", "linux,301,other," + tc.setting, "--target", "linux,302,other," + tc.setting},
				{"config", "add-target", "demo", "--target", "linux,301,other," + tc.setting},
				{"config", "add-target", "missing", "--target", "linux,301,other," + tc.setting},
			} {
				if err := Execute(context.Background(), append(base, args...), &out, &errOut); err == nil {
					t.Fatalf("invalid edit succeeded: %v", args)
				}
				updated, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(updated, contents) {
					t.Fatalf("failed edit changed YAML: %v, %v", args, err)
				}
			}
		})
	}
}

func TestCLIAddTargetDefaultsToOnlyProfile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "steam-release.yaml")
	var out, errOut bytes.Buffer
	base := []string{"--config", path}
	if err := Execute(context.Background(), append(base, "init", "--app-id", "100", "--profile", "demo",
		"--engine", "prebuilt", "--target", "windows,101,build/windows"), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if err := Execute(context.Background(), append(base, "config", "add-target", "--target", "linux,102,build/linux"), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	document, err := config.Load(path)
	if err != nil || len(document.Config.Profiles["demo"].Targets) != 2 {
		t.Fatalf("target not added to default profile: %#v, %v", document, err)
	}
}
