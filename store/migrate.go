package store

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/internal/disk"
)

// Migrate copies each named file the app kept before in the system's config folder (<config dir>/<id>/<name>) to home.File(id, name), once per file:
// a done copy leaves a mark at home.MigratedMark, so a file the user deletes later never comes back. A missing old file or a copy that fails leaves no mark.
func Migrate(id string, names ...string) error {
	old, err := os.UserConfigDir()
	if err != nil {
		return nil // no old folder to copy from
	}
	var errs []error
	for _, name := range names {
		from := filepath.Join(old, id, name)
		mark, err := home.MigratedMark(id, name)
		if err != nil {
			return err
		}
		if exists(mark) || !exists(from) {
			continue
		}
		to, err := home.File(id, name)
		if err == nil {
			err = CopyOnce(from, to)
		}
		if err == nil {
			err = disk.Replace(mark, nil, 0o644) // the empty mark
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// CopyOnce copies the file or folder from to to when to is missing and from exists, and leaves from as it was; otherwise it does nothing.
// A folder appears whole or not at all, and a to made meanwhile by someone else is kept, never overwritten.
func CopyOnce(from, to string) error { return disk.CopyOnce(from, to) }

// exists reports whether something is at path.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
