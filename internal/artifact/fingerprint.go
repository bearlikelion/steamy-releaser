package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/thornity/steam-release-tool/internal/config"
	"github.com/thornity/steam-release-tool/internal/safefile"
)

// Fingerprint binds a retry checkpoint to the selected content trees and to
// build outputs outside those trees. It reads files but never changes them.
func Fingerprint(ctx context.Context, document *config.Document, profileName string, targets []string) (string, error) {
	selection, err := document.Select(profileName, targets)
	if err != nil {
		return "", err
	}
	names := slices.Clone(selection.TargetNames)
	slices.Sort(names)
	digest := sha256.New()
	writeField(digest, "steam-releaser-artifact-v1")
	writeField(digest, selection.Name)
	writeField(digest, fmt.Sprint(len(names)))
	contentDigests := map[string]string{}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		target := selection.Profile.Targets[name]
		content, err := filepath.EvalSymlinks(document.ResolveProject(target.Content))
		if err != nil {
			return "", fmt.Errorf("resolve content for %s: %w", name, err)
		}
		output, err := filepath.EvalSymlinks(document.ResolveProject(target.Output))
		if err != nil {
			return "", fmt.Errorf("resolve output for %s: %w", name, err)
		}
		contentDigest, ok := contentDigests[content]
		if !ok {
			contentDigest, err = fingerprintTree(ctx, content)
			if err != nil {
				return "", fmt.Errorf("fingerprint content for %s: %w", name, err)
			}
			contentDigests[content] = contentDigest
		}
		writeField(digest, name)
		writeField(digest, content)
		writeField(digest, contentDigest)
		writeField(digest, output)
		relative, relErr := filepath.Rel(content, output)
		if relErr != nil || !filepath.IsLocal(relative) {
			writeField(digest, "external-output")
			outputDigest, err := fingerprintOutput(ctx, output)
			if err != nil {
				return "", fmt.Errorf("fingerprint output for %s: %w", name, err)
			}
			writeField(digest, outputDigest)
		} else {
			writeField(digest, "output-in-content")
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func writeField(digest hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = io.WriteString(digest, value)
}

func fingerprintTree(ctx context.Context, directory string) (string, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	digest := sha256.New()
	buffer := make([]byte, 128<<10)
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if entry.IsDir() != info.IsDir() || (entry.Type()&fs.ModeSymlink != 0) != (info.Mode()&fs.ModeSymlink != 0) {
			return fmt.Errorf("artifact changed while reading %s", name)
		}
		writeField(digest, filepath.ToSlash(name))
		writeField(digest, info.Mode().String())
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := root.Readlink(name)
			if err != nil {
				return err
			}
			target, err := root.Stat(name)
			if err != nil {
				return err
			}
			if !target.IsDir() && !target.Mode().IsRegular() {
				return fmt.Errorf("artifact link %s does not point to a regular file or directory", name)
			}
			writeField(digest, link)
			return nil
		case info.IsDir():
			return nil
		case info.Mode().IsRegular():
			file, err := safefile.OpenCandidate(root, name)
			if err != nil {
				return err
			}
			readErr := fingerprintFile(ctx, digest, file, info, buffer)
			return errorsJoinClose(readErr, file.Close())
		default:
			return fmt.Errorf("artifact %s is not a regular file or directory", name)
		}
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func fingerprintOutput(ctx context.Context, output string) (string, error) {
	info, err := os.Stat(output)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return fingerprintTree(ctx, output)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("build output is not a regular file or directory")
	}
	root, err := os.OpenRoot(filepath.Dir(output))
	if err != nil {
		return "", err
	}
	defer root.Close()
	file, err := safefile.OpenCandidate(root, filepath.Base(output))
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	readErr := fingerprintFile(ctx, digest, file, info, make([]byte, 128<<10))
	if err := errorsJoinClose(readErr, file.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func fingerprintFile(ctx context.Context, digest hash.Hash, file *os.File, expected fs.FileInfo, buffer []byte) error {
	before, err := file.Stat()
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() || !os.SameFile(expected, before) {
		return fmt.Errorf("artifact changed before reading %s", file.Name())
	}
	writeField(digest, before.Mode().String())
	writeField(digest, fmt.Sprint(before.Size()))
	var read int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			_, _ = digest.Write(buffer[:n])
			read += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	after, err := file.Stat()
	if err != nil {
		return err
	}
	if read != before.Size() || before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("artifact changed while reading %s", file.Name())
	}
	return nil
}

func errorsJoinClose(readErr, closeErr error) error {
	if readErr != nil {
		return readErr
	}
	return closeErr
}
