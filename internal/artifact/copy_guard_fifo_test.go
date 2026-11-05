//go:build linux || darwin

package artifact

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestCopyGuardRejectsNamedPipeWithoutBlocking(t *testing.T) {
	document := artifactDocument(t, config.Profile{
		Targets:   map[string]config.Target{"linux": {Content: "build", Output: "build/pipe"}},
		PostBuild: []config.CopyRule{{From: "pipe"}},
	})
	pipe := document.ResolveProject("pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skipf("named pipes unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := SnapshotCopySources(ctx, document, "release", nil, []ProtectedCopyOutput{{Name: "linux", Path: document.ResolveProject("build/pipe"), FreshnessEvidence: true}})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("named pipe was not rejected: %v", err)
		}
	case <-time.After(5 * time.Second):
		// Unblock an unsafe FIFO open before failing the regression test.
		go func() {
			writer, err := os.OpenFile(pipe, os.O_WRONLY, 0)
			if err == nil {
				_ = writer.Close()
			}
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("named pipe blocked source inspection")
	}
}
