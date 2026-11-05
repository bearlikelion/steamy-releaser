package steam

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thornity/steam-release-tool/internal/auth"
	"github.com/thornity/steam-release-tool/internal/release"
)

func TestInterleavedSteamHelper(t *testing.T) {
	if os.Getenv("STEAM_RELEASER_INTERLEAVED_HELPER") != "1" {
		return
	}
	input := bufio.NewReader(os.Stdin)
	_, _ = fmt.Fprint(os.Stdout, "ERROR")
	if _, err := input.ReadString('\n'); err != nil {
		os.Exit(2)
	}
	_, _ = fmt.Fprintln(os.Stderr, "progress")
	if _, err := input.ReadString('\n'); err != nil {
		os.Exit(3)
	}
	_, _ = fmt.Fprintln(os.Stdout, "! Permission denied")
	os.Exit(0)
}

type interleavedSteamRunner struct {
	runner     release.CommandRunner
	executable string
	result     release.RunResult
}

func (r *interleavedSteamRunner) Run(ctx context.Context, plan release.CommandPlan, interactive bool) (release.RunResult, error) {
	return r.runner.Run(ctx, plan, interactive)
}

func (r *interleavedSteamRunner) RunWithInput(ctx context.Context, plan release.CommandPlan, input io.Reader) (release.RunResult, error) {
	plan.Program = r.executable
	plan.Args = []string{"-test.run=^TestInterleavedSteamHelper$"}
	plan.Environment = map[string]string{"STEAM_RELEASER_INTERLEAVED_HELPER": "1"}
	result, err := r.runner.RunWithInput(ctx, plan, input)
	r.result = result
	return result, err
}

func TestPublisherRejectsInterleavedSteamError(t *testing.T) {
	publisher, _ := publisherFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, responses, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer responses.Close()
	success := false
	reporter := release.LockReporter(release.ReporterFunc(func(event release.Event) {
		switch event.Message {
		case "ERROR", "progress\n":
			_, _ = fmt.Fprintln(responses, "continue")
		}
		if event.Level == release.LevelSuccess {
			success = true
		}
	}))
	runner := &interleavedSteamRunner{runner: release.CommandRunner{Reporter: reporter}, executable: executable}
	publisher.Runner, publisher.Reporter = runner, reporter
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err = publisher.Upload(ctx, "", nil, UploadOptions{Username: "builder", Input: input})
	if !strings.Contains(runner.result.Output, "ERRORprogress\n! Permission denied") {
		t.Fatalf("fixture did not interleave the streams: %q", runner.result.Output)
	}
	if err == nil || success {
		t.Fatalf("reported a successful upload after SteamCMD's error: %v", err)
	}
	if _, err := auth.LoadUsername(); !os.IsNotExist(err) {
		t.Fatalf("cached a username after a failed upload: %v", err)
	}
}
