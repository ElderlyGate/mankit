package licenses

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/tugbayatilla/mankit/v4/store"
)

// A license that names lines, none of them this app's, says so, never an empty list of lines.
func TestALicenseNamingNoLineOfTheAppSaysSo(t *testing.T) {
	f := Found{product: "designer", Taken: []Taken{{License: License{ID: "acme-kanban", Lines: map[string][]int{"kanban": {1}}}, Active: true, Elsewhere: true}}}
	if got, want := f.says(verdict{state: StateFree}, "Designer", "Designer 1"), "The license acme-kanban names no line of Designer"; got != want {
		t.Fatalf("says %q, want %q", got, want)
	}
}

// failing is a Tx whose Get fails with err.
type failing struct {
	store.Tx
	err error
}

func (f failing) Get(store.Key) (store.Item, bool, error) { return store.Item{}, false, f.err }

// A choice the store cannot read names nothing removed only when it is no regular file or one it may not read; any other error is returned (G-CODE-6).
func TestUnchooseReturnsAnErrorItCannotExplain(t *testing.T) {
	boom := errors.New("the disk failed")
	dir := t.TempDir()
	readable := filepath.Join(dir, "readable")
	if err := os.WriteFile(readable, []byte("acme-2026\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "locked")
	if err := os.WriteFile(locked, []byte("acme-2026\n"), 0); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]error{readable: boom, filepath.Join(dir, "gone"): boom, pipe: nil, locked: nil} {
		if err := unchoose(failing{err: boom}, path, nil); !errors.Is(err, want) && (want != nil || err != nil) {
			t.Fatalf("%s: err = %v, want %v", filepath.Base(path), err, want)
		}
	}
}

func TestAnIDKeptForOldFilesIsNotTaken(t *testing.T) {
	for _, id := range []string{oldID(PlaceOrg), oldID(PlaceApp)} {
		l := License{ID: id, For: "ada@acme.example", KeyID: "test-2026"}
		if err := l.checkCommon(); !errors.Is(err, ErrNotTaken) {
			t.Errorf("checkCommon(%q) = %v, want ErrNotTaken", id, err)
		}
	}
}
