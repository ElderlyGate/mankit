package disk

import (
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/gofrs/flock"
)

// The writes below name a file by its machine path, for the change queue below the store, which cannot open a scope (the store is built on it): a queue's ticket, its turn and its lock.

// Remove takes the file at path away; one already gone is not an error, as two tidying the same file are both right.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Rename moves from to to, replacing what is at to.
func Rename(from, to string) error { return os.Rename(from, to) }

// Touch says a file is alive by its time, for a reader that tells a stale one by it.
func Touch(path string, at time.Time) error { return os.Chtimes(path, at, at) }

// Lock is the lock file at path, made readable by this user alone when it is first taken; the system gives a lock back when its process ends.
func Lock(path string) *flock.Flock { return flock.New(path, flock.SetPermissions(0o600)) }
