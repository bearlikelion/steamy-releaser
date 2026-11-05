//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly && !solaris && !windows

package service

import "errors"

func tryProjectLock(string) (func(), error) {
	return nil, errors.New("project locking is not supported on this platform")
}
