package artifact

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
)

func artifactDocument(t *testing.T, profile config.Profile) *config.Document {
	t.Helper()
	return &config.Document{
		Path:   filepath.Join(t.TempDir(), "steam-release.yaml"),
		Config: config.Config{Profiles: map[string]config.Profile{"release": profile}},
	}
}

func writeArtifact(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareCreatesBuildDirectoriesOnly(t *testing.T) {
	document := artifactDocument(t, config.Profile{})
	plan := &release.Plan{Engine: "unity", Profile: "release", Targets: []release.TargetPlan{
		{Name: "built", Output: document.ResolveProject("build/game/Game"), Content: document.ResolveProject("content/game"), Command: &release.CommandPlan{}},
		{Name: "prebuilt", Output: document.ResolveProject("existing/Game"), Content: document.ResolveProject("existing")},
	}}
	if err := Prepare(document, plan); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"build/game", "content/game", ".steam-releaser/logs/release"} {
		info, err := os.Stat(document.ResolveProject(path))
		if err != nil || !info.IsDir() {
			t.Errorf("directory %s was not prepared: %v", path, err)
		}
	}
	if _, err := os.Stat(document.ResolveProject("existing")); !os.IsNotExist(err) {
		t.Fatalf("prebuilt content was created: %v", err)
	}
}

func TestValidateArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		files    map[string]string
		required []string
		wantErr  string
	}{
		{"valid recursive requirement", map[string]string{"game": "binary", "data/level.pak": "level"}, []string{"data/**/*.pak"}, ""},
		{"missing output", map[string]string{"readme.txt": "readme"}, nil, "missing build output"},
		{"gitkeep is not content", map[string]string{".gitkeep": ""}, nil, "content directory is empty"},
		{"empty content", nil, nil, "content directory is empty"},
		{"missing requirement", map[string]string{"game": "binary"}, []string{"data/**/*.pak"}, "missing required artifact"},
		{"malformed glob", map[string]string{"game": "binary"}, []string{"["}, "missing required artifact"},
		{"traversal requirement", map[string]string{"game": "binary"}, []string{"../secret"}, "unsafe required pattern"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := artifactDocument(t, config.Profile{Targets: map[string]config.Target{
				"linux": {Output: "build/game", Content: "build", Required: tc.required},
			}})
			if err := os.MkdirAll(document.ResolveProject("build"), 0o755); err != nil {
				t.Fatal(err)
			}
			for path, content := range tc.files {
				writeArtifact(t, document.ResolveProject(filepath.Join("build", path)), content, 0o644)
			}
			err := Validate(document, "", nil)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("Validate = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateOnlySelectedTargetsAndAggregatesErrors(t *testing.T) {
	document := artifactDocument(t, config.Profile{Targets: map[string]config.Target{
		"linux":   {Output: "linux/game", Content: "linux"},
		"windows": {Output: "windows/game.exe", Content: "windows"},
	}})
	writeArtifact(t, document.ResolveProject("linux/game"), "binary", 0o755)
	if err := Validate(document, "release", []string{"linux"}); err != nil {
		t.Fatal(err)
	}
	err := Validate(document, "release", nil)
	if err == nil || !strings.Contains(err.Error(), "missing build output") || !strings.Contains(err.Error(), "missing content directory") {
		t.Fatalf("expected both missing artifact errors, got %v", err)
	}
	if err := Validate(document, "release", []string{"unknown"}); err == nil {
		t.Fatal("accepted unknown target")
	}
}

func TestApplyCopyRulesCopiesTreesAndFiltersTargets(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		name := "selected targets"
		var targets []string
		if restricted {
			name, targets = "rule targets", []string{"linux"}
		}
		t.Run(name, func(t *testing.T) {
			selected := []string{"linux"}
			if restricted {
				selected = nil
			}
			document := artifactDocument(t, config.Profile{
				Targets: map[string]config.Target{"linux": {Content: "build/linux"}, "windows": {Content: "build/windows"}},
				PostBuild: []config.CopyRule{
					{From: "extras/*.txt", Targets: targets},
					{From: "extras/runtime", To: "support", Targets: targets},
				},
			})
			writeArtifact(t, document.ResolveProject("extras/license.txt"), "license", 0o644)
			writeArtifact(t, document.ResolveProject("extras/runtime/bin/helper"), "helper", 0o755)
			// A second copy must truncate stale destination contents.
			writeArtifact(t, document.ResolveProject("build/linux/license.txt"), "old, longer license", 0o644)
			var events []release.Event
			err := ApplyCopyRules(document, "", selected, release.ReporterFunc(func(event release.Event) { events = append(events, event) }))
			if err != nil {
				t.Fatal(err)
			}
			for path, want := range map[string]string{"license.txt": "license", "support/runtime/bin/helper": "helper"} {
				got, err := os.ReadFile(document.ResolveProject("build/linux/" + path))
				if err != nil || string(got) != want {
					t.Fatalf("copied %s = %q, %v; want %q", path, got, err, want)
				}
			}
			if _, err := os.Stat(document.ResolveProject("build/windows")); !os.IsNotExist(err) {
				t.Fatalf("copied files to excluded target: %v", err)
			}
			if len(events) != 2 || events[0].Target != "linux" || events[1].Target != "linux" {
				t.Fatalf("unexpected copy events: %#v", events)
			}
			if runtime.GOOS != "windows" {
				info, err := os.Stat(document.ResolveProject("build/linux/support/runtime/bin/helper"))
				if err != nil || info.Mode().Perm() != 0o755 {
					t.Fatalf("executable permissions were not preserved: %v, %v", info, err)
				}
			}
		})
	}
}

func TestApplyCopyRulesRejectsUnsafeOrUnmatchedPaths(t *testing.T) {
	for _, tc := range []struct {
		name, from, to, wantErr string
	}{
		{"source traversal", "../outside", "", "source must stay inside"},
		{"absolute source", filepath.Join(t.TempDir(), "outside"), "", "source must stay inside"},
		{"destination traversal", "extras/*.txt", "../outside", "cannot escape content"},
		{"absolute destination", "extras/*.txt", t.TempDir(), "cannot escape content"},
		{"no matches", "missing/*.txt", "", "matched no files"},
		{"malformed glob", "[", "", "invalid post_build glob"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := artifactDocument(t, config.Profile{
				Targets:   map[string]config.Target{"linux": {Content: "build/linux"}},
				PostBuild: []config.CopyRule{{From: tc.from, To: tc.to}},
			})
			writeArtifact(t, document.ResolveProject("extras/license.txt"), "license", 0o644)
			err := ApplyCopyRules(document, "", nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ApplyCopyRules = %v, want %q", err, tc.wantErr)
			}
			if _, err := os.Stat(document.ResolveProject("build")); !os.IsNotExist(err) {
				t.Fatalf("invalid copy rule created build files: %v", err)
			}
		})
	}
}
