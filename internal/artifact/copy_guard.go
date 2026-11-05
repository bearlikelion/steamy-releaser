package artifact

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/safefile"
)

// ProtectedCopyOutput describes an output or content tree produced by this
// build, or one verified before being reused by a retry.
type ProtectedCopyOutput struct {
	Name              string
	Path              string
	Reused            bool
	FreshnessEvidence bool
}

type copySourceID struct {
	path string
	link bool
}

type copySignature struct {
	exists bool
	kind   byte
	mode   fs.FileMode
	digest [sha256.Size]byte
}

func (signature copySignature) sameContent(other copySignature) bool {
	return signature.exists && other.exists && signature.kind == other.kind && signature.digest == other.digest
}

func (signature copySignature) sameRepresentation(other copySignature) bool {
	return signature.sameContent(other) && (signature.kind != 'f' || signature.mode == other.mode)
}

type copyRecord struct {
	known bool
	fresh bool
	sig   copySignature
}

const maxCopyGuardEntries = 1000000
const maxCopyGuardPathBytes = 128 << 20

// CopyGuard prevents an unchanged post-build source from replacing different
// build content. Its observations do not prove complete build provenance.
type CopyGuard struct {
	ctx                     context.Context
	project                 string
	outputs                 []ProtectedCopyOutput
	baseline                map[copySourceID]copySignature
	seen                    map[copySourceID]bool
	written                 map[string]copyRecord
	links                   map[string]bool
	pending                 map[string]bool
	fresh                   map[string]bool
	buffer                  []byte
	seenBytes, writtenBytes int
}

// SnapshotCopySources records bytes of existing post-build sources that can
// overwrite protected content. Missing sources may be created by the build.
func SnapshotCopySources(ctx context.Context, document *config.Document, profileName string, selected []string, outputs []ProtectedCopyOutput) (*CopyGuard, error) {
	guard := &CopyGuard{ctx: ctx, project: document.ProjectRoot(), outputs: outputs,
		baseline: make(map[copySourceID]copySignature), seen: make(map[copySourceID]bool), written: make(map[string]copyRecord), links: make(map[string]bool),
		pending: make(map[string]bool), fresh: make(map[string]bool), buffer: make([]byte, 128<<10)}
	selection, err := document.Select(profileName, selected)
	if err != nil {
		return nil, err
	}
	if len(outputs) == 0 || len(selection.Profile.PostBuild) == 0 {
		return nil, nil
	}
	project, err := os.OpenRoot(document.ProjectRoot())
	if err != nil {
		return nil, fmt.Errorf("open project for post-build source checks: %w", err)
	}
	defer project.Close()
	selectedSet := make(map[string]bool, len(selection.TargetNames))
	for _, name := range selection.TargetNames {
		selectedSet[name] = true
	}
	for _, rule := range selection.Profile.PostBuild {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !filepath.IsLocal(rule.From) {
			return nil, fmt.Errorf("post_build source must stay inside the project root: %q", rule.From)
		}
		to := rule.To
		if to == "" {
			to = "."
		}
		if !filepath.IsLocal(to) {
			return nil, fmt.Errorf("post_build destination must be relative and cannot escape content: %q", to)
		}
		targets := rule.Targets
		if len(targets) == 0 {
			targets = selection.TargetNames
		}
		matches, err := doublestar.Glob(project.FS(), filepath.ToSlash(rule.From), doublestar.WithFailOnIOErrors())
		if err != nil {
			return nil, fmt.Errorf("invalid post_build glob %q: %w", rule.From, err)
		}
		for _, targetName := range targets {
			if !selectedSet[targetName] {
				continue
			}
			content := document.ResolveProject(selection.Profile.Targets[targetName].Content)
			for _, match := range matches {
				source := filepath.FromSlash(match)
				destination := filepath.Join(content, to, filepath.Base(source))
				if err := guard.captureSource(project, source, destination); err != nil {
					return nil, fmt.Errorf("inspect post_build source %s: %w", match, err)
				}
			}
		}
	}
	return guard, nil
}

func (guard *CopyGuard) captureSource(project *os.Root, source, destination string) error {
	info, err := project.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return guard.captureFile(project, source, source, destination, false)
	}
	original, err := filepath.EvalSymlinks(filepath.Join(project.Name(), source))
	if err != nil {
		return err
	}
	candidate, err := resolveDestination(destination)
	if err != nil {
		return err
	}
	if relative, err := filepath.Rel(original, candidate); err == nil && filepath.IsLocal(relative) {
		return fmt.Errorf("cannot copy a directory inside itself")
	}
	input, err := project.OpenRoot(source)
	if err != nil {
		return err
	}
	defer input.Close()
	return fs.WalkDir(input.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := guard.ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		local := filepath.FromSlash(name)
		return guard.captureFile(input, local, filepath.Join(source, local), filepath.Join(destination, local), entry.Type()&fs.ModeSymlink != 0)
	})
}

