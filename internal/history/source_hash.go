package history

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/thornity/steam-release-tool/internal/localstore"
	"github.com/thornity/steam-release-tool/internal/safefile"
)

const maxSourceSubmodules = 4096
const maxSourceSubmoduleDepth = 32
const maxUntrackedFiles = 100000
const maxUntrackedPathBytes = 64 << 20

// SourceHash binds tracked and non-ignored untracked working-tree bytes and
// registered Git submodules to a retry checkpoint. Ignored inputs remain out.
func SourceHash(root string) (string, error) {
	return SourceHashContext(context.Background(), root)
}

// SourceHashContext stops reading source files when the operation is canceled.
func SourceHashContext(parent context.Context, root string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	project, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		return "", err
	}
	state := &sourceHashState{buffer: make([]byte, 128<<10), internal: filepath.Join(project, ".steam-releaser")}
	if directory, err := localstore.Directory(); err == nil {
		if directory, err = filepath.Abs(directory); err == nil {
			if resolved, resolveErr := filepath.EvalSymlinks(directory); resolveErr == nil {
				directory = resolved
			}
			state.database = filepath.Join(directory, localstore.Filename)
		}
	}
	digest, err := state.repository(ctx, root, false, 0)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest[:]), nil
}

type sourceHashState struct {
	submodules int
	buffer     []byte
	internal   string
	database   string
}

type sourceGitlink struct {
	path string
	oid  string
}

func (state *sourceHashState) repository(ctx context.Context, directory string, submodule bool, depth int) ([sha256.Size]byte, error) {
	var empty [sha256.Size]byte
	if depth > maxSourceSubmoduleDepth {
		return empty, fmt.Errorf("submodule nesting exceeds the source fingerprint limit")
	}
	repoRoot, err := sourceGitOutput(ctx, directory, "rev-parse", "--show-toplevel")
	if err != nil {
		return empty, fmt.Errorf("find Git repository for %s: %w", directory, err)
	}
	repoRoot = filepath.FromSlash(repoRoot)
	if !filepath.IsAbs(repoRoot) {
		return empty, fmt.Errorf("Git returned a non-absolute repository path")
	}
	projectPath, err := filepath.Abs(directory)
	if err != nil {
		return empty, err
	}
	projectPath, err = filepath.EvalSymlinks(projectPath)
	if err != nil {
		return empty, fmt.Errorf("resolve project path %s: %w", directory, err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return empty, fmt.Errorf("resolve Git repository %s: %w", repoRoot, err)
	}
	relative, err := filepath.Rel(resolvedRoot, projectPath)
	if err != nil || (relative != "." && !filepath.IsLocal(relative)) {
		return empty, fmt.Errorf("Git repository does not contain project path %s", directory)
	}
	if submodule {
		wanted, err := os.Lstat(directory)
		if err != nil {
			return empty, fmt.Errorf("inspect submodule %s: %w", directory, err)
		}
		if !wanted.IsDir() || wanted.Mode()&fs.ModeSymlink != 0 {
			return empty, fmt.Errorf("submodule %s is not an initialized directory", directory)
		}
		actual, err := os.Stat(repoRoot)
		if err != nil || !os.SameFile(wanted, actual) {
			return empty, fmt.Errorf("submodule %s is not an initialized Git repository", directory)
		}
	}
	commit, err := sourceGitOutput(ctx, repoRoot, "rev-parse", "--verify", "HEAD")
	if err != nil || !sourceGitOID(commit) {
		return empty, fmt.Errorf("cannot identify Git commit for %s", repoRoot)
	}
	root, err := os.OpenRoot(repoRoot)
	if err != nil {
		return empty, fmt.Errorf("open Git repository %s: %w", repoRoot, err)
	}
	defer root.Close()
	digest := sha256.New()
	writeSourceField(digest, []byte("steam-releaser-source-v4"))
	writeSourceField(digest, []byte(commit))
	links, err := state.sourceGitIndex(ctx, repoRoot, root, digest)
	if err != nil {
		return empty, err
	}
	if err := state.hashUntracked(ctx, repoRoot, resolvedRoot, root, digest); err != nil {
		return empty, err
	}
	writeSourceField(digest, []byte(fmt.Sprint(len(links))))
	for _, link := range links {
		state.submodules++
		if state.submodules > maxSourceSubmodules {
			return empty, fmt.Errorf("too many submodules to fingerprint")
		}
		child := filepath.Join(repoRoot, filepath.FromSlash(link.path))
		resolvedChild, err := filepath.EvalSymlinks(child)
		if err != nil {
			return empty, fmt.Errorf("resolve submodule %s: %w", child, err)
		}
		relative, err := filepath.Rel(resolvedRoot, resolvedChild)
		if err != nil || !filepath.IsLocal(relative) {
			return empty, fmt.Errorf("submodule %s is outside its Git repository", child)
		}
		childDigest, err := state.repository(ctx, child, true, depth+1)
		if err != nil {
			return empty, err
		}
		writeSourceField(digest, []byte(link.path))
		writeSourceField(digest, []byte(link.oid))
		writeSourceField(digest, childDigest[:])
	}
	copy(empty[:], digest.Sum(nil))
	return empty, nil
}

func writeSourceField(digest hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = digest.Write(value)
}

func sourceGitOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

// Git repository-selection variables inherited from another Git operation
// would otherwise make -C inspect a different repository or index.
func sourceGitCommand(ctx context.Context, directory string, args ...string) *exec.Cmd {
	arguments := append([]string{"--no-optional-locks", "-C", directory, "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false"}, args...)
	command := exec.CommandContext(ctx, "git", arguments...)
	environment := os.Environ()
	command.Env = make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "GIT_CONFIG_KEY_") || strings.HasPrefix(upper, "GIT_CONFIG_VALUE_") {
			continue
		}
		switch upper {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_PREFIX", "GIT_NAMESPACE", "GIT_CEILING_DIRECTORIES", "GIT_SHALLOW_FILE", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_ATTR_SOURCE", "GIT_EXTERNAL_DIFF", "GIT_DIFF_OPTS":
			continue
		}
		command.Env = append(command.Env, entry)
	}
	return command
}

