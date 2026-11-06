package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thornity/steam-release-tool/internal/auth"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/history"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/service"
)

func TestDesktopPreferencesShareDatabaseWithAccountAndHistory(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	app := NewDesktopApp()
	if err := auth.CacheUsername("builder"); err != nil {
		t.Fatal(err)
	}
	if err := history.Save(history.Record{ID: "attempt", Root: "game", Request: history.Request{ConfigPath: "release.yaml"}}); err != nil {
		t.Fatal(err)
	}
	if err := app.SavePreferences(map[string]string{"steam-releaser:last-config": "release.yaml"}); err != nil {
		t.Fatal(err)
	}
	preferences, err := NewDesktopApp().GetPreferences(map[string]string{
		"steam-releaser:last-config": "stale.yaml", "steam-releaser:notifications": "true", "steam_username": "other",
	})
	want := map[string]string{"steam-releaser:last-config": "release.yaml", "steam-releaser:notifications": "true"}
	if err != nil || !maps.Equal(preferences, want) {
		t.Fatalf("desktop preferences = %#v, %v", preferences, err)
	}
	if username, err := auth.LoadUsername(); err != nil || username != "builder" {
		t.Fatalf("preferences overwrote account: %q, %v", username, err)
	}
	if _, err := history.Find("attempt"); err != nil {
		t.Fatalf("preferences overwrote history: %v", err)
	}
}

