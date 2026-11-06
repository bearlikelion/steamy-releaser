package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectGodotProject(t *testing.T) {
	root := t.TempDir()
	write := func(name, contents string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("project.godot", "[application]\nconfig/name=\"Space Game\"\n")
	write("export_presets.cfg", `[preset.0]
name="Windows Desktop"
platform="Windows Desktop"
export_path="builds/windows/Space.exe"

[preset.0.options]
name="not a preset"

[preset.1]
name="Web"
platform="Web"
export_path="web/index.html"

[preset.2]
name="Linux/X11"
platform="Linux"
export_path=""
`)
	write("steam-release.yaml", "app_id: 1\n")
	got := Detect(root)
	want := Detection{
		Engine: "godot", Name: "Space Game", Profiles: []string{},
		Presets: []DetectedPreset{
			{Name: "Windows Desktop", Platform: "windows", Output: "builds/windows/Space.exe"},
			{Name: "Linux/X11", Platform: "linux"},
		},
		Existing: filepath.Join(root, "steam-release.yaml"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Detect() = %#v, want %#v", got, want)
	}
}

func TestDetectEmptyFolderIsPrebuilt(t *testing.T) {
	root := t.TempDir()
	got := Detect(root)
	if got.Engine != "prebuilt" || got.Name != filepath.Base(root) || got.Existing != "" {
		t.Fatalf("Detect() = %#v", got)
	}
}
