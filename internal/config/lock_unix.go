//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly || solaris

package config

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockConfiguration(file *os.File) (func(), error) {
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errors.New("another configuration edit is already in progress")
		}
		return nil, err
	}
	return func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }, nil
}
