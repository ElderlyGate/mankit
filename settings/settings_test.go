package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tugbayatilla/mankit/settings"
	"github.com/tugbayatilla/mankit/theme"
)

var prefix = settings.Key{Name: "prefix", Help: "free text"}

var keys = settings.Keys("app", prefix, settings.Key{Name: "github", Default: "https", Values: []string{"https", "ssh"}})

func row(t *testing.T, path, key string) settings.Row {
	t.Helper()
	rows, err := settings.Load(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("no row %s", key)
	return settings.Row{}
}

func TestPathTakesADirOverride(t *testing.T) {
	if got := settings.Path("app", "/x"); got != filepath.Join("/x", "app", "config.yaml") {
		t.Fatal(got)
	}
	dir, _ := os.UserConfigDir()
	if got := settings.Path("app", ""); got != filepath.Join(dir, "app", "config.yaml") {
		t.Fatal(got)
	}
}

func TestLoadMissingFileIsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if r := row(t, path, "theme"); r.Value != "default" || r.Set {
		t.Fatalf("%+v", r)
	}
	if r := row(t, path, "commands"); r.Value != "0 commands" || r.Set {
		t.Fatalf("%+v", r)
	}
}

func TestSetKeepsCommentsUnknownKeysAndCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := `# yaml-language-server: $schema=x
# my notes
theme: nord # my theme
other: kept # mine
commands:
  - id: a
    label: A
    run: echo a
  - {id: b, label: B, run: echo b}
`
	os.WriteFile(path, []byte(orig), 0o644)
	if err := settings.Set(path, keys, "theme", "gruvbox"); err != nil {
		t.Fatal(err)
	}
	if err := settings.Set(path, keys, "github", "ssh"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{"# yaml-language-server: $schema=x\n# my notes\n", "theme: gruvbox # my theme\n", "other: kept # mine\n", "  - id: a\n    label: A\n    run: echo a\n", "{id: b, label: B, run: echo b}", "github: ssh\n"} {
		if !strings.Contains(s, want) {
			t.Fatalf("lost %q:\n%s", want, s)
		}
	}
	if r := row(t, path, "commands"); r.Value != "2 commands" || !r.Set {
		t.Fatalf("%+v", r)
	}
	if r := row(t, path, "github"); r.Value != "ssh" || !r.Set {
		t.Fatalf("%+v", r)
	}
}

func TestSetEmptyRemovesTheKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("theme: nord\nbackground: dark\n"), 0o644)
	if err := settings.Set(path, keys, "theme", ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "background: dark\n" {
		t.Fatalf("%q", b)
	}
	if r := row(t, path, "theme"); r.Value != "default" || r.Set {
		t.Fatalf("%+v", r)
	}
}

func TestSetMakesTheFileAndFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app", "config.yaml")
	if err := settings.Set(path, keys, "background", "light"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "background: light\n" {
		t.Fatalf("%q", b)
	}
}

func TestSetKeepsAFileOfOnlyComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("# theme: default\n"), 0o644)
	if err := settings.Set(path, keys, "theme", "mono"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "# theme: default\ntheme: mono\n" {
		t.Fatalf("%q", b)
	}
}

func TestSetRefusesBadKeysAndValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, c := range []struct{ key, value, want string }{
		{"colour", "x", "have: theme, background, prefix, github"},
		{"background", "dim", "auto, dark, light"},
		{"theme", "pink", "nord"},
		{"commands", "x", "app commands edit"},
		{"prefix", "a\nb", "control"},
	} {
		err := settings.Set(path, keys, c.key, c.value)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s=%q: %v", c.key, c.value, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("a refused value wrote the file")
	}
}

func TestSetFreeTextKeepsAnyValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, v := range []string{"my repos", "true", "# not a comment", "a: b"} {
		if err := settings.Set(path, keys, "prefix", v); err != nil {
			t.Fatal(err)
		}
		if r := row(t, path, "prefix"); r.Value != v || !r.Set {
			t.Fatalf("%q: %+v", v, r)
		}
	}
}

