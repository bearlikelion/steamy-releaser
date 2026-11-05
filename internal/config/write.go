package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// createConfiguration writes the complete file before making its destination
// visible. Publishing fails if another initializer has won the name.
func createConfiguration(path string, contents []byte) (resultErr error) {
	var file *os.File
	var temporary string
	for range 10 {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return fmt.Errorf("choose temporary configuration name: %w", err)
		}
		temporary = filepath.Join(filepath.Dir(path), ".steam-releaser-"+hex.EncodeToString(nonce[:])+".tmp")
		var err error
		// Unlike chmod after CreateTemp, this lets the OS apply the user's umask
		// and the directory's default access controls at creation time.
		file, err = os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("create temporary configuration: %w", err)
		}
		break
	}
	if file == nil {
		return errors.New("could not choose an unused temporary configuration name")
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr := fmt.Errorf("remove temporary configuration %s: %w", temporary, err)
			if resultErr == nil {
				resultErr = fmt.Errorf("configuration was created at %s: %w", path, cleanupErr)
			} else {
				resultErr = errors.Join(resultErr, cleanupErr)
			}
		}
	}()
	if _, err := file.Write(contents); err != nil {
		return fmt.Errorf("write new configuration %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		closed = true
		return fmt.Errorf("close new configuration %s: %w", path, err)
	}
	closed = true
	if err := publishConfiguration(temporary, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("configuration already exists: %s (use --force to replace it): %w", path, err)
		}
		return fmt.Errorf("publish new configuration %s without replacing an existing file: %w", path, err)
	}
	return nil
}

func publishConfiguration(temporary, path string) error {
	linkErr := os.Link(temporary, path)
	if linkErr == nil || errors.Is(linkErr, os.ErrExist) {
		return linkErr
	}
	// Some filesystems do not support hard links. Use a native no-replace
	// rename, never a portable rename that might overwrite an existing file.
	renameErr := renameConfigurationNoReplace(temporary, path)
	if renameErr == nil || errors.Is(renameErr, os.ErrExist) {
		return renameErr
	}
	return errors.Join(linkErr, renameErr)
}

func openLockedConfiguration(path string, flags int) (*os.File, func(), error) {
	file, err := os.OpenFile(path, flags, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("configuration %s is not a regular file", path)
	}
	unlock, err := lockConfiguration(file)
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("lock configuration %s: %w", path, err)
	}
	return file, unlock, nil
}

// Keep writing the original inode so symlinks, hard links, ownership, and
// platform-specific access controls retain their existing behavior.
func replaceConfigurationContents(file *os.File, path string, contents []byte) error {
	if !sameConfigurationPath(file, path) {
		return fmt.Errorf("configuration %s changed while it was being edited", path)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Write(contents); err != nil {
		return err
	}
	return nil
}
