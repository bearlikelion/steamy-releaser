//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly || solaris

package service

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Lock the project directory itself so the lock cannot enter a content root or
// make an empty prebuilt artifact look nonempty. Separate opens of aliases to
// the same directory lock the same inode.
func tryProjectLock(projectRoot string) (func(), error) {
	file, err := os.Open(projectRoot)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.IsDir() {
		_ = file.Close()
		return nil, fmt.Errorf("project root is not a directory")
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrProjectBusy
		}
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}
