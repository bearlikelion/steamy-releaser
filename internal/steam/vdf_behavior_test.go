package steam

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestGenerateDepotMappingsAndSelectedTargets(t *testing.T) {
	root := t.TempDir()
	recursive := false
	document := &config.Document{Path: filepath.Join(root, "steam-release.yaml"), Config: config.Config{
		Project: config.Project{Root: "game"},
		Profiles: map[string]config.Profile{"release": {
			AppID: 100, Description: "Build beta release", BuildOutput: "steam-output",
			Targets: map[string]config.Target{
				"linux":   {DepotID: 102, Content: "build/linux", Mappings: []config.FileMapping{{LocalPath: "data/*", DepotPath: "assets", Recursive: &recursive}}, Exclusions: []string{"*.pdb", "private/*"}},
				"windows": {DepotID: 101, Content: "build/windows"},
			},
		}},
	}}
	scripts, err := Generate(document, "", []string{"linux"}, ScriptOptions{SetLive: "beta-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts.DepotPaths) != 1 || scripts.DepotPaths[102] == "" {
		t.Fatalf("unexpected depots: %#v", scripts.DepotPaths)
	}
	assertVDFContains(t, scripts.AppPath,
		`"Desc" "Build beta release"`, `"Preview" "0"`, `"SetLive" "beta-1"`,
		`"BuildOutput" "`+document.ResolveProject("steam-output")+`"`, `"102" "depot_build_102.vdf"`)
	assertVDFContains(t, scripts.DepotPaths[102],
		`"ContentRoot" "`+document.ResolveProject("build/linux")+`"`,
		`"LocalPath" "data/*"`, `"DepotPath" "assets"`, `"Recursive" "0"`,
		`"FileExclusion" "*.gitkeep"`, `"FileExclusion" "*.pdb"`, `"FileExclusion" "private/*"`)
	app, err := os.ReadFile(scripts.AppPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(app), "depot_build_101.vdf") {
		t.Fatalf("app script included unselected depot: %s", app)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(scripts.AppPath), "depot_build_101.vdf")); !os.IsNotExist(err) {
		t.Fatalf("generated unselected depot: %v", err)
	}
	if info, err := os.Stat(document.ResolveProject("steam-output")); err != nil || !info.IsDir() {
		t.Fatalf("build output directory was not created: %v", err)
	}

	// Regeneration replaces the files and sorts depot IDs independently of target order.
	scripts, err = Generate(document, "", []string{"linux", "windows"}, ScriptOptions{Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	assertVDFContains(t, scripts.DepotPaths[101], `"LocalPath" "*"`, `"DepotPath" "."`, `"Recursive" "1"`)
	app, err = os.ReadFile(scripts.AppPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(app), `"Preview" "1"`) || strings.Contains(string(app), "SetLive") || strings.Index(string(app), `"101"`) >= strings.Index(string(app), `"102"`) {
		t.Fatalf("regenerated app script is incorrect: %s", app)
	}
	entries, err := os.ReadDir(filepath.Dir(scripts.AppPath))
	if err != nil || len(entries) != 3 {
		t.Fatalf("unexpected generated files: %v, %v", entries, err)
	}
}

func TestGenerateExistingVDFPreservesFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "custom.vdf")
	const original = "custom hand-written Steam script\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	document := &config.Document{Path: filepath.Join(root, "steam-release.yaml"), Config: config.Config{
		Project: config.Project{Root: "game"},
		Profiles: map[string]config.Profile{"release": {
			ExistingVDF: "custom.vdf", Targets: map[string]config.Target{"linux": {DepotID: 101}, "windows": {DepotID: 102}},
		}},
	}}
	for _, tc := range []struct {
		name    string
		targets []string
		options ScriptOptions
		wantErr bool
	}{
		{"unchanged", nil, ScriptOptions{}, false},
		{"explicit all targets", []string{"linux", "windows"}, ScriptOptions{}, false},
		{"preview", nil, ScriptOptions{Preview: true}, true},
		{"set live", nil, ScriptOptions{SetLive: "beta"}, true},
		{"target subset", []string{"linux"}, ScriptOptions{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scripts, err := Generate(document, "", tc.targets, tc.options)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Generate error = %v, want error=%t", err, tc.wantErr)
			}
			if err == nil && (scripts.AppPath != path || len(scripts.DepotPaths) != 0) {
				t.Fatalf("did not use existing script: %#v", scripts)
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != original {
				t.Fatalf("existing script changed: %q, %v", contents, err)
			}
			if _, err := os.Stat(document.ProjectRoot()); !os.IsNotExist(err) {
				t.Fatalf("existing VDF generated project files: %v", err)
			}
		})
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(document, "", nil, ScriptOptions{}); err == nil || !strings.Contains(err.Error(), "existing VDF does not exist") {
		t.Fatalf("missing existing VDF error = %v", err)
	}
}

func TestGenerateSharedDepotCombinesMappingsAndRejectsPartialUpload(t *testing.T) {
	root := t.TempDir()
	document := &config.Document{Path: filepath.Join(root, "steam-release.yaml"), Config: config.Config{
		Project: config.Project{Root: "game"},
		Profiles: map[string]config.Profile{"release": {
			AppID: 100,
			Targets: map[string]config.Target{
				"windows": {DepotID: 101, Content: "builds", Mappings: []config.FileMapping{{LocalPath: "windows/*", DepotPath: "windows"}}},
				"linux":   {DepotID: 101, Content: "builds", Mappings: []config.FileMapping{{LocalPath: "linux/*", DepotPath: "linux"}}},
			},
		}},
	}}
	if _, err := Generate(document, "", []string{"windows"}, ScriptOptions{}); err == nil || !strings.Contains(err.Error(), "select all targets") {
		t.Fatalf("partial shared depot upload error = %v", err)
	}
	if _, err := os.Stat(document.ProjectRoot()); !os.IsNotExist(err) {
		t.Fatalf("partial upload created files: %v", err)
	}
	scripts, err := Generate(document, "", nil, ScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts.DepotPaths) != 1 {
		t.Fatalf("generated %d depots, want one", len(scripts.DepotPaths))
	}
	depot, err := os.ReadFile(scripts.DepotPaths[101])
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"LocalPath" "windows/*"`, `"DepotPath" "windows"`, `"LocalPath" "linux/*"`, `"DepotPath" "linux"`} {
		if !strings.Contains(string(depot), value) {
			t.Errorf("shared depot missing %s: %s", value, depot)
		}
	}
	if strings.Count(string(depot), `"FileMapping"`) != 2 {
		t.Fatalf("shared depot mappings = %s", depot)
	}
}

func assertVDFContains(t *testing.T, path string, values ...string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if !strings.Contains(string(contents), value) {
			t.Errorf("%s does not contain %q:\n%s", path, value, contents)
		}
	}
}