func TestSteamCMDCachedAccountsRequiresConsent(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	executable := filepath.Join(dir, "steamcmd.sh")
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	vdf := `"InstallConfigStore" { "Software" { "Valve" { "Steam" { "Accounts" { "builder" { "SteamID" "1" } } "ConnectCache" { "abc" "token" } } } } }`
	if err := os.WriteFile(filepath.Join(dir, "config", "config.vdf"), []byte(vdf), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEAMCMD_BIN", executable)
	app := NewDesktopApp()
	if app.SteamCMDLocation() != executable {
		t.Fatalf("location = %q", app.SteamCMDLocation())
	}
	if _, err := app.SteamCMDCachedAccounts(); err == nil {
		t.Fatal("read SteamCMD accounts without consent")
	}
	if err := app.SavePreferences(map[string]string{"steam-releaser:steamcmd-accounts": "allow"}); err != nil {
		t.Fatal(err)
	}
	accounts, err := app.SteamCMDCachedAccounts()
	if err != nil || len(accounts) != 1 || accounts[0] != "builder" {
		t.Fatalf("accounts = %q, %v", accounts, err)
	}
}

type desktopTestRunner struct {
	run func(release.CommandPlan) (release.RunResult, error)
}

func (r desktopTestRunner) Run(_ context.Context, plan release.CommandPlan, _ bool) (release.RunResult, error) {
	return r.run(plan)
}

func TestDesktopRecordsFailureThenRetriesUploadWithoutRebuilding(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "settings"))
	tool := filepath.Join(root, "tool")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "steam-release.yaml")
	document, err := config.Initialize(config.InitOptions{Path: path, Name: "Game", Engine: "custom", AppID: 100, Profile: "release", Targets: []config.InitTarget{{Name: "linux", DepotID: 101, Output: "build/game", Content: "build", BuildSetting: tool}}})
	if err != nil {
		t.Fatal(err)
	}
	originalConfig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEAMCMD_BIN", tool)
	app := NewDesktopApp()
	var finished OperationResult
	var notices []string
	app.notifications = true
	app.sendNotification = func(title, _ string) error { notices = append(notices, title); return nil }
	app.emit = func(name string, values ...any) {
		if name == "operation:finished" {
			finished = values[0].(OperationResult)
		}
	}
	builds, uploads := 0, 0
	mutateConfigDuringLoad := false
	app.loadService = func(path, projectRoot string, reporter release.Reporter) (*service.Service, error) {
		if mutateConfigDuringLoad {
			if err := os.WriteFile(path, append(append([]byte{}, originalConfig...), []byte("\n# changed during load\n")...), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		loaded, err := service.LoadWithProjectRoot(path, projectRoot, reporter)
		if mutateConfigDuringLoad {
			if err := os.WriteFile(path, originalConfig, 0o600); err != nil {
				t.Fatal(err)
			}
			mutateConfigDuringLoad = false
		}
		if err != nil {
			return nil, err
		}
		loaded.Runner = desktopTestRunner{run: func(command release.CommandPlan) (release.RunResult, error) {
			if command.Label == "linux" {
				builds++
				return release.RunResult{}, os.WriteFile(document.ResolveProject("build/game"), []byte("game"), 0o700)
			}
			uploads++
			if uploads == 1 {
				reporter.Report(release.Event{Target: "SteamCMD upload", Level: release.LevelInfo, Message: "echoed private-"})
				reporter.Report(release.Event{Target: "SteamCMD upload", Level: release.LevelInfo, Message: "response\n"})
				return release.RunResult{}, errors.New("network failure")
			}
			return release.RunResult{Output: "Successfully finished AppID 100 build (BuildID 12345)."}, nil
		}}
		return loaded, nil
	}
	app.secrets = []string{"private-response"}
	app.run(context.Background(), OperationRequest{Action: "ship", ConfigPath: path, Profile: "release", Targets: []string{"linux"}, Username: "builder", SetLive: "qa", Notifications: true}, nil)
	if finished.OK || finished.History == nil || !finished.History.BuildReady || finished.History.Status != "failed" {
		t.Fatalf("failed attempt: %#v", finished)
	}
	if len(finished.History.UploadFingerprint) != 64 || len(finished.History.TargetFingerprints["linux"]) != 64 {
		t.Fatalf("failed attempt is missing artifact checkpoints: %#v", finished.History)
	}
	if strings.Contains(finished.History.Logs, "private-response") || !strings.Contains(finished.History.Logs, "[redacted]") {
		t.Fatal("echoed secret was not redacted across output chunks")
	}
	failed := *finished.History
	if err := os.WriteFile(document.ResolveProject("build/game"), []byte("evil"), 0o700); err != nil {
		t.Fatal(err)
	}
	request, err := history.RetryRequest(failed, true)
	if err != nil {
		t.Fatal(err)
	}
	if request.Action != "upload" || request.SetLive != "qa" {
		t.Fatalf("unexpected retry: %#v", request)
	}
	app.run(context.Background(), request, nil)
	if finished.OK || !strings.Contains(finished.Error, "artifact contents changed") || builds != 1 || uploads != 1 {
		t.Fatalf("changed artifacts were not rejected before upload: builds=%d uploads=%d result=%#v", builds, uploads, finished)
	}
	if finished.History == nil || finished.History.UploadFingerprint != "" {
		t.Fatalf("failed verification carried forward an upload checkpoint: %#v", finished.History)
	}
	if err := os.WriteFile(document.ResolveProject("build/game"), []byte("game"), 0o700); err != nil {
		t.Fatal(err)
	}
	request, err = history.RetryRequest(failed, true)
	if err != nil {
		t.Fatal(err)
	}
	mutateConfigDuringLoad = true
	app.run(context.Background(), request, nil)
	if finished.OK || !strings.Contains(finished.Error, "configuration or project root changed") || builds != 1 || uploads != 1 {
		t.Fatalf("changed loaded configuration was not rejected: builds=%d uploads=%d result=%#v", builds, uploads, finished)
	}
	request, err = history.RetryRequest(failed, true)
	if err != nil {
		t.Fatal(err)
	}
	app.run(context.Background(), request, nil)
	if !finished.OK || builds != 1 || uploads != 2 || finished.History.BuildID != "12345" {
		t.Fatalf("retry: builds=%d uploads=%d result=%#v", builds, uploads, finished)
	}
	records, err := app.GetReleaseHistory(path, "")
	if err != nil || len(records) != 4 || records[0].Status != "complete" || records[1].Status != "failed" || records[2].Status != "failed" || records[3].Status != "failed" {
		t.Fatalf("persisted history: %#v, %v", records, err)
	}
	if strings.Join(notices, ",") != "Build completed,Release failed,Release failed,Release failed,Release completed" {
		t.Fatalf("notifications = %v", notices)
	}
}

func TestDesktopRetryRebuildsChangedPartialArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name          string
		useGit        bool
		changed       bool
		sourceChanged bool
		wantLinux     int
	}{
		{name: "without source checkpoint", wantLinux: 2},
		{name: "tracked source and unchanged artifact", useGit: true, wantLinux: 1},
		{name: "tracked source and changed artifact", useGit: true, changed: true, wantLinux: 2},
		{name: "tracked source changed before retry", useGit: true, sourceChanged: true, wantLinux: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "settings"))
			tool := filepath.Join(root, "tool")
			if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("STEAMCMD_BIN", tool)
			path := filepath.Join(root, "steam-release.yaml")
			document, err := config.Initialize(config.InitOptions{
				Path: path, Name: "Game", Engine: "custom", AppID: 100, Profile: "release",
				Targets: []config.InitTarget{
					{Name: "linux", DepotID: 101, Output: "build/linux/Game", Content: "build/linux", BuildSetting: tool},
					{Name: "windows", DepotID: 102, Output: "build/windows/Game.exe", Content: "build/windows", BuildSetting: tool},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.useGit {
				if _, err := exec.LookPath("git"); err != nil {
					t.Skip("git unavailable")
				}
				git := func(args ...string) {
					t.Helper()
					command := exec.Command("git", append([]string{"-C", root}, args...)...)
					if output, err := command.CombinedOutput(); err != nil {
						t.Fatalf("git %v: %s: %v", args, output, err)
					}
				}
				git("init", "--quiet")
				if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/build/\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				git("add", "steam-release.yaml", "tool", ".gitignore")
				git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+t.TempDir(), "commit", "--quiet", "-m", "fixture")
			}
			app := NewDesktopApp()
			var finished OperationResult
			app.emit = func(name string, values ...any) {
				if name == "operation:finished" {
					finished = values[0].(OperationResult)
				}
			}
			builds := map[string]int{}
			uploads := 0
			app.loadService = func(path, projectRoot string, reporter release.Reporter) (*service.Service, error) {
				loaded, err := service.LoadWithProjectRoot(path, projectRoot, reporter)
				if err != nil {
					return nil, err
				}
				loaded.Runner = desktopTestRunner{run: func(command release.CommandPlan) (release.RunResult, error) {
					if command.Label == "SteamCMD upload" {
						uploads++
						return release.RunResult{Output: "Successfully finished AppID 100 build (BuildID 12345)."}, nil
					}
					builds[command.Label]++
					if command.Label == "windows" && builds["windows"] == 1 {
						return release.RunResult{}, errors.New("windows build failed")
					}
					output := map[string]string{"linux": "build/linux/Game", "windows": "build/windows/Game.exe"}[command.Label]
					path := document.ResolveProject(output)
					if err := os.WriteFile(path, []byte(command.Label), 0o700); err != nil {
						return release.RunResult{}, err
					}
					modified := time.Unix(1_000_000_000+int64(builds[command.Label]), 0)
					return release.RunResult{}, os.Chtimes(path, modified, modified)
				}}
				return loaded, nil
			}
			app.run(context.Background(), OperationRequest{Action: "ship", ConfigPath: path, Profile: "release", Username: "builder"}, nil)
			if finished.OK || finished.History == nil || len(finished.History.TargetFingerprints["linux"]) != 64 {
				t.Fatalf("partial failure did not record a target checkpoint: %#v", finished)
			}
			if tc.changed {
				if err := os.WriteFile(document.ResolveProject("build/linux/Game"), []byte("modified"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if tc.sourceChanged {
				if err := os.WriteFile(tool, []byte("changed source"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var request history.Request
			if tc.sourceChanged {
				request, err = history.PrepareRetryRequest(*finished.History, false)
			} else {
				request, err = history.RetryRequest(*finished.History, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			app.run(context.Background(), request, nil)
			if tc.sourceChanged {
				if finished.OK || !strings.Contains(finished.Error, "source files changed") || builds["linux"] != 1 || builds["windows"] != 1 || uploads != 0 {
					t.Fatalf("changed source reached a build or upload: builds=%v uploads=%d result=%#v", builds, uploads, finished)
				}
				if finished.History == nil || len(finished.History.CompletedTargets) != 0 || len(finished.History.TargetFingerprints) != 0 || finished.History.UploadFingerprint != "" {
					t.Fatalf("failed source verification carried forward checkpoints: %#v", finished.History)
				}
				request, err = history.RetryRequest(*finished.History, false)
				if err != nil || len(request.ReuseTargets) != 0 {
					t.Fatalf("subsequent retry reused an unverified target: %#v, %v", request, err)
				}
				old := time.Unix(1, 0)
				if err := os.Chtimes(document.ResolveProject("build/linux/Game"), old, old); err != nil {
					t.Fatal(err)
				}
				app.run(context.Background(), request, nil)
			}
			if !finished.OK || builds["linux"] != tc.wantLinux || builds["windows"] != 2 || uploads != 1 {
				t.Fatalf("retry builds=%v uploads=%d result=%#v", builds, uploads, finished)
			}
		})
	}
}

func TestDesktopReportsUnavailableSourceCheckpoint(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	tool := filepath.Join(root, "tool")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "steam-release.yaml")
	document, err := config.Initialize(config.InitOptions{Path: path, Name: "Game", Engine: "custom", AppID: 100, Profile: "release", Targets: []config.InitTarget{{Name: "linux", DepotID: 101, Output: "build/game", Content: "build", BuildSetting: tool}}})
	if err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(root, "asset.txt")
	if err := os.WriteFile(asset, []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	git("init", "--quiet")
	git("add", "steam-release.yaml", "tool", "asset.txt")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+t.TempDir(), "commit", "--quiet", "-m", "fixture")
	if err := os.Remove(asset); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(asset, 0o755); err != nil {
		t.Fatal(err)
	}
	app := NewDesktopApp()
	var finished OperationResult
	var warnings []string
	app.emit = func(name string, values ...any) {
		if name == "operation:finished" {
			finished = values[0].(OperationResult)
		}
		if name == "release:event" {
			event := values[0].(release.Event)
			if event.Level == release.LevelWarning {
				warnings = append(warnings, event.Message)
			}
		}
	}
	app.loadService = func(path, projectRoot string, reporter release.Reporter) (*service.Service, error) {
		loaded, err := service.LoadWithProjectRoot(path, projectRoot, reporter)
		if err != nil {
			return nil, err
		}
		loaded.Runner = desktopTestRunner{run: func(release.CommandPlan) (release.RunResult, error) {
			return release.RunResult{}, os.WriteFile(document.ResolveProject("build/game"), []byte("game"), 0o700)
		}}
		return loaded, nil
	}
	app.run(context.Background(), OperationRequest{Action: "build", ConfigPath: path, Profile: "release"}, nil)
	if !finished.OK || finished.History == nil || finished.History.SourceHash != "unavailable" {
		t.Fatalf("unexpected build result without source checkpoint: %#v", finished)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "Could not record a source checkpoint") {
		t.Fatalf("missing source checkpoint warning: %v", warnings)
	}
}

func TestDesktopRejectsConfigurationChangedAfterLoading(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	tool := filepath.Join(root, "tool")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "steam-release.yaml")
	if _, err := config.Initialize(config.InitOptions{Path: path, Name: "Game", Engine: "custom", AppID: 100, Profile: "release", Targets: []config.InitTarget{{Name: "linux", DepotID: 101, Output: "build/game", Content: "build", BuildSetting: tool}}}); err != nil {
		t.Fatal(err)
	}
	app := NewDesktopApp()
	var finished OperationResult
	app.emit = func(name string, values ...any) {
		if name == "operation:finished" {
			finished = values[0].(OperationResult)
		}
	}
	builds := 0
	app.loadService = func(path, projectRoot string, reporter release.Reporter) (*service.Service, error) {
		loaded, err := service.LoadWithProjectRoot(path, projectRoot, reporter)
		if err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return nil, err
		}
		_, writeErr := file.WriteString("\n# changed after loading\n")
		closeErr := file.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		loaded.Runner = desktopTestRunner{run: func(release.CommandPlan) (release.RunResult, error) {
			builds++
			return release.RunResult{}, nil
		}}
		return loaded, nil
	}
	app.run(context.Background(), OperationRequest{Action: "build", ConfigPath: path, Profile: "release"}, nil)
	if finished.OK || !strings.Contains(finished.Error, "configuration changed while starting") || builds != 0 {
		t.Fatalf("changed configuration reached a build: builds=%d result=%#v", builds, finished)
	}
}

func TestDesktopReadinessIncludesPrebuiltArtifactsAndUploadOptions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "settings"))
	path := filepath.Join(root, "release.yaml")
	if err := os.WriteFile(path, []byte("app_id: 100\ntargets:\n  linux: {depot_id: 101, content: missing}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := NewDesktopApp().CheckReadiness(OperationRequest{ConfigPath: path, Profile: "release", Targets: []string{"linux"}, Preview: true, SetLive: "qa"})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, issue := range report.Issues {
		codes[issue.Code] = true
	}
	if report.OK() || !codes["artifact.invalid"] || !codes["steam.options"] || !codes["steam.account"] {
		t.Fatalf("readiness report: %#v", report)
	}
}

func TestDesktopReadinessReportsInvalidExistingVDF(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "settings"))
	path := filepath.Join(root, "release.yaml")
	source := "app_id: 100\nexisting_vdf: custom.vdf\ntargets:\n  linux: {depot_id: 101, content: build}\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "custom.vdf")
	if err := os.WriteFile(script, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		preview    bool
	}{
		{"incompatible preview", "cannot rewrite profile existing_vdf", true},
		{"missing script", "existing VDF does not exist", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.preview {
				if err := os.Remove(script); err != nil {
					t.Fatal(err)
				}
			}
			report, err := NewDesktopApp().CheckReadiness(OperationRequest{ConfigPath: path, Profile: "release", Preview: tc.preview})
			if err != nil {
				t.Fatal(err)
			}
			for _, issue := range report.Issues {
				if issue.Code == "steam.script" && strings.Contains(issue.Message, tc.want) {
					return
				}
			}
			t.Fatalf("readiness did not report %q: %#v", tc.want, report)
		})
	}
}

