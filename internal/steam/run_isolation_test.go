package steam

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestGeneratedScriptsAndDefaultOutputAreIsolatedPerRun(t *testing.T) {
	root := t.TempDir()
	document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
		Profiles: map[string]config.Profile{"release": {AppID: 100,
			Targets: map[string]config.Target{"linux": {DepotID: 101, Content: "build"}}}},
	}}
	first, err := Generate(document, "release", nil, ScriptOptions{Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	firstApp, err := os.ReadFile(first.AppPath)
	if err != nil {
		t.Fatal(err)
	}
	firstDepot, err := os.ReadFile(first.DepotPaths[101])
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(document, "release", nil, ScriptOptions{SetLive: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if first.AppPath == second.AppPath || first.DepotPaths[101] == second.DepotPaths[101] {
		t.Fatalf("both runs share Steam scripts: %#v and %#v", first, second)
	}
	if got, err := os.ReadFile(first.AppPath); err != nil || string(got) != string(firstApp) {
		t.Fatalf("later run changed the first app script: %q, %v", got, err)
	}
	if got, err := os.ReadFile(first.DepotPaths[101]); err != nil || string(got) != string(firstDepot) {
		t.Fatalf("later run changed the first depot script: %q, %v", got, err)
	}
	secondApp, err := os.ReadFile(second.AppPath)
	if err != nil {
		t.Fatal(err)
	}
	firstOutput := generatedBuildOutput(t, firstApp)
	secondOutput := generatedBuildOutput(t, secondApp)
	if first.BuildOutput != firstOutput || second.BuildOutput != secondOutput {
		t.Fatalf("upload results do not report the Steam output directories: %#v, %#v", first, second)
	}
	if firstOutput == secondOutput {
		t.Fatalf("both runs share Steam output directory %q", firstOutput)
	}
	for _, output := range []string{firstOutput, secondOutput} {
		if info, err := os.Stat(output); err != nil || !info.IsDir() {
			t.Fatalf("Steam output directory %s is unavailable: %v", output, err)
		}
	}
	if !strings.Contains(string(firstApp), `"Preview" "1"`) || !strings.Contains(string(secondApp), `"SetLive" "beta"`) {
		t.Fatalf("run-specific options lost: %s\n%s", firstApp, secondApp)
	}
}

func TestConcurrentScriptGenerationKeepsEachRunsOptions(t *testing.T) {
	root := t.TempDir()
	document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
		Profiles: map[string]config.Profile{"release": {AppID: 100,
			Targets: map[string]config.Target{"linux": {DepotID: 101, Content: "build"}}}},
	}}
	const count = 12
	type result struct {
		branch  string
		scripts Scripts
		err     error
	}
	results := make(chan result, count)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(count)
	for i := range count {
		go func() {
			branch := fmt.Sprintf("beta-%d", i)
			ready.Done()
			<-start
			scripts, err := Generate(document, "release", nil, ScriptOptions{SetLive: branch})
			results <- result{branch, scripts, err}
		}()
	}
	ready.Wait()
	close(start)
	seenScripts, seenOutputs := map[string]bool{}, map[string]bool{}
	for range count {
		result := <-results
		if result.err != nil {
			t.Fatalf("generate scripts for %s: %v", result.branch, result.err)
		}
		if seenScripts[result.scripts.AppPath] {
			t.Fatalf("script path reused: %s", result.scripts.AppPath)
		}
		seenScripts[result.scripts.AppPath] = true
		app, err := os.ReadFile(result.scripts.AppPath)
		if err != nil || !strings.Contains(string(app), `"SetLive" "`+result.branch+`"`) {
			t.Fatalf("wrong options for %s: %s, %v", result.branch, app, err)
		}
		output := generatedBuildOutput(t, app)
		if seenOutputs[output] {
			t.Fatalf("Steam output path reused: %s", output)
		}
		seenOutputs[output] = true
	}
}

func TestExplicitSteamOutputPathRemainsUnchanged(t *testing.T) {
	root := t.TempDir()
	for _, configured := range []string{filepath.Join(".steam-releaser", "output"), filepath.Join(root, "external-output")} {
		t.Run(configured, func(t *testing.T) {
			document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
				Profiles: map[string]config.Profile{"release": {AppID: 100, BuildOutput: configured,
					Targets: map[string]config.Target{"linux": {DepotID: 101, Content: "build"}}}},
			}}
			for range 2 {
				scripts, err := Generate(document, "release", nil, ScriptOptions{})
				if err != nil {
					t.Fatal(err)
				}
				app, err := os.ReadFile(scripts.AppPath)
				if err != nil {
					t.Fatal(err)
				}
				if output := generatedBuildOutput(t, app); output != document.ResolveProject(configured) {
					t.Fatalf("explicit output path changed: %s", output)
				}
			}
		})
	}
}

func TestGeneratedStateMustStayOutsideSelectedContent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		content     string
		buildOutput string
		setup       func(*testing.T, string)
	}{
		{name: "project root", content: "."},
		{name: "configured output in content", content: "build", buildOutput: "build/steam-logs"},
		{name: "content in configured output", content: "build/assets", buildOutput: "build"},
		{name: "content in managed output", content: ".steam-releaser/output/game"},
		{name: "content symlink to project", content: "alias", setup: func(t *testing.T, root string) {
			if err := os.Symlink(root, filepath.Join(root, "alias")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
		{name: "generated state symlink into content", content: "build", setup: func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "build", "state"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join("build", "state"), filepath.Join(root, ".steam-releaser")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, root)
			}
			document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
				Profiles: map[string]config.Profile{"release": {AppID: 100, BuildOutput: tc.buildOutput,
					Targets: map[string]config.Target{"linux": {DepotID: 101, Content: tc.content}}}},
			}}
			if err := ValidateScriptSelection(document, "release", nil, ScriptOptions{}); err == nil || !strings.Contains(err.Error(), "content") {
				t.Fatalf("unsafe generated state passed preflight: %v", err)
			}
			if _, err := Generate(document, "release", nil, ScriptOptions{}); err == nil {
				t.Fatal("unsafe generated state produced scripts")
			}
			if _, err := os.Stat(filepath.Join(root, ".steam-releaser", "generated")); !os.IsNotExist(err) {
				t.Fatalf("preflight failure created generated scripts: %v", err)
			}
		})
	}
}

func TestGeneratedStateCheckUsesSelectedTargets(t *testing.T) {
	root := t.TempDir()
	document := &config.Document{Path: filepath.Join(root, "release.yaml"), Config: config.Config{
		Profiles: map[string]config.Profile{"release": {AppID: 100, Targets: map[string]config.Target{
			"safe":   {DepotID: 101, Content: "build/safe"},
			"unsafe": {DepotID: 102, Content: "."},
		}}},
	}}
	if err := ValidateScriptSelection(document, "release", []string{"safe"}, ScriptOptions{}); err != nil {
		t.Fatalf("unselected target blocked a safe upload: %v", err)
	}
	if err := ValidateScriptSelection(document, "release", nil, ScriptOptions{}); err == nil {
		t.Fatal("unsafe selected target passed preflight")
	}
}

func generatedBuildOutput(t *testing.T, app []byte) string {
	t.Helper()
	for _, line := range strings.Split(string(app), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), `"BuildOutput" "`); ok {
			if output, ok := strings.CutSuffix(value, `"`); ok {
				return output
			}
		}
	}
	t.Fatalf("app script is missing BuildOutput: %s", app)
	return ""
}
