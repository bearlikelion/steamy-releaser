//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly && !solaris && !windows

package config

import (
	"errors"
	"os"
)

func lockConfiguration(*os.File) (func(), error) {
	return nil, errors.New("configuration locking is not supported on this platform")
}
