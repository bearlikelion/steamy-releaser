package history

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSourceHashDetectsNestedSubmoduleChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	inner, library, game := filepath.Join(root, "inner"), filepath.Join(root, "library"), filepath.Join(root, "game")
	git := func(directory string, args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", directory}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	initRepo := func(directory string) {
		t.Helper()
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		git(directory, "init", "--quiet")
	}
	commit := func(directory string) {
		t.Helper()
		git(directory, "add", ".")
		git(directory, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+t.TempDir(), "commit", "--quiet", "-m", "fixture")
	}
	initRepo(inner)
	if err := os.WriteFile(filepath.Join(inner, "asset.bin"), []byte{0, 'a'}, 0o600); err != nil {
		t.Fatal(err)
	}
	commit(inner)
	initRepo(library)
	git(library, "-c", "protocol.file.allow=always", "submodule", "add", "--quiet", inner, "nested dir")
	commit(library)
	initRepo(game)
	if err := os.Mkdir(filepath.Join(game, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(game, "project", "game.txt"), []byte("game"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(game, "-c", "protocol.file.allow=always", "submodule", "add", "--quiet", library, "assets/library with spaces")
	git(filepath.Join(game, "assets", "library with spaces"), "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive", "--quiet")
	commit(game)
	git(game, "config", "diff.ignoreSubmodules", "all")
	git(filepath.Join(game, "assets", "library with spaces"), "config", "diff.ignoreSubmodules", "all")

	project := filepath.Join(game, "project")
	configPath := filepath.Join(project, "release.yaml")
	if err := os.WriteFile(configPath, []byte("app_id: 100\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configHash, err := ConfigHash(configPath)
	if err != nil {
		t.Fatal(err)
	}
	commitID, _, _ := GitMetadata(project)
	asset := filepath.Join(game, "assets", "library with spaces", "nested dir", "asset.bin")
	if err := os.WriteFile(asset, []byte{0, 'b'}, 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := SourceHash(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset, []byte{0, 'c'}, 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := SourceHash(project)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different tracked contents in a nested submodule produced the same source fingerprint")
	}

	snapshot := RetrySnapshot{ConfigPath: configPath, ConfigHash: configHash, Root: project, Commit: commitID, SourceHash: first}
	if err := VerifyRetrySnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "source files changed") {
		t.Fatalf("retry accepted modified nested submodule contents: %v", err)
	}
	nested := filepath.Dir(asset)
	commit(nested)
	committed, err := SourceHash(project)
	if err != nil || committed == second {
		t.Fatalf("source fingerprint missed the submodule's new commit: %v", err)
	}
	libraryCheckout := filepath.Join(game, "assets", "library with spaces")
	git(libraryCheckout, "add", "nested dir")
	staged, err := SourceHash(project)
	if err != nil || staged == committed {
		t.Fatalf("source fingerprint missed the staged submodule revision: %v", err)
	}
	git(game, "submodule", "deinit", "--force", "--", "assets/library with spaces")
	if _, err := SourceHash(project); err == nil {
		t.Fatal("source fingerprint accepted an uninitialized submodule")
	}
}

func TestSourceHashUsesTheProjectsGitIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, "asset.txt"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "asset.txt")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+t.TempDir(), "commit", "--quiet", "-m", "fixture")
	wantCommit := git("rev-parse", "HEAD")
	indexPath := filepath.Join(root, ".git", "index")
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	before, err := SourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(root, "not-the-repository"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(root, "not-the-index"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.worktree")
	t.Setenv("GIT_CONFIG_VALUE_0", filepath.Join(root, "not-the-worktree"))
	after, err := SourceHash(root)
	if err != nil || before != after {
		t.Fatalf("environment redirected the source fingerprint: %q, %v", after, err)
	}
	commit, _, _ := GitMetadata(root)
	if commit != wantCommit {
		t.Fatalf("environment redirected Git metadata: %q, want %q", commit, wantCommit)
	}
	indexAfter, err := os.ReadFile(indexPath)
	if err != nil || !bytes.Equal(indexBefore, indexAfter) {
		t.Fatalf("source inspection changed the Git index: %v", err)
	}
}

func TestSourceGitlinksPreserveNewlinesInPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain newlines")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "--quiet")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+t.TempDir(), "commit", "--allow-empty", "--quiet", "-m", "fixture")
	oid := git("rev-parse", "HEAD")
	path := "nested\nmodule"
	git("update-index", "--add", "--cacheinfo", "160000,"+oid+","+path)
	directory, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	state := &sourceHashState{buffer: make([]byte, 128<<10)}
	links, err := state.sourceGitIndex(context.Background(), root, directory, sha256.New())
	if err != nil || len(links) != 1 || links[0].path != path || links[0].oid != oid {
		t.Fatalf("submodule paths = %#v, %v", links, err)
	}
}

func TestSourceHashDetectsChangesHiddenByGitIndexFlags(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		t.Run(flag, func(t *testing.T) {
			root := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				command := exec.Command("git", append([]string{"-C", root}, args...)...)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %s: %v", args, output, err)
				}
			}
			git("init", "--quiet")
			path := filepath.Join(root, "asset.txt")
			if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			git("add", "asset.txt")
			git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+t.TempDir(), "commit", "--quiet", "-m", "fixture")
			git("update-index", flag, "asset.txt")
			before, err := SourceHash(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
				t.Fatal(err)
			}
			after, err := SourceHash(root)
			if err != nil || before == after {
				t.Fatalf("source fingerprint missed a change hidden by %s: %v", flag, err)
			}
		})
	}
}

