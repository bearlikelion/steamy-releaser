package steam

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestImportAppBuildFollowsDepotScripts(t *testing.T) {
	base := t.TempDir()
	scripts := filepath.Join(base, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, contents string) {
		if err := os.WriteFile(filepath.Join(scripts, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("app_build_480.vdf", `"AppBuild"
{
	"AppID" "480" // Spacewar
	"Desc" "Nightly build"
	"ContentRoot" "..\content\"
	"Depots"
	{
		"481" "depot_build_481.vdf"
		"482"
		{
			"FileMapping"
			{
				"LocalPath" "linux\*"
				"DepotPath" "."
				"recursive" "1"
			}
			"FileExclusion" "*.debug"
		}
		"483"
		{
			"FileMapping" { "LocalPath" "mac\Game.app\*" "DepotPath" "Game.app" }
			"FileMapping" { "LocalPath" "shared\*" "DepotPath" "." }
		}
	}
}
`)
	write("depot_build_481.vdf", `"DepotBuildConfig"
{
	"DepotID" "481"
	"FileMapping" { "LocalPath" "*" "DepotPath" "." "recursive" "1" }
	"FileExclusion" "*.pdb"
	"ContentRoot" "..\content\windows"
}
`)
	got, err := ImportAppBuild(filepath.Join(scripts, "app_build_480.vdf"))
	if err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(base, "content")
	if got.AppID != 480 || got.Description != "Nightly build" || got.ContentRoot != content || len(got.Depots) != 3 {
		t.Fatalf("ImportAppBuild() = %#v", got)
	}
	windows, linux, mac := got.Depots[0], got.Depots[1], got.Depots[2]
	if windows.DepotID != 481 || windows.Content != filepath.Join(content, "windows") || windows.Platform != "windows" || len(windows.Mappings) != 0 || windows.Exclusions[0] != "*.pdb" {
		t.Fatalf("windows depot = %#v", windows)
	}
	if linux.Content != filepath.Join(content, "linux") || linux.Platform != "linux" || linux.Exclusions[0] != "*.debug" {
		t.Fatalf("linux depot = %#v", linux)
	}
	if mac.Content != content || mac.Platform != "macos" || len(mac.Mappings) != 2 || mac.Mappings[0].LocalPath != "mac/Game.app/*" {
		t.Fatalf("mac depot = %#v", mac)
	}
}

func TestImportAppBuildRejectsOtherFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "depot.vdf")
	for _, contents := range []string{`"DepotBuildConfig" { "DepotID" "1" }`, `"AppBuild" { "AppID" "1"`, `"AppBuild" { "AppID" "x" "Depots" { } }`} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ImportAppBuild(path); err == nil {
			t.Fatalf("ImportAppBuild(%q) succeeded", contents)
		}
	}
}

func TestDiscoverAppBuildsSkipsToolDirectories(t *testing.T) {
	root := t.TempDir()
	write := func(path, contents string) {
		path = filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app := `"appbuild" { "appid" "%s" "contentroot" "" "depots" { "%s1" "depot.vdf" } }`
	write("Build/scripts/app_10.vdf", fmt.Sprintf(app, "10", "1"))
	write("Build/scripts/depot.vdf", `"DepotBuildConfig" { "DepotID" "11" "contentroot" "../Windows" }`)
	write("Build/output/depot_build_11.vdf", `"DepotBuildConfig" { "DepotID" "11" }`)
	write(".git/app_20.vdf", fmt.Sprintf(app, "20", "2"))
	got := DiscoverAppBuilds(root)
	if len(got) != 1 || got[0].Build.AppID != 10 || got[0].Build.Depots[0].Content != filepath.Join(root, "Build", "Windows") {
		t.Fatalf("DiscoverAppBuilds() = %#v", got)
	}
}
