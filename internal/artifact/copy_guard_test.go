package artifact

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestCopyGuardBoundsPathTracking(t *testing.T) {
	guard := &CopyGuard{ctx: context.Background(), seen: make(map[copySourceID]bool), seenBytes: maxCopyGuardPathBytes}
	if err := guard.captureFile(nil, "source", "source", "destination", false); err == nil || !strings.Contains(err.Error(), "too many post_build source paths") {
		t.Fatalf("unbounded source path tracking: %v", err)
	}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	guard.written = make(map[string]copyRecord)
	guard.writtenBytes = maxCopyGuardPathBytes
	if _, err := guard.beforeCopy(nil, "source", "source", root, "destination", false); err == nil || !strings.Contains(err.Error(), "too many post_build destination paths") {
		t.Fatalf("unbounded destination path tracking: %v", err)
	}
}