func TestSourceHashDetectsTrackedBytesWhenGitStatCacheDoesNot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	git("init", "--quiet")
	path := filepath.Join(root, "asset.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1577836800, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	git("add", "asset.txt")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+t.TempDir(), "commit", "--quiet", "-m", "fixture")
	git("config", "core.trustctime", "false")
	git("config", "core.checkStat", "minimal")
	before, err := SourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("modified"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	after, err := SourceHash(root)
	if err != nil || before == after {
		t.Fatalf("tracked contents changed without changing the source fingerprint: %v", err)
	}
}

func TestSourceHashTracksMissingFilesAndSymlinkTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require Windows privileges")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	git("init", "--quiet")
	file, link := filepath.Join(root, "asset.txt"), filepath.Join(root, "asset-link")
	if err := os.WriteFile(file, []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("asset.txt", link); err != nil {
		t.Fatal(err)
	}
	git("add", "asset.txt", "asset-link")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+t.TempDir(), "commit", "--quiet", "-m", "fixture")
	before, err := SourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	missing, err := SourceHash(root)
	if err != nil || before == missing {
		t.Fatalf("tracked file deletion was not detected: %v", err)
	}
	if err := os.WriteFile(file, []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := SourceHash(root)
	if err != nil || before != restored {
		t.Fatalf("restored tracked bytes did not restore the fingerprint: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.txt", link); err != nil {
		t.Fatal(err)
	}
	changed, err := SourceHash(root)
	if err != nil || before == changed {
		t.Fatalf("tracked symlink target change was not detected: %v", err)
	}
}

func TestSourceHashHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SourceHashContext(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("source hash error = %v, want cancellation", err)
	}
}

type cancelAfterFirstSourceRead struct {
	cancel context.CancelFunc
	reads  int
}

func (reader *cancelAfterFirstSourceRead) Read(data []byte) (int, error) {
	reader.reads++
	data[0] = 'x'
	reader.cancel()
	return 1, nil
}

func TestSourceHashStopsReadingAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &cancelAfterFirstSourceRead{cancel: cancel}
	_, _, err := hashSourceContents(ctx, reader, make([]byte, 8))
	if !errors.Is(err, context.Canceled) || reader.reads != 1 {
		t.Fatalf("source read cancellation = %v after %d reads", err, reader.reads)
	}
}

func TestSourceHashIncludesUntrackedFilesAndExcludesOwnedState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	t.Setenv("STEAM_RELEASER_CONFIG_DIR", filepath.Join(root, "settings"))
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	git("init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/ignored/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".gitignore")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+t.TempDir(), "commit", "--quiet", "-m", "fixture")
	for _, directory := range []string{"assets", "ignored", ".steam-releaser/generated", "settings"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	asset := filepath.Join(root, "assets", "new.bin")
	ignored := filepath.Join(root, "ignored", "cache.bin")
	generated := filepath.Join(root, ".steam-releaser", "generated", "app.vdf")
	database := filepath.Join(root, "settings", "steam-releaser.db")
	for _, path := range []string{asset, ignored, generated, database, database + "-wal", database + "-shm", database + "-journal"} {
		if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := SourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ignored, generated, database, database + "-wal", database + "-shm", database + "-journal"} {
		if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	afterOwned, err := SourceHash(root)
	if err != nil || before != afterOwned {
		t.Fatalf("ignored or application-owned files changed the source checkpoint: %v", err)
	}
	if err := os.WriteFile(asset, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterAsset, err := SourceHash(root)
	if err != nil || before == afterAsset {
		t.Fatalf("changed untracked source was not detected: %v", err)
	}
	if err := os.Remove(asset); err != nil {
		t.Fatal(err)
	}
	afterRemoval, err := SourceHash(root)
	if err != nil || afterRemoval == afterAsset {
		t.Fatalf("removed untracked source was not detected: %v", err)
	}
}

func TestSourceHashRejectsUnregisteredNestedRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	git := func(directory string, args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", directory}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	git(root, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, "game.txt"), []byte("game"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(root, "add", "game.txt")
	git(root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+t.TempDir(), "commit", "--quiet", "-m", "fixture")
	nested := filepath.Join(root, "unregistered")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	git(nested, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(nested, "asset.bin"), []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SourceHash(root); err == nil {
		t.Fatal("source fingerprint accepted an unregistered nested repository")
	}
}