func TestNextCycles(t *testing.T) {
	bg := settings.Background
	for cur, want := range map[string]string{"": "dark", "auto": "dark", "dark": "light", "light": "auto", "odd": "auto"} {
		if got := settings.Next(bg, cur); got != want {
			t.Fatalf("Next(%q) = %q, want %q", cur, got, want)
		}
	}
	if got := settings.Next(prefix, "x"); got != "x" {
		t.Fatal(got)
	}
}

func TestThemeResolvesFromTheSetFile(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	path := settings.Path("app", t.TempDir())
	if err := settings.Set(path, keys, "theme", "solarized"); err != nil {
		t.Fatal(err)
	}
	settings.Set(path, keys, "background", "light")
	ch, err := theme.ResolveIn(path, "", false)
	if err != nil || ch.Base.Name != "solarized" || ch.Colors != theme.Solarized.Light {
		t.Fatalf("%s %v", ch.Base.Name, err)
	}
}

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func set(t *testing.T, path, key, value, want string) {
	t.Helper()
	if err := settings.Set(path, keys, key, value); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != want {
		t.Fatalf("%s=%q:\ngot  %q\nwant %q", key, value, b, want)
	}
}

func TestRemovingACommentedFirstKeyKeepsItsComments(t *testing.T) {
	path := write(t, "# yaml-language-server: $schema=x\n# the colours\ntheme: nord # mine\nbackground: dark\n")
	set(t, path, "theme", "", "# yaml-language-server: $schema=x\n# the colours\nbackground: dark\n")
}

func TestSetChangesOnlyTheKeysLine(t *testing.T) {
	body := "# top\n\ncommands:\n    - id: a\n      label: A\n\n      run: echo a   # spaced\n\nbackground:   dark    # wide\n\n\nother:    [1,   2]\n"
	path := write(t, body)
	set(t, path, "background", "light", strings.Replace(body, "background:   dark", "background:   light", 1))
}

func TestSetKeepsTheQuotingStyle(t *testing.T) {
	path := write(t, "theme: \"nord\" # dq\nprefix: 'my repos'\n")
	set(t, path, "theme", "gruvbox", "theme: \"gruvbox\" # dq\nprefix: 'my repos'\n")
	set(t, path, "prefix", "it's", "theme: \"gruvbox\" # dq\nprefix: 'it''s'\n")
	set(t, path, "github", "ssh", "theme: \"gruvbox\" # dq\nprefix: 'it''s'\ngithub: ssh\n")
	set(t, path, "github", "", "theme: \"gruvbox\" # dq\nprefix: 'it''s'\n")
}

func TestSetFillsAnEmptyValueAndKeepsAMissingNewline(t *testing.T) {
	path := write(t, "theme:   # pick one\nother: 1")
	set(t, path, "theme", "nord", "theme: nord   # pick one\nother: 1")
	set(t, path, "prefix", "true", "theme: nord   # pick one\nother: 1\nprefix: 'true'")
}

func TestSetKeepsPermissions(t *testing.T) {
	path := write(t, "theme: nord\n")
	os.Chmod(path, 0o600)
	set(t, path, "theme", "mono", "theme: mono\n")
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatal(fi.Mode())
	}
}

func TestSetWritesThroughASymlink(t *testing.T) {
	real := write(t, "theme: nord\n")
	link := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	set(t, link, "theme", "mono", "theme: mono\n")
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a file")
	}
	if b, _ := os.ReadFile(real); string(b) != "theme: mono\n" {
		t.Fatalf("%q", b)
	}
}

