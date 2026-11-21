// Package home is where the org's apps keep their files: one config folder, one data folder and one logs folder for the org, and a folder per product in each.
package home

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tugbayatilla/mankit/v4/build"
)

// Org is the name of the org's folders, written only here so a rename changes one line.
const Org = "elderlygate"

// Dir is the config folder: $XDG_CONFIG_HOME/<Org>, else ~/.config/<Org>, the same on Linux and macOS.
func Dir() (string, error) { return base("XDG_CONFIG_HOME", ".config") }

// Data is the data folder: $XDG_DATA_HOME/<Org>, else ~/.local/share/<Org>, the same on Linux and macOS.
func Data() (string, error) { return base("XDG_DATA_HOME", filepath.Join(".local", "share")) }

// Logs is the logs folder (G-STACK-8): $XDG_STATE_HOME/<Org>, else ~/.local/state/<Org> on Linux, and ~/Library/Logs/<Org> on macOS.
func Logs() (string, error) {
	if runtime.GOOS == "darwin" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, "Library", "Logs", Org), nil
	}
	return base("XDG_STATE_HOME", filepath.Join(".local", "state"))
}

// ProductLogs is <Logs>/<id>, the logs folder of the product with that ID.
func ProductLogs(id string) (string, error) { return under(Logs, id) }

// dev ends the name of a dev build's own folders (ADR-L5).
const dev = "-dev"

// Folder is the name of the product id's config and data folders: id in a release, <id>-dev in every other build (build.Released), so a dev build never reads or writes a release's (ADR-L5).
func Folder(id string) string {
	if build.Released() {
		return id
	}
	return id + dev
}

// OrgDir is the folder of the org's own files, the org license and its queue: Dir in a release, <Org>-dev beside it in every other build, so a dev build never reads or writes a release's org license (ADR-L5). Products stay under Dir, in Folder(id).
func OrgDir() (string, error) {
	dir, err := Dir()
	if err != nil || build.Released() {
		return dir, err
	}
	return dir + dev, nil
}

// ID is the product ID whose folder is named folder, the reverse of Folder.
func ID(folder string) string {
	if build.Released() {
		return folder
	}
	return strings.TrimSuffix(folder, dev)
}

// Product is <Dir>/<Folder(id)>, the config folder of the product with that ID.
func Product(id string) (string, error) { return under(Dir, Folder(id)) }

// ProductData is <Data>/<Folder(id)>, the data folder of the product with that ID.
func ProductData(id string) (string, error) { return under(Data, Folder(id)) }

// File is <Product(id)>/<name>; it only names the path and never touches a file (Migrate copies the old ones).
func File(id, name string) (string, error) {
	dir, err := Product(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// Config is <dir>/<Folder(id)>/config.yaml, the one file of each app a person edits; an empty dir is File(id, "config.yaml"). It only names the path.
func Config(id, dir string) (string, error) {
	if dir == "" {
		return File(id, "config.yaml")
	}
	return filepath.Join(dir, Folder(id), "config.yaml"), nil
}

// Current is the file to read id's name from now: File(id, name), or the old file in the system's config folder, read in place,
// while File is missing, store.Migrate has not copied it and the old one exists. It never touches a file.
func Current(id, name string) (string, error) {
	to, err := File(id, name)
	if err != nil || exists(to) {
		return to, err
	}
	mark, err := MigratedMark(id, name)
	if err != nil || exists(mark) {
		return to, err
	}
	if old, err := os.UserConfigDir(); err == nil && exists(filepath.Join(old, id, name)) {
		return filepath.Join(old, id, name), nil
	}
	return to, nil
}

// MigratedMark is the mark store.Migrate leaves once it copied id's old name, one per Folder(id), so a dev build's copy never stops a release's: Current reads the old file in place only while it is missing.
func MigratedMark(id, name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".migrated", Folder(id), name), nil
}

// exists reports whether something is at path.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
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