type boundedGitOutput struct {
	bytes.Buffer
}

func (output *boundedGitOutput) Write(data []byte) (int, error) {
	if len(data) > 32768-output.Len() {
		return 0, fmt.Errorf("Git output exceeds the size limit")
	}
	return output.Buffer.Write(data)
}

func sourceGitOutput(ctx context.Context, directory string, args ...string) (string, error) {
	command := sourceGitCommand(ctx, directory, args...)
	var output boundedGitOutput
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return "", err
	}
	value := strings.TrimSuffix(output.String(), "\n")
	if runtime.GOOS == "windows" {
		value = strings.TrimSuffix(value, "\r")
	}
	return value, nil
}

func (state *sourceHashState) sourceGitIndex(ctx context.Context, directory string, root *os.Root, digest hash.Hash) ([]sourceGitlink, error) {
	command := sourceGitCommand(ctx, directory, "ls-files", "--stage", "--full-name", "-v", "-z")
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	scanner.Split(splitNULTerminated)
	var links []sourceGitlink
	var parseErr error
	var count uint64
	for scanner.Scan() {
		entry := scanner.Bytes()
		if len(entry) < 3 || entry[1] != ' ' {
			parseErr = fmt.Errorf("cannot inspect an invalid Git index entry")
			break
		}
		status := entry[0]
		if status == 'M' {
			parseErr = fmt.Errorf("Git index contains unmerged entries")
			break
		}
		entry = entry[2:]
		header, path, ok := bytes.Cut(entry, []byte{'\t'})
		fields := strings.Fields(string(header))
		if !ok || len(fields) != 3 || fields[2] != "0" || !sourceGitOID(fields[1]) || !filepath.IsLocal(filepath.FromSlash(string(path))) {
			parseErr = fmt.Errorf("cannot inspect an invalid or conflicted Git index entry")
			break
		}
		writeSourceField(digest, []byte("index-entry"))
		writeSourceField(digest, path)
		writeSourceField(digest, []byte(fields[0]))
		writeSourceField(digest, []byte(fields[1]))
		writeSourceField(digest, []byte{status})
		count++
		switch fields[0] {
		case "160000":
			links = append(links, sourceGitlink{path: string(path), oid: fields[1]})
			if len(links) > maxSourceSubmodules {
				parseErr = fmt.Errorf("too many submodules to fingerprint")
			}
		case "100644", "100755", "120000":
			parseErr = state.hashWorkingFile(ctx, root, filepath.FromSlash(string(path)), digest)
		default:
			parseErr = fmt.Errorf("unsupported tracked Git index mode %s", fields[0])
		}
		if parseErr != nil {
			break
		}
	}
	if parseErr == nil {
		parseErr = scanner.Err()
	}
	if parseErr != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if parseErr != nil {
		return nil, fmt.Errorf("inspect tracked source in %s: %w", directory, parseErr)
	}
	if waitErr != nil {
		return nil, fmt.Errorf("list tracked files in %s: %w", directory, waitErr)
	}
	writeSourceField(digest, []byte("end-index"))
	var countBytes [8]byte
	binary.BigEndian.PutUint64(countBytes[:], count)
	writeSourceField(digest, countBytes[:])
	sort.Slice(links, func(i, j int) bool { return links[i].path < links[j].path })
	return links, nil
}

