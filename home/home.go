// Package home is where the org's apps keep their files: one config folder and one data folder for the org, and a folder per product in each.
package home

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Org is the name of the org's folders, written only here so a rename changes one line.
const Org = "elderlygate"

// Dir is the config folder: $XDG_CONFIG_HOME/<Org>, else ~/.config/<Org>, the same on Linux and macOS.
func Dir() (string, error) { return base("XDG_CONFIG_HOME", ".config") }

// Data is the data folder: $XDG_DATA_HOME/<Org>, else ~/.local/share/<Org>, the same on Linux and macOS.
func Data() (string, error) { return base("XDG_DATA_HOME", filepath.Join(".local", "share")) }

// Product is <Dir>/<id>, the config folder of the product with that ID.
func Product(id string) (string, error) { return under(Dir, id) }

// ProductData is <Data>/<id>, the data folder of the product with that ID.
func ProductData(id string) (string, error) { return under(Data, id) }

// File is <Product(id)>/<name>; it only names the path and never touches a file (Migrate copies the old ones).
func File(id, name string) (string, error) {
	dir, err := Product(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// Migrate copies each named file the app kept before in the system's config folder (<config dir>/<id>/<name>) to File(id, name), once per file:
// a done copy leaves a mark in <Dir>/.migrated/<id>/, so a file the user deletes later never comes back. A copy that fails is tried again next time.
func Migrate(id string, names ...string) error {
	old, err := os.UserConfigDir()
	if err != nil {
		return nil // no old folder to copy from
	}
	dir, err := Dir()
	if err != nil {
		return err
	}
	marks := filepath.Join(dir, ".migrated", id)
	var errs []error
	for _, name := range names {
		mark := filepath.Join(marks, name)
		if _, err := os.Lstat(mark); err == nil {
			continue
		}
		to, err := File(id, name)
		if err == nil {
			err = CopyOnce(filepath.Join(old, id, name), to)
		}
		if err == nil {
			err = touch(mark)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// touch makes the empty file path and its folders.
func touch(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// CopyOnce copies the file or folder from to to when to is missing and from exists, and leaves from as it was; otherwise it does nothing.
// A folder appears whole or not at all, and a to made meanwhile by someone else is kept, never overwritten.
func CopyOnce(from, to string) error {
	if _, err := os.Lstat(to); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fi, err := os.Stat(from)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(to), "."+filepath.Base(to)+".*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	staged := filepath.Join(tmp, filepath.Base(to))
	if fi.IsDir() {
		err = copyTree(from, staged)
	} else {
		err = copyFile(from, staged, fi.Mode())
	}
	if err != nil {
		return err
	}
	if fi.IsDir() {
		err = renameNoReplace(staged, to)
	} else {
		err = os.Link(staged, to)
	}
	if errors.Is(err, fs.ErrExist) {
		return nil // made meanwhile: the newer one stays
	}
	return err
}

// copyTree copies the folder from to the new folder to: folders, regular files and symlinks, each with its mode.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		fi, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.Mkdir(dst, fi.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, dst)
		case fi.Mode().IsRegular():
			return copyFile(path, dst, fi.Mode())
		}
		return nil
	})
}

// copyFile copies the regular file from to the new file to with mode.
func copyFile(from, to string, mode fs.FileMode) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Chmod(to, mode.Perm())
}

// base is $env/<Org> when env holds an absolute path, else ~/<fallback>/<Org>.
func base(env, fallback string) (string, error) {
	dir := os.Getenv(env)
	if !filepath.IsAbs(dir) {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(h, fallback)
	}
	return filepath.Join(dir, Org), nil
}

// under is name inside the folder root returns.
func under(root func() (string, error), name string) (string, error) {
	dir, err := root()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
