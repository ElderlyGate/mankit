package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tugbayatilla/mankit/v4/home"
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
	if err := os.Symlink("github.json", filepath.Join(dir, "old", "link")); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(dir, "new", "control-room")
	if err := store.CopyOnce(filepath.Join(dir, "old"), to); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(to, "github.json")) != "{}" || read(t, filepath.Join(to, "cache", "a")) != "1" {
		t.Fatal("the folder was not copied whole")
	}
	if link, err := os.Readlink(filepath.Join(to, "link")); err != nil || link != "github.json" {
		t.Errorf("link = %q, %v", link, err)
	}
	if read(t, filepath.Join(dir, "old", "cache", "a")) != "1" {
		t.Error("the old folder changed")
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "new")); len(entries) != 1 {
		t.Errorf("left %d entries beside the copy, want none", len(entries)-1)
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
