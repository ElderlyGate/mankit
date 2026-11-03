package home_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tugbayatilla/mankit/home"
)

// env points HOME and XDG_CONFIG_HOME at temp dirs and returns HOME.
func env(t *testing.T, xdg string) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	return h
}

func TestPaths(t *testing.T) {
	xdg := t.TempDir()
	tests := []struct {
		name, configEnv, dataEnv string
		config, data             func(home string) string
	}{
		{"XDG folders", filepath.Join(xdg, "config"), filepath.Join(xdg, "data"),
			func(string) string { return filepath.Join(xdg, "config", "elderlygate") },
			func(string) string { return filepath.Join(xdg, "data", "elderlygate") }},
		{"no XDG folders", "", "",
			func(h string) string { return filepath.Join(h, ".config", "elderlygate") },
			func(h string) string { return filepath.Join(h, ".local", "share", "elderlygate") }},
		{"relative XDG folders are ignored", "rel", "rel",
			func(h string) string { return filepath.Join(h, ".config", "elderlygate") },
			func(h string) string { return filepath.Join(h, ".local", "share", "elderlygate") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := env(t, tt.configEnv)
			t.Setenv("XDG_DATA_HOME", tt.dataEnv)
			config, data := tt.config(h), tt.data(h)
			dir, _ := home.Dir()
			product, _ := home.Product("gitman")
			file, _ := home.File("gitman", "split")
			dataDir, _ := home.Data()
			productData, _ := home.ProductData("control-room")
			got := []string{dir, product, file, dataDir, productData}
			want := []string{config, filepath.Join(config, "gitman"),
				filepath.Join(config, "gitman", "split"), data, filepath.Join(data, "control-room")}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("got %q, want %q", got[i], want[i])
				}
			}
		})
	}
}

// oldFile writes body to <system config dir>/<id>/<name>, where the apps kept their files before.
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

func TestFileOnlyNamesThePath(t *testing.T) {
	env(t, t.TempDir())
	oldFile(t, "gitman", "config.yaml", "theme: nord\n")
	path, err := home.File("gitman", "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("File copied or made a file: %v", err)
	}
}

func TestMigrateCopiesTheOldFileOnceAndKeepsIt(t *testing.T) {
	env(t, t.TempDir())
	old := oldFile(t, "gitman", "config.yaml", "theme: nord\n")
	if err := home.Migrate("gitman", "config.yaml"); err != nil {
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
	if err := home.Migrate("gitman", "config.yaml"); err != nil || read(t, path) != "theme: nord\n" {
		t.Errorf("the old file was copied again over the new one (%v)", err)
	}
}

func TestMigrateNeverBringsBackADeletedFile(t *testing.T) {
	env(t, t.TempDir())
	oldFile(t, "gitman", "split", "40\n")
	if err := home.Migrate("gitman", "split"); err != nil {
		t.Fatal(err)
	}
	path, _ := home.File("gitman", "split")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := home.Migrate("gitman", "split"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the deleted file came back: %v", err)
	}
}

func TestMigrateWithNoOldFileMakesNothing(t *testing.T) {
	env(t, t.TempDir())
	if err := home.Migrate("gitman", "split"); err != nil {
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
	if err := home.Migrate("gitman", "split"); err != nil || read(t, path) != "40\n" {
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
	if err := home.CopyOnce(filepath.Join(dir, "old", "github.json"), to); err != nil {
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
	if err := home.CopyOnce(filepath.Join(dir, "old"), to); err != nil {
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
	if err := home.CopyOnce(filepath.Join(dir, "old"), filepath.Join(dir, "new")); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "new")) != "new" {
		t.Fatal("an existing file was replaced")
	}
}

func TestCopyOnceWithNothingToCopy(t *testing.T) {
	dir := t.TempDir()
	to := filepath.Join(dir, "new")
	if err := home.CopyOnce(filepath.Join(dir, "missing"), to); err != nil {
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
	if err := home.Migrate("gitman", "config.yaml"); err != nil {
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
