package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thornity/steam-release-tool/internal/artifact"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/steam"
)

func TestUnchangedBuildOutputStopsBuildAndShip(t *testing.T) {
	for _, action := range []string{"build", "ship"} {
		for _, directoryOutput := range []bool{false, true} {
			name := action + "/file"
			if directoryOutput {
				name = action + "/directory"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				tool := filepath.Join(root, "tool")
				if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(t.TempDir(), "account"))
				output := filepath.Join(root, "build", "Game")
				if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
					t.Fatal(err)
				}
				game := output
				if directoryOutput {
					if err := os.Mkdir(output, 0o755); err != nil {
						t.Fatal(err)
					}
					game = filepath.Join(output, "game.bin")
				}
				if err := os.WriteFile(game, []byte("old game"), 0o600); err != nil {
					t.Fatal(err)
				}
				keep := filepath.Join(root, "build", "user-file.txt")
				if err := os.WriteFile(keep, []byte("keep me"), 0o600); err != nil {
					t.Fatal(err)
				}
				document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
					DepotID: 101, Output: "build/Game", Content: "build",
					Build: config.Build{Command: []string{tool, "{output}"}},
				})
				document.Config.Tools.SteamCMD = tool
				var events []release.Event
				runner := &fakeRunner{}
				svc := &Service{Document: document, Runner: runner, Reporter: release.ReporterFunc(func(event release.Event) {
					events = append(events, event)
				})}
				var err error
				if action == "build" {
					err = svc.Build(context.Background(), "release", RunOptions{CaptureFingerprints: true})
				} else {
					_, err = svc.Ship(context.Background(), "release", RunOptions{CaptureFingerprints: true}, steam.UploadOptions{Username: "builder"})
				}
				if err == nil || !strings.Contains(err.Error(), "no change was detected in its existing output") {
					t.Fatalf("%s accepted an unchanged output: %v", action, err)
				}
				if len(runner.commands) != 1 || runner.commands[0].Label != "target" {
					t.Fatalf("unexpected commands: %#v", runner.commands)
				}
				for _, event := range events {
					if event.Kind == "build-complete" || event.Kind == "upload-checkpoint" {
						t.Fatalf("unchanged output received a success checkpoint: %#v", events)
					}
				}
				for path, want := range map[string]string{game: "old game", keep: "keep me"} {
					got, err := os.ReadFile(path)
					if err != nil || string(got) != want {
						t.Fatalf("changed %s: %q, %v", path, got, err)
					}
				}
			})
		}
	}
}

func TestRebuiltOutputMayHaveIdenticalBytes(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "build", "Game")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("same game"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1, 0)
	if err := os.Chtimes(output, old, old); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build",
		Build: config.Build{Command: []string{"builder", "{output}"}},
	})
	runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
		return release.RunResult{}, os.WriteFile(command.Args[0], []byte("same game"), 0o600)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err != nil {
		t.Fatalf("rebuilding identical bytes was rejected: %v", err)
	}
}

func TestPostBuildCopyMayReplaceTheConfiguredOutput(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "build", "Game")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("old game"), 0o600); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build",
		Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "generated/Game"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		if err := os.MkdirAll(filepath.Join(root, "generated"), 0o755); err != nil {
			return release.RunResult{}, err
		}
		return release.RunResult{}, os.WriteFile(filepath.Join(root, "generated", "Game"), []byte("new game"), 0o600)
	}}
	var events []release.Event
	svc := &Service{Document: document, Runner: runner, Reporter: release.ReporterFunc(func(event release.Event) {
		events = append(events, event)
	})}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err != nil {
		t.Fatalf("post-build output copy was rejected: %v", err)
	}
	got, err := os.ReadFile(output)
	if err != nil || string(got) != "new game" {
		t.Fatalf("post-build output = %q, %v", got, err)
	}
	var warned, completed bool
	for _, event := range events {
		warned = warned || event.Level == release.LevelWarning && strings.Contains(event.Message, "post-build source was created or changed during the build")
		completed = completed || event.Kind == "build-complete"
	}
	if !warned || !completed {
		t.Fatalf("missing warning or completed event: %#v", events)
	}
}

