// Package home is where the org's apps keep their files: one config folder and one data folder for the org, and a folder per product in each.
package home

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/tugbayatilla/mankit/v3/internal/disk"
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

// Config is <dir>/<id>/config.yaml, the one file of each app a person edits; an empty dir is File(id, "config.yaml"). It only names the path.
func Config(id, dir string) (string, error) {
	if dir == "" {
		return File(id, "config.yaml")
	}
	return filepath.Join(dir, id, "config.yaml"), nil
}

// Current is the file to read id's name from now: File(id, name), or the old file in the system's config folder, read in place,
// while File is missing, Migrate has not copied it and the old one exists. It never touches a file.
func Current(id, name string) (string, error) {
	to, err := File(id, name)
	if err != nil || exists(to) {
		return to, err
	}
	dir, err := Dir()
	if err != nil || exists(filepath.Join(dir, ".migrated", id, name)) {
		return to, err
	}
	if old, err := os.UserConfigDir(); err == nil && exists(filepath.Join(old, id, name)) {
		return filepath.Join(old, id, name), nil
	}
	return to, nil
}

// Migrate copies each named file the app kept before in the system's config folder (<config dir>/<id>/<name>) to File(id, name), once per file:
// a done copy leaves a mark in <Dir>/.migrated/<id>/, so a file the user deletes later never comes back. A missing old file or a copy that fails leaves no mark.
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
		mark, from := filepath.Join(marks, name), filepath.Join(old, id, name)
		if exists(mark) || !exists(from) {
			continue
		}
		to, err := File(id, name)
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

// exists reports whether something is at path.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// CopyOnce copies the file or folder from to to when to is missing and from exists, and leaves from as it was; otherwise it does nothing.
// A folder appears whole or not at all, and a to made meanwhile by someone else is kept, never overwritten.
func CopyOnce(from, to string) error { return disk.CopyOnce(from, to) }

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
