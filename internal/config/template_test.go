package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestAnnotatedOptionsCanBeEnabledAtTheirGeneratedScope(t *testing.T) {
	for _, tc := range []struct{ engine, setting string }{
		{"prebuilt", ""}, {"godot", "Linux Desktop"},
		{"unity", "StandaloneLinux64"}, {"unity", "Assets/Settings/Linux.asset"},
		{"custom", "build-game"},
	} {
		t.Run(tc.engine+tc.setting, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "steam-release.yaml")
			_, err := Initialize(InitOptions{Path: path, Engine: tc.engine, AppID: 100,
				Targets: []InitTarget{{Name: "linux", DepotID: 101, Output: "builds/linux/Game", BuildSetting: tc.setting}},
			})
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source := string(contents)
			// Unity's target/profile examples are alternatives to the active setting.
			source = strings.ReplaceAll(source, "# target: StandaloneWindows64", "# Alternative remains disabled")
			source = strings.ReplaceAll(source, "# profile: Assets/Settings/Windows.asset", "# Alternative remains disabled")
			if err := os.WriteFile(path, []byte(enableTemplateExamples(source)), 0o600); err != nil {
				t.Fatal(err)
			}
			document, err := Load(path)
			if err != nil {
				t.Fatalf("enabled template is invalid: %v\n%s", err, enableTemplateExamples(source))
			}
			project := document.Config.Project
			profile := document.Config.Profiles[DefaultProfile]
			target := profile.Targets["linux"]
			if project.Name != "My Game" || project.Root != "../game" || document.Config.Tools.SteamCMD != "/path/to/steamcmd" {
				t.Fatalf("project/tool examples enabled in the wrong scope: %#v", document.Config)
			}
			if profile.Description != "Release build" || len(profile.PostBuild) != 1 || profile.PostBuild[0].Targets[0] != "linux" || len(document.Config.Profiles["demo"].Targets) != 1 {
				t.Fatalf("profile examples enabled in the wrong scope: %#v", document.Config.Profiles)
			}
			if target.Platform != "linux" || target.Architecture != "x64" || len(target.Required) != 1 || len(target.Mappings) != 1 || len(target.Exclusions) != 2 {
				t.Fatalf("target examples enabled in the wrong scope: %#v", target)
			}
			if tc.engine != "prebuilt" && (len(target.Build.Args) != 1 || target.Build.Args[0] != "--verbose" || target.Build.Env["ENABLE_ANALYTICS"] != "0" || target.Build.Env["OPTIONAL_SETTING"] != "") {
				t.Fatalf("build examples lost token/string values: %#v", target.Build)
			}
			if tc.engine == "prebuilt" && strings.Contains(source, "# args:") {
				t.Fatal("prebuilt template contains build-only options")
			}
			if tc.engine == "godot" && (project.Engine.ShouldImport() || project.Engine.MaxParallel != 2) {
				t.Fatalf("Godot examples not enabled: %#v", project.Engine)
			}
			if tc.engine == "unity" && (target.Build.NoGraphics == nil || *target.Build.NoGraphics || target.Build.ExecuteMethod != "Company.Build.Release") {
				t.Fatalf("Unity examples not enabled: %#v", target.Build)
			}
		})
	}
}

// Only commented YAML examples are enabled; descriptive prose stays commented.
func enableTemplateExamples(source string) string {
	key := regexp.MustCompile(`^[a-z_][a-z0-9_-]*:`)
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		prefix, comment, ok := strings.Cut(line, "# ")
		if !ok || strings.TrimSpace(prefix) != "" {
			continue
		}
		if key.MatchString(comment) || strings.HasPrefix(comment, " ") {
			lines[i] = prefix + comment
		}
	}
	return strings.Join(lines, "\n")
}
