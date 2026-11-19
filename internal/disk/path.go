package disk

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

// The writes below name a file by its machine path, for a writer below the store, which cannot open a scope (the store is built on it): a queue's ticket, and a server's log until the log writer has its home.

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
