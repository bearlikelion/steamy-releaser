package steam

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestGenerateCreatesAppAndDepotVDF(t *testing.T) {
	directory := t.TempDir()
	document := &config.Document{Path: filepath.Join(directory, "steam-release.yaml"), Config: config.Config{
		Version: 1,
		Project: config.Project{Name: "Example", Engine: config.Engine{Type: "prebuilt"}},
		Profiles: map[string]config.Profile{"release": {
			AppID:   100,
			Targets: map[string]config.Target{"windows": {Platform: "windows", DepotID: 101, Output: "build/game.exe", Content: "build"}},
		}},
	}}
	scripts, err := Generate(document, "release", nil, ScriptOptions{Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(scripts.AppPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"AppID" "100"`, `"Preview" "1"`, `"101" "depot_build_101.vdf"`} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("app VDF does not contain %q:\n%s", expected, contents)
		}
	}
}

func TestValidateScriptOptionsRejectsUnsafeCombinations(t *testing.T) {
	for _, options := range []ScriptOptions{
		{Preview: true, SetLive: "beta"},
		{SetLive: "default"},
		{SetLive: "invalid branch"},
	} {
		if err := ValidateScriptOptions(options); err == nil {
			t.Fatalf("expected options to fail: %#v", options)
		}
	}
}