func TestStalePostBuildCopyCannotReplaceBuildOutput(t *testing.T) {
	for _, directoryOutput := range []bool{false, true} {
		for _, buildWritesOutput := range []bool{false, true} {
			name := "file"
			if directoryOutput {
				name = "directory"
			}
			if buildWritesOutput {
				name += "/built"
			} else {
				name += "/no-op"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(t.TempDir(), "account"))
				tool := filepath.Join(root, "tool")
				if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
					t.Fatal(err)
				}
				output := filepath.Join(root, "build", "Game")
				source := filepath.Join(root, "generated", "Game")
				game, stale := output, source
				if directoryOutput {
					game, stale = filepath.Join(output, "game.bin"), filepath.Join(source, "game.bin")
				}
				for _, path := range []string{game, stale} {
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("stale game"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				old := time.Unix(1, 0)
				if err := os.Chtimes(game, old, old); err != nil {
					t.Fatal(err)
				}
				document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
					DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{tool}},
				})
				document.Config.Tools.SteamCMD = tool
				profile := document.Config.Profiles["release"]
				profile.PostBuild = []config.CopyRule{{From: "generated/Game"}}
				document.Config.Profiles["release"] = profile
				runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
					if command.Label == "target" && buildWritesOutput {
						return release.RunResult{}, os.WriteFile(game, []byte("fresh game"), 0o600)
					}
					return release.RunResult{}, nil
				}}
				svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
				_, err := svc.Ship(context.Background(), "release", RunOptions{}, steam.UploadOptions{Username: "builder"})
				if err == nil {
					t.Fatal("stale post-build copy was allowed to reach upload")
				}
				if len(runner.commands) != 1 || runner.commands[0].Label != "target" {
					t.Fatalf("unexpected commands after stale copy: %#v", runner.commands)
				}
				want := "stale game"
				if buildWritesOutput {
					want = "fresh game"
				}
				contents, readErr := os.ReadFile(game)
				if readErr != nil || string(contents) != want {
					t.Fatalf("stale copy changed output: %q, %v; want %q", contents, readErr, want)
				}
				if !buildWritesOutput {
					info, err := os.Stat(game)
					if err != nil || !info.ModTime().Equal(old) {
						t.Fatalf("stale copy modified the prior output: %v", err)
					}
				}
			})
		}
	}
}

func TestStalePostBuildCopyCannotUseAnOutputDirectoryAlias(t *testing.T) {
	root := t.TempDir()
	output, source := filepath.Join(root, "build", "actual", "Game"), filepath.Join(root, "generated", "Game")
	for _, path := range []string{output, source} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("old game"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("actual", filepath.Join(root, "build", "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/actual/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "generated/Game", To: "alias"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		return release.RunResult{}, os.WriteFile(output, []byte("fresh game"), 0o600)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err == nil {
		t.Fatal("stale source was allowed to replace the aliased output")
	}
	if contents, err := os.ReadFile(output); err != nil || string(contents) != "fresh game" {
		t.Fatalf("alias copy changed the fresh output: %q, %v", contents, err)
	}
}

func TestStalePostBuildCopyCannotReplaceFreshContentSidecar(t *testing.T) {
	root := t.TempDir()
	game, sidecar, stale := filepath.Join(root, "build", "Game"), filepath.Join(root, "build", "Game.pck"), filepath.Join(root, "generated", "Game.pck")
	for _, path := range []string{game, sidecar, stale} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("old data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "generated/Game.pck"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		if err := os.WriteFile(game, []byte("new game"), 0o600); err != nil {
			return release.RunResult{}, err
		}
		return release.RunResult{}, os.WriteFile(sidecar, []byte("new data"), 0o600)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err == nil {
		t.Fatal("stale sidecar copy was accepted")
	}
	if contents, err := os.ReadFile(sidecar); err != nil || string(contents) != "new data" {
		t.Fatalf("stale copy changed the fresh sidecar: %q, %v", contents, err)
	}
}

func TestUnrelatedFreshAssetCannotMakeNoOpOutputFresh(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "build", "Game")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("old game"), 0o600); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "generated/LICENSE"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		if err := os.MkdirAll(filepath.Join(root, "generated"), 0o755); err != nil {
			return release.RunResult{}, err
		}
		return release.RunResult{}, os.WriteFile(filepath.Join(root, "generated", "LICENSE"), []byte("new license"), 0o600)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err == nil {
		t.Fatal("unrelated new asset made a no-op output look fresh")
	}
	if contents, err := os.ReadFile(output); err != nil || string(contents) != "old game" {
		t.Fatalf("no-op output changed: %q, %v", contents, err)
	}
}

