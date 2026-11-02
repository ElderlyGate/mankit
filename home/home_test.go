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
		name string
		xdg  string
		base func(home string) string
	}{
		{"XDG_CONFIG_HOME", xdg, func(string) string { return filepath.Join(xdg, "elderlygate") }},
		{"no XDG_CONFIG_HOME", "", func(h string) string { return filepath.Join(h, ".config", "elderlygate") }},
		{"relative XDG_CONFIG_HOME is ignored", "rel", func(h string) string { return filepath.Join(h, ".config", "elderlygate") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := tt.base(env(t, tt.xdg))
			dir, _ := home.Dir()
			global, _ := home.Global()
			product, _ := home.Product("gitman")
			file, _ := home.File("gitman", "split")
			want := []string{base, filepath.Join(base, "global-config.yaml"), filepath.Join(base, "gitman"), filepath.Join(base, "gitman", "split")}
			for i, got := range []string{dir, global, product, file} {
				if got != want[i] {
					t.Errorf("got %q, want %q", got, want[i])
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

func TestFileCopiesTheOldFileOnceAndKeepsIt(t *testing.T) {
	env(t, t.TempDir())
	old := oldFile(t, "gitman", "config.yaml", "theme: nord\n")
	path, err := home.File("gitman", "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
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
	if path, _ = home.File("gitman", "config.yaml"); read(t, path) != "theme: nord\n" {
		t.Error("the old file was copied again over the new one")
	}
}

func TestFileWithNoOldFileMakesNothing(t *testing.T) {
	env(t, t.TempDir())
	path, err := home.File("gitman", "split")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a file was made: %v", err)
	}
}
