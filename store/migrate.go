package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/problem"
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
			err = EditFile(mark, func([]byte, bool) ([]byte, error) { return []byte{}, nil }) // the empty mark
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// CopyOnce copies the file or folder from to to when to is missing and from exists, and leaves from as it was; otherwise it does nothing.
// The copy goes through the store: a file in the turn of the folder it goes into, a folder in its own turn, made by the copy, so nothing beside to is touched. It is whole or not at all, a to made meanwhile is kept, never overwritten, a file that may be run stays so, and a link inside from is refused, as the store writes no link (ADR-S22).
func CopyOnce(from, to string) error {
	if copied(to) {
		return nil
	}
	fi, err := os.Stat(from)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type file struct {
		body []byte
		exec bool
	}
	files := map[Key]file{}
	root, prefix := to, ""
	if !fi.IsDir() {
		b, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		root, prefix = filepath.Dir(to), filepath.Base(to)
		files[Key(prefix)] = file{b, fi.Mode()&0o111 != 0}
	} else if err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.Type()&fs.ModeSymlink != 0:
			return problem.FailAt(problem.TypeRefused, ReasonOutside, p, fmt.Sprintf("%s is a link, and the store writes no link, so %s is not copied", p, from))
		case d.IsDir():
			return nil
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		k, err := ParseKey(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		files[k] = file{b, info.Mode()&0o111 != 0}
		return err
	}); err != nil {
		return err
	}
	s, err := ownFolder(root)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Change(context.Background(), "", "copy "+from, func(tx Tx) error {
		// Made meanwhile: the newer one stays.
		var at Key
		if prefix != "" {
			at = Key(prefix)
		}
		if there, err := ListIn(tx, at); err != nil || len(there) > 0 {
			return err
		}
		for k, f := range files {
			put := tx.Put
			if f.exec {
				put = func(k Key, b []byte, w Want) error { return PutExec(tx, k, b, w) }
			}
			if err := put(k, f.body, Absent()); err != nil {
				return err
			}
		}
		return nil
	})
}

// copied says whether to holds something already: a file, or a folder with anything in it but the store's own .queue, which a copy cut short leaves.
func copied(to string) bool {
	info, err := os.Lstat(to)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return true
	}
	entries, _ := os.ReadDir(to)
	for _, e := range entries {
		if !reserved[e.Name()] {
			return true
		}
	}
	return false
}

// exists reports whether something is at path.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
