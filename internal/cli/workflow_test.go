package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thornity/steam-release-tool/internal/auth"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/release"
	"github.com/thornity/steam-release-tool/internal/steam"
	"go.yaml.in/yaml/v3"
)

// The test executable doubles as a portable build tool and SteamCMD substitute.
// This exercises real subprocesses on all supported runners without a Steam account.
func TestMain(m *testing.M) {
	if mode := os.Getenv("STEAM_RELEASER_TEST_PROCESS"); mode != "" {
		os.Exit(runWorkflowProcess(mode))
	}
	os.Exit(m.Run())
}

func runWorkflowProcess(mode string) int {
	if mode == "timeout" {
		time.Sleep(10 * time.Second)
		return 0
	}
	file, err := os.OpenFile(os.Getenv("STEAM_RELEASER_TEST_RECORD"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	err = json.NewEncoder(file).Encode(os.Args[1:])
	_ = file.Close()
	if err != nil {
		return 1
	}
	if len(os.Args) == 3 && os.Args[1] == "build" {
		if mode == "build-failure" {
			fmt.Fprintln(os.Stderr, "engine failed")
			return 1
		}
		if err := os.MkdirAll(filepath.Dir(os.Args[2]), 0o755); err != nil {
			return 1
		}
		if err := os.WriteFile(os.Args[2], []byte("game"), 0o755); err != nil {
			return 1
		}
		fmt.Println("Build completed")
		return 0
	}
	if mode != "interactive" && (len(os.Args) < 3 || os.Args[1] != "+@NoPromptForPassword" || os.Args[2] != "1") {
		fmt.Println("ERROR! Expected unattended SteamCMD")
		return 1
	}
	if mode == "auth-failure" {
		// SteamCMD can report failure while returning exit status zero.
		fmt.Println("FAILED (No cached credentials)")
		return 0
	}
	for i, arg := range os.Args {
		if arg != "+run_app_build" || i+1 >= len(os.Args) {
			continue
		}
		contents, err := os.ReadFile(os.Args[i+1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if mode == "log-only" {
			var output string
			for _, line := range strings.Split(string(contents), "\n") {
				if value, ok := strings.CutPrefix(strings.TrimSpace(line), `"BuildOutput" `); ok {
					output = strings.Trim(value, `"`)
				}
			}
			if output == "" {
				fmt.Fprintln(os.Stderr, "missing BuildOutput")
				return 1
			}
			if err := os.WriteFile(filepath.Join(output, "app_build_100.log"), []byte("Successfully finished AppID 100 build (BuildID 12345).\n"), 0o600); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Println("SteamCMD exited without a console completion")
			return 0
		}
		if strings.Contains(string(contents), `"Preview" "1"`) {
			fmt.Println("Successfully finished AppID 100 build preview.")
		} else {
			fmt.Println("Successfully finished AppID 100 build (BuildID 12345).")
		}
		return 0
	}
	fmt.Println("SteamCMD command completed")
	return 0
}

func workflowFixture(t *testing.T, mode string) (string, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "game with spaces")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(root, "commands.jsonl")
	t.Setenv("STEAM_RELEASER_TEST_PROCESS", mode)
	// These single-threaded test doubles do no application work. Avoid the
	// race runtime's default one-second exit delay in each subprocess while
	// leaving race detection and its normal exit delay enabled in the parent.
	t.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	t.Setenv("STEAM_RELEASER_TEST_RECORD", record)
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "auth"))
	t.Setenv("STEAMCMD_BIN", executable)
	t.Setenv("STEAM_USERNAME", "builder")
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_ACTIONS", "true")
	profile := config.Profile{AppID: 100, Targets: map[string]config.Target{
		"linux": {DepotID: 101, Output: "build/linux/Game", Content: "build/linux", Required: []string{"Game", "license.txt"},
			Build: config.Build{Command: []string{executable, "build", "{output}"}}},
		"windows": {DepotID: 102, Output: "build/windows/Game.exe", Content: "build/windows",
			Build: config.Build{Command: []string{executable, "build", "{output}"}}},
	}, PostBuild: []config.CopyRule{{From: "license.txt", Targets: []string{"linux"}}}}
	contents, err := yaml.Marshal(config.Config{
		Project:  config.Project{Name: "CI game", Engine: config.Engine{Type: "custom"}},
		Profiles: map[string]config.Profile{"beta": profile},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "steam-release.yaml")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "license.txt"), []byte("license"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, root, record
}

func readWorkflowCommands(t *testing.T, path string) [][]string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	var commands [][]string
	for {
		var args []string
		if err := decoder.Decode(&args); errors.Is(err, io.EOF) {
			return commands
		} else if err != nil {
			t.Fatal(err)
		}
		commands = append(commands, args)
	}
}

func TestCLIShipInGitHubActions(t *testing.T) {
	path, root, record := workflowFixture(t, "success")
	var out, errOut bytes.Buffer
	if err := Execute(context.Background(), []string{"--config", path, "--json", "--verbose", "--timeout", "30s",
		"ship", "--target", "linux", "--set-live", "beta"}, &out, &errOut); err != nil {
		t.Fatalf("ship: %v\n%s", err, errOut.String())
	}
	var result steam.UploadResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Username != "builder" {
		t.Fatalf("invalid JSON result: %s, %v", out.String(), err)
	}
	if info, err := os.Stat(result.Scripts.BuildOutput); err != nil || !info.IsDir() {
		t.Fatalf("upload result did not report its Steam output directory: %q, %v", result.Scripts.BuildOutput, err)
	}
	commands := readWorkflowCommands(t, record)
	if len(commands) != 2 || commands[0][0] != "build" || commands[0][1] != filepath.Join(root, "build/linux/Game") {
		t.Fatalf("unexpected build commands: %v", commands)
	}
	want := []string{"+@NoPromptForPassword", "1", "+login", "builder", "+run_app_build", result.BuildScript, "+quit"}
	if !slices.Equal(commands[1], want) {
		t.Fatalf("unexpected unattended upload: %v", commands[1])
	}
	if _, err := os.Stat(filepath.Join(root, "build/linux/license.txt")); err != nil {
		t.Fatalf("post-build copy did not run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "build/windows")); !os.IsNotExist(err) {
		t.Fatalf("unselected target was built: %v", err)
	}
	script, err := os.ReadFile(result.BuildScript)
	if err != nil || !strings.Contains(string(script), `"SetLive" "beta"`) || strings.Contains(string(script), `"102"`) {
		t.Fatalf("wrong upload selection/options: %s, %v", script, err)
	}
	decoder := json.NewDecoder(&errOut)
	var events []release.Event
	for {
		var event release.Event
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("progress polluted JSON output: %v", err)
		}
		events = append(events, event)
	}
	if len(events) == 0 || events[len(events)-1].Level != release.LevelSuccess {
		t.Fatalf("missing success event: %v", events)
	}
}

func TestCLISeparateBuildAndPreviewUpload(t *testing.T) {
	path, _, record := workflowFixture(t, "success")
	var out, errOut bytes.Buffer
	base := []string{"--config", path, "--json", "--non-interactive"}
	if err := Execute(context.Background(), append(base, "build", "--target", "linux"), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var completed struct {
		Action string `json:"action"`
		OK     bool   `json:"ok"`
	}
	if err := json.Unmarshal(out.Bytes(), &completed); err != nil || !completed.OK || completed.Action != "build" {
		t.Fatalf("missing build JSON result: %s, %v", out.String(), err)
	}
	out.Reset()
	if err := Execute(context.Background(), append(base, "upload", "--target", "linux", "--preview"), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var result steam.UploadResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(result.BuildScript)
	if err != nil || !strings.Contains(string(script), `"Preview" "1"`) {
		t.Fatalf("preview was not applied: %s, %v", script, err)
	}
	if commands := readWorkflowCommands(t, record); len(commands) != 2 || commands[1][0] != "+@NoPromptForPassword" {
		t.Fatalf("separate upload rebuilt artifacts: %v", commands)
	}
}

func TestCLIUploadReadsFreshSteamBuildLog(t *testing.T) {
	path, _, _ := workflowFixture(t, "log-only")
	var out, errOut bytes.Buffer
	if err := Execute(context.Background(), []string{"--config", path, "--json", "ship", "--target", "linux"}, &out, &errOut); err != nil {
		t.Fatalf("ship with a log-only completion: %v\n%s", err, errOut.String())
	}
	var result steam.UploadResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.BuildID != "12345" {
		t.Fatalf("missing build ID from the fresh log: %s, %v", out.String(), err)
	}
}

func TestCLIAuthenticationModes(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		flags      []string
	}{
		{"local default", "interactive", nil},
		{"automatic CI", "success", nil},
		{"explicit unattended", "success", []string{"--non-interactive"}},
		{"interactive override", "interactive", []string{"--non-interactive=false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _, record := workflowFixture(t, tc.mode)
			if tc.name == "local default" {
				t.Setenv("CI", "false")
				t.Setenv("GITHUB_ACTIONS", "0")
			}
			var out, errOut bytes.Buffer
			base := append([]string{"--config", path, "--json"}, tc.flags...)
			for _, args := range [][]string{{"auth", "login", "builder"}, {"auth", "logout"}} {
				out.Reset()
				if err := Execute(context.Background(), append(base, args...), &out, &errOut); err != nil {
					t.Fatal(err)
				}
				if !json.Valid(out.Bytes()) {
					t.Fatalf("authentication did not emit JSON: %s", out.String())
				}
			}
			for _, args := range readWorkflowCommands(t, record) {
				if (args[0] == "+@NoPromptForPassword") != (tc.mode != "interactive") {
					t.Fatalf("wrong prompt mode: %v", args)
				}
			}
			if _, err := auth.LoadUsername(); !os.IsNotExist(err) {
				t.Fatalf("logout retained cached username: %v", err)
			}
		})
	}
}

func TestCLITimeoutsApplyToEveryProcessOperation(t *testing.T) {
	for _, command := range [][]string{
		{"build", "--target", "linux"},
		{"upload", "--target", "linux"},
		{"auth", "login", "builder"},
		{"auth", "logout"},
	} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			path, root, _ := workflowFixture(t, "timeout")
			content := filepath.Join(root, "build/linux")
			if err := os.MkdirAll(content, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"Game", "license.txt"} {
				if err := os.WriteFile(filepath.Join(content, name), []byte("artifact"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			var out, errOut bytes.Buffer
			args := append([]string{"--config", path, "--json", "--timeout", "500ms"}, command...)
			err := Execute(context.Background(), args, &out, &errOut)
			if !errors.Is(err, context.DeadlineExceeded) || ExitCode(err) != 124 || out.Len() != 0 {
				t.Fatalf("timeout returned success or wrong exit code: %v, %d, %s", err, ExitCode(err), out.String())
			}
		})
	}
}

func TestCLIRejectsNegativeTimeoutBeforeChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steam-release.yaml")
	var out, errOut bytes.Buffer
	err := Execute(context.Background(), []string{"--timeout=-1s", "init", "--config", path,
		"--app-id", "100", "--target", "linux,101,build/linux"}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "positive duration") {
		t.Fatalf("negative timeout accepted: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid timeout created a configuration: %v", err)
	}
}

func TestCLIExitCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{nil, 0}, {errors.New("upload failed"), 1},
		{errors.New("build cancelled by engine"), 1},
		{fmt.Errorf("wrapped: %w", context.Canceled), 130},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), 124},
	} {
		if code := ExitCode(tc.err); code != tc.code {
			t.Fatalf("ExitCode(%v) = %d, want %d", tc.err, code, tc.code)
		}
	}
}