func (guard *CopyGuard) captureFile(root *os.Root, source, logicalSource, destination string, link bool) error {
	id := copySourceID{path: copyPathKey(logicalSource), link: link}
	if !guard.seen[id] {
		if len(guard.seen) >= maxCopyGuardEntries || guard.seenBytes+len(id.path) > maxCopyGuardPathBytes {
			return fmt.Errorf("too many post_build source paths to verify")
		}
		guard.seen[id] = true
		guard.seenBytes += len(id.path)
	}
	affected, err := guard.affected(destination)
	if err != nil || len(affected) == 0 {
		return err
	}
	if _, exists := guard.baseline[id]; exists {
		return nil
	}
	signature, err := guard.signature(root, source, link, false)
	if err != nil {
		return err
	}
	guard.baseline[id] = signature
	return nil
}

// SetPending identifies outputs for which the build command did not itself
// produce an observable change. Only a fresh copy can establish progress.
func (guard *CopyGuard) SetPending(names []string) {
	for _, name := range names {
		guard.pending[name] = true
	}
}

func (guard *CopyGuard) Fresh(name string) bool { return guard != nil && guard.fresh[name] }

type copyDecision struct {
	entry    string
	sig      copySignature
	known    bool
	fresh    bool
	skip     bool
	link     bool
	affected []int
}

func (guard *CopyGuard) beforeCopy(sourceRoot *os.Root, source, logicalSource string, destinationRoot *os.Root, destination string, link bool) (copyDecision, error) {
	if err := guard.ctx.Err(); err != nil {
		return copyDecision{}, err
	}
	fullDestination := filepath.Join(destinationRoot.Name(), destination)
	entry, err := copyEntryPath(fullDestination)
	if err != nil {
		return copyDecision{}, err
	}
	affected, err := guard.affected(fullDestination)
	if err != nil {
		return copyDecision{}, err
	}
	decision := copyDecision{entry: copyPathKey(entry), link: link, affected: affected}
	if _, exists := guard.written[decision.entry]; !exists && (len(guard.written) >= maxCopyGuardEntries || guard.writtenBytes+len(decision.entry) > maxCopyGuardPathBytes) {
		return copyDecision{}, fmt.Errorf("too many post_build destination paths to verify")
	}
	if len(affected) == 0 {
		return decision, nil
	}
	signature, err := guard.signature(sourceRoot, source, link, false)
	if err != nil {
		return copyDecision{}, err
	}
	decision.sig, decision.known = signature, true
	previous, previouslyWritten, err := guard.sourceRecord(logicalSource, link)
	if err != nil {
		return copyDecision{}, err
	}
	if previouslyWritten {
		decision.fresh = previous.known && previous.fresh && previous.sig.sameContent(signature)
	} else {
		id := copySourceID{path: copyPathKey(logicalSource), link: link}
		baseline, captured := guard.baseline[id]
		decision.fresh = captured && !baseline.sameContent(signature) || !guard.seen[id]
	}
	current, err := guard.signature(destinationRoot, destination, link, true)
	if err != nil {
		return copyDecision{}, err
	}
	same := signature.sameRepresentation(current)
	for _, index := range affected {
		output := guard.outputs[index]
		if output.Reused && !same {
			return copyDecision{}, fmt.Errorf("post_build copy would change reused output %s; start a new release", output.Name)
		}
		if !decision.fresh && !same {
			exact, err := copyPathsEqual(fullDestination, output.Path)
			if err != nil {
				return copyDecision{}, err
			}
			// A static new asset may be added to a directory that the build
			// already changed, but it cannot establish output freshness or
			// overwrite existing output contents.
			if current.exists || output.FreshnessEvidence && (guard.pending[output.Name] || exact) {
				return copyDecision{}, fmt.Errorf("post_build source for output %s could not be verified as created or changed by this build; rebuild or review the artifact before uploading", output.Name)
			}
		}
	}
	decision.skip = same
	return decision, nil
}

func (guard *CopyGuard) afterCopy(decision copyDecision) {
	if _, exists := guard.written[decision.entry]; !exists {
		guard.writtenBytes += len(decision.entry)
	}
	guard.written[decision.entry] = copyRecord{known: decision.known, fresh: decision.fresh, sig: decision.sig}
	if decision.link && !decision.skip {
		guard.links[decision.entry] = true
	}
	if decision.fresh {
		for _, index := range decision.affected {
			if guard.outputs[index].FreshnessEvidence {
				guard.fresh[guard.outputs[index].Name] = true
			}
		}
	}
}

