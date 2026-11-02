// Package home is where the org's apps keep their config: one folder for the org, a global file every app reads, and a folder per product.
package home

import (
	"io"
	"os"
	"path/filepath"
)

// Org is the name of the org's config folder, written only here so a rename changes one line.
const Org = "elderlygate"

// Dir is $XDG_CONFIG_HOME/<Org>, else ~/.config/<Org>, the same on Linux and macOS.
func Dir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(base) {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(h, ".config")
	}
	return filepath.Join(base, Org), nil
}

// Global is <Dir>/global-config.yaml, the settings every app shares.
func Global() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "global-config.yaml"), nil
}

// Product is <Dir>/<id>, the folder of the product with that ID.
func Product(id string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id), nil
}

// File is <Product(id)>/<name>; when it is missing, a file kept before in the system's config folder (<config dir>/<id>/<name>) is copied there first and left as it was.
// A copy that fails is tried again next time.
func File(id, name string) (string, error) {
	dir, err := Product(id)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		if old, err := os.UserConfigDir(); err == nil {
			copyFile(filepath.Join(old, id, name), path)
		}
	}
	return path, nil
}

// copyFile copies from to a new file to, with from's mode, through a temp file; nothing happens when from is missing.
func copyFile(from, to string) {
	src, err := os.Open(from)
	if err != nil {
		return
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil || !fi.Mode().IsRegular() || os.MkdirAll(filepath.Dir(to), 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(to), "."+filepath.Base(to)+".*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	_, err = io.Copy(tmp, src)
	if cerr := tmp.Close(); err != nil || cerr != nil || os.Chmod(tmp.Name(), fi.Mode().Perm()) != nil {
		return
	}
	os.Rename(tmp.Name(), to)
}
