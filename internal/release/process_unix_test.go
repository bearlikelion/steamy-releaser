//go:build unix

package release

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandRunnerCancellationKillsDescendants(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err := (CommandRunner{}).Run(ctx, CommandPlan{
		Label:      "cancellation fixture",
		Program:    "/bin/sh",
		Args:       []string{"-c", `sleep 30 & echo $! > "$1"; wait`, "fixture", pidPath},
		WorkingDir: t.TempDir(),
	}, false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline cancellation, got %v", err)
	}
	contents, readErr := os.ReadFile(pidPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(contents)))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	for range 20 {
		if killErr := syscall.Kill(pid, 0); errors.Is(killErr, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant process %d survived cancellation", pid)
}
