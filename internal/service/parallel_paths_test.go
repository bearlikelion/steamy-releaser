package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
)

func TestBuildSerializesSharedContentAndParallelizesDisjointContent(t *testing.T) {
	for _, shared := range []bool{true, false} {
		name := "disjoint"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			tool := filepath.Join(root, "builder")
			if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
				t.Fatal(err)
			}
			target := func(name string) config.Target {
				content := filepath.Join("build", name)
				if shared {
					content = "build"
				}
				return config.Target{DepotID: 101, Output: filepath.Join("build", name, "Game"), Content: content,
					Build: config.Build{Command: []string{tool, "{output}"}}}
			}
			document := testDocument(root, config.Engine{Type: "custom", MaxParallel: 2}, target("linux"))
			profile := document.Config.Profiles["release"]
			profile.Targets = map[string]config.Target{"linux": target("linux"), "windows": target("windows")}
			document.Config.Profiles["release"] = profile
			started := make(chan string, 2)
			gate := make(chan struct{})
			var once sync.Once
			releaseGate := func() { once.Do(func() { close(gate) }) }
			runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
				started <- command.Label
				<-gate
				return release.RunResult{}, os.WriteFile(command.Args[0], []byte(command.Label), 0o700)
			}}
			svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
			done := make(chan error, 1)
			go func() { done <- svc.Build(context.Background(), "release", RunOptions{}) }()
			defer func() {
				releaseGate()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("build did not stop after test")
				}
			}()
			select {
			case <-started:
			case err := <-done:
				done <- err
				t.Fatalf("build stopped before its first target: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("first target did not start")
			}
			if shared {
				select {
				case second := <-started:
					t.Fatalf("target %s ran concurrently with a shared content root", second)
				case <-time.After(250 * time.Millisecond):
				}
			} else {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("disjoint targets did not run in parallel")
				}
			}
			releaseGate()
			select {
			case err := <-done:
				done <- err
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("build did not finish")
			}
		})
	}
}

func TestBuildRechecksOutputAliasesAfterProjectLock(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"real", "other"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "other"), alias); err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("directory symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	target := func(output, content string) config.Target {
		return config.Target{DepotID: 101, Output: output, Content: content,
			Build: config.Build{Command: []string{"builder", "{output}"}}}
	}
	document := testDocument(root, config.Engine{Type: "custom", MaxParallel: 2}, target("real/Game", "real"))
	profile := document.Config.Profiles["release"]
	profile.Targets = map[string]config.Target{
		"linux":   target("real/Game", "real"),
		"windows": target("alias/Game", "alias"),
	}
	document.Config.Profiles["release"] = profile
	runner := &fakeRunner{}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	prepared, err := svc.prepareBuild(context.Background(), "release", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), alias); err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireProjectLock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := svc.buildLocked(context.Background(), "release", RunOptions{}, prepared); err == nil || !strings.Contains(err.Error(), "overlapping build outputs") {
		t.Fatalf("changed output alias was accepted: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("build started after output paths changed: %#v", runner.commands)
	}
}

func TestBuildRechecksSharedContentAfterProjectLock(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"real", "other"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "other"), alias); err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("directory symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	target := func(output, content string) config.Target {
		return config.Target{DepotID: 101, Output: output, Content: content,
			Build: config.Build{Command: []string{"builder", "{output}"}}}
	}
	document := testDocument(root, config.Engine{Type: "custom", MaxParallel: 2}, target("real/linux/Game", "real"))
	profile := document.Config.Profiles["release"]
	profile.Targets = map[string]config.Target{
		"linux":   target("real/linux/Game", "real"),
		"windows": target("alias/windows/Game", "alias"),
	}
	document.Config.Profiles["release"] = profile
	var active atomic.Int32
	var overlapped atomic.Bool
	runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
		if active.Add(1) > 1 {
			overlapped.Store(true)
		}
		defer active.Add(-1)
		time.Sleep(50 * time.Millisecond)
		return release.RunResult{}, os.WriteFile(command.Args[0], []byte(command.Label), 0o700)
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	prepared, err := svc.prepareBuild(context.Background(), "release", RunOptions{})
	if err != nil || prepared.buildPlan.Parallelism != 2 {
		t.Fatalf("initial paths were not planned in parallel: %v", err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), alias); err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireProjectLock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := svc.buildLocked(context.Background(), "release", RunOptions{}, prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.buildPlan.Parallelism != 1 || overlapped.Load() {
		t.Fatal("changed shared content ran concurrently")
	}
}
