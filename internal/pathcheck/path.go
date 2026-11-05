package pathcheck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Path describes a path after resolving its existing ancestors and symlinks.
type Path struct {
	Path string
	Info fs.FileInfo
}

// Resolve allows the final components to be created later but rejects
// dangling symlinks, which make the intended destination ambiguous.
func Resolve(path string) (Path, error) {
	current, err := filepath.Abs(path)
	if err != nil {
		return Path{}, err
	}
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			info, statErr := os.Stat(resolved)
			if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
				return Path{}, statErr
			}
			return Path{Path: resolved, Info: info}, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return Path{}, err
		}
		info, statErr := os.Lstat(current)
		if statErr == nil && info.Mode()&fs.ModeSymlink != 0 {
			return Path{}, fmt.Errorf("dangling symlink %s", current)
		}
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return Path{}, statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return Path{}, err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// Contains checks whether child is parent or lies below it. Existing file
// aliases are compared by identity; macOS comparisons are conservative on
// case-sensitive volumes because many installations use case-insensitive ones.
func Contains(parent, child Path) bool {
	if parent.Info != nil && child.Info != nil && os.SameFile(parent.Info, child.Info) {
		return true
	}
	base, candidate := parent.Path, child.Path
	if runtime.GOOS == "darwin" {
		base, candidate = strings.ToLower(base), strings.ToLower(candidate)
	}
	relative, err := filepath.Rel(base, candidate)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func Overlap(a, b Path) bool {
	return Contains(a, b) || Contains(b, a)
}
