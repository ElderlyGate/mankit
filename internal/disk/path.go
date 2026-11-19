package disk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

// The writes below name a file by its machine path, for a writer below the store, which cannot open a scope (the store is built on it), or one handed a path by the app: a config file a person edits, a server's record, token and log, a queue's ticket.

// WriteFile puts data at path whole or not at all, with mode, in a folder that is there, through no link (Write).
func WriteFile(path string, data []byte, mode fs.FileMode) error {
	r, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer r.Close()
	return Write(r, filepath.Base(path), data, mode)
}

// Replace puts data whole in the real file path names, a link followed to it, keeping that file's mode, or mode for a new one; its folders are made when missing.
// It is for a file a person edits and may keep as a link to their own copy, such as config.yaml.
func Replace(path string, data []byte, mode fs.FileMode) error {
	path, err := target(path)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return WriteFile(path, data, mode)
}

// target is the file path names after following links, even to a target that does not exist yet.
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

// WriteOnce puts data at path with mode only when nothing is there yet, through a temporary file linked into place: two writers at once both keep the one that landed first, and neither is an error.
func WriteOnce(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+TempMark+"*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return syncPath(filepath.Dir(path))
}

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

// Log opens path empty, made this user's alone, for a stream written as it comes, such as a server's log; it is never whole, so it is only for what a person reads.
func Log(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
}

// JSON is v as a file the core writes holds it: indented by two spaces, ending in a newline.
func JSON(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// YAML is v as a config file holds it: indented by two spaces.
func YAML(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
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
