package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

// Windows does not support byte-range locks on directories. A named mutex
// avoids placing a lock file inside a project's potential upload content.
func tryProjectLock(projectRoot string) (func(), error) {
	info, err := os.Stat(projectRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project root is not a directory")
	}
	canonical, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return nil, err
	}
	identity := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(canonical))))
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`Global\SteamReleaserProject-%x`, identity))
	if err != nil {
		return nil, err
	}

	// Mutex ownership belongs to the OS thread, including during release.
	runtime.LockOSThread()
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		runtime.UnlockOSThread()
		return nil, err
	}
	status, err := windows.WaitForSingleObject(handle, 0)
	if err != nil || (status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED) {
		_ = windows.CloseHandle(handle)
		runtime.UnlockOSThread()
		if err != nil {
			return nil, err
		}
		if status == uint32(windows.WAIT_TIMEOUT) {
			return nil, ErrProjectBusy
		}
		return nil, fmt.Errorf("unexpected project mutex status %d", status)
	}
	return func() {
		_ = windows.ReleaseMutex(handle)
		_ = windows.CloseHandle(handle)
		runtime.UnlockOSThread()
	}, nil
}
