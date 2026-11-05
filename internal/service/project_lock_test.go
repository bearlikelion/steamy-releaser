package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/steam"
)

// A distinct goroutine also ensures the Windows mutex is acquired on a
// different OS thread from the owner.
func tryProjectLockElsewhere(root string) error {
	result := make(chan error, 1)
	go func() {
		unlock, err := acquireProjectLock(context.Background(), root)
		if err == nil {
			unlock()
		}
		result <- err
	}()
	return <-result
}

func TestProjectLockExcludesOtherOperationsWithoutCreatingFiles(t *testing.T) {
	root := t.TempDir()
	unlock, err := acquireProjectLock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	if err := tryProjectLockElsewhere(root); !errors.Is(err, ErrProjectBusy) {
		t.Fatalf("another owner acquired the same project: %v", err)
	}
	if err := tryProjectLockElsewhere(t.TempDir()); err != nil {
		t.Fatalf("a different project was blocked: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("locking changed project content: %v; %v", entries, err)
	}
	unlock()
	unlock = nil
	if err := tryProjectLockElsewhere(root); err != nil {
		t.Fatalf("the released project remained locked: %v", err)
	}
}

func TestProjectLockTreatsSymlinkAliasesAsTheSameProject(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "project-alias")
	if err := os.Symlink(root, alias); err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("directory symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	unlock, err := acquireProjectLock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := tryProjectLockElsewhere(alias); !errors.Is(err, ErrProjectBusy) {
		t.Fatalf("a symlink alias bypassed the project lock: %v", err)
	}
}

func TestProjectLockReleasesAfterProcessExit(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProjectLockHelper$")
	cmd.Env = append(os.Environ(), "STEAM_RELEASER_LOCK_HELPER=1", "STEAM_RELEASER_LOCK_ROOT="+root)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && line != "locked\n" {
			err = fmt.Errorf("unexpected helper output %q", line)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("lock helper did not start")
	}
	if err := tryProjectLockElsewhere(root); !errors.Is(err, ErrProjectBusy) {
		t.Fatalf("another process did not hold the project lock: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := tryProjectLockElsewhere(root)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrProjectBusy) || time.Now().After(deadline) {
			t.Fatalf("project remained locked after process exit: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestProjectLockHelper(t *testing.T) {
	if os.Getenv("STEAM_RELEASER_LOCK_HELPER") != "1" {
		return
	}
	unlock, err := acquireProjectLock(context.Background(), os.Getenv("STEAM_RELEASER_LOCK_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestBuildUploadAndShipRejectAnOccupiedProject(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "tool")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(t.TempDir(), "account"))
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/game", Content: "build",
		Build: config.Build{Command: []string{tool, "{output}"}},
	})
	document.Config.Tools.SteamCMD = tool
	runner := &fakeRunner{}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	unlock, err := acquireProjectLock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	for _, action := range []string{"build", "upload", "ship"} {
		result := make(chan error, 1)
		go func() {
			var err error
			switch action {
			case "build":
				err = svc.Build(context.Background(), "release", RunOptions{})
			case "upload":
				_, err = svc.Upload(context.Background(), "release", RunOptions{}, steam.UploadOptions{Username: "builder"})
			case "ship":
				_, err = svc.Ship(context.Background(), "release", RunOptions{}, steam.UploadOptions{Username: "builder"})
			}
			result <- err
		}()
		if err := <-result; !errors.Is(err, ErrProjectBusy) {
			t.Fatalf("%s did not reject an occupied project: %v", action, err)
		}
	}
	if len(runner.commands) != 0 {
		t.Fatalf("occupied project started commands: %#v", runner.commands)
	}
}

func TestShipKeepsProjectLockedThroughUpload(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "tool")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(t.TempDir(), "account"))
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/game", Content: "build",
		Build: config.Build{Command: []string{tool, "{output}"}},
	})
	document.Config.Tools.SteamCMD = tool
	runner := &fakeRunner{run: func(_ context.Context, command release.CommandPlan) (release.RunResult, error) {
		if err := tryProjectLockElsewhere(root); !errors.Is(err, ErrProjectBusy) {
			return release.RunResult{}, fmt.Errorf("%s ran without the project lock: %w", command.Label, err)
		}
		if command.Label == "target" {
			return release.RunResult{}, os.WriteFile(command.Args[0], []byte("game"), 0o700)
		}
		return release.RunResult{Output: "Successfully finished AppID 100 build (BuildID 12345)."}, nil
	}}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	if _, err := svc.Ship(context.Background(), "release", RunOptions{}, steam.UploadOptions{Username: "builder"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("expected a build and upload, got %#v", runner.commands)
	}
	if err := tryProjectLockElsewhere(root); err != nil {
		t.Fatalf("ship did not release the project lock: %v", err)
	}
}

func TestRetryInputsAreVerifiedUnderProjectLock(t *testing.T) {
	root := t.TempDir()
	document := testDocument(root, config.Engine{Type: "prebuilt"}, config.Target{
		DepotID: 101, Output: "build/game", Content: "build",
	})
	runner := &fakeRunner{}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	stale := errors.New("retry inputs changed")
	verify := func() error {
		if err := tryProjectLockElsewhere(root); !errors.Is(err, ErrProjectBusy) {
			return fmt.Errorf("retry inputs were checked without the project lock: %w", err)
		}
		return stale
	}
	called := false
	run := RunOptions{VerifyRetry: verify, OnLocked: func(context.Context) error {
		called = true
		return nil
	}}
	if _, err := svc.Upload(context.Background(), "release", run, steam.UploadOptions{Username: "builder"}); !errors.Is(err, stale) {
		t.Fatalf("retry input verification = %v", err)
	}
	if called || len(runner.commands) != 0 {
		t.Fatalf("retry with changed inputs reached locked work: called=%t, commands=%#v", called, runner.commands)
	}
}

func TestOperationHookRunsUnderProjectLockBeforeWork(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "tool")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	document := testDocument(root, config.Engine{Type: "custom"}, config.Target{
		DepotID: 101, Output: "build/game", Content: "build",
		Build: config.Build{Command: []string{tool, "{output}"}},
	})
	document.Config.Tools.SteamCMD = tool
	runner := &fakeRunner{}
	svc := &Service{Document: document, Runner: runner, Reporter: release.NopReporter{}}
	stopped := errors.New("locked hook stopped operation")
	for _, action := range []string{"build", "upload", "ship"} {
		called := false
		run := RunOptions{OnLocked: func(context.Context) error {
			called = true
			if err := tryProjectLockElsewhere(root); !errors.Is(err, ErrProjectBusy) {
				return fmt.Errorf("operation hook ran without the project lock: %w", err)
			}
			return stopped
		}}
		var err error
		switch action {
		case "build":
			err = svc.Build(context.Background(), "release", run)
		case "upload":
			_, err = svc.Upload(context.Background(), "release", run, steam.UploadOptions{Username: "builder"})
		case "ship":
			_, err = svc.Ship(context.Background(), "release", run, steam.UploadOptions{Username: "builder"})
		}
		if !called || !errors.Is(err, stopped) || len(runner.commands) != 0 {
			t.Fatalf("%s: called=%t, error=%v, commands=%#v", action, called, err, runner.commands)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "build")); !os.IsNotExist(err) {
		t.Fatalf("locked hook failure created build output: %v", err)
	}
}
