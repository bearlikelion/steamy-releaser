package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDiscoverFindsNearestConfiguration(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "game", "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	parentConfig := filepath.Join(root, "steam-release.yaml")
	nearConfig := filepath.Join(root, "game", "steam-release.yml")
	file := filepath.Join(nested, "game.go")
	for _, path := range []string{parentConfig, nearConfig, file} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A directory named like a configuration must not hide the real file.
	if err := os.Mkdir(filepath.Join(nested, "steam-release.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, start := range []string{nested, file} {
		if got, err := Discover(start); err != nil || got != nearConfig {
			t.Fatalf("Discover(%s) = %s, %v; want %s", start, got, err, nearConfig)
		}
	}
	if err := os.Remove(nearConfig); err != nil {
		t.Fatal(err)
	}
	if got, err := Discover(nested); err != nil || got != parentConfig {
		t.Fatalf("parent fallback = %s, %v; want %s", got, err, parentConfig)
	}
}

func TestInitializeProtectsExistingConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steam-release.yaml")
	const original = "user configuration\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	options := InitOptions{Path: path, Engine: "prebuilt", AppID: 100, Targets: []InitTarget{{Name: "linux", DepotID: 101, Content: "build/linux"}}}
	if _, err := Initialize(options); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Initialize overwrote existing configuration: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != original {
		t.Fatalf("existing configuration changed: %q, %v", contents, err)
	}
	options.Force = true
	if _, err := Initialize(options); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("forced replacement is invalid: %v", err)
	}
}

func TestConcurrentInitializeDoesNotOverwriteAnotherInitializer(t *testing.T) {
	const attempts = 32
	path := filepath.Join(t.TempDir(), "steam-release.yaml")
	type result struct {
		name string
		err  error
	}
	results := make(chan result, attempts)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(attempts)
	for i := range attempts {
		go func() {
			name := fmt.Sprintf("Project %d", i)
			ready.Done()
			<-start
			_, err := Initialize(InitOptions{Path: path, Name: name, Engine: "prebuilt", AppID: 100,
				Targets: []InitTarget{{Name: "linux", DepotID: 101, Content: "build/linux"}}})
			results <- result{name: name, err: err}
		}()
	}
	ready.Wait()
	close(start)
	var winner string
	for range attempts {
		result := <-results
		if result.err == nil {
			if winner != "" {
				t.Fatalf("multiple initializers succeeded: %s and %s", winner, result.name)
			}
			winner = result.name
		} else if !strings.Contains(result.err.Error(), "already exists") {
			t.Fatalf("unexpected initialization error: %v", result.err)
		}
	}
	if winner == "" {
		t.Fatal("no initializer succeeded")
	}
	document, err := Load(path)
	if err != nil || document.Config.Project.Name != winner {
		t.Fatalf("winning configuration was changed: %#v, %v", document, err)
	}
}

func TestInitializeRejectsDanglingConfigurationSymlink(t *testing.T) {
	dir := t.TempDir()
	path, target := filepath.Join(dir, "steam-release.yaml"), filepath.Join(dir, "missing.yaml")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Initialize(InitOptions{Path: path, Engine: "prebuilt", AppID: 100,
		Targets: []InitTarget{{Name: "linux", DepotID: 101, Content: "build/linux"}}})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("dangling symlink accepted: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("initialization created the symlink target: %v", err)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("configuration symlink changed: %v", err)
	}
}

func TestInitializeRejectsDuplicateTargetsWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steam-release.yaml")
	_, err := Initialize(InitOptions{Path: path, Engine: "prebuilt", AppID: 100, Targets: []InitTarget{
		{Name: "linux", DepotID: 101, Content: "linux"},
		{Name: "linux", DepotID: 102, Content: "other"},
	}})
	if err == nil || !strings.Contains(err.Error(), "duplicate target") {
		t.Fatalf("duplicate target error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid initialization wrote a configuration: %v", err)
	}
}

func TestInitializeDetectsEngineRelativeToConfig(t *testing.T) {
	for _, tc := range []struct{ engine, marker, setting string }{
		{"godot", "project.godot", "Linux"},
		{"unity", "ProjectSettings/ProjectVersion.txt", "StandaloneLinux64"},
		{"prebuilt", "", ""},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			root := t.TempDir()
			if tc.marker != "" {
				path := filepath.Join(root, "game", tc.marker)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			document, err := Initialize(InitOptions{
				Path: filepath.Join(root, "config", "steam-release.yaml"), Root: "../game", Engine: "auto", AppID: 100, Profile: "beta",
				Targets: []InitTarget{{Name: "linux", DepotID: 101, Output: "build/linux/Game", BuildSetting: tc.setting}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if document.Config.Project.Engine.Type != tc.engine || document.ProjectRoot() != filepath.Join(root, "game") {
				t.Fatalf("incorrect inferred project: %#v", document.Config.Project)
			}
			selection, err := document.Select("", nil)
			if err != nil || selection.Name != "beta" {
				t.Fatalf("named profile was lost: %#v, %v", selection, err)
			}
		})
	}
}