func (state *sourceHashState) hashUntracked(ctx context.Context, directory, resolvedRoot string, root *os.Root, digest hash.Hash) error {
	command := sourceGitCommand(ctx, directory, "ls-files", "--others", "--exclude-standard", "--full-name", "-z")
	output, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	scanner.Split(splitNULTerminated)
	var paths []string
	var pathBytes int
	var parseErr error
	for scanner.Scan() {
		path := string(scanner.Bytes())
		local := filepath.FromSlash(path)
		if !filepath.IsLocal(local) {
			parseErr = fmt.Errorf("cannot inspect an unsafe untracked path")
			break
		}
		if state.ownedPath(filepath.Join(resolvedRoot, local)) {
			continue
		}
		pathBytes += len(path)
		if len(paths) >= maxUntrackedFiles || pathBytes > maxUntrackedPathBytes {
			parseErr = fmt.Errorf("too many untracked paths to fingerprint")
			break
		}
		paths = append(paths, path)
	}
	if parseErr == nil {
		parseErr = scanner.Err()
	}
	if parseErr != nil {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if parseErr != nil {
		return fmt.Errorf("inspect untracked source in %s: %w", directory, parseErr)
	}
	if waitErr != nil {
		return fmt.Errorf("list untracked files in %s: %w", directory, waitErr)
	}
	sort.Strings(paths)
	for _, path := range paths {
		writeSourceField(digest, []byte("untracked-entry"))
		writeSourceField(digest, []byte(path))
		if err := state.hashWorkingFile(ctx, root, filepath.FromSlash(path), digest); err != nil {
			return fmt.Errorf("inspect untracked source in %s: %w", directory, err)
		}
	}
	writeSourceField(digest, []byte("end-untracked"))
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(paths)))
	writeSourceField(digest, count[:])
	return nil
}

// Exclude only Steam Releaser's own generated files, not arbitrary build paths.
func (state *sourceHashState) ownedPath(path string) bool {
	if state.internal != "" {
		if relative, err := filepath.Rel(state.internal, path); err == nil && (relative == "." || filepath.IsLocal(relative)) {
			return true
		}
	}
	if state.database == "" {
		return false
	}
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		expected := state.database + suffix
		if path == expected || runtime.GOOS == "windows" && strings.EqualFold(path, expected) {
			return true
		}
	}
	return false
}

func (state *sourceHashState) hashWorkingFile(ctx context.Context, root *os.Root, path string, digest hash.Hash) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		writeSourceField(digest, []byte("missing"))
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect source file %s: %w", path, err)
	}
	writeSourceField(digest, []byte(info.Mode().String()))
	if info.Mode()&fs.ModeSymlink != 0 {
		link, err := root.Readlink(path)
		if err != nil {
			return err
		}
		after, err := root.Lstat(path)
		if err != nil || !sameSourceFile(info, after) {
			return fmt.Errorf("source link changed while inspecting %s", path)
		}
		writeSourceField(digest, []byte(link))
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source path %s is not a regular file or symlink", path)
	}
	file, err := safefile.OpenCandidate(root, path)
	if err != nil {
		return err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !sameSourceFile(info, before) {
		return fmt.Errorf("source file changed before reading %s", path)
	}
	content, read, err := hashSourceContents(ctx, file, state.buffer)
	if err != nil {
		return err
	}
	after, err := file.Stat()
	if err != nil || read != before.Size() || !sameSourceFile(before, after) {
		return fmt.Errorf("source file changed while reading %s", path)
	}
	current, err := root.Lstat(path)
	if err != nil || !sameSourceFile(after, current) {
		return fmt.Errorf("source file changed after reading %s", path)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close source file %s: %w", path, err)
	}
	writeSourceField(digest, content[:])
	return nil
}

func hashSourceContents(ctx context.Context, reader io.Reader, buffer []byte) ([sha256.Size]byte, int64, error) {
	var result [sha256.Size]byte
	content := sha256.New()
	var read int64
	for {
		if err := ctx.Err(); err != nil {
			return result, read, err
		}
		n, readErr := reader.Read(buffer)
		if n > 0 {
			_, _ = content.Write(buffer[:n])
			read += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, read, readErr
		}
		if n == 0 {
			return result, read, io.ErrNoProgress
		}
	}
	copy(result[:], content.Sum(nil))
	return result, read, nil
}

func sameSourceFile(first, second fs.FileInfo) bool {
	return first != nil && second != nil && os.SameFile(first, second) &&
		first.Mode() == second.Mode() && first.Size() == second.Size() &&
		first.ModTime().Equal(second.ModTime())
}

func splitNULTerminated(data []byte, atEOF bool) (int, []byte, error) {
	if index := bytes.IndexByte(data, 0); index >= 0 {
		return index + 1, data[:index], nil
	}
	if atEOF && len(data) != 0 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	return 0, nil, nil
}
