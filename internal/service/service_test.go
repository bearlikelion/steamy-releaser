package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/thornity/steam-release-tool/internal/artifact"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/steam"
)

type fakeRunner struct {
	mu       sync.Mutex
	commands []release.CommandPlan
	run      func(context.Context, release.CommandPlan) (release.RunResult, error)
}

func TestRetryBuildReusesCompletedTargetsAndValidatesOriginalSelection(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "builder")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notice.txt"), []byte("legal"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := func(name string) config.Target {
		return config.Target{DepotID: 101, Output: "build/" + name + "/game", Content: "build/" + name, Required: []string{"notice.txt"}, Build: config.Build{Command: []string{tool, "{output}"}}}
	}
	document := testDocument(root, config.Engine{Type: "custom", MaxParallel: 1}, target("linux"))
	profile := document.Config.Profiles["release"]
	profile.Targets = map[string]config.Target{"linux": target("linux"), "windows": target("windows")}
	profile.PostBuild = []config.CopyRule{{From: "notice.txt"}}
	document.Config.Profiles["release"] = profile
	failWindows := true
	runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
		if command.Label == "windows" && failWindows {
			return release.RunResult{}, errors.New("windows build failed")
		}
		return release.RunResult{}, os.WriteFile(command.Args[0], []byte("game"), 0o700)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if err := svc.Build(context.Background(), "release", RunOptions{}); err == nil {
		t.Fatal("initial build unexpectedly succeeded")
	}
	if err := os.WriteFile(filepath.Join(root, "build", "linux", "notice.txt"), []byte("legal"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := artifact.Fingerprint(context.Background(), document, "release", []string{"linux"})
	if err != nil {
		t.Fatal(err)
	}
	failWindows = false
	runner.commands = nil
	retry := RunOptions{Targets: []string{"linux", "windows"}, ReuseTargets: []string{"linux"}, TargetFingerprints: map[string]string{"linux": fingerprint}}
	if err := svc.Build(context.Background(), "release", retry); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || runner.commands[0].Label != "windows" {
		t.Fatalf("retry rebuilt completed targets: %#v", runner.commands)
	}
	for _, name := range []string{"linux", "windows"} {
		if _, err := os.Stat(filepath.Join(root, "build", name, "notice.txt")); err != nil {
			t.Fatalf("copy rules did not run for %s: %v", name, err)
		}
	}
	if err := os.Remove(filepath.Join(root, "build", "linux", "game")); err != nil {
		t.Fatal(err)
	}
	runner.commands = nil
	if err := svc.Build(context.Background(), "release", retry); err != nil {
		t.Fatalf("missing reused output should be rebuilt: %v", err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("missing reused output did not trigger a rebuild: %#v", runner.commands)
	}
	if err := svc.Build(context.Background(), "release", RunOptions{Targets: []string{"windows"}, ReuseTargets: []string{"linux"}}); err == nil {
		t.Fatal("unselected reuse target was accepted")
	}
}

func TestRetryRejectsChangedPrebuiltArtifacts(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "build", "Game")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("original"), 0o700); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "prebuilt"}, config.Target{
		DepotID: 101, Output: "build/Game", Content: "build",
	})
	fingerprint, err := artifact.Fingerprint(context.Background(), document, "release", []string{"target"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("modified"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	err = svc.Build(context.Background(), "release", RunOptions{
		ReuseTargets: []string{"target"}, TargetFingerprints: map[string]string{"target": fingerprint},
	})
	if err == nil || !strings.Contains(err.Error(), "prebuilt target target changed") {
		t.Fatalf("changed prebuilt artifact was accepted: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("changed prebuilt artifact started a command: %#v", runner.commands)
	}
	err = svc.Build(context.Background(), "release", RunOptions{UnverifiedTargets: []string{"target"}})
	if err == nil || !strings.Contains(err.Error(), "prebuilt targets have no artifact checkpoint") {
		t.Fatalf("unverified prebuilt artifact was accepted: %v", err)
	}
}

func (f *fakeRunner) Run(ctx context.Context, plan release.CommandPlan, _ bool) (release.RunResult, error) {
	f.mu.Lock()
	f.commands = append(f.commands, plan)
	f.mu.Unlock()
	if f.run != nil {
		return f.run(ctx, plan)
	}
	return release.RunResult{}, nil
}

func TestPrebuiltUploadGeneratesVDFAndInvokesSteamCMD(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(directory, "config"))
	steamCMD := filepath.Join(directory, "steamcmd")
	if err := os.WriteFile(steamCMD, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(directory, "build", "windows")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(content, "game.exe")
	if err := os.WriteFile(output, []byte("game"), 0o700); err != nil {
		t.Fatal(err)
	}
	document := testDocument(directory, config.Engine{Type: "prebuilt"}, config.Target{
		Platform: "windows", DepotID: 101, Output: "build/windows/game.exe", Content: "build/windows",
	})
	document.Config.Tools.SteamCMD = steamCMD
	runner := &fakeRunner{run: func(_ context.Context, plan release.CommandPlan) (release.RunResult, error) {
		return release.RunResult{Output: "Successfully finished AppID 100 build preview."}, nil
	}}
	service := &Service{Document: document, Reporter: release.NopReporter{}, Runner: runner}
	result, err := service.Upload(context.Background(), "release", RunOptions{}, steam.UploadOptions{Username: "builder_account", Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.BuildScript == "" {
		t.Fatal("upload did not return a build script")
	}
	if _, err := os.Stat(result.BuildScript); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || runner.commands[0].Program != steamCMD {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
	foundPreview := false
	contents, _ := os.ReadFile(result.BuildScript)
	if string(contents) != "" && strings.Contains(string(contents), `"Preview" "1"`) {
		foundPreview = true
	}
	if !foundPreview {
		t.Fatalf("preview was not written to VDF:\n%s", contents)
	}
}

func TestUploadRetryVerifiesArtifactCheckpointBeforeSteamCMD(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	tool := filepath.Join(root, "steamcmd")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "content", "Game")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("game"), 0o700); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "prebuilt"}, config.Target{
		DepotID: 101, Output: "content/Game", Content: "content",
	})
	document.Config.Tools.SteamCMD = tool
	var checkpoint string
	reporter := release.ReporterFunc(func(event release.Event) {
		if event.Kind == "upload-checkpoint" {
			checkpoint = event.Fingerprint
		}
	})
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		return release.RunResult{Output: "Successfully finished AppID 100 build (BuildID 12345)."}, nil
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: reporter}
	options := steam.UploadOptions{Username: "builder"}
	if _, err := svc.Upload(context.Background(), "release", RunOptions{CaptureFingerprints: true}, options); err != nil || len(checkpoint) != 64 {
		t.Fatalf("initial upload did not record a checkpoint: %q, %v", checkpoint, err)
	}
	if err := os.WriteFile(output, []byte("evil"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Upload(context.Background(), "release", RunOptions{UploadFingerprint: checkpoint}, options); err == nil || !strings.Contains(err.Error(), "artifact contents changed") {
		t.Fatalf("modified upload content was accepted: %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("modified content reached SteamCMD: %#v", runner.commands)
	}
	if err := os.WriteFile(output, []byte("game"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Upload(context.Background(), "release", RunOptions{UploadFingerprint: checkpoint}, options); err != nil {
		t.Fatalf("unchanged content could not be retried: %v", err)
	}
}

func TestUnavailableCheckpointDoesNotAuthorizeAnUploadRetry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	tool := filepath.Join(root, "steamcmd")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"content", "output"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "content", "Game"), []byte("game"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "outside"), filepath.Join(root, "output", "escape")); err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "prebuilt"}, config.Target{
		DepotID: 101, Output: "output", Content: "content",
	})
	document.Config.Tools.SteamCMD = tool
	var checkpoints, warnings int
	reporter := release.ReporterFunc(func(event release.Event) {
		if event.Kind == "upload-checkpoint" {
			checkpoints++
		}
		if event.Level == release.LevelWarning && strings.Contains(event.Message, "Could not record an artifact checkpoint") {
			warnings++
		}
	})
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		return release.RunResult{Output: "Successfully finished AppID 100 build (BuildID 12345)."}, nil
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: reporter}
	options := steam.UploadOptions{Username: "builder"}
	if _, err := svc.Upload(context.Background(), "release", RunOptions{CaptureFingerprints: true}, options); err != nil || checkpoints != 0 || warnings != 1 {
		t.Fatalf("unavailable checkpoint: checkpoints=%d warnings=%d error=%v", checkpoints, warnings, err)
	}
	if _, err := svc.Upload(context.Background(), "release", RunOptions{UploadFingerprint: strings.Repeat("a", 64)}, options); err == nil {
		t.Fatal("unavailable checkpoint authorized a retry")
	}
	if len(runner.commands) != 1 {
		t.Fatalf("unverified retry reached SteamCMD: %#v", runner.commands)
	}
}

