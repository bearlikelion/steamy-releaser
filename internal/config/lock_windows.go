package config

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockConfiguration(file *os.File) (func(), error) {
	handle := windows.Handle(file.Fd())
	offset := windows.Overlapped{}
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, ^uint32(0), ^uint32(0), &offset); err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errors.New("another configuration edit is already in progress")
		}
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(handle, 0, ^uint32(0), ^uint32(0), &offset) }, nil
}
