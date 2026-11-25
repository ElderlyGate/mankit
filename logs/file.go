package logs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"go.naywee.com/mankit/v5/home"
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
	std  bool // the process's stdout and stderr follow the file across rotations
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

// openFile opens a log file; a var only so a test can make it fail.
var openFile = os.OpenFile

// open opens the file at its path; when it cannot, l.f stays nil and the next Write tries again.
func (l *File) open() error {
	f, err := openFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, info.Size()
	if l.std {
		return toStd(f)
	}
	return nil
}

// TakeStd makes the process's stdout and stderr this file, now and after every rotation, so what a detached server says before it logs, such as a crash, lands in the current file.
func (l *File) TakeStd() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.std = true
	if l.f == nil {
		return l.open()
	}
	return toStd(l.f)
}

// Name is the file's path.
func (l *File) Name() string { return l.path }

// OS is the open file itself, for a process started to write to it directly, such as a detached server's output before it logs.
func (l *File) OS() *os.File { return l.f }

// Write appends p, rotating first when p would take the file past its size; p is written even when the rotation fails, which it says.
func (l *File) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var rotated error
	if l.f != nil && l.size > 0 && l.size+int64(len(p)) > rotateAt {
		if err := l.rotate(); err != nil {
			// Written all the same, to the file as it is, so a rotation that fails loses no record.
			rotated = fmt.Errorf("the log %s cannot be rotated: %w", l.path, err)
		}
	}
	// A file a rotation could not open again is opened now, so a write never goes to a closed file.
	if l.f == nil {
		if err := l.open(); err != nil {
			return 0, errors.Join(fmt.Errorf("the log %s cannot be opened: %w", l.path, err), rotated)
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, errors.Join(err, rotated)
}

// rotate moves each kept file one along, the oldest gone, and starts the file anew; when a step fails, the file is opened again as it is, so the log goes on, and when that fails too, l.f is nil for Write to open.
func (l *File) rotate() error {
	closed := l.f.Close()
	l.f = nil
	var shifted error
	if closed == nil {
		shifted = l.shift()
	}
	return errors.Join(closed, shifted, l.open())
}

// shift moves each kept file one along and the file to <name>.1, the oldest gone.
func (l *File) shift() error {
	if err := os.Remove(fmt.Sprintf("%s.%d", l.path, keep)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for i := keep - 1; i >= 1; i-- {
		if err := os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return os.Rename(l.path, l.path+".1")
}

// Close closes the file.
func (l *File) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
