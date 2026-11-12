package ask_test

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tugbayatilla/mankit/v2/ask"
)

func TestLoadAMissingFileSetsNothing(t *testing.T) {
	got, err := ask.Load(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil || len(got) != 0 {
		t.Fatalf("Load = %v, %v; want nothing set", got, err)
	}
}

func TestRememberSavesNeverAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("theme: nord # mine\nconfirm:\n  move: name\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ask.Remember(path, ask.Delete); err != nil {
		t.Fatal(err)
	}
	got, err := ask.Load(path)
	if want := map[ask.Type]ask.Style{ask.Move: ask.Name, ask.Delete: ask.Never}; err != nil || !maps.Equal(got, want) {
		t.Fatalf("Load = %v, %v; want %v", got, err, want)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "theme: nord # mine") {
		t.Errorf("the other keys changed:\n%s", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", fi.Mode().Perm())
	}
}

func TestSetEmptyRemovesTheTypeAndAnEmptySection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := ask.Set(path, ask.Delete, ask.Name); err != nil {
		t.Fatal(err)
	}
	if err := ask.Set(path, ask.Delete, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "confirm") {
		t.Fatalf("file = %q, want no confirm section", b)
	}
}

func TestSetAndLoadRefuseWhatIsNotAStyle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := ask.Set(path, ask.Delete, "always"); err == nil {
		t.Error("Set took an unknown style")
	}
	if err := ask.Set(path, "drop", ask.Never); err == nil {
		t.Error("Set took an unknown type")
	}
	if err := os.WriteFile(path, []byte("confirm: {delete: always}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ask.Load(path); err == nil || !strings.Contains(err.Error(), "confirm.delete") {
		t.Errorf("Load = %v, want an error naming confirm.delete", err)
	}
}
