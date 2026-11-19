package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/store"
)

// env makes a home and an XDG config folder of the test's own.
func env(t *testing.T, xdg string) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	return h
}

// oldFile is a file the app kept before in the system's config folder.
func oldFile(t *testing.T, id, name, body string) string {
	t.Helper()
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrateCopiesTheOldFileOnceAndKeepsIt(t *testing.T) {
	env(t, t.TempDir())
	old := oldFile(t, "gitman", "config.yaml", "theme: nord\n")
	if err := store.Migrate("gitman", "config.yaml"); err != nil {
		t.Fatal(err)
	}
	path, _ := home.File("gitman", "config.yaml")
	if got := read(t, path); got != "theme: nord\n" {
		t.Fatalf("new file = %q", got)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the old file's 0600", fi.Mode().Perm())
	}
	if got := read(t, old); got != "theme: nord\n" {
		t.Errorf("old file changed: %q", got)
	}
	if err := os.WriteFile(old, []byte("theme: mono\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate("gitman", "config.yaml"); err != nil || read(t, path) != "theme: nord\n" {
		t.Errorf("the old file was copied again over the new one (%v)", err)
	}
}

func TestMigrateNeverBringsBackADeletedFile(t *testing.T) {
	env(t, t.TempDir())
	oldFile(t, "gitman", "split", "40\n")
	if err := store.Migrate("gitman", "split"); err != nil {
		t.Fatal(err)
	}
	path, _ := home.File("gitman", "split")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate("gitman", "split"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the deleted file came back: %v", err)
	}
}

func TestMigrateWithNoOldFileMakesNothing(t *testing.T) {
	env(t, t.TempDir())
	if err := store.Migrate("gitman", "split"); err != nil {
		t.Fatal(err)
	}
	path, _ := home.File("gitman", "split")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a file was made: %v", err)
	}
	dir, _ := home.Dir()
	if _, err := os.Stat(filepath.Join(dir, ".migrated", "gitman", "split")); !os.IsNotExist(err) {
		t.Fatalf("a file that was never there is marked as copied: %v", err)
	}
	oldFile(t, "gitman", "split", "40\n")
	if err := store.Migrate("gitman", "split"); err != nil || read(t, path) != "40\n" {
		t.Fatalf("an old file found later is not copied (%v)", err)
	}
}

// tree writes files (path: body) under dir.
func tree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCopyOnceCopiesAFileAndKeepsTheOld(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"old/github.json": "{}"})
	to := filepath.Join(dir, "new", "control-room", "github.json")
	if err := store.CopyOnce(filepath.Join(dir, "old", "github.json"), to); err != nil {
		t.Fatal(err)
	}
	if read(t, to) != "{}" || read(t, filepath.Join(dir, "old", "github.json")) != "{}" {
		t.Fatal("the file was not copied, or the old one changed")
	}
}

func TestCopyOnceCopiesAFolder(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"old/github.json": "{}", "old/cache/a": "1"})
	to := filepath.Join(dir, "new", "control-room")
	if err := store.CopyOnce(filepath.Join(dir, "old"), to); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(to, "github.json")) != "{}" || read(t, filepath.Join(to, "cache", "a")) != "1" {
		t.Fatal("the folder was not copied whole")
	}
	if read(t, filepath.Join(dir, "old", "cache", "a")) != "1" {
		t.Error("the old folder changed")
	}
	if _, err := os.Stat(filepath.Join(to, ".queue")); !os.IsNotExist(err) {
		t.Error("the copy holds the store's .queue")
	}
	// The copy took the turn of its own folder, so nothing is beside it.
	if entries, _ := os.ReadDir(filepath.Join(dir, "new")); len(entries) != 1 {
		t.Errorf("left %d entries beside the copy, want none", len(entries)-1)
	}
}

