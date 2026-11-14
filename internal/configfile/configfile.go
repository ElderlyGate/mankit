// Package configfile is where the config files are and how they are written whole, shared by theme, usercmd, settings and ask.
package configfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tugbayatilla/mankit/v3/home"
)

// Path is <dir>/<app>/config.yaml; an empty dir is the app's folder in the org's config folder (home.File, a path only).
func Path(app, dir string) (string, error) {
	if dir == "" {
		return home.File(app, "config.yaml")
	}
	return filepath.Join(dir, app, "config.yaml"), nil
}

// WriteFile replaces path's real file through a synced temp file and a rename, keeping its mode.
func WriteFile(path string, b []byte) error {
	mode := fs.FileMode(0o644)
	path, err := target(path)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync() // best effort: the rename survives a crash
		d.Close()
	}
	return nil
}

// target is the file path names after following symlinks, even to a target that does not exist yet.
func target(path string) (string, error) {
	for range 40 {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			return real, nil
		}
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			return path, nil
		}
		link, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(path), link)
		}
		path = link
	}
	return "", fmt.Errorf("%s: too many symlinks", path)
}
