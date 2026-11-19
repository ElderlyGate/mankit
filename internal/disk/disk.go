// Package disk is the one safety core every writer in mankit shares, the store, the change queue and the writers below the store alike: inside an open folder it follows no link, reads and writes regular files only, never waits on a pipe, writes a file whole (G-CODE-5) and keeps an app's own folders private. Replace alone follows a link, on purpose: a config file a person edits may be a link to their own copy.
package disk

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// The reasons the core refuses a name; a caller decides by them with errors.Is, never by the message.
var (
	ErrLink     = errors.New("leads through a link")
	ErrNotFile  = errors.New("is not a regular file")
	ErrNotOwned = errors.New("is not this user's own, or others can read or write it")
)

// TempMark is in the name of every temporary file the core writes beside a file, so a reader of the folder can leave it out.
const TempMark = ".~disk-"

// IsTemp says whether a file name is one of the core's temporary files.
func IsTemp(name string) bool {
	return strings.HasPrefix(name, ".") && strings.Contains(name, TempMark)
}

// NoLinks refuses name when it or any folder on the way to it inside r is a link; a step not there yet is fine, as nothing can lead through it.
func NoLinks(r *os.Root, name string) error {
	name = path.Clean(filepath.ToSlash(name))
	if name == "." {
		return nil
	}
	at := ""
	for _, step := range strings.Split(name, "/") {
		at = path.Join(at, step)
		info, err := r.Lstat(at)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return &fs.PathError{Op: "open", Path: at, Err: ErrLink}
		}
	}
	return nil
}

// Read is the regular file name inside r, read through no link and without waiting on a pipe or a device; what it read is checked to be a file once open, so a file swapped after the look is still refused.
func Read(r *os.Root, name string) ([]byte, fs.FileInfo, error) {
	if err := NoLinks(r, name); err != nil {
		return nil, nil, err
	}
	// Asked before opening, as opening a pipe or a device can wait for ever.
	info, err := r.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, info, &fs.PathError{Op: "read", Path: name, Err: ErrNotFile}
	}
	f, err := r.OpenFile(name, os.O_RDONLY|nonBlocking|noFollow, 0)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if info, err = f.Stat(); err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, info, &fs.PathError{Op: "read", Path: name, Err: ErrNotFile}
	}
	body, err := io.ReadAll(f)
	if err != nil {
		return nil, nil, err
	}
	return body, info, nil
}

// Write puts data at name inside r whole or not at all: into a synced temporary file beside it with mode, then renamed into place, and the folder synced so the rename survives a power cut (G-CODE-5).
func Write(r *os.Root, name string, data []byte, mode fs.FileMode) error {
	if err := write(r, name, data, mode, true); err != nil {
		return err
	}
	return SyncDir(r, path.Dir(path.Clean(filepath.ToSlash(name))))
}

// WriteContent is Write that leaves the folder to the caller, who syncs it once with SyncDir after several writes in it.
func WriteContent(r *os.Root, name string, data []byte, mode fs.FileMode) error {
	return write(r, name, data, mode, true)
}

func write(r *os.Root, name string, data []byte, mode fs.FileMode, synced bool) error {
	if err := NoLinks(r, name); err != nil {
		return err
	}
	if info, err := r.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return &fs.PathError{Op: "write", Path: name, Err: ErrNotFile}
	}
	dir, base := path.Split(path.Clean(filepath.ToSlash(name)))
	tmp := path.Join(dir, "."+base+TempMark+random())
	f, err := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollow, 0o600)
	if err != nil {
		return err
	}
	defer r.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	// Without the sync a power cut can leave the renamed name pointing at empty or partial bytes.
	if synced {
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := r.Chmod(tmp, mode); err != nil {
		return err
	}
	return r.Rename(tmp, name)
}

// Append adds data to the end of the regular file name inside r, opened through no link, and syncs it, so what was appended is on the disk before anything it describes is done.
func Append(r *os.Root, name string, data []byte) error {
	if err := NoLinks(r, name); err != nil {
		return err
	}
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_APPEND|noFollow, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// SyncDir makes what was renamed, made or removed in the folder name inside r survive a power cut.
func SyncDir(r *os.Root, name string) error {
	d, err := r.Open(name)
	if err != nil {
		return err
	}
	defer d.Close()
	// A folder some systems cannot sync is not a failed write: the rename itself is done.
	if err := d.Sync(); err != nil && !errors.Is(err, errors.ErrUnsupported) && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}

// WriteWhole puts data at path in the folder dir whole, through a temporary file beside it renamed into place, readable by this user alone; it skips the syncs, so it is for files that matter only while their process runs, such as the change queue's tickets.
func WriteWhole(dir, path string, data []byte) error {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer r.Close()
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return err
	}
	return write(r, rel, data, 0o600, false)
}

// Private makes dir and the folders above it that are new only this user's, syncing each folder a new one was made in, and tightens to 0700 a dir this user owns that others could read or write; it says what it tightened.
// A dir that is a link, not a folder, or another user's is refused with ErrNotOwned, since what is kept there is acted on as this user.
func Private(dir string) (tightened []string, err error) {
	var made []string
	for p := filepath.Clean(dir); ; p = filepath.Dir(p) {
		if _, err := os.Lstat(p); err == nil || filepath.Dir(p) == p {
			break
		}
		made = append(made, p)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	for _, p := range made {
		if err := syncPath(filepath.Dir(p)); err != nil {
			return nil, err
		}
	}
	// Checked once made, through one handle opened never through a link, since another user may have made it first.
	f, err := os.OpenFile(dir, os.O_RDONLY|noFollow|directory, 0)
	if err != nil {
		if info, lerr := os.Lstat(dir); lerr == nil && (info.Mode()&fs.ModeSymlink != 0 || !info.IsDir()) {
			return nil, &fs.PathError{Op: "use", Path: dir, Err: ErrNotOwned}
		}
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || !Owned(info) {
		return nil, &fs.PathError{Op: "use", Path: dir, Err: ErrNotOwned}
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := f.Chmod(0o700); err != nil {
			return nil, err
		}
		tightened = append(tightened, dir)
	}
	return tightened, nil
}

// Tighten takes from every folder and file inside dir that this user owns what others may do with it, so it is this user's alone, and says which; each is changed through a handle opened never through a link. One another user owns is left as it is and said in skipped, with what is inside it.
func Tighten(dir string) (tightened, skipped []string, err error) {
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil
		}
		flag := os.O_RDONLY | noFollow | nonBlocking
		if d.IsDir() {
			flag |= directory
		}
		f, err := os.OpenFile(p, flag, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if info.IsDir() != d.IsDir() || !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		if !Owned(info) {
			skipped = append(skipped, p)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if info.Mode().Perm()&0o077 == 0 {
			return nil
		}
		if err := f.Chmod(info.Mode().Perm() &^ 0o077); err != nil {
			return err
		}
		tightened = append(tightened, p)
		return nil
	})
	return tightened, skipped, err
}

// syncPath is SyncDir for a folder named by its path.
func syncPath(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !errors.Is(err, errors.ErrUnsupported) && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}

// OwnFile refuses with ErrNotOwned a file another user owns or others can write.
func OwnFile(info fs.FileInfo, name string) error {
	if !Owned(info) || info.Mode().Perm()&0o022 != 0 {
		return &fs.PathError{Op: "use", Path: name, Err: ErrNotOwned}
	}
	return nil
}

// random is a short random text, so two temporary files never share a name.
func random() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b) // never fails: crypto/rand ends the program instead
	return hex.EncodeToString(b)
}
