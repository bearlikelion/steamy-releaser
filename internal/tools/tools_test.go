package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestResolveToolPrecedence(t *testing.T) {
	root := t.TempDir()
	name := "builder"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	paths := make(map[string]string)
	for _, source := range []string{"environment", "configuration", "path", "candidate"} {
		path := filepath.Join(root, source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fake executable"), 0o755); err != nil {
			t.Fatal(err)
		}
		paths[source] = path
	}
	t.Setenv("PATH", filepath.Dir(paths["path"]))
	for _, tc := range []struct {
		name, environment, configured string
		names, candidates             []string
		want                          Resolution
	}{
		{"environment overrides config", paths["environment"], paths["configuration"], []string{name}, []string{paths["candidate"]}, Resolution{paths["environment"], true, "environment TEST_RELEASE_TOOL"}},
		{"config overrides PATH", "", paths["configuration"], []string{name}, []string{paths["candidate"]}, Resolution{paths["configuration"], true, "configuration"}},
		{"relative config", "", filepath.Join("configuration", name), nil, nil, Resolution{paths["configuration"], true, "configuration"}},
		{"configured command on PATH", "", name, nil, nil, Resolution{paths["path"], true, "configuration"}},
		{"PATH overrides candidate", "", "", []string{name}, []string{paths["candidate"]}, Resolution{paths["path"], true, "PATH"}},
		{"candidate fallback", "", "", []string{"absent-builder"}, []string{"", root, paths["candidate"]}, Resolution{paths["candidate"], true, "common install location"}},
		{"missing", "", "", []string{"absent-builder"}, nil, Resolution{"absent-builder", false, "not found"}},
		{"bad override does not fall back", filepath.Join(root, "missing"), paths["configuration"], []string{name}, nil, Resolution{filepath.Join(root, "missing"), false, "environment TEST_RELEASE_TOOL"}},
		{"directory is not a tool", "", root, nil, nil, Resolution{root, false, "configuration"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TEST_RELEASE_TOOL", tc.environment)
			got := resolve(tc.configured, "TEST_RELEASE_TOOL", root, tc.names, tc.candidates)
			if got != tc.want {
				t.Fatalf("resolution = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestToolsResolveRelativeToProjectOrConfig(t *testing.T) {
	t.Setenv("GODOT_BIN", "")
	t.Setenv("UNITY_BIN", "")
	t.Setenv("STEAMCMD_BIN", "")
	root := t.TempDir()
	document := &config.Document{Path: filepath.Join(root, "steam-release.yaml"), Config: config.Config{
		Project: config.Project{Root: "game", Engine: config.Engine{Executable: "tools/editor"}},
		Tools:   config.Tools{SteamCMD: "tools/steamcmd"},
	}}
	for _, path := range []string{document.ResolveProject("tools/editor"), document.ResolveFromBase("tools/steamcmd")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fake executable"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		engine string
		lookup func(*config.Document) Resolution
		want   string
	}{
		{"godot", Godot, document.ResolveProject("tools/editor")},
		{"unity", Unity, document.ResolveProject("tools/editor")},
		{"custom", func(d *config.Document) Resolution { return Custom(d, "tools/editor") }, document.ResolveProject("tools/editor")},
		{"prebuilt", SteamCMD, document.ResolveFromBase("tools/steamcmd")},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			document.Config.Project.Engine.Type = tc.engine
			got := tc.lookup(document)
			if !got.Found || got.Path != tc.want {
				t.Fatalf("resolution = %#v, want %s", got, tc.want)
			}
		})
	}
}

func TestUnityDiscoveryRejectsCLIAndAcceptsEditorsInOtherLocations(t *testing.T) {
	t.Setenv("UNITY_BIN", "")
	root := t.TempDir()
	cliName, editorName := "unity", "Unity"
	if runtime.GOOS == "windows" {
		cliName += ".exe"
		editorName += ".exe"
	}
	cli := filepath.Join(root, "bin", cliName)
	editor := filepath.Join(root, "custom", "Editor", editorName)
	for _, path := range []string{cli, editor} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("executable"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(editor), "Data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(cli))
	if got := resolveUnity("", root, nil); got.Found {
		t.Fatalf("Unity CLI was mistaken for the Editor: %#v", got)
	}
	if got := resolveUnity("", root, []string{editor}); !got.Found || got.Path != editor || got.Source != "common install location" {
		t.Fatalf("Editor in custom location was not found: %#v", got)
	}
	wrapper := filepath.Join(root, "custom", "editor-wrapper")
	if err := os.WriteFile(wrapper, []byte("executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UNITY_BIN", wrapper)
	if got := resolveUnity("", root, nil); !got.Found || got.Path != wrapper || got.Source != "environment UNITY_BIN" {
		t.Fatalf("explicit Unity executable was not honored: %#v", got)
	}
	t.Setenv("UNITY_BIN", "")
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, "path", "Unity")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(editor, link); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", filepath.Dir(link))
		if got := resolveUnity("", root, nil); !got.Found || got.Path != link || got.Source != "PATH" {
			t.Fatalf("Editor symlink on PATH was not found: %#v", got)
		}
	}
}

func TestUnityAppBundleIsRecognized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Unity.app", "Contents", "MacOS", "Unity")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isUnityEditor(path) {
		t.Fatal("Unity.app Editor executable was not recognized")
	}
}

func TestUnityDiscoveryPrefersProjectEditorVersion(t *testing.T) {
	t.Setenv("UNITY_BIN", "")
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, "ProjectSettings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "ProjectSettings", "ProjectVersion.txt"), []byte("m_EditorVersion: 6000.0.72f1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "Unity"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	makeEditor := func(version string) string {
		path := filepath.Join(root, version, "Editor", name)
		if err := os.MkdirAll(filepath.Join(filepath.Dir(path), "Data"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("executable"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	older := makeEditor("6000.0.23f1")
	matching := makeEditor("6000.0.72f1")
	t.Setenv("PATH", filepath.Dir(older))
	if got := resolveUnity("", project, []string{matching}); !got.Found || got.Path != matching {
		t.Fatalf("project Editor version was not preferred: %#v", got)
	}
	if got := resolveUnity("", project, nil); !got.Found || got.Path != older {
		t.Fatalf("PATH Editor was not used as fallback: %#v", got)
	}
}
