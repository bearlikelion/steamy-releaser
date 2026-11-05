//go:build !windows

package config

import "os"

func sameConfigurationPath(file *os.File, path string) bool {
	opened, err := file.Stat()
	if err != nil {
		return false
	}
	current, err := os.Stat(path)
	return err == nil && os.SameFile(opened, current)
}
