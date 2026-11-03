package home

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace moves from to to, failing with fs.ErrExist when to exists.
func renameNoReplace(from, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		return renameIfMissing(from, to) // the file system cannot refuse a replace itself
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err} // EEXIST is fs.ErrExist
	}
	return nil
}
