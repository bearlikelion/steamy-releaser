package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/release"
)

func TestInitAndPlanPrebuilt(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "steam-release.yaml")
	var output, errors bytes.Buffer
	err := Execute(context.Background(), []string{
		"--config", configPath,
		"init", "--engine", "prebuilt", "--name", "Example", "--app-id", "100",
		"--target", "windows,windows,101,build/windows/game.exe,build/windows,",
		"--target", "linux,linux,102,build/linux/game,build/linux,",
	}, &output, &errors)
	if err != nil {
		t.Fatalf("init failed: %v\n%s", err, errors.String())
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	err = Execute(context.Background(), []string{"--config", configPath, "plan", "release"}, &output, &errors)
	if err != nil {
		t.Fatalf("plan failed: %v\n%s", err, errors.String())
	}
	if !strings.Contains(output.String(), "engine: prebuilt") {
		t.Fatalf("unexpected plan:\n%s", output.String())
	}
	for _, test := range []struct {
		name    string
		flags   []string
		preview bool
		setLive string
	}{
		{name: "preview", flags: []string{"--preview"}, preview: true},
		{name: "beta branch", flags: []string{"--set-live", "beta"}, setLive: "beta"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output.Reset()
			args := []string{"--config", configPath, "--json", "plan", "release", "--target", "linux"}
			if err := Execute(context.Background(), append(args, test.flags...), &output, &errors); err != nil {
				t.Fatal(err)
			}
			var plan release.Plan
			if err := json.Unmarshal(output.Bytes(), &plan); err != nil {
				t.Fatal(err)
			}
			if len(plan.Targets) != 1 || plan.Targets[0].Name != "linux" {
				t.Fatalf("target flag was not applied: %#v", plan.Targets)
			}
			if plan.Upload.Preview != test.preview || plan.Upload.SetLive != test.setLive {
				t.Fatalf("upload flags were not applied: %#v", plan.Upload)
			}
		})
	}
}

func TestMinimalInitAndDefaultProfileCommands(t *testing.T) {
	for _, profile := range []string{"release", "demo"} {
		t.Run(profile, func(t *testing.T) {
			directory := t.TempDir()
			configPath := filepath.Join(directory, "steam-release.yaml")
			steamCMD := filepath.Join(directory, "steamcmd")
			t.Setenv("STEAMCMD_BIN", steamCMD)
			if err := os.WriteFile(steamCMD, []byte("fake"), 0o700); err != nil {
				t.Fatal(err)
			}
			content := filepath.Join(directory, "builds", "windows")
			if err := os.MkdirAll(content, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(content, "Game.exe"), []byte("game"), 0o600); err != nil {
				t.Fatal(err)
			}
			var out, errOut bytes.Buffer
			if err := Execute(context.Background(), []string{"--config", configPath, "init", "--engine", "prebuilt", "--app-id", "100", "--profile", profile, "--target", "windows,101,builds/windows"}, &out, &errOut); err != nil {
				t.Fatal(err)
			}
			for _, command := range []string{"plan", "doctor", "build"} {
				out.Reset()
				if err := Execute(context.Background(), []string{"--config", configPath, "--json", command}, &out, &errOut); err != nil {
					t.Fatalf("%s without a profile: %v", command, err)
				}
				if command != "build" {
					var result struct {
						Profile string `json:"profile"`
					}
					if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Profile != profile {
						t.Fatalf("%s did not resolve profile %s: %s; %v", command, profile, out.String(), err)
					}
				}
			}
			// Invalid options must be rejected before either command launches SteamCMD.
			for _, command := range []string{"upload", "ship"} {
				if err := Execute(context.Background(), []string{"--config", configPath, command, "--preview", "--set-live", "beta"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "cannot be used together") {
					t.Fatalf("%s without a profile did not reach option validation: %v", command, err)
				}
			}
		})
	}
}

func TestParseInitTargetShortForm(t *testing.T) {
	for _, test := range []struct{ value, setting string }{
		{"windows,101,builds/windows", ""},
		{"windows,101,builds/windows,Windows Desktop", "Windows Desktop"},
		{`windows,101,"builds/windows, with spaces",Windows Desktop`, "Windows Desktop"},
	} {
		target, err := parseInitTarget(test.value)
		if err != nil || target.Name != "windows" || target.DepotID != 101 || target.BuildSetting != test.setting {
			t.Fatalf("target = %#v, error = %v", target, err)
		}
	}
	for _, value := range []string{"windows,101", "windows,wrong,builds/windows", "windows,101,builds/windows\nlinux,102,builds/linux"} {
		if _, err := parseInitTarget(value); err == nil {
			t.Fatalf("invalid target was accepted: %q", value)
		}
	}
}

func TestPlanUsesLocalProjectRootOverride(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "checkout")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "steam-release.yaml")
	if err := os.WriteFile(path, []byte("app_id: 100\ntargets:\n  windows: {depot_id: 101, content: builds/windows}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := Execute(context.Background(), []string{"--config", path, "--project-root", project, "--json", "plan"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var plan release.Plan
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 1 || plan.Targets[0].Content != filepath.Join(project, "builds", "windows") {
		t.Fatalf("plan did not use local project: %#v", plan.Targets)
	}
}
