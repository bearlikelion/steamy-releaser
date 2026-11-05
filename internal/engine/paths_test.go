package engine

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/thornity/steam-release-tool/internal/config"
)

func setTargetPaths(document *config.Document, linuxOutput, linuxContent, windowsOutput, windowsContent string) {
	profile := document.Config.Profiles["release"]
	linux := profile.Targets["linux"]
	linux.Output, linux.Content = linuxOutput, linuxContent
	profile.Targets["linux"] = linux
	windows := profile.Targets["windows"]
	windows.Output, windows.Content = windowsOutput, windowsContent
	profile.Targets["windows"] = windows
	document.Config.Profiles["release"] = profile
}

func TestPlanRejectsOverlappingBuildOutputs(t *testing.T) {
	for _, test := range []struct {
		name, linuxOutput, windowsOutput string
	}{
		{name: "same file", linuxOutput: "build/Game", windowsOutput: "build/Game"},
		{name: "nested output", linuxOutput: "build/Game.app", windowsOutput: "build/Game.app/Contents/Game"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := planDocument(t, config.Engine{Type: "custom", MaxParallel: 1})
			setTargetPaths(document, test.linuxOutput, "build/linux", test.windowsOutput, "build/windows")
			if _, err := Plan(document, "release", nil); err == nil || !strings.Contains(err.Error(), "overlapping build outputs") {
				t.Fatalf("overlapping outputs were accepted: %v", err)
			}
		})
	}
}

func TestPlanProtectsCaseInsensitiveOutputAliases(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("case-insensitive path comparison is platform-specific")
	}
	document := planDocument(t, config.Engine{Type: "custom", MaxParallel: 2})
	setTargetPaths(document, "build/Case/Game", "build/Case", "build/case/game", "build/case")
	if _, err := Plan(document, "release", nil); err == nil || !strings.Contains(err.Error(), "overlapping build outputs") {
		t.Fatalf("case-insensitive output aliases were accepted: %v", err)
	}
}

func TestPlanSerializesSharedContentButKeepsDisjointTargetsParallel(t *testing.T) {
	for _, test := range []struct {
		name, linuxContent, windowsContent string
		wantParallelism                    int
	}{
		{name: "shared content", linuxContent: "build", windowsContent: "build", wantParallelism: 1},
		{name: "nested content", linuxContent: "build", windowsContent: "build/windows", wantParallelism: 1},
		{name: "distinct content", linuxContent: "build/linux", windowsContent: "build/windows", wantParallelism: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := planDocument(t, config.Engine{Type: "custom", MaxParallel: 2})
			setTargetPaths(document, "build/linux/Game", test.linuxContent, "build/windows/Game.exe", test.windowsContent)
			plan, err := Plan(document, "release", nil)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Parallelism != test.wantParallelism {
				t.Fatalf("parallelism = %d, want %d", plan.Parallelism, test.wantParallelism)
			}
			if test.wantParallelism == 1 {
				for _, target := range plan.Targets {
					if target.Command == nil || len(target.Command.Warnings) == 0 {
						t.Fatalf("shared content did not produce a warning for %s", target.Name)
					}
				}
			}
		})
	}
}

func TestPlanRejectsOutputAliasesThroughSymlinks(t *testing.T) {
	document := planDocument(t, config.Engine{Type: "custom", MaxParallel: 2})
	root := document.ProjectRoot()
	if err := os.Mkdir(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("directory symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	setTargetPaths(document, "real/Game", "real", "alias/Game", "alias")
	if _, err := Plan(document, "release", nil); err == nil || !strings.Contains(err.Error(), "overlapping build outputs") {
		t.Fatalf("symlinked output aliases were accepted: %v", err)
	}
}

func TestPlanAllowsSharedPrebuiltOutput(t *testing.T) {
	document := planDocument(t, config.Engine{Type: "prebuilt"})
	setTargetPaths(document, "build/Game", "build", "build/Game", "build")
	if _, err := Plan(document, "release", nil); err != nil {
		t.Fatalf("shared prebuilt content was rejected: %v", err)
	}
}

func TestPlanDetectsHardLinkedOutputs(t *testing.T) {
	document := planDocument(t, config.Engine{Type: "custom", MaxParallel: 2})
	root := document.ProjectRoot()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	if err := os.WriteFile(first, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skipf("hard links unavailable: %v", err)
		}
		t.Fatal(err)
	}
	setTargetPaths(document, "first", "build/linux", "second", "build/windows")
	if _, err := Plan(document, "release", nil); err == nil || !strings.Contains(err.Error(), "overlapping build outputs") {
		t.Fatalf("hard-linked build outputs were accepted: %v", err)
	}
}

func TestPlanSerializesOutputInsideAnotherContentRoot(t *testing.T) {
	document := planDocument(t, config.Engine{Type: "custom", MaxParallel: 2})
	setTargetPaths(document, "release/linux", "staging/linux", "release/windows", "release")
	plan, err := Plan(document, "release", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Parallelism != 1 {
		t.Fatalf("a build output inside another target's content did not serialize: %d", plan.Parallelism)
	}
}

func TestPlanRejectsDanglingPathSymlinks(t *testing.T) {
	document := planDocument(t, config.Engine{Type: "custom", MaxParallel: 2})
	root := document.ProjectRoot()
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "alias")); err != nil {
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	setTargetPaths(document, "alias/Game", "alias", "build/windows/Game", "build/windows")
	if _, err := Plan(document, "release", nil); err == nil || !strings.Contains(err.Error(), "dangling symlink") {
		t.Fatalf("dangling output symlink was accepted: %v", err)
	}
}
