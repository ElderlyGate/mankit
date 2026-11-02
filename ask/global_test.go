package ask_test

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/tugbayatilla/mankit/ask"
)

// globalDir points XDG_CONFIG_HOME at a temp dir and returns the global file's path in it.
func globalDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return filepath.Join(dir, "elderlygate", "global-config.yaml")
}

// writeGlobal puts body in the global file and returns its path.
func writeGlobal(t *testing.T, body string) string {
	t.Helper()
	path := globalDir(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGlobal(t *testing.T) {
	tests := []struct {
		name    string
		body    *string
		want    map[ask.Type]ask.Style
		wantErr bool
	}{
		{"missing file", nil, map[ask.Type]ask.Style{}, false},
		{"empty file", ptr(""), map[ask.Type]ask.Style{}, false},
		{"no confirm section", ptr("other: 1\n"), map[ask.Type]ask.Style{}, false},
		{"empty confirm section", ptr("confirm:\n"), map[ask.Type]ask.Style{}, false},
		{"styles", ptr("confirm: {delete: name, move: never}\n"), map[ask.Type]ask.Style{ask.Delete: ask.Name, ask.Move: ask.Never}, false},
		{"unknown style", ptr("confirm: {move: always}\n"), nil, true},
		{"unknown type", ptr("confirm: {remove: never}\n"), nil, true},
		{"confirm not a map", ptr("confirm: never\n"), nil, true},
		{"not a map", ptr("- a\n"), nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.body == nil {
				globalDir(t)
			} else {
				writeGlobal(t, *tt.body)
			}
			got, err := ask.Global()
			if (err != nil) != tt.wantErr || !maps.Equal(got, tt.want) {
				t.Fatalf("Global() = %v, %v; want %v, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestSetGlobalMakesTheFileAndFolder(t *testing.T) {
	path := globalDir(t)
	if err := ask.SetGlobal(ask.Move, ask.Never); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "confirm:\n  move: never\n" {
		t.Fatalf("file = %q", got)
	}
}

func TestSetGlobalKeepsTheRestOfTheFile(t *testing.T) {
	path := writeGlobal(t, "# shared by every app\ntheme: nord # mine\nconfirm:\n  delete: name\n  move: yes-no\nother:\n  a: 1\n")
	if err := ask.SetGlobal(ask.Move, ask.Never); err != nil {
		t.Fatal(err)
	}
	if err := ask.SetGlobal(ask.Run, ask.Name); err != nil {
		t.Fatal(err)
	}
	want := "# shared by every app\ntheme: nord # mine\nconfirm:\n  delete: name\n  move: never\n  run: name\nother:\n  a: 1\n"
	if got := readFile(t, path); got != want {
		t.Fatalf("file =\n%s\nwant\n%s", got, want)
	}
}

func TestSetGlobalRefusesUnknownWords(t *testing.T) {
	path := writeGlobal(t, "confirm: {move: never}\n")
	if err := ask.SetGlobal("remove", ask.Never); err == nil {
		t.Error("an unknown type was written")
	}
	if err := ask.SetGlobal(ask.Move, "always"); err == nil {
		t.Error("an unknown style was written")
	}
	if got := readFile(t, path); got != "confirm: {move: never}\n" {
		t.Fatalf("file changed: %q", got)
	}
}

func TestClearGlobal(t *testing.T) {
	path := writeGlobal(t, "theme: nord\nconfirm:\n  delete: name\n  move: never\n")
	if err := ask.ClearGlobal(ask.Delete); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "theme: nord\nconfirm:\n  move: never\n" {
		t.Fatalf("after clearing delete: %q", got)
	}
	if err := ask.ClearGlobal(ask.Move); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "theme: nord\n" {
		t.Fatalf("after clearing the last type: %q", got)
	}
}

func TestClearGlobalOnAMissingFileWritesNothing(t *testing.T) {
	path := globalDir(t)
	if err := ask.ClearGlobal(ask.Move); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a file was made: %v", err)
	}
}