func TestFreshPostBuildSourceIsDetectedWithPreservedMetadata(t *testing.T) {
	root := t.TempDir()
	output, source := filepath.Join(root, "build", "Game"), filepath.Join(root, "generated", "Game")
	for _, path := range []string{output, source} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("old game"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Unix(1, 0)
	if err := os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "generated/Game"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		if err := os.WriteFile(source, []byte("new game"), 0o600); err != nil {
			return release.RunResult{}, err
		}
		return release.RunResult{}, os.Chtimes(source, old, old)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err != nil {
		t.Fatalf("fresh source was rejected: %v", err)
	}
	if contents, err := os.ReadFile(output); err != nil || string(contents) != "new game" {
		t.Fatalf("incorrect output: %q, %v", contents, err)
	}
}

func TestFreshPostBuildSourceCanCreateMissingOutput(t *testing.T) {
	root := t.TempDir()
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "generated/Game"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		if err := os.MkdirAll(filepath.Join(root, "generated"), 0o755); err != nil {
			return release.RunResult{}, err
		}
		return release.RunResult{}, os.WriteFile(filepath.Join(root, "generated", "Game"), []byte("fresh game"), 0o600)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err != nil {
		t.Fatalf("fresh source could not supply a missing output: %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(root, "build", "Game")); err != nil || string(contents) != "fresh game" {
		t.Fatalf("incorrect output: %q, %v", contents, err)
	}
}

func TestChainedCopyCannotMakeAnOldSourceLookFresh(t *testing.T) {
	root := t.TempDir()
	output, source := filepath.Join(root, "build", "Game"), filepath.Join(root, "old", "Game")
	for _, path := range []string{output, source} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("old game"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "old/Game", To: "staging"}, {From: "build/staging/Game"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		return release.RunResult{}, os.WriteFile(output, []byte("fresh game"), 0o600)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err == nil {
		t.Fatal("chained stale copy was accepted")
	}
	if contents, err := os.ReadFile(output); err != nil || string(contents) != "fresh game" {
		t.Fatalf("chained copy changed the fresh output: %q, %v", contents, err)
	}
}

func TestChainedCopyCanCarryFreshSourceToOutput(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "build", "Game")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("old game"), 0o600); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "generated/Game", To: "staging"}, {From: "build/staging/Game"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		if err := os.MkdirAll(filepath.Join(root, "generated"), 0o755); err != nil {
			return release.RunResult{}, err
		}
		return release.RunResult{}, os.WriteFile(filepath.Join(root, "generated", "Game"), []byte("fresh game"), 0o600)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err != nil {
		t.Fatalf("fresh chained source was rejected: %v", err)
	}
	if contents, err := os.ReadFile(output); err != nil || string(contents) != "fresh game" {
		t.Fatalf("incorrect chained output: %q, %v", contents, err)
	}
}

func TestCopiedDirectoryLinkCannotMakeAnOldSourceLookFresh(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "build", "Game")
	old := filepath.Join(root, "build", "links", "existing", "Game")
	for _, path := range []string{output, old} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("old game"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "links", "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("existing", filepath.Join(root, "links", "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "links"}, {From: "build/links/alias/Game"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		return release.RunResult{}, os.WriteFile(output, []byte("fresh game"), 0o600)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	err := svc.Build(context.Background(), "release", RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "could not be verified") {
		t.Fatalf("copied link made an old source look fresh: %v", err)
	}
	if contents, err := os.ReadFile(output); err != nil || string(contents) != "fresh game" {
		t.Fatalf("link copy changed the fresh output: %q, %v", contents, err)
	}
}

func TestLaterStaleCopyCannotReplaceFreshPostBuildCopy(t *testing.T) {
	root := t.TempDir()
	output, stale := filepath.Join(root, "build", "Game"), filepath.Join(root, "old", "Game")
	for _, path := range []string{output, stale} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("old game"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "generated/Game"}, {From: "old/Game"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		if err := os.MkdirAll(filepath.Join(root, "generated"), 0o755); err != nil {
			return release.RunResult{}, err
		}
		return release.RunResult{}, os.WriteFile(filepath.Join(root, "generated", "Game"), []byte("fresh game"), 0o600)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err == nil {
		t.Fatal("later stale copy replaced a fresh output")
	}
	if contents, err := os.ReadFile(output); err != nil || string(contents) != "fresh game" {
		t.Fatalf("later stale copy changed fresh output: %q, %v", contents, err)
	}
}

func TestPostBuildCopyCannotChangeReusedOutput(t *testing.T) {
	root := t.TempDir()
	output, source := filepath.Join(root, "build", "Game"), filepath.Join(root, "generated", "Game")
	for path, contents := range map[string]string{output: "saved game", source: "older game"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "generated/Game"}}
	document.Config.Profiles["release"] = profile
	fingerprint, err := artifact.Fingerprint(context.Background(), document, "release", []string{"target"})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	err = svc.Build(context.Background(), "release", RunOptions{ReuseTargets: []string{"target"}, TargetFingerprints: map[string]string{"target": fingerprint}})
	if err == nil || !strings.Contains(err.Error(), "reused output") || len(runner.commands) != 0 {
		t.Fatalf("copy changed a reused target: %v, %#v", err, runner.commands)
	}
	if contents, err := os.ReadFile(output); err != nil || string(contents) != "saved game" {
		t.Fatalf("reused output changed: %q, %v", contents, err)
	}
	if err := os.WriteFile(source, []byte("saved game"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.Build(context.Background(), "release", RunOptions{ReuseTargets: []string{"target"}, TargetFingerprints: map[string]string{"target": fingerprint}}); err != nil {
		t.Fatalf("identical copy to a reused output was rejected: %v", err)
	}
}

func TestPostBuildCopyCannotChangeReusedContent(t *testing.T) {
	root := t.TempDir()
	output, source := filepath.Join(root, "build", "Game"), filepath.Join(root, "legal", "LICENSE")
	for path, contents := range map[string]string{output: "saved game", source: "license"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "legal/LICENSE"}}
	document.Config.Profiles["release"] = profile
	fingerprint, err := artifact.Fingerprint(context.Background(), document, "release", []string{"target"})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	err = svc.Build(context.Background(), "release", RunOptions{ReuseTargets: []string{"target"}, TargetFingerprints: map[string]string{"target": fingerprint}})
	if err == nil || !strings.Contains(err.Error(), "reused output") || len(runner.commands) != 0 {
		t.Fatalf("copy changed reused content: %v, %#v", err, runner.commands)
	}
	if _, err := os.Stat(filepath.Join(root, "build", "LICENSE")); !os.IsNotExist(err) {
		t.Fatalf("copy changed reused content: %v", err)
	}
}

func TestOtherBuildCannotChangeReusedContentBeforeUpload(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(t.TempDir(), "account"))
	tool := filepath.Join(root, "tool")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "build", "linux", "Game")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("saved game"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := func(name string, depot uint64) config.Target {
		return config.Target{DepotID: depot, Output: "build/" + name + "/Game", Content: "build/" + name,
			Build: config.Build{Command: []string{tool}}}
	}
	document := testDocument(root, config.Engine{Type: "custom"}, target("linux", 101))
	document.Config.Tools.SteamCMD = tool
	profile := document.Config.Profiles["release"]
	profile.Targets = map[string]config.Target{"linux": target("linux", 101), "windows": target("windows", 102)}
	document.Config.Profiles["release"] = profile
	fingerprint, err := artifact.Fingerprint(context.Background(), document, "release", []string{"linux"})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
		if command.Label != "windows" {
			return release.RunResult{}, nil
		}
		if err := os.WriteFile(filepath.Join(root, "build", "linux", "extra.txt"), []byte("changed"), 0o600); err != nil {
			return release.RunResult{}, err
		}
		return release.RunResult{}, os.WriteFile(filepath.Join(root, "build", "windows", "Game"), []byte("windows game"), 0o600)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	_, err = svc.Ship(context.Background(), "release", RunOptions{ReuseTargets: []string{"linux"}, TargetFingerprints: map[string]string{"linux": fingerprint}}, steam.UploadOptions{Username: "builder"})
	if err == nil || !strings.Contains(err.Error(), "reused build contents") {
		t.Fatalf("changed reused content reached upload: %v", err)
	}
	if len(runner.commands) != 1 || runner.commands[0].Label != "windows" {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
}

func TestStaticAssetCanBeAddedInsideFreshDirectoryOutput(t *testing.T) {
	root := t.TempDir()
	game, license := filepath.Join(root, "build", "Game", "game.bin"), filepath.Join(root, "legal", "LICENSE")
	for path, contents := range map[string]string{game: "old game", license: "license"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Unix(1, 0)
	if err := os.Chtimes(game, old, old); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "legal/LICENSE", To: "Game"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		return release.RunResult{}, os.WriteFile(game, []byte("new game"), 0o600)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err != nil {
		t.Fatalf("static asset in a fresh output was rejected: %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(root, "build", "Game", "LICENSE")); err != nil || string(contents) != "license" {
		t.Fatalf("static asset not copied: %q, %v", contents, err)
	}
}

func TestStaticCopyCannotCreateMissingBuildOutput(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "generated", "Game")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("stale game"), 0o600); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "generated/Game"}}
	document.Config.Profiles["release"] = profile
	svc := &Service{Document: document, Runner: &fakeRunner{}, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err == nil {
		t.Fatal("static source created a missing build output")
	}
	if _, err := os.Stat(filepath.Join(root, "build", "Game")); !os.IsNotExist(err) {
		t.Fatalf("static source changed the missing output: %v", err)
	}
}

func TestStaticCopyCannotMakeAnEmptyOutputLookFresh(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "generated", "Game", "game.bin")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("stale game"), 0o600); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build", Build: config.Build{Command: []string{"builder"}},
	})
	profile := document.Config.Profiles["release"]
	profile.PostBuild = []config.CopyRule{{From: "generated/Game"}}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		return release.RunResult{}, os.MkdirAll(filepath.Join(root, "build", "Game"), 0o755)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err == nil {
		t.Fatal("static source made an empty build output look fresh")
	}
	if _, err := os.Stat(filepath.Join(root, "build", "Game", "game.bin")); !os.IsNotExist(err) {
		t.Fatalf("static source changed the empty output: %v", err)
	}
}

func TestDirectoryObservationDetectsInPlaceChanges(t *testing.T) {
	output := t.TempDir()
	file := filepath.Join(output, "data", "game.bin")
	if err := os.Mkdir(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1, 0)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	before, err := observeOutput(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	same, err := observeOutput(context.Background(), output)
	if err != nil || !before.unchanged(same) {
		t.Fatalf("unchanged directory not recognized: %v", err)
	}
	if err := os.WriteFile(file, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := observeOutput(context.Background(), output)
	if err != nil || before.unchanged(after) {
		t.Fatalf("in-place directory change not recognized: %v", err)
	}
}

func TestOutputObservationHonorsCancellationAndMissingOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := observeOutput(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("observation error = %v, want cancellation", err)
	}
	missing, err := observeOutput(context.Background(), filepath.Join(t.TempDir(), "missing"))
	if err != nil || missing.info != nil || missing.unchanged(missing) {
		t.Fatalf("missing output should be left to artifact validation: %#v, %v", missing, err)
	}
}
