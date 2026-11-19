package store

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/store/internal/disk"
)

// Own is the folder dir opened for app as one of its own, as Config and Data open theirs: for mankit's packages whose API names a file by its path, such as settings.Set and the split's width, so what they write goes through the port too (ADR-S22).
// Unlike Config and Data it changes nothing it did not make: a folder that is there keeps its mode and what is in it, one made is 0700, and each file it writes is this user's alone (0600). A folder that is another user's, or a link, is refused.
func Own(app App, dir string) (*Scope, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, problem.FailAt(problem.TypeFailed, "", "", fmt.Sprintf("the folder %s cannot be found: %v", dir, err))
	}
	if err := disk.Mine(abs); err != nil {
		return nil, diskFail(err, "", fmt.Sprintf("%s's folder %s", app.ID, abs))
	}
	return open(app, abs, true, func(Key) string { return abs })
}

// ownFolder is Own for a folder whose last step names the app, as the folder of an app's file is.
func ownFolder(dir string) (*Scope, error) {
	return Own(App{ID: filepath.Base(filepath.Clean(dir)), Version: version()}, dir)
}

// EditFile changes the one file at path, in its folder opened with Own: edit gets what is there, nil and false when nothing is, and gives back what to keep, nil to remove it. It is written whole, only if the file still holds what edit was given, and read back; what edit gives back unchanged writes nothing.
// A file that is a link is refused, never followed or replaced.
// Inside a change whose fn holds this folder's turn, as when an app's change of its config folder calls settings.Set, the write joins that change instead of waiting for the turn it holds; a folder another scope's change holds is refused at once.
// Joined, it is made with that change or not at all: a nested write is lost when the outer change fails. Only fn's own goroutine joins; a goroutine fn starts waits for the turn like any other, so fn must not wait for it.
func EditFile(path string, edit func(body []byte, there bool) ([]byte, error)) error {
	key, err := ParseKey(filepath.Base(path))
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return problem.FailAt(problem.TypeFailed, "", "", fmt.Sprintf("the folder of %s cannot be found: %v", path, err))
	}
	// Called inside a change that holds this folder's turn, such as settings.Set in that change's fn: the write joins that change, made with it or not at all.
	if x := heldHere(dir); x != nil {
		if !x.s.private || x.s.root != dir {
			return problem.FailAt(problem.TypeRefused, "", path, fmt.Sprintf("%s is in a folder whose turn a change of this process holds, so it cannot be written apart from that change; write it through that change's Tx", path))
		}
		return editIn(x, key, edit)
	}
	s, err := ownFolder(dir)
	if err != nil {
		return err
	}
	defer s.Close()
	// In the folder's own turn, as removing the file is a change to the folder.
	return s.Change(context.Background(), "", "edit "+string(key), func(tx Tx) error { return editIn(tx, key, edit) })
}

// editIn is EditFile's write of key in tx.
func editIn(tx Tx, key Key, edit func(body []byte, there bool) ([]byte, error)) error {
	item, there, err := tx.Get(key)
	if err != nil {
		return err
	}
	body, err := edit(item.Body, there)
	if err != nil {
		return err
	}
	want := Absent()
	if there {
		want = Holds(item.Sum)
	}
	switch {
	case body == nil && !there:
		return nil
	case body == nil:
		return tx.Remove(key, want)
	case there && bytes.Equal(body, item.Body):
		return nil
	}
	return tx.Put(key, body, want)
}

// version is mankit's own version in the running program, written on the tickets of an Own folder.
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, m := range info.Deps {
			if strings.HasPrefix(m.Path, "github.com/tugbayatilla/mankit/") {
				return m.Version
			}
		}
		if strings.HasPrefix(info.Main.Path, "github.com/tugbayatilla/mankit/") && info.Main.Version != "" {
			return info.Main.Version
		}
	}
	return "devel"
}
