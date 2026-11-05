package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadStrictValidConfig(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "steam-release.yaml")
	source := `version: 1
project:
  name: Example
  engine:
    type: godot
profiles:
  release:
    app_id: 100
    targets:
      windows:
        platform: windows
        depot_id: 101
        output: builds/windows/game.exe
        content: builds/windows
        build:
          preset: Windows Desktop
`
	source += strings.Repeat("# trailing comment retained in the hash\n", 4096)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if document.Config.Project.Engine.Type != "godot" {
		t.Fatalf("unexpected engine: %s", document.Config.Project.Engine.Type)
	}
	wantHash := sha256.Sum256([]byte(source))
	if document.ConfigHash != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("configuration hash does not match the decoded bytes: %s", document.ConfigHash)
	}
	if got := document.ResolveProject("builds/windows"); got != filepath.Join(directory, "builds", "windows") {
		t.Fatalf("unexpected resolved path: %s", got)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "steam-release.yaml")
	source := `version: 1
surprise: true
project:
  name: Example
  engine: {type: prebuilt}
profiles: {}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field surprise not found") {
		t.Fatalf("expected strict YAML error, got %v", err)
	}
}

func TestSharedDepotRequiresCommonContentAndMappings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "steam-release.yaml")
	base := `app_id: 100
targets:
  windows:
    depot_id: 101
    output: builds/windows/Game.exe
    content: builds
    build: {preset: Windows Desktop}
    mappings: [{local_path: "windows/*", depot_path: windows}]
  linux:
    depot_id: 101
    output: builds/linux/Game.x86_64
    content: builds
    build: {preset: Linux}
    mappings: [{local_path: "linux/*", depot_path: linux}]
`
	for _, test := range []struct {
		name string
		yaml string
		want string
	}{
		{"valid", base, ""},
		{"different content", strings.Replace(base, "    content: builds\n    build: {preset: Linux}", "    content: elsewhere\n    build: {preset: Linux}", 1), "different content directories"},
		{"missing mappings", strings.Replace(base, "    mappings: [{local_path: \"linux/*\", depot_path: linux}]\n", "", 1), "must define separate mappings"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if test.want == "" && err != nil || test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadWithProjectRootKeepsSharedConfigUnchanged(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "person's game")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "steam-release.yaml")
	source := []byte("app_id: 100\ntargets:\n  windows: {depot_id: 101, content: builds/windows}\n")
	if err := os.WriteFile(path, source, 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := LoadWithProjectRoot(path, project)
	if err != nil {
		t.Fatal(err)
	}
	if document.ProjectRoot() != project || document.ResolveProject("builds/windows") != filepath.Join(project, "builds", "windows") {
		t.Fatalf("project override was not applied: %#v", document)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != string(source) {
		t.Fatalf("shared YAML was changed: %s, %v", contents, err)
	}
	if _, err := LoadWithProjectRoot(path, filepath.Join(root, "missing")); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("invalid root override error = %v", err)
	}
}
