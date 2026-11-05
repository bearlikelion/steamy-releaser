package config

import "os"

func sameConfigurationPath(file *os.File, path string) bool {
	opened, err := file.Stat()
	if err != nil {
		return false
	}
	// On Windows, SameFile may reopen an os.Stat result without sharing. That
	// fails while our writable handle is open. Stat a second shared handle so
	// both identities are already loaded; no content needs to be read through it.
	current, err := os.Open(path)
	if err != nil {
		return false
	}
	defer current.Close()
	info, err := current.Stat()
	return err == nil && os.SameFile(opened, info)
}