func TestDesktopRecoversInterruptedHistory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "settings"))
	path := filepath.Join(root, "release.yaml")
	if err := os.WriteFile(path, []byte("app_id: 100\ntargets:\n  linux: {depot_id: 101, content: build}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := history.Save(history.Record{ID: "interrupted", Status: "running", Root: root, Request: history.Request{ConfigPath: path, Action: "build"}}); err != nil {
		t.Fatal(err)
	}
	records, err := NewDesktopApp().GetReleaseHistory(path, "")
	if err != nil || len(records) != 1 || records[0].Status != "cancelled" {
		t.Fatalf("interrupted history: %#v, %v", records, err)
	}
}

func TestDesktopLoginResponsePipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	app := NewDesktopApp()
	app.busy = true
	app.steamInput = make(chan string, 1)
	go feedSteamInput(writer, app.steamInput)
	if err := app.SubmitSteamInput("private response"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil || line != "private response\n" {
		t.Fatalf("login input = %q, %v", line, err)
	}
	for _, response := range []string{"", "two\nlines", "two\rlines", string(make([]byte, 1025))} {
		if err := app.SubmitSteamInput(response); err == nil {
			t.Fatalf("accepted invalid login response %q", response)
		}
	}
	app.mu.Lock()
	app.busy = false
	close(app.steamInput)
	app.steamInput = nil
	app.mu.Unlock()
	if err := app.SubmitSteamInput("late"); err == nil {
		t.Fatal("accepted login response after operation finished")
	}
}

