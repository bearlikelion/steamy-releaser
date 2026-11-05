package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ValidateUnityProjectRoot rejects a repository folder that does not contain
// the Unity project itself, before the Editor or build preparation can run.
func ValidateUnityProjectRoot(root string) error {
	if isUnityProject(root) {
		return nil
	}
	message := fmt.Sprintf("Unity project was not found at %q; set the game project folder (--project-root in the CLI) to the directory containing Assets/ and ProjectSettings/ProjectVersion.txt", root)
	// Repositories often keep their Unity project one directory below the root.
	// Suggest a child only when there is exactly one match; never switch projects.
	entries, err := os.ReadDir(root)
	if err == nil {
		var candidate string
		for _, entry := range entries {
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			path := filepath.Join(root, entry.Name())
			if !isUnityProject(path) {
				continue
			}
			if candidate != "" {
				return errors.New(message)
			}
			candidate = path
		}
		if candidate != "" {
			message += fmt.Sprintf(". Unity project found at %q", candidate)
		}
	}
	return errors.New(message)
}

func isUnityProject(root string) bool {
	assets, err := os.Stat(filepath.Join(root, "Assets"))
	if err != nil || !assets.IsDir() {
		return false
	}
	version, err := os.Stat(filepath.Join(root, "ProjectSettings", "ProjectVersion.txt"))
	return err == nil && version.Mode().IsRegular()
}
