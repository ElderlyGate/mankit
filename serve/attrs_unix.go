//go:build !windows

package serve

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// detachAttrs puts the child in its own session, so closing the terminal that started it leaves it running.
func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// private is nil for a plain file only this user reads and writes.
func private(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("is not a plain file")
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("is open to other users (mode %04o, not 0600)", info.Mode().Perm())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("belongs to another user (uid %d)", st.Uid)
	}
	return nil
}

// connRefused are the error numbers of a connection nothing listens for.
var connRefused = []error{syscall.ECONNREFUSED}