func TestDesktopClearsHistoryFromGUIWithoutDeletingRecords(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "settings"))
	path := filepath.Join(root, "release.yaml")
	if err := os.WriteFile(path, []byte("app_id: 100\ntargets:\n  linux: {depot_id: 101, content: build}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := history.Record{ID: "saved", Status: "failed", Logs: "existing build logs", Root: root, Request: history.Request{ConfigPath: path, Action: "build"}}
	inFlight := history.Record{ID: "in-flight", Status: "running", Root: root, Request: history.Request{ConfigPath: path, Action: "build"}}
	for _, attempt := range []history.Record{record, inFlight} {
		if err := history.Save(attempt); err != nil {
			t.Fatal(err)
		}
	}
	app := NewDesktopApp()
	app.busy = true
	if err := app.ClearReleaseHistory(path, "", []string{record.ID}); err != nil {
		t.Fatal(err)
	}
	if !app.busy {
		t.Fatal("clearing history changed the active operation")
	}
	if records, err := app.GetReleaseHistory(path, ""); err != nil || len(records) != 1 || records[0].ID != inFlight.ID {
		t.Fatalf("clear affected the in-flight attempt: %#v, %v", records, err)
	}
	inFlight.Status = "complete"
	if err := history.Save(inFlight); err != nil {
		t.Fatal(err)
	}
	if records, err := NewDesktopApp().GetReleaseHistory(path, ""); err != nil || len(records) != 1 || records[0].ID != inFlight.ID {
		t.Fatalf("cleared history appeared or new attempt disappeared after reopening: %#v, %v", records, err)
	}
	if stored, err := history.Find(record.ID); err != nil || stored.Logs != record.Logs || stored.Status != record.Status {
		t.Fatalf("clear removed or changed saved history: %#v, %v", stored, err)
	}
	if err := app.ClearReleaseHistory(filepath.Join(root, "missing.yaml"), "", []string{record.ID}); err == nil {
		t.Fatal("accepted an invalid project selection")
	}
	app.exiting = true
	if err := app.ClearReleaseHistory(path, "", []string{inFlight.ID}); err == nil {
		t.Fatal("cleared history while the app was closing")
	}
	if records, err := history.ListVisible(path, root); err != nil || len(records) != 1 {
		t.Fatalf("rejected clear changed history: %#v, %v", records, err)
	}
}

type desktopInputTestRunner struct {
	run func(io.Reader) (release.RunResult, error)
}

func (r desktopInputTestRunner) Run(_ context.Context, _ release.CommandPlan, _ bool) (release.RunResult, error) {
	return r.run(nil)
}

func (r desktopInputTestRunner) RunWithInput(_ context.Context, _ release.CommandPlan, input io.Reader) (release.RunResult, error) {
	return r.run(input)
}

func TestDesktopUsesExistingCredentialFileAfterRestartWithoutSavingPasswords(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "settings"))
	tool := filepath.Join(root, "tool")
	if err := os.WriteFile(tool, []byte("fake"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEAMCMD_BIN", tool)
	path := filepath.Join(root, "release.yaml")
	if _, err := config.Initialize(config.InitOptions{Path: path, Engine: "custom", AppID: 100, Targets: []config.InitTarget{{Name: "linux", DepotID: 101, Output: "build/game", BuildSetting: tool}}}); err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(root, "credentials.json")
	credentialData := []byte("{\n  \"username\": \"builder\",\n  \"password\": \"private-password\"\n}\n")
	if err := os.WriteFile(credentialPath, credentialData, 0o400); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	app := NewDesktopApp()
	if _, err := app.ConfigureCredentialFile(true, credentialPath); err != nil {
		t.Fatal(err)
	}
	runLogin := func(app *DesktopApp, password string, saved, fail bool) OperationResult {
		app.ctx = context.Background()
		finished := make(chan OperationResult, 1)
		var visible strings.Builder
		app.emit = func(name string, values ...any) {
			if name == "operation:finished" {
				finished <- values[0].(OperationResult)
			}
			if name == "release:event" {
				visible.WriteString(values[0].(release.Event).Message)
			}
		}
		app.loadService = func(path, projectRoot string, reporter release.Reporter) (*service.Service, error) {
			loaded, err := service.LoadWithProjectRoot(path, projectRoot, reporter)
			if err != nil {
				return nil, err
			}
			loaded.Runner = desktopInputTestRunner{run: func(input io.Reader) (release.RunResult, error) {
				reporter.Report(release.Event{Target: "SteamCMD login", Message: "Password: "})
				if saved {
					accepted, err := app.SubmitSavedSteamPassword()
					if err != nil || !accepted {
						return release.RunResult{}, errors.New("saved password was not submitted")
					}
				} else if err := app.SubmitSteamPassword(password); err != nil {
					return release.RunResult{}, err
				}
				reader := bufio.NewReader(input)
				line, err := reader.ReadString('\n')
				if err != nil || line != password+"\n" {
					return release.RunResult{}, errors.New("wrong password was sent to SteamCMD")
				}
				split := len(password) / 2
				reporter.Report(release.Event{Target: "SteamCMD login", Message: "echoed " + password[:split]})
				reporter.Report(release.Event{Target: "SteamCMD login", Message: password[split:] + "\n"})
				if err := app.SubmitSteamInput("fresh-guard-code"); err != nil {
					return release.RunResult{}, err
				}
				if line, err := reader.ReadString('\n'); err != nil || line != "fresh-guard-code\n" {
					return release.RunResult{}, errors.New("guard response was not submitted")
				}
				if fail {
					return release.RunResult{Output: "Invalid password"}, nil
				}
				return release.RunResult{Output: "Logged in OK"}, nil
			}}
			return loaded, nil
		}
		if err := app.StartOperation(OperationRequest{Action: "login", ConfigPath: path, Username: "builder"}); err != nil {
			t.Fatal(err)
		}
		select {
		case result := <-finished:
			if strings.Contains(visible.String(), password) {
				t.Fatal("password was exposed in frontend output")
			}
			return result
		case <-time.After(5 * time.Second):
			app.CancelOperation()
			t.Fatal("login did not finish")
			return OperationResult{}
		}
	}
	if result := runLogin(app, "manual-password", false, false); !result.OK {
		t.Fatalf("initial login failed: %s", result.Error)
	}
	credentials, err := auth.ReadCredentialFile(credentialPath)
	if err != nil || credentials.Username != "builder" || credentials.Password != "private-password" {
		t.Fatal("a manual password or Guard response replaced the file password")
	}
	restarted := NewDesktopApp()
	if restarted.CachedUsername() != "builder" {
		t.Fatal("saved username was not restored")
	}
	settings, err := restarted.GetCredentialFileSettings()
	encoded, _ := json.Marshal(settings)
	if err != nil || !settings.Enabled || !settings.HasCredentials || bytes.Contains(encoded, []byte("private-password")) {
		t.Fatal("credential settings were not restored safely")
	}
	if result := runLogin(restarted, "private-password", true, false); !result.OK {
		t.Fatalf("saved login failed: %s", result.Error)
	}
	if result := runLogin(restarted, "wrong-password", false, true); result.OK {
		t.Fatal("failed login was accepted")
	}
	credentials, err = auth.ReadCredentialFile(credentialPath)
	if err != nil || credentials.Password != "private-password" {
		t.Fatal("failed login replaced the saved password")
	}
	if _, err := restarted.ConfigureCredentialFile(false, ""); err != nil {
		t.Fatal(err)
	}
	if result := runLogin(restarted, "private-password", true, false); result.OK {
		t.Fatal("disabled credential file was used")
	}
	got, err := os.ReadFile(credentialPath)
	if err != nil || !bytes.Equal(got, credentialData) {
		t.Fatal("sign-in or disabling modified the existing credential file")
	}
	info, err := os.Stat(credentialPath)
	if err != nil || info.Mode() != originalInfo.Mode() || !info.ModTime().Equal(originalInfo.ModTime()) {
		t.Fatal("existing credential file metadata was changed")
	}
}

func TestDesktopCredentialFileRequiresExistingCredentials(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "credentials.json")
	app := NewDesktopApp()
	if _, err := app.ConfigureCredentialFile(true, path); !os.IsNotExist(err) {
		t.Fatal("accepted a missing credential file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created a missing credential file")
	}
	for _, data := range []string{`{"username":"","password":""}`, `{"unrelated":"preserve me"}`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := app.ConfigureCredentialFile(true, path); err == nil {
			t.Fatal("accepted a file without valid Steam credentials")
		}
		got, _ := os.ReadFile(path)
		if string(got) != data {
			t.Fatal("invalid credential file was modified")
		}
	}
	settings, err := app.GetCredentialFileSettings()
	if err != nil || settings.Enabled || settings.Path != "" {
		t.Fatal("invalid file selection changed the credential settings")
	}
}