func (guard *CopyGuard) sourceRecord(source string, link bool) (copyRecord, bool, error) {
	full := filepath.Join(guard.project, source)
	if len(guard.links) > 0 {
		for parent := filepath.Dir(full); ; parent = filepath.Dir(parent) {
			parentEntry, err := copyEntryPath(parent)
			if err != nil {
				return copyRecord{}, false, err
			}
			if guard.links[copyPathKey(parentEntry)] {
				return copyRecord{}, true, nil
			}
			if next := filepath.Dir(parent); next == parent {
				break
			}
		}
	}
	entry, err := copyEntryPath(full)
	if err != nil {
		return copyRecord{}, false, err
	}
	entry = copyPathKey(entry)
	if record, ok := guard.written[entry]; ok {
		return record, true, nil
	}
	if !link {
		resolved, err := resolveDestination(full)
		if err != nil {
			return copyRecord{}, false, err
		}
		if record, ok := guard.written[copyPathKey(resolved)]; ok {
			return record, true, nil
		}
	}
	return copyRecord{}, false, nil
}

func (guard *CopyGuard) affected(destination string) ([]int, error) {
	var affected []int
	for index, output := range guard.outputs {
		overlap, err := copyPathsOverlap(destination, output.Path)
		if err != nil {
			return nil, err
		}
		if overlap {
			affected = append(affected, index)
		}
	}
	return affected, nil
}

func copyPathsEqual(first, second string) (bool, error) {
	a, err := copyEntryPath(first)
	if err != nil {
		return false, err
	}
	b, err := copyEntryPath(second)
	if err != nil {
		return false, err
	}
	return copyPathKey(a) == copyPathKey(b), nil
}

func copyPathsOverlap(first, second string) (bool, error) {
	firstKey, secondKey := copyPathKey(first), copyPathKey(second)
	if copyPathContains(firstKey, secondKey) || copyPathContains(secondKey, firstKey) {
		return true, nil
	}
	a, err := copyEntryPath(first)
	if err != nil {
		return false, err
	}
	b, err := copyEntryPath(second)
	if err != nil {
		return false, err
	}
	resolvedA, err := resolveDestination(first)
	if err != nil {
		return false, err
	}
	resolvedB, err := resolveDestination(second)
	if err != nil {
		return false, err
	}
	for _, left := range []string{a, resolvedA} {
		for _, right := range []string{b, resolvedB} {
			if copyPathContains(copyPathKey(left), copyPathKey(right)) || copyPathContains(copyPathKey(right), copyPathKey(left)) {
				return true, nil
			}
		}
	}
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo), nil
}

func copyEntryPath(name string) (string, error) {
	parent, err := resolveDestination(filepath.Dir(name))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(name)), nil
}

func copyPathKey(name string) string {
	name = filepath.Clean(name)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		name = strings.ToLower(name)
	}
	return name
}

func copyPathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func (guard *CopyGuard) signature(root *os.Root, name string, preserveLink, allowMissing bool) (copySignature, error) {
	var empty copySignature
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) && allowMissing {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	if preserveLink || allowMissing && info.Mode()&fs.ModeSymlink != 0 {
		if info.Mode()&fs.ModeSymlink == 0 {
			return copySignature{exists: true, kind: 'd'}, nil
		}
		link, err := root.Readlink(name)
		if err != nil {
			return empty, err
		}
		return copySignature{exists: true, kind: 'l', digest: sha256.Sum256([]byte(link))}, nil
	}
	if allowMissing && !info.Mode().IsRegular() {
		return copySignature{exists: true, kind: 'd'}, nil
	}
	current, err := root.Stat(name)
	if err != nil {
		return empty, err
	}
	if !current.Mode().IsRegular() {
		return empty, fmt.Errorf("post_build source is not a regular file: %s", name)
	}
	file, err := safefile.OpenCandidate(root, name)
	if err != nil {
		return empty, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return empty, fmt.Errorf("post_build source is not a regular file: %s", name)
	}
	current, err = root.Stat(name)
	if err != nil || !os.SameFile(before, current) {
		return empty, fmt.Errorf("post_build file changed before inspection: %s", name)
	}
	digest := sha256.New()
	var count int64
	for {
		if err := guard.ctx.Err(); err != nil {
			return empty, err
		}
		n, readErr := file.Read(guard.buffer)
		if n > 0 {
			_, _ = digest.Write(guard.buffer[:n])
			count += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return empty, readErr
		}
		if n == 0 {
			return empty, io.ErrNoProgress
		}
	}
	after, err := file.Stat()
	if err != nil || count != before.Size() || !os.SameFile(before, after) || before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		return empty, fmt.Errorf("post_build file changed during inspection: %s", name)
	}
	current, err = root.Stat(name)
	if err != nil || !os.SameFile(after, current) {
		return empty, fmt.Errorf("post_build file changed after inspection: %s", name)
	}
	var result copySignature
	result.exists, result.kind, result.mode = true, 'f', before.Mode().Perm()
	copy(result.digest[:], digest.Sum(nil))
	return result, nil
}
