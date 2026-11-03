package home

import (
	"errors"
	"io/fs"
	"os"
)

// renameIfMissing moves from to to unless to exists (fs.ErrExist); the check and the move are two steps.
func renameIfMissing(from, to string) error {
	if _, err := os.Lstat(to); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			err = &os.LinkError{Op: "rename", Old: from, New: to, Err: fs.ErrExist}
		}
		return err
	}
	return os.Rename(from, to)
}