func TestSetRefusesWhatItCannotPatchInPlace(t *testing.T) {
	for name, body := range map[string]string{
		"two documents": "theme: nord\n---\ntheme: mono\n",
		"anchor":        "theme: &t nord\n",
		"alias":         "x: &t nord\ntheme: *t\n",
		"duplicate":     "theme: nord\ntheme: mono\n",
		"block":         "theme: |\n  nord\n",
		"next line":     "theme:\n  nord\n",
		"two lines":     "theme: \"no\n  rd\"\n",
		"list":          "theme: [nord]\n",
		"not keys":      "- nord\n",
	} {
		path := write(t, body)
		if err := settings.Set(path, keys, "theme", "mono"); err == nil {
			t.Errorf("%s: patched", name)
		}
		if b, _ := os.ReadFile(path); string(b) != body {
			t.Errorf("%s: the file changed to %q", name, b)
		}
	}
}

func TestNullOrEmptyIsNotSet(t *testing.T) {
	path := write(t, "theme: ~\nbackground: \"\"\ncommands:\n")
	for _, key := range []string{"theme", "background", "commands"} {
		if r := row(t, path, key); r.Set {
			t.Errorf("%+v", r)
		}
	}
}

func TestRowsSayDefaultAndReadOnly(t *testing.T) {
	path := write(t, "")
	if r := row(t, path, "background"); r.Default != "auto" || r.ReadOnly {
		t.Fatalf("%+v", r)
	}
	if r := row(t, path, "commands"); !r.ReadOnly {
		t.Fatalf("%+v", r)
	}
}

func TestKeysCopiesTheValues(t *testing.T) {
	ks := settings.Keys("app")
	ks[1].Values[0] = "changed"
	if settings.Background.Values[0] != "auto" || settings.Keys("app")[1].Values[0] != "auto" {
		t.Fatal("Keys shares Values")
	}
}

func TestSetRefusesToLoseOrChangeOtherKeys(t *testing.T) {
	for name, c := range map[string]struct{ body, key, value string }{
		"remove from braces":      {"{theme: nord, other: 1}\n", "theme", ""},
		"add to braces":           {"{theme: nord}\n", "prefix", "x"},
		"append after open block": {"prefix: |\n  a\n  b", "theme", "nord"},
	} {
		path := write(t, c.body)
		if err := settings.Set(path, keys, c.key, c.value); err == nil {
			t.Errorf("%s: patched", name)
		}
		if b, _ := os.ReadFile(path); string(b) != c.body {
			t.Errorf("%s: the file changed to %q", name, b)
		}
	}
}

func TestSetQuotesFlowCharactersInBraces(t *testing.T) {
	path := write(t, "{theme: nord, prefix: a}\n")
	set(t, path, "prefix", "a, b", "{theme: nord, prefix: 'a, b'}\n")
}

func TestSetRefusesInvisibleCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, v := range []string{"a\u2028b", "a\u2029b", "a\u0085b", "\ufeffa", "a\tb"} {
		if err := settings.Set(path, keys, "prefix", v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
	set(t, path, "prefix", "my repos", "prefix: my repos\n")
}

func TestSetKeepsCRLF(t *testing.T) {
	path := write(t, "theme: nord\r\n")
	set(t, path, "background", "dark", "theme: nord\r\nbackground: dark\r\n")
	set(t, path, "theme", "mono", "theme: mono\r\nbackground: dark\r\n")
}

func TestSetKeepsAByteOrderMark(t *testing.T) {
	path := write(t, "\ufefftheme: nord # c\n")
	set(t, path, "theme", "mono", "\ufefftheme: mono # c\n")
	set(t, path, "theme", "", "\ufeff")
}

func TestSetWritesThroughABrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real", "config.yaml")
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(filepath.Join("real", "config.yaml"), link); err != nil {
		t.Skip(err)
	}
	set(t, link, "theme", "mono", "theme: mono\n")
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced by a file")
	}
	if b, _ := os.ReadFile(real); string(b) != "theme: mono\n" {
		t.Fatalf("%q", b)
	}
}
