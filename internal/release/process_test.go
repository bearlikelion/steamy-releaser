package release

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Run the test executable as a portable child process without a shell or installed tools.
func TestCommandRunnerHelperProcess(t *testing.T) {
	mode := os.Getenv("STEAM_RELEASER_TEST_PROCESS")
	if mode == "" {
		return
	}
	if mode == "fail" {
		fmt.Fprintln(os.Stderr, "build failed")
		os.Exit(23)
	}
	if mode == "login" {
		fmt.Fprint(os.Stdout, "Password: ")
		response, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil || response != "secret-response\n" {
			os.Exit(25)
		}
		fmt.Fprintln(os.Stdout, "Logged in")
		return
	}
	if mode == "long-line" {
		fmt.Fprintln(os.Stdout, strings.Repeat("x", 2<<20))
		fmt.Fprintln(os.Stderr, "finished long output")
		os.Exit(0)
	}
	if mode == "over-limit" {
		fmt.Fprintln(os.Stdout, strings.Repeat("x", maxCapturedOutput+4096))
		fmt.Fprintln(os.Stderr, "diagnostic output")
		os.Exit(0)
	}
	directory, err := os.Getwd()
	if err != nil {
		os.Exit(24)
	}
	index := 0
	for i, arg := range os.Args {
		if arg == "--" {
			index = i + 1
			break
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"args": os.Args[index:], "directory": directory,
		"override": os.Getenv("STEAM_RELEASER_TEST_OVERRIDE"), "inherited": os.Getenv("STEAM_RELEASER_TEST_INHERITED"),
		"pwd": os.Getenv("PWD"), "literal": os.Getenv("STEAM_RELEASER_TEST_LITERAL"), "empty": os.Getenv("STEAM_RELEASER_TEST_EMPTY"),
	})
	fmt.Fprintln(os.Stderr, "compiler warning")
	os.Exit(0)
}

func TestCommandRunnerDrainsLongOutputLines(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := (CommandRunner{}).Run(ctx, CommandPlan{
		Label: "verbose builder", Program: executable,
		Args:        []string{"-test.run=^TestCommandRunnerHelperProcess$"},
		Environment: map[string]string{"STEAM_RELEASER_TEST_PROCESS": "long-line"},
	}, false)
	if err != nil {
		t.Fatalf("long line blocked the process: %v", err)
	}
	if strings.Count(result.Output, "x") != 2<<20 || !strings.Contains(result.Output, "finished long output") {
		t.Fatal("long process output was lost")
	}
	if strings.Count(result.Stdout, "x") != 2<<20 || result.Stderr != "finished long output\n" {
		t.Fatal("stdout and stderr were not captured separately")
	}
}

