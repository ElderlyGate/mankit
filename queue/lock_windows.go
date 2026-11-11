package queue

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes f's lock for this handle alone, waiting for it or not; the system gives it back when the process dies.
func lockFile(f *os.File, wait bool) (bool, error) {
	how := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if !wait {
		how |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), how, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}
