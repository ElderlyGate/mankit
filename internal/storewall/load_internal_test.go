package storewall

import (
	"os"
	"path/filepath"
	"testing"
)

// A line without why is refused, naming the file and line; :tests allows a package's test files only.
func TestExceptions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	put := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("write install\n")
	if _, err := load(dir); err == nil {
		t.Fatal("a line without why was taken")
	}
	put("# comment\nwrite ...:tests fixtures\nencode export/... the user's export\n")
	e, err := load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		what, pkg  string
		test, want bool
	}{
		{write, "example.com/app/serve", true, true},
		{write, "example.com/app/serve", false, false},
		{encode, "example.com/app/export", false, true},
		{encode, "example.com/app/export/yaml", false, true},
		{encode, "example.com/app/exporter", false, false},
		{write, "example.com/app/export", false, false},
	} {
		if got := e.allow(c.what, c.pkg, c.test); got != c.want {
			t.Errorf("allow(%s, %s, test %v) = %v, want %v", c.what, c.pkg, c.test, got, c.want)
		}
	}
}
