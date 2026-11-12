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

// privateDir is nil for a folder; Windows keeps a user's profile folder to that user.
func privateDir(info os.FileInfo) error {
	if !info.IsDir() {
		return errors.New("is not a folder")
	}
	return nil
}

// openNoFollow opens path for reading, refusing a link in its place.
func openNoFollow(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("it is a link, not a plain file")
	}
	return os.Open(path)
}

// connRefused are the error numbers of a connection nothing listens for: Windows answers WSAECONNREFUSED, not ECONNREFUSED.
var connRefused = []error{syscall.ECONNREFUSED, syscall.Errno(10061)}
