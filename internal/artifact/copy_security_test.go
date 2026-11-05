package artifact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thornity/steam-release-tool/internal/config"
)

func TestCopyRejectsSourceChangedAfterInspection(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "source"), filepath.Join(root, "output")
	writeArtifact(t, source, "old bytes", 0o600)
	writeArtifact(t, destination, "keep this", 0o600)
	old := time.Unix(1, 0)
	if err := os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}
	project, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	guard := &CopyGuard{ctx: context.Background(), buffer: make([]byte, 128<<10)}
	expected, err := guard.signature(project, "source", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("new bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(context.Background(), project, project, "source", "output", &expected, guard.buffer); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("changed source was copied: %v", err)
	}
	if contents, err := os.ReadFile(destination); err != nil || string(contents) != "keep this" {
		t.Fatalf("destination changed after rejected copy: %q, %v", contents, err)
	}
}

func symlinkArtifact(t *testing.T, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestCopyRulesKeepSymlinksWithinRoots(t *testing.T) {
	for _, kind := range []string{"source file", "source directory", "destination file", "destination directory"} {
		t.Run(kind, func(t *testing.T) {
			document := artifactDocument(t, config.Profile{
				Targets:   map[string]config.Target{"linux": {Content: "build"}},
				PostBuild: []config.CopyRule{{From: "extras/license.txt", To: "support"}},
			})
			outside := filepath.Join(t.TempDir(), "private.txt")
			writeArtifact(t, outside, "private original", 0o600)
			writeArtifact(t, document.ResolveProject("extras/license.txt"), "license", 0o644)
			switch kind {
			case "source file":
				if err := os.Remove(document.ResolveProject("extras/license.txt")); err != nil {
					t.Fatal(err)
				}
				symlinkArtifact(t, outside, document.ResolveProject("extras/license.txt"))
			case "source directory":
				symlinkArtifact(t, filepath.Dir(outside), document.ResolveProject("private"))
				document.Config.Profiles["release"].PostBuild[0].From = "private/private.txt"
			case "destination file":
				symlinkArtifact(t, outside, document.ResolveProject("build/support/license.txt"))
			case "destination directory":
				symlinkArtifact(t, filepath.Dir(outside), document.ResolveProject("build/support"))
			}
			if err := ApplyCopyRules(document, "", nil, nil); err == nil {
				t.Fatal("copy followed a symlink outside its root")
			}
			got, err := os.ReadFile(outside)
			if err != nil || string(got) != "private original" {
				t.Fatalf("external file changed: %q, %v", got, err)
			}
			if kind == "destination directory" {
				if _, err := os.Stat(filepath.Join(filepath.Dir(outside), "license.txt")); !os.IsNotExist(err) {
					t.Fatalf("wrote outside content: %v", err)
				}
			}
		})
	}
}

func TestCopyRulesRejectSameFileWithoutTruncating(t *testing.T) {
	for _, hardLink := range []bool{false, true} {
		t.Run(map[bool]string{false: "same path", true: "hard link"}[hardLink], func(t *testing.T) {
			from := "build/license.txt"
			if hardLink {
				from = "extras/license.txt"
			}
			document := artifactDocument(t, config.Profile{
				Targets:   map[string]config.Target{"linux": {Content: "build"}},
				PostBuild: []config.CopyRule{{From: from}},
			})
			writeArtifact(t, document.ResolveProject(from), "original content", 0o644)
			if hardLink {
				if err := os.MkdirAll(document.ResolveProject("build"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(document.ResolveProject(from), document.ResolveProject("build/license.txt")); err != nil {
					t.Skipf("hard links unavailable: %v", err)
				}
			}
			if err := ApplyCopyRules(document, "", nil, nil); err == nil {
				t.Fatal("accepted copying a file onto itself")
			}
			got, err := os.ReadFile(document.ResolveProject(from))
			if err != nil || string(got) != "original content" {
				t.Fatalf("source was truncated: %q, %v", got, err)
			}
		})
	}
}

func TestGuardedCopyStillRejectsSameFile(t *testing.T) {
	document := artifactDocument(t, config.Profile{
		Targets:   map[string]config.Target{"linux": {Content: "build", Output: "build/license.txt"}},
		PostBuild: []config.CopyRule{{From: "build/license.txt"}},
	})
	path := document.ResolveProject("build/license.txt")
	writeArtifact(t, path, "original", 0o644)
	guard, err := SnapshotCopySources(context.Background(), document, "release", nil, []ProtectedCopyOutput{{Name: "linux", Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyCopyRulesWithGuard(document, "release", nil, nil, guard); err == nil || !strings.Contains(err.Error(), "onto itself") {
		t.Fatalf("guarded same-file copy was accepted: %v", err)
	}
	if contents, err := os.ReadFile(path); err != nil || string(contents) != "original" {
		t.Fatalf("same-file copy changed source: %q, %v", contents, err)
	}
}

func TestCopyRulesRejectDirectoryInsideItself(t *testing.T) {
	document := artifactDocument(t, config.Profile{
		Targets:   map[string]config.Target{"linux": {Content: "extras/build"}},
		PostBuild: []config.CopyRule{{From: "extras"}},
	})
	writeArtifact(t, document.ResolveProject("extras/license.txt"), "license", 0o644)
	if err := ApplyCopyRules(document, "", nil, nil); err == nil || !strings.Contains(err.Error(), "inside itself") {
		t.Fatalf("recursive copy error = %v", err)
	}
}

func TestCopyRulesPreserveBundleSymlinks(t *testing.T) {
	document := artifactDocument(t, config.Profile{
		Targets:   map[string]config.Target{"macos": {Content: "build"}},
		PostBuild: []config.CopyRule{{From: "extras/Runtime.framework"}},
	})
	source := document.ResolveProject("extras/Runtime.framework")
	writeArtifact(t, filepath.Join(source, "Versions/A/Runtime"), "runtime", 0o755)
	symlinkArtifact(t, "A", filepath.Join(source, "Versions/Current"))
	symlinkArtifact(t, "Versions/Current/Runtime", filepath.Join(source, "Runtime"))
	if err := ApplyCopyRules(document, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"Versions/Current": "A", "Runtime": "Versions/Current/Runtime"} {
		got, err := os.Readlink(document.ResolveProject("build/Runtime.framework/" + path))
		if err != nil || filepath.ToSlash(got) != want {
			t.Fatalf("bundle link %s = %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestCopyRuleDoesNotRemoveDirectoryToReplaceItWithLink(t *testing.T) {
	document := artifactDocument(t, config.Profile{
		Targets:   map[string]config.Target{"linux": {Content: "build"}},
		PostBuild: []config.CopyRule{{From: "extras/tree"}},
	})
	writeArtifact(t, document.ResolveProject("extras/tree/data"), "data", 0o600)
	symlinkArtifact(t, "data", document.ResolveProject("extras/tree/link"))
	destination := document.ResolveProject("build/tree/link")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ApplyCopyRules(document, "", nil, nil); err == nil {
		t.Fatal("copy removed an existing directory to replace it with a link")
	}
	if info, err := os.Stat(destination); err != nil || !info.IsDir() {
		t.Fatalf("existing directory changed: %v", err)
	}
}

func TestCopyRuleReplacesFileWithStagedLink(t *testing.T) {
	document := artifactDocument(t, config.Profile{
		Targets:   map[string]config.Target{"linux": {Content: "build"}},
		PostBuild: []config.CopyRule{{From: "extras/tree"}},
	})
	writeArtifact(t, document.ResolveProject("extras/tree/data"), "data", 0o600)
	symlinkArtifact(t, "data", document.ResolveProject("extras/tree/link"))
	destination := document.ResolveProject("build/tree/link")
	writeArtifact(t, destination, "old file", 0o600)
	if err := ApplyCopyRules(document, "", nil, nil); err != nil {
		t.Fatalf("could not replace file with staged link: %v", err)
	}
	if target, err := os.Readlink(destination); err != nil || target != "data" {
		t.Fatalf("incorrect replacement link: %q, %v", target, err)
	}
}

func TestValidateRejectsEscapingAndBrokenArtifactLinks(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "external", true: "broken"}[broken], func(t *testing.T) {
			document := artifactDocument(t, config.Profile{
				Targets: map[string]config.Target{"linux": {Output: "build/game", Content: "build"}},
			})
			writeArtifact(t, document.ResolveProject("build/game"), "game", 0o755)
			outside := filepath.Join(t.TempDir(), "secret")
			if !broken {
				writeArtifact(t, outside, "secret", 0o600)
			}
			symlinkArtifact(t, outside, document.ResolveProject("build/data"))
			if err := Validate(document, "", nil); err == nil {
				t.Fatal("accepted an unsafe artifact link")
			}
		})
	}
}
