package artifact

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/thornity/steam-release-tool/internal/safefile"
)

func copyIntoContent(project *os.Root, source, content, destination string, guard *CopyGuard) error {
	info, err := project.Stat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		// Resolve existing ancestors before creating anything. A directory copied
		// into itself would otherwise grow indefinitely during traversal.
		original, err := filepath.EvalSymlinks(filepath.Join(project.Name(), source))
		if err != nil {
			return err
		}
		candidate, err := resolveDestination(filepath.Join(content, destination))
		if err != nil {
			return err
		}
		if relative, err := filepath.Rel(original, candidate); err == nil && filepath.IsLocal(relative) {
			return fmt.Errorf("cannot copy a directory inside itself")
		}
	}
	if err := os.MkdirAll(content, 0o755); err != nil {
		return err
	}
	output, err := os.OpenRoot(content)
	if err != nil {
		return err
	}
	defer output.Close()
	if !info.IsDir() {
		return guardedCopyFile(project, output, source, source, destination, guard)
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
		target := filepath.Join(destination, filepath.FromSlash(name))
		if entry.IsDir() {
			return output.MkdirAll(target, 0o755)
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			// Preserve framework/bundle links only when they resolve within the
			// copied tree. Do not follow directory links while walking.
			if _, err := input.Stat(name); err != nil {
				return err
			}
			link, err := input.Readlink(name)
			if err != nil {
				return err
			}
			if !filepath.IsLocal(filepath.Join(filepath.Dir(filepath.FromSlash(name)), link)) {
				return fmt.Errorf("symlink %q escapes the copied tree", name)
			}
			var decision copyDecision
			if guard != nil {
				decision, err = guard.beforeCopy(input, name, filepath.Join(source, filepath.FromSlash(name)), output, target, true)
				if err != nil {
					return err
				}
				if decision.skip {
					guard.afterCopy(decision)
					return nil
				}
				if decision.known && decision.sig.digest != sha256.Sum256([]byte(link)) {
					return fmt.Errorf("post_build source link changed while preparing the copy: %s", name)
				}
			}
			if err := output.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			temporary := path.Join(filepath.ToSlash(filepath.Dir(target)), ".copy-link-"+rand.Text())
			if err := output.Symlink(link, temporary); err != nil {
				return err
			}
			defer output.Remove(temporary)
			if err := output.Rename(temporary, target); err != nil {
				return err
			}
			if guard != nil {
				guard.afterCopy(decision)
			}
			return nil
		}
		return guardedCopyFile(input, output, name, filepath.Join(source, filepath.FromSlash(name)), target, guard)
	})
}

func guardedCopyFile(sourceRoot, destinationRoot *os.Root, source, logicalSource, destination string, guard *CopyGuard) error {
	var decision copyDecision
	if guard != nil {
		sourceInfo, err := sourceRoot.Stat(source)
		if err != nil {
			return err
		}
		destinationInfo, err := destinationRoot.Stat(destination)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && os.SameFile(sourceInfo, destinationInfo) {
			return fmt.Errorf("cannot copy a file onto itself")
		}
		decision, err = guard.beforeCopy(sourceRoot, source, logicalSource, destinationRoot, destination, false)
		if err != nil {
			return err
		}
		if decision.skip {
			guard.afterCopy(decision)
			return nil
		}
	}
	var expected *copySignature
	var buffer []byte
	ctx := context.Background()
	if guard != nil {
		ctx = guard.ctx
		buffer = guard.buffer
		if decision.known {
			expected = &decision.sig
		}
	}
	if err := copyFile(ctx, sourceRoot, destinationRoot, source, destination, expected, buffer); err != nil {
		return err
	}
	if guard != nil {
		guard.afterCopy(decision)
	}
	return nil
}

func resolveDestination(name string) (string, error) {
	resolved, err := filepath.EvalSymlinks(name)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return resolved, err
	}
	parent := filepath.Dir(name)
	if parent == name {
		return "", err
	}
	resolved, err = resolveDestination(parent)
	return filepath.Join(resolved, filepath.Base(name)), err
}

func copyFile(ctx context.Context, sourceRoot, destinationRoot *os.Root, source, destination string, expected *copySignature, buffer []byte) error {
	info, err := sourceRoot.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source %q is not a regular file", source)
	}
	input, err := safefile.OpenCandidate(sourceRoot, source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err = input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source %q is not a regular file", source)
	}
	previous, err := destinationRoot.Stat(destination)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && os.SameFile(info, previous) {
		return fmt.Errorf("cannot copy a file onto itself")
	}
	if err == nil && !previous.Mode().IsRegular() {
		return fmt.Errorf("destination %q is not a regular file", destination)
	}
	if err := destinationRoot.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	// Stage the copy so a failed copy cannot truncate existing artifacts or
	// follow a destination file symlink. Root also confines ancestor symlinks.
	temporary := path.Join(filepath.ToSlash(filepath.Dir(destination)), ".copy-"+rand.Text())
	output, err := destinationRoot.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer destinationRoot.Remove(temporary)
	var copyErr error
	if expected == nil {
		_, copyErr = io.Copy(output, input)
	} else {
		digest := sha256.New()
		if len(buffer) == 0 {
			buffer = make([]byte, 128<<10)
		}
		var count int64
		for copyErr == nil {
			if err := ctx.Err(); err != nil {
				copyErr = err
				break
			}
			n, readErr := input.Read(buffer)
			if n > 0 {
				_, _ = digest.Write(buffer[:n])
				written, writeErr := output.Write(buffer[:n])
				count += int64(written)
				if writeErr != nil {
					copyErr = writeErr
				} else if written != n {
					copyErr = io.ErrShortWrite
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				copyErr = readErr
			} else if n == 0 {
				copyErr = io.ErrNoProgress
			}
		}
		var actual [sha256.Size]byte
		copy(actual[:], digest.Sum(nil))
		if copyErr == nil {
			copyErr = ctx.Err()
		}
		after, statErr := input.Stat()
		current, currentErr := sourceRoot.Stat(source)
		if copyErr == nil && (statErr != nil || currentErr != nil || count != info.Size() || !os.SameFile(info, after) || !os.SameFile(after, current) || info.Mode() != after.Mode() || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) || expected.kind != 'f' || expected.mode != info.Mode().Perm() || expected.digest != actual) {
			copyErr = fmt.Errorf("post_build source changed while copying %s", source)
		}
	}
	modeErr := output.Chmod(info.Mode().Perm())
	if err := errors.Join(copyErr, modeErr, output.Close()); err != nil {
		return err
	}
	return destinationRoot.Rename(temporary, destination)
}
