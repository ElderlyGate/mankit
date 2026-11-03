package home

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// A folder or file made at to while CopyOnce staged its copy is kept: the copy fails with fs.ErrExist instead.
func TestRenameNoReplaceKeepsWhatIsThere(t *testing.T) {
	tests := []struct {
		name string
		make func(to string) error
	}{
		{"an empty folder", func(to string) error { return os.Mkdir(to, 0o755) }},
		{"a folder with a file", func(to string) error {
			if err := os.Mkdir(to, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(to, "new"), nil, 0o600)
		}},
		{"a file", func(to string) error { return os.WriteFile(to, nil, 0o600) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			from, to := filepath.Join(dir, "staged"), filepath.Join(dir, "to")
			if err := os.Mkdir(from, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := tt.make(to); err != nil {
				t.Fatal(err)
			}
			before, _ := os.Lstat(to)
			if err := renameNoReplace(from, to); !errors.Is(err, fs.ErrExist) {
				t.Fatalf("err = %v, want fs.ErrExist", err)
			}
			if after, _ := os.Lstat(to); !os.SameFile(before, after) {
				t.Fatal("what was at to was replaced")
			}
		})
	}
}
