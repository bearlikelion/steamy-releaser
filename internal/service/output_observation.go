package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
)

// outputObservation records filesystem metadata, not the provenance or bytes
// of an artifact. It is used to detect an obvious no-op build without modifying
// files that may be shared with the user's project.
type outputObservation struct {
	info       fs.FileInfo
	tree       [sha256.Size]byte
	hasEntries bool
}

func observeOutput(ctx context.Context, path string) (outputObservation, error) {
	if err := ctx.Err(); err != nil {
		return outputObservation{}, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return outputObservation{}, nil
	}
	if err != nil {
		return outputObservation{}, err
	}
	observation := outputObservation{info: info}
	if !info.IsDir() {
		observation.hasEntries = true
		return observation, nil
	}
	// On Windows os.Stat can defer reading the file ID until SameFile is
	// called. Resolve it before OpenRoot holds a handle on the directory.
	if !os.SameFile(info, info) {
		return outputObservation{}, fmt.Errorf("cannot identify output directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return outputObservation{}, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil {
		return outputObservation{}, err
	}
	if !os.SameFile(info, opened) {
		return outputObservation{}, fmt.Errorf("output directory changed while inspecting it")
	}
	digest := sha256.New()
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if entry.IsDir() != current.IsDir() || (entry.Type()&fs.ModeSymlink != 0) != (current.Mode()&fs.ModeSymlink != 0) {
			return fmt.Errorf("output changed while inspecting %s", name)
		}
		if name != "." && !current.IsDir() {
			observation.hasEntries = true
		}
		writeObservationField(digest, name)
		writeObservationField(digest, current.Mode().String())
		writeObservationField(digest, fmt.Sprint(current.Size()))
		writeObservationField(digest, fmt.Sprint(current.ModTime().UnixNano()))
		if current.Mode()&fs.ModeSymlink != 0 {
			link, err := root.Readlink(name)
			if err != nil {
				return err
			}
			writeObservationField(digest, link)
		}
		return nil
	})
	if err != nil {
		return outputObservation{}, err
	}
	copy(observation.tree[:], digest.Sum(nil))
	return observation, nil
}

func writeObservationField(digest hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = io.WriteString(digest, value)
}

// unchanged reports that no change was observable in the metadata inspected.
// A missing output is handled by later validation, including post-build copies.
func (before outputObservation) unchanged(after outputObservation) bool {
	if before.info == nil || after.info == nil {
		return false
	}
	return before.info.Mode() == after.info.Mode() &&
		before.info.Size() == after.info.Size() &&
		before.info.ModTime().Equal(after.info.ModTime()) &&
		before.tree == after.tree
}
