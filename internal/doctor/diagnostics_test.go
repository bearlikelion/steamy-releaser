package doctor

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestDoctorGodotDiagnosticsAndSelection(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "editor")
	t.Setenv("GODOT_BIN", tool)
	t.Setenv("STEAMCMD_BIN", tool)
	for name, contents := range map[string]string{
		"editor": "fake", "project.godot": "fixture",
		"export_presets.cfg": "[preset.0]\nname=\"Linux Desktop\"\n[preset.1]\nname=unquoted-invalid\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	document := &config.Document{Path: filepath.Join(root, "steam-release.yaml"), Config: config.Config{
		Project: config.Project{Name: "Game", Engine: config.Engine{Type: "godot"}},
		Profiles: map[string]config.Profile{"release": {Targets: map[string]config.Target{
			"linux":   {Output: "build/linux/Game", Build: config.Build{Preset: "Linux Desktop"}},
			"missing": {Output: "build/missing/Game", Build: config.Build{Preset: "unquoted-invalid"}},
		}}},
	}}
	report, err := Run(document, "", []string{"linux"})
	if err != nil || !report.OK() || report.Project != "Game" || report.Profile != "release" {
		t.Fatalf("valid selected target failed diagnostics: %#v, %v", report, err)
	}
	assertIssue(t, report, "artifact.missing", "linux", SeverityWarning)
	for _, issue := range report.Issues {
		if issue.Target == "missing" {
			t.Fatalf("diagnosed unselected target: %#v", issue)
		}
	}
	report, err = Run(document, "", nil)
	if err != nil || report.OK() {
		t.Fatalf("missing preset was accepted: %#v, %v", report, err)
	}
	assertIssue(t, report, "godot.preset", "missing", SeverityError)
	if err := os.Remove(filepath.Join(root, "project.godot")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(tool); err != nil {
		t.Fatal(err)
	}
	report, err = Run(document, "", []string{"linux"})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"project.godot", "tool.godot", "tool.steamcmd"} {
		assertIssue(t, report, code, "", SeverityError)
	}
}

func TestDoctorUnityAndMissingProject(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "Unity")
	t.Setenv("UNITY_BIN", tool)
	t.Setenv("STEAMCMD_BIN", tool)
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	document := &config.Document{Path: filepath.Join(root, "steam-release.yaml"), Config: config.Config{
		Project: config.Project{Root: "missing", Engine: config.Engine{Type: "unity"}},
		Profiles: map[string]config.Profile{"release": {Targets: map[string]config.Target{
			"linux": {Output: "build/Game", Build: config.Build{Target: "StandaloneLinux64"}},
		}}},
	}}
	report, err := Run(document, "", nil)
	if err != nil || report.OK() {
		t.Fatalf("missing Unity project was accepted: %#v, %v", report, err)
	}
	assertIssue(t, report, "project.root", "", SeverityError)
	assertIssue(t, report, "project.unity", "", SeverityError)
	assertIssue(t, report, "unity.module", "linux", SeverityWarning)
	if err := os.Remove(tool); err != nil {
		t.Fatal(err)
	}
	report, err = Run(document, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	assertIssue(t, report, "tool.unity", "", SeverityError)
	if _, err := Run(document, "", []string{"unknown"}); err == nil {
		t.Fatal("accepted unknown target")
	}
}

func TestDoctorUnityBuildProfileMustExist(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "Unity")
	t.Setenv("UNITY_BIN", tool)
	t.Setenv("STEAMCMD_BIN", tool)
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(root, "ProjectSettings")
	if err := os.MkdirAll(settings, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settings, "ProjectVersion.txt"), []byte("m_EditorVersion: 6000.0.72f1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	document := &config.Document{Path: filepath.Join(root, "steam-release.yaml"), Config: config.Config{
		Project: config.Project{Engine: config.Engine{Type: "unity"}},
		Profiles: map[string]config.Profile{"release": {Targets: map[string]config.Target{
			"windows": {Output: "Builds/windows/Game.exe", Build: config.Build{Profile: "Assets/Settings/Windows.asset"}},
		}}},
	}}
	report, err := Run(document, "", nil)
	if err != nil || report.OK() {
		t.Fatalf("missing Unity Build Profile was accepted: %#v, %v", report, err)
	}
	assertIssue(t, report, "unity.profile", "windows", SeverityError)
	profilePath := document.ResolveProject("Assets/Settings/Windows.asset")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err = Run(document, "", nil)
	if err != nil || !report.OK() {
		t.Fatalf("existing Unity Build Profile failed diagnostics: %#v, %v", report, err)
	}
}

func TestDoctorUnitySuggestsNestedProjectFolder(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "Fletch")
	for _, folder := range []string{"Assets", "ProjectSettings"} {
		if err := os.MkdirAll(filepath.Join(project, folder), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "ProjectSettings", "ProjectVersion.txt"), []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	document := &config.Document{Path: filepath.Join(root, "steam-release.yaml"), Config: config.Config{
		Project: config.Project{Engine: config.Engine{Type: "unity"}},
		Profiles: map[string]config.Profile{"release": {Targets: map[string]config.Target{
			"linux": {Output: "build/Game", Build: config.Build{Target: "StandaloneLinux64"}},
		}}},
	}}
	report, err := Run(document, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range report.Issues {
		if issue.Code == "project.unity" && issue.Severity == SeverityError && strings.Contains(issue.Message, strconv.Quote(project)) {
			return
		}
	}
	t.Fatalf("missing nested Unity project suggestion: %#v", report.Issues)
}

func assertIssue(t *testing.T, report Report, code, target string, severity Severity) {
	t.Helper()
	for _, issue := range report.Issues {
		if issue.Code == code && issue.Target == target && issue.Severity == severity {
			return
		}
	}
	t.Errorf("missing %s issue for %q with severity %s: %#v", code, target, severity, report.Issues)
}
