package steam

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestGeneratedScriptsRejectUnsafeValuesBeforeWriting(t *testing.T) {
	fields := []struct {
		name   string
		mutate func(*config.Document, string)
	}{
		{"description", func(document *config.Document, value string) {
			profile := document.Config.Profiles["release"]
			profile.Description = value
			document.Config.Profiles["release"] = profile
		}},
		{"build output", func(document *config.Document, value string) {
			profile := document.Config.Profiles["release"]
			profile.BuildOutput = value
			document.Config.Profiles["release"] = profile
		}},
		{"project path", func(document *config.Document, value string) { document.Config.Project.Root = value }},
		{"content path", func(document *config.Document, value string) {
			target := document.Config.Profiles["release"].Targets["linux"]
			target.Content = value
			document.Config.Profiles["release"].Targets["linux"] = target
		}},
		{"mapping source", func(document *config.Document, value string) {
			target := document.Config.Profiles["release"].Targets["linux"]
			target.Mappings = []config.FileMapping{{LocalPath: value}}
			document.Config.Profiles["release"].Targets["linux"] = target
		}},
		{"mapping destination", func(document *config.Document, value string) {
			target := document.Config.Profiles["release"].Targets["linux"]
			target.Mappings = []config.FileMapping{{DepotPath: value}}
			document.Config.Profiles["release"].Targets["linux"] = target
		}},
		{"exclusion", func(document *config.Document, value string) {
			target := document.Config.Profiles["release"].Targets["linux"]
			target.Exclusions = []string{value}
			document.Config.Profiles["release"].Targets["linux"] = target
		}},
	}
	values := []struct{ name, value string }{
		{"quote injection", `name" Preview 0 //`},
		{"nul", "name\x00suffix"},
		{"newline", "name\nsuffix"},
		{"unicode control", "name\u0085suffix"},
		{"invalid utf8", "name\xffsuffix"},
		{"oversized", strings.Repeat("x", 4096)},
	}
	for _, field := range fields {
		for _, value := range values {
			t.Run(field.name+"/"+value.name, func(t *testing.T) {
				root := t.TempDir()
				document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
					Project: config.Project{Root: "."},
					Profiles: map[string]config.Profile{"release": {AppID: 100, Targets: map[string]config.Target{
						"linux": {DepotID: 101, Content: "build/linux"},
					}}},
				}}
				field.mutate(document, value.value)
				options := ScriptOptions{Preview: true}
				if err := ValidateScriptSelection(document, "release", nil, options); err == nil || !strings.Contains(err.Error(), "Steam build script") {
					t.Fatalf("unsafe value passed preflight: %v", err)
				}
				if _, err := Generate(document, "release", nil, options); err == nil || !strings.Contains(err.Error(), "Steam build script") {
					t.Fatalf("unsafe value generated a script: %v", err)
				}
				entries, err := os.ReadDir(root)
				if err != nil || len(entries) != 0 {
					t.Fatalf("rejected input wrote files: %v, %v", entries, err)
				}
			})
		}
	}
}

func TestUnsafeGeneratedScriptLeavesExistingScriptsUntouched(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, ".steam-releaser", "generated", "release")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(directory, "app_build.vdf"), filepath.Join(directory, "depot_build_101.vdf")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("previous script"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
		Profiles: map[string]config.Profile{"release": {AppID: 100, Description: `unsafe" Preview 0 //`,
			Targets: map[string]config.Target{"linux": {DepotID: 101, Content: "build"}}}},
	}}
	if _, err := Generate(document, "release", nil, ScriptOptions{Preview: true}); err == nil {
		t.Fatal("unsafe value was accepted")
	}
	for _, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != "previous script" {
			t.Fatalf("rejected input changed %s: %q, %v", path, contents, err)
		}
	}
}

func TestGeneratedScriptsRejectEscapingMappingPathsOnEveryHost(t *testing.T) {
	paths := []string{
		"../private/*", "dir/../../private", `..\private\*`, `dir\..\..\private`,
		"/absolute/*", `C:\private\*`, `C:relative`, `\\server\share\*`,
		`dir\.. \private`, "file:stream",
	}
	for _, field := range []string{"local", "depot", "exclusion"} {
		for _, value := range paths {
			t.Run(field+"/"+value, func(t *testing.T) {
				root := t.TempDir()
				target := config.Target{DepotID: 101, Content: "build"}
				switch field {
				case "local":
					target.Mappings = []config.FileMapping{{LocalPath: value}}
				case "depot":
					target.Mappings = []config.FileMapping{{DepotPath: value}}
				case "exclusion":
					target.Exclusions = []string{value}
				}
				document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
					Profiles: map[string]config.Profile{"release": {AppID: 100, Targets: map[string]config.Target{"linux": target}}},
				}}
				if err := ValidateScriptSelection(document, "release", nil, ScriptOptions{}); err == nil || !strings.Contains(err.Error(), "relative") {
					t.Fatalf("unsafe mapping path %q passed preflight: %v", value, err)
				}
				if _, err := Generate(document, "release", nil, ScriptOptions{}); err == nil {
					t.Fatalf("unsafe mapping path %q generated a script", value)
				}
				if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
					t.Fatalf("unsafe mapping path wrote files: %v, %v", entries, err)
				}
			})
		}
	}
}