func TestCLIUnattendedFailures(t *testing.T) {
	for _, tc := range []struct{ mode, message string }{
		{"build-failure", "failed"},
		{"auth-failure", "config/config.vdf"},
		{"timeout", "deadline exceeded"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			path, _, record := workflowFixture(t, tc.mode)
			var out, errOut bytes.Buffer
			limit := "30s"
			if tc.mode == "timeout" {
				limit = "500ms"
			}
			err := Execute(context.Background(), []string{"--config", path, "--json", "--timeout", limit, "ship", "--target", "linux"}, &out, &errOut)
			if err == nil || !strings.Contains(err.Error(), tc.message) || out.Len() != 0 {
				t.Fatalf("failure emitted success: error=%v, stdout=%s, stderr=%s", err, out.String(), errOut.String())
			}
			if tc.mode == "timeout" {
				if !errors.Is(err, context.DeadlineExceeded) || ExitCode(err) != 124 {
					t.Fatalf("incorrect timeout result: %v, exit=%d", err, ExitCode(err))
				}
			} else {
				commands := readWorkflowCommands(t, record)
				if tc.mode == "build-failure" && len(commands) != 1 {
					t.Fatalf("failed build started upload: %v", commands)
				}
				if ExitCode(err) != 1 {
					t.Fatalf("failure exit=%d", ExitCode(err))
				}
			}
		})
	}
}
