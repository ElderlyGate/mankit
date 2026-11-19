package logs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/tugbayatilla/mankit/v4/home"
)

// A log file is rotated once it would pass rotateAt, and the keep files before it stay as <name>.1 (the newest) to <name>.<keep> (G-STACK-8); vars only so a test can shorten them.
var (
	rotateAt int64 = 10 << 20
	keep           = 5
)

// File is a log file in the app's logs folder, written as records come and rotated by size; only this user may read it. Logs are the one thing an app writes outside the store (ADR-S22).
type File struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

// Path is <logs folder of id>/<name>, such as the file a detached server of id logs to.
func Path(id, name string) (string, error) {
	dir, err := home.ProductLogs(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// Open opens the log file at path to append to, making its folder this user's alone.
func Open(path string) (*File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	l := &File{path: path}
	return l, l.open()
}

func (l *File) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, info.Size()
	return nil
}

// Name is the file's path.
func (l *File) Name() string { return l.path }

// OS is the open file itself, for a process started to write to it directly, such as a detached server's output before it logs.
func (l *File) OS() *os.File { return l.f }

// Write appends p, rotating first when p would take the file past its size.
func (l *File) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size > 0 && l.size+int64(len(p)) > rotateAt {
		if err := l.rotate(); err != nil {
			return 0, fmt.Errorf("the log %s cannot be rotated: %w", l.path, err)
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

// rotate moves each kept file one along, the oldest gone, and starts the file anew.
func (l *File) rotate() error {
	if err := l.f.Close(); err != nil {
		return err
	}
	if err := os.Remove(fmt.Sprintf("%s.%d", l.path, keep)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for i := keep - 1; i >= 1; i-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil {
		return err
	}
	return l.open()
}

// Close closes the file.
func (l *File) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
