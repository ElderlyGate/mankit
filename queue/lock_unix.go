//go:build !windows

package queue

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes f's exclusive lock on the whole file without waiting; false when another open file holds it. The system gives it back when its process ends.
func lockFile(f *os.File) (bool, error) {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, syscall.EWOULDBLOCK):
			return false, nil
		case errors.Is(err, syscall.EINTR):
			continue
		default:
			return false, err
		}
	}
}

func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
