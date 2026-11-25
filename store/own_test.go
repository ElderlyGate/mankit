package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.naywee.com/mankit/v5/problem"
	"go.naywee.com/mankit/v5/store"
)

// Own changes nothing it did not make: a project's folder and what is in it keep their modes, and only the file written is this user's alone.
func TestEditFileTouchesOnlyTheFileItWrites(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "src", "code.go"), "package x\n")
	write(t, filepath.Join(dir, "run.sh"), "#!/bin/sh\n")
	modes := map[string]os.FileMode{dir: 0o755, filepath.Join(dir, "src"): 0o755, filepath.Join(dir, "src", "code.go"): 0o644, filepath.Join(dir, "run.sh"): 0o755}
	for p, m := range modes {
		if err := os.Chmod(p, m); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "config.yaml")
	if err := store.EditFile(path, func([]byte, bool) ([]byte, error) { return []byte("theme: nord\n"), nil }); err != nil {
		t.Fatal(err)
	}
	for p, want := range modes {
		if info, _ := os.Stat(p); info.Mode().Perm() != want {
			t.Errorf("%s is %v, want %v as it was", p, info.Mode().Perm(), want)
		}
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("the file written is %v, want this user's alone", info.Mode().Perm())
	}
	// A folder Own makes is this user's alone.
	made := filepath.Join(dir, "made", "deeper")
	if err := store.EditFile(filepath.Join(made, "split"), func([]byte, bool) ([]byte, error) { return []byte("40\n"), nil }); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(made); info.Mode().Perm() != 0o700 {
		t.Errorf("a folder made is %v, want 0700", info.Mode().Perm())
	}
}

// Own refuses a folder that is a link, and the refusal names the folder's path, not the app's.
func TestOwnRefusesALinkedFolderNamingItsPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	_, err := store.Own(store.App{ID: "acme"}, dir)
	var p *problem.Problem
	if !errors.As(err, &p) || !strings.Contains(p.Detail, "the folder "+dir) || strings.Contains(p.Detail, "acme") {
		t.Fatalf("err = %v", err)
	}
}

// settings.Set and the like called inside a change that holds the folder's turn join that change, made with it, instead of waiting for the turn it holds.
func TestEditFileInsideAChangeOfItsFolderJoinsIt(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Own(store.App{ID: "app", Version: "1.0.0"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(dir, "config.yaml")
	err = s.Change(context.Background(), "", "nested", func(tx store.Tx) error {
		if err := tx.Put("other.yml", []byte("o\n"), store.Absent()); err != nil {
			return err
		}
		return store.EditFile(path, func([]byte, bool) ([]byte, error) { return []byte("theme: mono\n"), nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "theme: mono\n" {
		t.Fatalf("config.yaml %q, want the nested write made with the change", b)
	}
	// A change that fails makes neither.
	_ = s.Change(context.Background(), "", "nested and failing", func(tx store.Tx) error {
		if err := store.EditFile(path, func([]byte, bool) ([]byte, error) { return []byte("theme: nord\n"), nil }); err != nil {
			return err
		}
		return errors.New("stop")
	})
	if b, _ := os.ReadFile(path); string(b) != "theme: mono\n" {
		t.Fatalf("config.yaml %q after a failed change, want it as it was", b)
	}
}

// A change of another scope that holds the same line is refused at once, never waited for.
func TestEditFileInAFolderAnotherScopeHoldsIsRefusedAtOnce(t *testing.T) {
	root := t.TempDir()
	served, err := store.Served(app, root, func(store.Key) string { return root })
	if err != nil {
		t.Fatal(err)
	}
	defer served.Close()
	path := filepath.Join(root, "config.yaml")
	err = served.Change(context.Background(), "", "served", func(store.Tx) error {
		return store.EditFile(path, func([]byte, bool) ([]byte, error) { return []byte("x\n"), nil })
	})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeRefused || !strings.Contains(p.Detail, "turn a change of this process holds") {
		t.Fatalf("got %v, want refused at once", err)
	}
}
