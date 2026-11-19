package disk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func modeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// noTemp fails when a temporary file of the core is left in dir.
func noTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), TempMark) {
			t.Errorf("a temporary file is left: %s", e.Name())
		}
	}
}

func TestRemoveTakesAFileAndAGoneOneIsFine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "split")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("still there: %v", err)
	}
	if err := Remove(path); err != nil {
		t.Errorf("removing a gone file: %v", err)
	}
}

func TestJSONIsIndentedByTwoWithANewline(t *testing.T) {
	b, err := JSON(map[string]any{"a": []int{1}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"a\": [\n    1\n  ]\n}\n"; string(b) != want {
		t.Errorf("JSON = %q, want %q", b, want)
	}
	if _, err := JSON(func() {}); err == nil {
		t.Error("a func was encoded")
	}
}

func TestYAMLIsIndentedByTwo(t *testing.T) {
	b, err := YAML(map[string]any{"confirm": map[string]string{"delete": "name"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "confirm:\n  delete: name\n"; string(b) != want {
		t.Errorf("YAML = %q, want %q", b, want)
	}
}
