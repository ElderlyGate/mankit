//go:build unix

package store

import (
	"errors"
	"syscall"
)

// alive says whether a process with this id runs on this machine; one another user runs answers that it may not be signalled, and is running too.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
