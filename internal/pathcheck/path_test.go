package pathcheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainsHandlesMissingPathsAndSiblingPrefixes(t *testing.T) {
	root := t.TempDir()
	resolve := func(path string) Path {
		t.Helper()
		resolved, err := Resolve(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	parent := resolve("build")
	child := resolve(filepath.Join("build", "new", "Game"))
	sibling := resolve(filepath.Join("build-other", "Game"))
	if !Contains(parent, child) || Contains(child, parent) || !Overlap(parent, child) {
		t.Fatalf("missing descendant not recognized: parent=%q child=%q", parent.Path, child.Path)
	}
	if Overlap(parent, sibling) {
		t.Fatalf("sibling path was mistaken for a descendant: %q, %q", parent.Path, sibling.Path)
	}
}

func TestContainsResolvesSymlinkBeforeMissingDescendants(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "content"), filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	content, err := Resolve(filepath.Join(root, "content"))
	if err != nil {
		t.Fatal(err)
	}
	aliased, err := Resolve(filepath.Join(root, "alias", "new", "Game"))
	if err != nil {
		t.Fatal(err)
	}
	if !Contains(content, aliased) {
		t.Fatalf("symlinked descendant was missed: %q, %q", content.Path, aliased.Path)
	}
}

func TestOverlapDetectsHardLinkedFiles(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	if err := os.WriteFile(first, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	a, err := Resolve(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Resolve(second)
	if err != nil {
		t.Fatal(err)
	}
	if !Overlap(a, b) {
		t.Fatalf("hard-linked files were not recognized as aliases: %q, %q", a.Path, b.Path)
	}
}
