package disk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

func TestWriteFileWritesWholeWithItsModeAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "record.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, path); got != "new\n" {
		t.Errorf("body = %q", got)
	}
	if runtime.GOOS != "windows" && modeOf(t, path) != 0o600 {
		t.Errorf("mode = %v, want 0600 whatever was there", modeOf(t, path))
	}
	noTemp(t, dir)
}

func TestWriteFileRefusesALink(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "real"), filepath.Join(dir, "token")
	if err := os.WriteFile(real, []byte("real\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if err := WriteFile(link, []byte("new\n"), 0o600); !errors.Is(err, ErrLink) {
		t.Fatalf("err = %v, want ErrLink", err)
	}
	if got := readString(t, real); got != "real\n" {
		t.Errorf("the link's target was written: %q", got)
	}
}

func TestWriteFileNeedsItsFolder(t *testing.T) {
	if err := WriteFile(filepath.Join(t.TempDir(), "missing", "f"), nil, 0o600); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestReplaceMakesFoldersAndANewFileWithMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "config.yaml")
	if err := Replace(path, []byte("theme: nord\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, path); got != "theme: nord\n" {
		t.Errorf("body = %q", got)
	}
	if runtime.GOOS != "windows" && modeOf(t, path) != 0o644 {
		t.Errorf("mode = %v, want 0644", modeOf(t, path))
	}
}

func TestReplaceKeepsTheModeOfTheFileItReplaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix modes")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Replace(path, []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if modeOf(t, path) != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", modeOf(t, path))
	}
	noTemp(t, dir)
}

// A config.yaml kept as a link to the person's own copy stays a link, and the copy is what changes.
func TestReplaceWritesThroughALinkAndKeepsIt(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "config.yaml")
	link := filepath.Join(dir, "app", "config.yaml")
	for _, d := range []string{filepath.Dir(real), filepath.Dir(link)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(real, []byte("a\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if err := Replace(link, []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("the link is gone: %v %v", fi, err)
	}
	if got := readString(t, real); got != "b\n" {
		t.Errorf("the copy holds %q", got)
	}
	if modeOf(t, real) != 0o640 {
		t.Errorf("mode = %v, want 0640 kept", modeOf(t, real))
	}
}

// Many writers at once: one body lands, every writer is fine, and none replaces it.
func TestWriteOnceKeepsTheFirstOfManyAtOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	var wg sync.WaitGroup
	errs := make([]error, 16)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = WriteOnce(path, []byte(strings.Repeat(string(rune('a'+i)), 8)+"\n"), 0o600)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d: %v", i, err)
		}
	}
	got := readString(t, path)
	if len(got) != 9 || strings.Count(got, got[:1]) != 8 {
		t.Errorf("body = %q, want one writer's whole", got)
	}
	if err := WriteOnce(path, []byte("later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if again := readString(t, path); again != got {
		t.Errorf("replaced: %q, want %q", again, got)
	}
	if runtime.GOOS != "windows" && modeOf(t, path) != 0o600 {
		t.Errorf("mode = %v, want 0600", modeOf(t, path))
	}
	noTemp(t, dir)
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