// The store writes no link (ADR-S22), so a folder holding one is not copied at all, and says which.
func TestCopyOnceRefusesAFolderHoldingALink(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"old/github.json": "{}"})
	if err := os.Symlink("github.json", filepath.Join(dir, "old", "link")); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(dir, "new", "control-room")
	var p *problem.Problem
	if err := store.CopyOnce(filepath.Join(dir, "old"), to); !errors.As(err, &p) || p.Reason != store.ReasonOutside || !strings.Contains(p.Detail, "link") {
		t.Fatalf("a folder with a link = %v, want refused outside", err)
	}
	if _, err := os.Lstat(to); !os.IsNotExist(err) {
		t.Fatal("part of the folder was copied")
	}
}

func TestCopyOnceLeavesAnExistingTarget(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"old": "old", "new": "new"})
	if err := store.CopyOnce(filepath.Join(dir, "old"), filepath.Join(dir, "new")); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "new")) != "new" {
		t.Fatal("an existing file was replaced")
	}
}

func TestCopyOnceWithNothingToCopy(t *testing.T) {
	dir := t.TempDir()
	to := filepath.Join(dir, "new")
	if err := store.CopyOnce(filepath.Join(dir, "missing"), to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(to); !os.IsNotExist(err) {
		t.Fatalf("something was made: %v", err)
	}
}

func TestCurrentReadsTheOldFileInPlaceUntilMigrateCopiedIt(t *testing.T) {
	env(t, t.TempDir())
	path, _ := home.File("gitman", "config.yaml")
	if got, err := home.Current("gitman", "config.yaml"); err != nil || got != path {
		t.Fatalf("no old file: Current = %q (%v), want %q", got, err, path)
	}
	old := oldFile(t, "gitman", "config.yaml", "theme: nord\n")
	if got, _ := home.Current("gitman", "config.yaml"); got != old {
		t.Fatalf("before Migrate: Current = %q, want the old %q", got, old)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Current made a file: %v", err)
	}
	if err := store.Migrate("gitman", "config.yaml"); err != nil {
		t.Fatal(err)
	}
	if got, _ := home.Current("gitman", "config.yaml"); got != path {
		t.Fatalf("after Migrate: Current = %q, want %q", got, path)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got, _ := home.Current("gitman", "config.yaml"); got != path {
		t.Fatalf("a deleted new file brings back the old one: %q", got)
	}
}

// A copied file that may be run stays so (a sample's script, say).
func TestCopyOnceKeepsTheRunBit(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"old/run.sh": "#!/bin/sh\n", "old/data.yml": "a: 1\n"})
	if err := os.Chmod(filepath.Join(dir, "old", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(dir, "new", "sample")
	if err := store.CopyOnce(filepath.Join(dir, "old"), to); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(to, "run.sh")); info.Mode().Perm()&0o100 == 0 {
		t.Errorf("run.sh is %v, want it to run", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Join(to, "data.yml")); info.Mode().Perm()&0o111 != 0 {
		t.Errorf("data.yml is %v, want no run bit", info.Mode().Perm())
	}
	one := filepath.Join(dir, "one", "run.sh")
	if err := store.CopyOnce(filepath.Join(dir, "old", "run.sh"), one); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(one); info.Mode().Perm()&0o100 == 0 {
		t.Errorf("a single file copied is %v, want it to run", info.Mode().Perm())
	}
}

// A copy touches nothing beside its target: the folder it goes into and what is there keep their modes.
func TestCopyOnceLeavesTheFolderAroundItAsItIs(t *testing.T) {
	dir := t.TempDir()
	tree(t, dir, map[string]string{"old/a.json": "{}", "data/other/keep.txt": "k"})
	parent := filepath.Join(dir, "data")
	for _, p := range []string{parent, filepath.Join(parent, "other")} {
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(parent, "other", "keep.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.CopyOnce(filepath.Join(dir, "old"), filepath.Join(parent, "control-room")); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{parent: 0o755, filepath.Join(parent, "other"): 0o755, filepath.Join(parent, "other", "keep.txt"): 0o644} {
		if info, _ := os.Stat(p); info.Mode().Perm() != want {
			t.Errorf("%s is %v, want %v as it was", p, info.Mode().Perm(), want)
		}
	}
	if _, err := os.Stat(filepath.Join(parent, ".queue")); !os.IsNotExist(err) {
		t.Error("a .queue was made beside the copy")
	}
}
