package artifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestFingerprintTracksContentAndExternalOutput(t *testing.T) {
	document := artifactDocument(t, config.Profile{Targets: map[string]config.Target{
		"linux":   {Output: "output/Game", Content: "content"},
		"windows": {Output: "output/Game", Content: "content"},
	}})
	output := document.ResolveProject("output/Game")
	asset := document.ResolveProject("content/data/asset")
	writeArtifact(t, output, "binary", 0o700)
	writeArtifact(t, asset, "asset", 0o600)
	fingerprint := func(targets []string) string {
		t.Helper()
		value, err := Fingerprint(context.Background(), document, "release", targets)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	initial := fingerprint([]string{"linux", "windows"})
	if initial != fingerprint([]string{"windows", "linux"}) || initial != fingerprint(nil) {
		t.Fatal("target order changed the fingerprint")
	}
	if err := os.WriteFile(asset, []byte("asset changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if initial == fingerprint(nil) {
		t.Fatal("changed content was not detected")
	}
	if err := os.WriteFile(asset, []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	if initial != fingerprint(nil) {
		t.Fatal("restored content did not restore the fingerprint")
	}
	if err := os.WriteFile(output, []byte("binary changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if initial == fingerprint(nil) {
		t.Fatal("changed output outside the content root was not detected")
	}
	if err := os.WriteFile(output, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeArtifact(t, document.ResolveProject("content/new-file"), "new", 0o600)
	if initial == fingerprint(nil) {
		t.Fatal("new content was not detected")
	}
}

func TestFingerprintTracksLinksAndRejectsEscapingLinks(t *testing.T) {
	document := artifactDocument(t, config.Profile{Targets: map[string]config.Target{
		"linux": {Output: "content/Game", Content: "content"},
	}})
	writeArtifact(t, document.ResolveProject("content/Game"), "binary", 0o700)
	writeArtifact(t, document.ResolveProject("content/first"), "same", 0o600)
	writeArtifact(t, document.ResolveProject("content/second"), "same", 0o600)
	link := document.ResolveProject("content/link")
	if err := os.Symlink("first", link); err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	initial, err := Fingerprint(context.Background(), document, "release", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("second", link); err != nil {
		t.Fatal(err)
	}
	changed, err := Fingerprint(context.Background(), document, "release", nil)
	if err != nil || changed == initial {
		t.Fatalf("link change was not detected: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	writeArtifact(t, document.ResolveProject("outside"), "private", 0o600)
	if err := os.Symlink(filepath.Join("..", "outside"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := Fingerprint(context.Background(), document, "release", nil); err == nil {
		t.Fatal("escaping symlink was accepted")
	}
}

func TestFingerprintTracksExecutableMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix executable bits")
	}
	document := artifactDocument(t, config.Profile{Targets: map[string]config.Target{
		"linux": {Output: "content/Game", Content: "content"},
	}})
	game := document.ResolveProject("content/Game")
	writeArtifact(t, game, "binary", 0o700)
	initial, err := Fingerprint(context.Background(), document, "release", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(game, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := Fingerprint(context.Background(), document, "release", nil)
	if err != nil || changed == initial {
		t.Fatalf("executable mode change was not detected: %v", err)
	}
}

func TestFingerprintHonorsCancellation(t *testing.T) {
	document := artifactDocument(t, config.Profile{Targets: map[string]config.Target{
		"linux": {Output: "content/Game", Content: "content"},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Fingerprint(ctx, document, "release", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled fingerprint = %v", err)
	}
}
