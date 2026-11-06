package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestPreviewConfigUsesRequestedTargetsAndRoot(t *testing.T) {
	base := t.TempDir()
	request := CreateConfigRequest{
		Path:        filepath.Join(base, "release", "steam-release.yaml"),
		ProjectRoot: filepath.Join(base, "game"),
		Name:        "Space Game", Engine: "prebuilt", AppID: 480,
		Targets: []CreateTargetRequest{{Name: "windows", Platform: "windows", DepotID: 481, Path: "builds/windows"}},
	}
	contents, err := (&DesktopApp{}).PreviewConfig(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"root: ../game", "app_id: 480", "depot_id: 481", "content: builds/windows"} {
		if !strings.Contains(contents, want) {
			t.Fatalf("preview is missing %q:\n%s", want, contents)
		}
	}
	if strings.Contains(contents, "macos:") {
		t.Fatalf("preview kept starter targets:\n%s", contents)
	}
}

func TestPreviewConfigKeepsImportedDepotDetails(t *testing.T) {
	game := t.TempDir()
	request := CreateConfigRequest{
		Path: filepath.Join(game, "steam-release.yaml"), Name: "Space Game", Engine: "prebuilt", AppID: 480,
		Targets: []CreateTargetRequest{{
			Name: "macos", Platform: "macos", DepotID: 483, Path: filepath.Join(game, "content"),
			Mappings:   []config.FileMapping{{LocalPath: "mac/Game.app/*", DepotPath: "Game.app"}},
			Exclusions: []string{"*.pdb"},
		}},
	}
	contents, err := (&DesktopApp{}).PreviewConfig(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"content: content", "local_path: mac/Game.app/*", "depot_path: Game.app", "- '*.pdb'"} {
		if !strings.Contains(contents, want) {
			t.Fatalf("preview is missing %q:\n%s", want, contents)
		}
	}
}