func TestExistingVDFDoesNotAuthorizeAnUploadCheckpoint(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	for name, contents := range map[string]string{"steamcmd": "fake", "custom.vdf": "custom script"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "content", "Game"), []byte("game"), 0o700); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "prebuilt"}, config.Target{
		DepotID: 101, Output: "content/Game", Content: "content",
	})
	document.Config.Tools.SteamCMD = filepath.Join(root, "steamcmd")
	profile := document.Config.Profiles["release"]
	profile.ExistingVDF = "custom.vdf"
	document.Config.Profiles["release"] = profile
	var checkpoints, warnings int
	reporter := release.ReporterFunc(func(event release.Event) {
		if event.Kind == "upload-checkpoint" {
			checkpoints++
		}
		if event.Level == release.LevelWarning && strings.Contains(event.Message, "Cannot record an upload checkpoint for existing_vdf") {
			warnings++
		}
	})
	runner := &fakeRunner{run: func(_ context.Context, _ release.CommandPlan) (release.RunResult, error) {
		return release.RunResult{Output: "Successfully finished AppID 100 build (BuildID 12345)."}, nil
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: reporter}
	options := steam.UploadOptions{Username: "builder"}
	if _, err := svc.Upload(context.Background(), "release", RunOptions{CaptureFingerprints: true}, options); err != nil || checkpoints != 0 || warnings != 1 {
		t.Fatalf("custom script checkpoint: checkpoints=%d warnings=%d error=%v", checkpoints, warnings, err)
	}
	if _, err := svc.Upload(context.Background(), "release", RunOptions{UploadFingerprint: strings.Repeat("a", 64)}, options); err == nil || !strings.Contains(err.Error(), "cannot verify upload contents referenced by existing_vdf") {
		t.Fatalf("custom script incorrectly allowed automatic retry: %v", err)
	}
	if _, err := svc.Ship(context.Background(), "release", RunOptions{VerifyRetry: func() error { return nil }}, options); err == nil || !strings.Contains(err.Error(), "cannot verify upload contents referenced by existing_vdf") {
		t.Fatalf("legacy custom script ship retry was accepted: %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("unverified retry reached SteamCMD: %#v", runner.commands)
	}
}

func TestCustomBuildExpandsOutputAndValidatesArtifacts(t *testing.T) {
	directory := t.TempDir()
	tool := filepath.Join(directory, "builder")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := config.Target{
		Platform: "linux", DepotID: 102, Output: "build/linux/game", Content: "build/linux",
		Build: config.Build{Command: []string{tool}, Args: []string{"{output}"}},
	}
	document := testDocument(directory, config.Engine{Type: "custom", MaxParallel: 2}, target)
	runner := &fakeRunner{run: func(_ context.Context, plan release.CommandPlan) (release.RunResult, error) {
		if len(plan.Args) == 0 {
			return release.RunResult{}, fmt.Errorf("custom command did not receive output")
		}
		if err := os.MkdirAll(filepath.Dir(plan.Args[0]), 0o755); err != nil {
			return release.RunResult{}, err
		}
		return release.RunResult{}, os.WriteFile(plan.Args[0], []byte("game"), 0o700)
	}}
	var events []release.Event
	service := &Service{Document: document, Reporter: release.ReporterFunc(func(event release.Event) { events = append(events, event) }), Runner: runner}
	if err := service.Build(context.Background(), "release", RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 || events[0].Kind != "build-start" || events[1].Kind != "build-complete" || events[1].Target != "target" {
		t.Fatalf("unexpected build lifecycle events: %#v", events)
	}
	if _, err := os.Stat(filepath.Join(directory, "build", "linux", "game")); err != nil {
		t.Fatal(err)
	}
}

func TestShipRejectsInvalidOptionsBeforeBuilding(t *testing.T) {
	for _, options := range []steam.UploadOptions{
		{Preview: true, SetLive: "beta"},
		{SetLive: "default"},
		{SetLive: "invalid branch"},
	} {
		t.Run(fmt.Sprintf("preview=%t/branch=%s", options.Preview, options.SetLive), func(t *testing.T) {
			directory := t.TempDir()
			document := testDocument(directory, config.Engine{Type: "custom"}, config.Target{
				Platform: "linux", DepotID: 101, Output: "build/game", Content: "build",
				Build: config.Build{Command: []string{"builder"}},
			})
			runner := &fakeRunner{}
			service := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
			_, err := service.Ship(context.Background(), "release", RunOptions{}, options)
			if err == nil || err.Error() != options.Validate().Error() {
				t.Fatalf("expected upload option error, got %v", err)
			}
			if len(runner.commands) != 0 {
				t.Fatalf("invalid options started commands: %#v", runner.commands)
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("invalid options created files: %v", entries)
			}
		})
	}
}

func TestCancelledOperationsHaveNoSideEffects(t *testing.T) {
	directory := t.TempDir()
	service := &Service{
		Document: testDocument(directory, config.Engine{Type: "prebuilt"}, config.Target{
			Platform: "linux", DepotID: 101, Output: "build/game", Content: "build",
		}),
		Reporter: release.NopReporter{},
		Runner:   &fakeRunner{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Build(ctx, "release", RunOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("build error = %v, want cancellation", err)
	}
	if _, err := service.Upload(ctx, "release", RunOptions{}, steam.UploadOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("upload error = %v, want cancellation", err)
	}
	if _, err := service.Ship(ctx, "release", RunOptions{}, steam.UploadOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ship error = %v, want cancellation", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cancelled operations created files: %v", entries)
	}
}

func TestTargetBuildsStopAfterFailure(t *testing.T) {
	buildErr := errors.New("build failed")
	var events []release.Event
	runner := &fakeRunner{run: func(context.Context, release.CommandPlan) (release.RunResult, error) {
		return release.RunResult{}, buildErr
	}}
	service := &Service{Runner: runner, Reporter: release.ReporterFunc(func(event release.Event) { events = append(events, event) })}
	plan := &release.Plan{Parallelism: 1, Targets: []release.TargetPlan{
		{Name: "first", Command: &release.CommandPlan{Label: "first"}},
		{Name: "second", Command: &release.CommandPlan{Label: "second"}},
	}}
	if _, err := service.runTargetBuilds(context.Background(), "", plan, false); !errors.Is(err, buildErr) {
		t.Fatalf("error = %v, want build failure", err)
	}
	if len(runner.commands) != 1 || runner.commands[0].Label != "first" {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
	if len(events) != 2 || events[0].Kind != "build-start" || events[0].Target != "first" || events[1].Kind != "build-failed" || events[1].Target != "first" {
		t.Fatalf("unexpected build lifecycle events: %#v", events)
	}
}

func TestPrebuiltTargetReportsCompletion(t *testing.T) {
	var events []release.Event
	service := &Service{Runner: &fakeRunner{}, Reporter: release.ReporterFunc(func(event release.Event) { events = append(events, event) })}
	plan := &release.Plan{Targets: []release.TargetPlan{{Name: "linux"}}}
	if _, err := service.runTargetBuilds(context.Background(), "", plan, false); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "build-prebuilt" || events[0].Target != "linux" {
		t.Fatalf("unexpected prebuilt lifecycle events: %#v", events)
	}
}

func TestTargetBuildFailureCancelsOtherWorkers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{}, 2)
	fail := make(chan struct{})
	buildErr := errors.New("build failed")
	runner := &fakeRunner{run: func(ctx context.Context, plan release.CommandPlan) (release.RunResult, error) {
		started <- struct{}{}
		if plan.Label == "first" {
			select {
			case <-fail:
				return release.RunResult{}, buildErr
			case <-ctx.Done():
			}
		} else {
			<-ctx.Done()
		}
		return release.RunResult{}, ctx.Err()
	}}
	service := &Service{Runner: runner, Reporter: release.NopReporter{}}
	plan := &release.Plan{Parallelism: 2, Targets: []release.TargetPlan{
		{Name: "first", Command: &release.CommandPlan{Label: "first"}},
		{Name: "second", Command: &release.CommandPlan{Label: "second"}},
		{Name: "queued", Command: &release.CommandPlan{Label: "queued"}},
	}}
	done := make(chan error, 1)
	go func() {
		_, err := service.runTargetBuilds(ctx, "", plan, false)
		done <- err
	}()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("both workers did not start")
		}
	}
	close(fail)
	select {
	case err := <-done:
		if !errors.Is(err, buildErr) || !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want build failure and sibling cancellation", err)
		}
	case <-ctx.Done():
		t.Fatal("build failure did not stop the other worker")
	}
	if len(runner.commands) != 2 {
		t.Fatalf("queued build ran after failure: %#v", runner.commands)
	}
}

func TestTargetBuildsPreserveContextErrors(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	for _, ctx := range []context.Context{cancelled, expired} {
		service := &Service{Runner: &fakeRunner{}, Reporter: release.NopReporter{}}
		_, err := service.runTargetBuilds(ctx, "", &release.Plan{Targets: []release.TargetPlan{{Name: "prebuilt"}}}, false)
		if !errors.Is(err, ctx.Err()) {
			t.Fatalf("error = %v, want %v", err, ctx.Err())
		}
	}
}

func testDocument(directory string, engine config.Engine, target config.Target) *config.Document {
	return &config.Document{
		Path: filepath.Join(directory, "steam-release.yaml"),
		Config: config.Config{
			Version: 1,
			Project: config.Project{Name: "Example", Engine: engine},
			Profiles: map[string]config.Profile{"release": {
				AppID:   100,
				Targets: map[string]config.Target{"target": target},
			}},
		},
	}
}