func TestGeneratedScriptsPreserveBackslashesAndSafeText(t *testing.T) {
	root := t.TempDir()
	document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
		Profiles: map[string]config.Profile{"release": {AppID: 100, Description: "Beta's release – {preview} // safe",
			Targets: map[string]config.Target{"windows": {DepotID: 101, Content: "build/windows", Mappings: []config.FileMapping{
				{LocalPath: `windows\Game\*`, DepotPath: `Game\Data`},
			}, Exclusions: []string{`symbols\*.pdb`}}}}},
	}}
	scripts, err := Generate(document, "release", nil, ScriptOptions{Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	assertVDFContains(t, scripts.AppPath, `"Desc" "Beta's release – {preview} // safe"`, `"Preview" "1"`)
	assertVDFContains(t, scripts.DepotPaths[101], `"LocalPath" "windows\Game\*"`, `"DepotPath" "Game\Data"`, `"FileExclusion" "symbols\*.pdb"`)
}

func TestExistingScriptDoesNotSerializeGeneratedFields(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "custom.vdf")
	if err := os.WriteFile(script, []byte("user supplied"), 0o600); err != nil {
		t.Fatal(err)
	}
	document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
		Profiles: map[string]config.Profile{"release": {AppID: 100, ExistingVDF: "custom.vdf", Description: `unused "description"`,
			Targets: map[string]config.Target{"linux": {DepotID: 101, Content: "build"}}}},
	}}
	scripts, err := Generate(document, "release", nil, ScriptOptions{})
	if err != nil || scripts.AppPath != script {
		t.Fatalf("existing script rejected or replaced: %#v, %v", scripts, err)
	}
}

func TestGeneratedScriptsDoNotWriteThroughEscapingSymlinks(t *testing.T) {
	for _, linkPath := range []string{".steam-releaser", filepath.Join(".steam-releaser", "generated"), filepath.Join(".steam-releaser", "output")} {
		t.Run(linkPath, func(t *testing.T) {
			root, external := t.TempDir(), t.TempDir()
			link := filepath.Join(root, linkPath)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			sentinels := []string{filepath.Join(external, "generated", "release", "app_build.vdf"), filepath.Join(external, "release", "app_build.vdf")}
			for _, sentinel := range sentinels {
				if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(sentinel, []byte("external file"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
				Profiles: map[string]config.Profile{"release": {AppID: 100,
					Targets: map[string]config.Target{"linux": {DepotID: 101, Content: "build"}}}},
			}}
			if err := ValidateScriptSelection(document, "release", nil, ScriptOptions{}); err == nil {
				t.Fatal("escaping generated-state path passed preflight")
			}
			if _, err := Generate(document, "release", nil, ScriptOptions{}); err == nil {
				t.Fatal("accepted generated-state path escaping the project")
			}
			for _, sentinel := range sentinels {
				contents, err := os.ReadFile(sentinel)
				if err != nil || string(contents) != "external file" {
					t.Fatalf("external file changed: %q, %v", contents, err)
				}
			}
		})
	}
}

func TestGeneratedScriptsAllowExplicitExternalBuildOutput(t *testing.T) {
	root, external := t.TempDir(), t.TempDir()
	output := filepath.Join(external, "steam-logs")
	document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
		Profiles: map[string]config.Profile{"release": {AppID: 100, BuildOutput: output,
			Targets: map[string]config.Target{"linux": {DepotID: 101, Content: "build"}}}},
	}}
	scripts, err := Generate(document, "release", nil, ScriptOptions{})
	if err != nil {
		t.Fatalf("explicit external output was rejected: %v", err)
	}
	if info, err := os.Stat(output); err != nil || !info.IsDir() {
		t.Fatalf("explicit external output was not created: %v", err)
	}
	assertVDFContains(t, scripts.AppPath, `"BuildOutput" "`+output+`"`)
}

func TestGeneratedScriptsRejectUnsafeProfileDirectoryNames(t *testing.T) {
	for _, name := range []string{".", "..", "...", "release.", "release ", "../outside", `..\outside`, "nested/name", `nested\name`, "C:drive"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
				Profiles: map[string]config.Profile{name: {AppID: 100, Targets: map[string]config.Target{
					"linux": {DepotID: 101, Content: "build"},
				}}},
			}}
			if _, err := Generate(document, name, nil, ScriptOptions{}); err == nil {
				t.Fatalf("unsafe profile name %q was accepted", name)
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatalf("unsafe profile name wrote files: %v, %v", entries, err)
			}
		})
	}
}

func TestGeneratedScriptsAllowInternalDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("state", filepath.Join(root, ".steam-releaser")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
		Profiles: map[string]config.Profile{"release": {AppID: 100,
			Targets: map[string]config.Target{"linux": {DepotID: 101, Content: "build"}}}},
	}}
	scripts, err := Generate(document, "release", nil, ScriptOptions{})
	if err != nil {
		t.Fatalf("internal generated-state symlink was rejected: %v", err)
	}
	assertVDFContains(t, scripts.AppPath, `"AppID" "100"`)
}
