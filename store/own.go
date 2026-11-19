package store

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/tugbayatilla/mankit/v4/problem"
)

// Own is the folder dir opened as an own folder, private to this user, as Config and Data open theirs: for mankit's packages whose API names a file by its path, such as settings.Set and the split's width, so what they write goes through the port too (ADR-S22).
// The folder's last step names the app on its tickets.
func Own(dir string) (*Scope, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, problem.FailAt(problem.TypeFailed, "", "", fmt.Sprintf("the folder %s cannot be found: %v", dir, err))
	}
	return ownAt(App{ID: filepath.Base(abs), Version: version()}, abs)
}

// EditFile changes the one file at path, in its folder opened with Own: edit gets what is there, nil and false when nothing is, and gives back what to keep, nil to remove it. It is written whole, only if the file still holds what edit was given, and read back; what edit gives back unchanged writes nothing.
// A file that is a link is refused, never followed or replaced.
func EditFile(path string, edit func(body []byte, there bool) ([]byte, error)) error {
	s, err := Own(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer s.Close()
	key, err := ParseKey(filepath.Base(path))
	if err != nil {
		return err
	}
	// In the folder's own turn, as removing the file is a change to the folder.
	return s.Change(context.Background(), "", "edit "+string(key), func(tx Tx) error {
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
	})
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
