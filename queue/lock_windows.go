package queue

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockAt is the one byte locked, far past any content, because a Windows lock also keeps others from reading the bytes it covers.
var lockAt = windows.Overlapped{OffsetHigh: 1 << 30}

// lockFile takes f's exclusive lock without waiting; false when another handle holds it. The system gives it back when its process ends.
func lockFile(f *os.File) (bool, error) {
	at := lockAt
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &at)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) error {
	at := lockAt
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &at)
}