func TestCommandRunnerBoundsCombinedAndSeparateOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := (CommandRunner{}).Run(ctx, CommandPlan{
		Label: "verbose builder", Program: executable,
		Args:        []string{"-test.run=^TestCommandRunnerHelperProcess$"},
		Environment: map[string]string{"STEAM_RELEASER_TEST_PROCESS": "over-limit"},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OutputTruncated || len(result.Output) != maxCapturedOutput || len(result.Stdout)+len(result.Stderr) != len(result.Output) {
		t.Fatalf("unexpected capture sizes: combined=%d stdout=%d stderr=%d truncated=%v", len(result.Output), len(result.Stdout), len(result.Stderr), result.OutputTruncated)
	}
}

func TestCommandRunnerStreamsPromptsAndAcceptsInput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	runner := CommandRunner{Reporter: LockReporter(ReporterFunc(func(event Event) { events = append(events, event) }))}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runner.RunWithInput(ctx, CommandPlan{
		Label: "SteamCMD login", Program: executable,
		Args:        []string{"-test.run=^TestCommandRunnerHelperProcess$"},
		Environment: map[string]string{"STEAM_RELEASER_TEST_PROCESS": "login"},
	}, strings.NewReader("secret-response\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Password: ") || !strings.Contains(result.Output, "Logged in") {
		t.Fatalf("missing prompt or result: %q", result.Output)
	}
	foundPrompt := false
	for _, event := range events {
		if strings.Contains(event.Message, "secret-response") {
			t.Fatalf("input was reported as output: %#v", event)
		}
		if strings.Contains(event.Message, "Password: ") {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Fatalf("prompt was not reported: %#v", events)
	}
}

func TestInteractiveCommandRunnerShowsPromptBeforeResponse(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, response, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer response.Close()
	originalStdin := os.Stdin
	os.Stdin = input
	defer func() { os.Stdin = originalStdin }()
	runner := CommandRunner{Reporter: LockReporter(ReporterFunc(func(event Event) {
		if strings.Contains(event.Message, "Password: ") {
			_, _ = response.WriteString("secret-response\n")
		}
	}))}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, CommandPlan{
		Label: "SteamCMD login", Program: executable,
		Args:        []string{"-test.run=^TestCommandRunnerHelperProcess$"},
		Environment: map[string]string{"STEAM_RELEASER_TEST_PROCESS": "login"},
	}, true)
	if err != nil || !strings.Contains(result.Output, "Logged in") {
		t.Fatalf("interactive prompt was buffered while SteamCMD waited for input: %q, %v", result.Output, err)
	}
}

func TestCommandRunnerPreservesArgumentsEnvironmentAndOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	// Resolve macOS's /var -> /private/var alias before comparing the child's cwd.
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEAM_RELEASER_TEST_OVERRIDE", "parent")
	t.Setenv("STEAM_RELEASER_TEST_INHERITED", "inherited value")
	t.Setenv("PWD", "stale parent directory")
	t.Setenv("STEAM_RELEASER_TEST_EMPTY", "parent value")
	args := []string{"path with spaces", `literal"quote`, "", "$(not-a-command)", "semi;colon"}
	var events []Event
	runner := CommandRunner{Reporter: LockReporter(ReporterFunc(func(event Event) { events = append(events, event) }))}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, CommandPlan{
		Label: "builder", Program: executable,
		Args: append([]string{"-test.run=^TestCommandRunnerHelperProcess$", "--"}, args...), WorkingDir: directory,
		Environment: map[string]string{"STEAM_RELEASER_TEST_PROCESS": "inspect", "STEAM_RELEASER_TEST_OVERRIDE": "child", "STEAM_RELEASER_TEST_LITERAL": "$VALUE={output}", "STEAM_RELEASER_TEST_EMPTY": ""},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	foundOutput, foundWarning := false, false
	for _, event := range events {
		if event.Target != "builder" {
			t.Errorf("event lost command label: %#v", event)
		}
		if event.Level == LevelInfo && strings.HasPrefix(event.Message, "{") {
			var got struct {
				Args                           []string
				Directory, Override, Inherited string
				PWD, Literal, Empty            string
			}
			if err := json.Unmarshal([]byte(event.Message), &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Args, args) || got.Directory != directory || got.Override != "child" || got.Inherited != "inherited value" {
				t.Fatalf("child received wrong arguments or environment: %#v", got)
			}
			if got.Literal != "$VALUE={output}" || got.Empty != "" || runtime.GOOS != "windows" && got.PWD != directory {
				t.Fatalf("child environment was expanded or had wrong PWD: %#v", got)
			}
			foundOutput = true
		}
		if event.Level == LevelWarning && event.Message == "compiler warning" {
			foundWarning = true
		}
	}
	if !foundOutput || !foundWarning || !strings.Contains(result.Output, "compiler warning") || !strings.Contains(result.Output, "inherited value") {
		t.Fatalf("missing stdout/stderr: result=%q events=%#v", result.Output, events)
	}
}

func TestCommandRunnerReturnsProcessErrors(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := (CommandRunner{}).Run(ctx, CommandPlan{
		Label: "failing builder", Program: executable, Args: []string{"-test.run=^TestCommandRunnerHelperProcess$"},
		Environment: map[string]string{"STEAM_RELEASER_TEST_PROCESS": "fail"},
	}, false)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 || !strings.Contains(err.Error(), "failing builder") {
		t.Fatalf("process error lost exit status or label: %v", err)
	}
	if !strings.Contains(result.Output, "build failed") {
		t.Fatalf("failed process output was lost: %q", result.Output)
	}
	_, err = (CommandRunner{}).Run(ctx, CommandPlan{Label: "missing builder", Program: filepath.Join(t.TempDir(), "missing")}, false)
	if err == nil || !strings.Contains(err.Error(), "start missing builder") {
		t.Fatalf("start error = %v", err)
	}
}

func TestCapturedOutputIsBounded(t *testing.T) {
	var buffer cappedBuffer
	buffer.WriteString(strings.Repeat("a", maxCapturedOutput-2))
	if n, err := buffer.Write([]byte("bcde")); err != nil || n != 4 {
		t.Fatalf("truncating write = %d, %v", n, err)
	}
	buffer.WriteString("discarded")
	if err := buffer.WriteByte('\n'); err != nil {
		t.Fatal(err)
	}
	if buffer.Len() != maxCapturedOutput || !strings.HasSuffix(buffer.String(), "bc") || !buffer.truncated {
		t.Fatalf("capture did not preserve prefix up to limit: length=%d", buffer.Len())
	}
}