func TestDesktopSavedCredentialPasswordMatchesAccountAndIsTriedOnce(t *testing.T) {
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "credentials.json")
	data := []byte(`{"username":"builder","password":"private"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewDesktopApp()
	if _, err := app.ConfigureCredentialFile(true, path); err != nil {
		t.Fatal(err)
	}
	app.credentialFile = auth.CredentialFileConfig{Enabled: true, Path: path}
	app.busy = true
	app.steamInput = make(chan string, 1)
	app.steamUsername = "other_account"
	if accepted, err := app.SubmitSavedSteamPassword(); err != nil || accepted {
		t.Fatal("saved password was submitted for a different account")
	}
	app.steamUsername, app.triedCredentialPassword = "builder", false
	if accepted, err := app.SubmitSavedSteamPassword(); err != nil || !accepted {
		t.Fatal("matching saved password was not submitted")
	}
	if <-app.steamInput != "private" || len(app.secrets) != 1 {
		t.Fatal("saved password was not registered for redaction")
	}
	if accepted, err := app.SubmitSavedSteamPassword(); err != nil || accepted {
		t.Fatal("saved password was retried in the same operation")
	}
	if _, err := app.ConfigureCredentialFile(false, ""); err == nil {
		t.Fatal("allowed a credential file change during authentication")
	}
	app.emit = func(string, ...any) {}
	app.finish(OperationResult{Action: "logout", OK: true})
	credentials, err := auth.ReadCredentialFile(path)
	if err != nil || credentials.Password != "private" {
		t.Fatal("clearing cached credentials modified the existing credential file")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("logout modified the existing credential file")
	}
}

func TestLoadConfigWithDefaults(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(directory, "cache"))
	path := filepath.Join(directory, "steam-release.yaml")
	source := "app_id: 100\ntargets:\n  windows:\n    depot_id: 101\n    content: builds/windows\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := NewDesktopApp().LoadConfig(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Project != filepath.Base(directory) || summary.Root != directory || summary.Engine != "prebuilt" || summary.DefaultProfile != "release" {
		t.Fatalf("desktop did not receive resolved defaults: %#v", summary)
	}
	if len(summary.Profiles) != 1 || summary.Profiles[0].AppID != 100 || len(summary.Profiles[0].Targets) != 1 {
		t.Fatalf("unexpected profiles: %#v", summary.Profiles)
	}
	target := summary.Profiles[0].Targets[0]
	if target.Name != "windows" || target.Platform != "windows" || target.DepotID != 101 || target.Output != filepath.Join(directory, "builds", "windows") {
		t.Fatalf("unexpected desktop target: %#v", target)
	}
	project := filepath.Join(directory, "local checkout")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	overridden, err := NewDesktopApp().LoadConfig(path, project)
	if err != nil || overridden.Root != project || overridden.Profiles[0].Targets[0].Output != filepath.Join(project, "builds", "windows") {
		t.Fatalf("desktop did not use local project root: %#v, %v", overridden, err)
	}
}

func TestDesktopCreatesAnnotatedReleaseConfig(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(directory, "cache"))
	path := filepath.Join(directory, "steam-release.yaml")
	app := NewDesktopApp()
	created, err := app.CreateConfig(CreateConfigRequest{
		Path: path, Name: "New Game", Engine: "prebuilt", AppID: 100,
	})
	if err != nil || created.Project != "New Game" || created.DefaultProfile != "release" || len(created.Profiles) != 1 || len(created.Profiles[0].Targets) != 3 || created.ConfigHash == "" {
		t.Fatalf("create config: %#v, %v", created, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(contents, []byte("profiles:\n  release:")) || !bytes.Contains(contents, []byte("# demo:")) {
		t.Fatalf("missing profile template: %s, %v", contents, err)
	}
	if _, err := app.CreateConfig(CreateConfigRequest{Path: path, Name: "Other", Engine: "prebuilt", AppID: 200}); err == nil {
		t.Fatal("create should not replace an existing configuration")
	}
}

func TestDesktopOpensInvalidYAMLForRepairWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config with spaces.yaml")
	const source = "profiles: [broken YAML\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewDesktopApp()
	var opened string
	app.openConfigFile = func(path string) error { opened = path; return nil }
	if err := app.EditConfig(path); err != nil || opened != path {
		t.Fatalf("could not open invalid YAML for repair: %q, %v", opened, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != source {
		t.Fatalf("opening the editor changed YAML: %q, %v", contents, err)
	}
	opened = ""
	for _, invalid := range []string{"", filepath.Join(t.TempDir(), "missing.yaml"), filepath.Dir(path), strings.TrimSuffix(path, ".yaml") + ".txt"} {
		if err := app.EditConfig(invalid); err == nil {
			t.Fatalf("accepted invalid editor path %q", invalid)
		}
	}
	app.busy = true
	if err := app.EditConfig(path); err == nil || opened != "" {
		t.Fatal("opened configuration during a release")
	}
	app.busy = false
	app.openConfigFile = func(string) error { return errors.New("no editor available") }
	if err := app.EditConfig(path); err == nil || !strings.Contains(err.Error(), "no editor available") {
		t.Fatalf("editor error was not reported: %v", err)
	}
}

func TestDesktopReloadReflectsExternalConfigurationEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.yaml")
	app := NewDesktopApp()
	if _, err := app.CreateConfig(CreateConfigRequest{Path: path, Name: "Game", Engine: "godot", AppID: 100}); err != nil {
		t.Fatal(err)
	}
	before, err := app.LoadConfig(path, "")
	if err != nil {
		t.Fatal(err)
	}
	const source = `profiles:
  demo:
    app_id: 200
    targets:
      linux:
        depot_id: 201
        output: builds/demo/Game
        build:
          preset: Linux Demo
          args: ["--verbose", "", "path with spaces"]
          env: {EDITION: "demo", EMPTY: ""}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := app.LoadConfig(path, "")
	if err != nil || after.ConfigHash == before.ConfigHash || after.DefaultProfile != "demo" || after.Profiles[0].AppID != 200 {
		t.Fatalf("reload did not refresh the configuration: %#v, %v", after, err)
	}
	target := after.Profiles[0].Targets[0]
	if target.BuildSetting != "Linux Demo" || len(target.BuildArgs) != 3 || target.BuildArgs[1] != "" || target.BuildEnv["EDITION"] != "demo" {
		t.Fatalf("reload lost build options: %#v", target)
	}
}

