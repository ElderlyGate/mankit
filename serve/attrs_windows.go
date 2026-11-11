package serve

import (
	"errors"
	"os"
	"syscall"
)

// detachAttrs gives the child its own process group, so a console ctrl-c no longer reaches it.
func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// private is nil for a plain file; Windows keeps a user's profile folder to that user.
func private(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("is not a plain file")
	}
	return nil
}