func TestDesktopStarterTargetsForEachEngine(t *testing.T) {
	for _, tc := range []struct {
		engine string
		field  string
	}{
		{"prebuilt", "REPLACE_WITH_CONTENT"},
		{"godot", "REPLACE_WITH_WINDOWS_PRESET"},
		{"unity", "REPLACE_WITH_WINDOWS_PROFILE.asset"},
		{"custom", "replace-with-build-command"},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "steam-release.yaml")
			_, err := NewDesktopApp().CreateConfig(CreateConfigRequest{
				Path: path, Name: "Game", Engine: tc.engine, AppID: 42,
			})
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(path)
			if err != nil || !bytes.Contains(contents, []byte(tc.field)) {
				t.Fatalf("missing %s placeholder in YAML: %s, %v", tc.engine, contents, err)
			}
			document, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if document.Config.Project.Name != "Game" || document.Config.Project.Engine.Type != tc.engine {
				t.Fatalf("incorrect project settings: %#v", document.Config.Project)
			}
			profile := document.Config.Profiles["release"]
			if profile.AppID != 42 || len(profile.Targets) != 3 {
				t.Fatalf("incorrect profile: %#v", profile)
			}
			for i, name := range []string{"windows", "linux", "macos"} {
				target, ok := profile.Targets[name]
				if !ok || target.Platform != name || target.DepotID != uint64(1000001+i) {
					t.Fatalf("incorrect %s target: %#v", name, target)
				}
				if !strings.Contains(target.Content+target.Output, "REPLACE_WITH") {
					t.Fatalf("%s target has no placeholder path: %#v", name, target)
				}
			}
		})
	}
}

func TestDesktopPrefersReleaseProfile(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(directory, "cache"))
	path := filepath.Join(directory, "steam-release.yaml")
	source := `profiles:
  demo:
    app_id: 100
    targets:
      linux: {depot_id: 101, content: builds/demo}
  release:
    app_id: 200
    targets:
      linux: {depot_id: 201, content: builds/release}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := NewDesktopApp().LoadConfig(path, "")
	if err != nil || summary.DefaultProfile != "release" || len(summary.Profiles) != 2 {
		t.Fatalf("unexpected profile default: %#v, %v", summary, err)
	}
}
