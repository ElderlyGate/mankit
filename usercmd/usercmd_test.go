package usercmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var spec = Spec{Views: []string{"log", "status"}, Placeholders: []string{"hash", "file"}}

func write(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "commands.yaml")
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	cmds, err := Load(write(t, `
commands:
  - id: pick
    key: C
    label: cherry-pick
    view: log
    run: git cherry-pick {hash}
  - id: stash
    label: stash
    run: git stash push -m {input}
    ask: message
`), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 || cmds[1].View != "all" {
		t.Fatalf("%+v", cmds)
	}
}

func TestLoadRefuses(t *testing.T) {
	cases := map[string]string{
		"unknown field":       "commands:\n  - {id: a, label: a, run: x, colour: red}",
		"bad view":            "commands:\n  - {id: a, label: a, run: x, view: nope}",
		"unknown placeholder": "commands:\n  - {id: a, label: a, run: 'echo {nope}'}",
		"input without ask":   "commands:\n  - {id: a, label: a, run: 'echo {input}'}",
		"duplicate id":        "commands:\n  - {id: a, label: a, run: x}\n  - {id: a, label: b, run: y}",
		"missing run":         "commands:\n  - {id: a, label: a}",
	}
	for name, yaml := range cases {
		if _, err := Load(write(t, yaml), spec); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

func TestMissingOrEmptyFileIsNoCommands(t *testing.T) {
	if cmds, err := Load(filepath.Join(t.TempDir(), "none.yaml"), spec); err != nil || cmds != nil {
		t.Fatal(cmds, err)
	}
	if cmds, err := Load(write(t, ""), spec); err != nil || cmds != nil {
		t.Fatal(cmds, err)
	}
}

func TestScriptQuotesValues(t *testing.T) {
	c := Command{Label: "show", Run: "git show {hash} -- {file}"}
	got, err := c.Script(map[string]string{"hash": "abc123", "file": "a b'; rm -rf x"})
	if err != nil || got != `git show abc123 -- 'a b'\''; rm -rf x'` {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := c.Script(map[string]string{"hash": "abc"}); err == nil || !strings.Contains(err.Error(), "{file}") {
		t.Fatalf("a missing value names the placeholder: %v", err)
	}
}

func TestShellRunsInDir(t *testing.T) {
	dir := t.TempDir()
	out, err := Shell(context.Background(), dir, "pwd").Output()
	if err != nil || filepath.Clean(strings.TrimSpace(string(out))) != filepath.Clean(dir) {
		t.Fatalf("%q %v", out, err)
	}
}

func TestFreeKeysRefusesTakenKeys(t *testing.T) {
	cmds := []Command{
		{ID: "a", Key: "c", View: "log"}, // taken in log
		{ID: "b", Key: "c", View: "status"},
		{ID: "c", Key: "q", View: "status"}, // q is global
		{ID: "d", Key: "z", View: "all"},    // z is free everywhere
		{ID: "e", Key: "s", View: "all"},    // s is taken in status
	}
	got, clashes := FreeKeys(cmds, map[string][]string{"all": {"q"}, "log": {"c"}, "status": {"s"}})
	keys := []string{got[0].Key, got[1].Key, got[2].Key, got[3].Key, got[4].Key}
	if strings.Join(keys, ",") != ",c,,z," || len(clashes) != 3 {
		t.Fatalf("keys %q clashes %v", keys, clashes)
	}
	if !strings.Contains(ClashMessage(clashes), "a (c)") {
		t.Fatal(ClashMessage(clashes))
	}
}

func TestNormalizeKey(t *testing.T) {
	ok := map[string]string{"Z": "Z", "Ctrl+G": "ctrl+g", "alt+X": "alt+X", "F5": "f5", "": "", "?": "?"}
	for in, want := range ok {
		if got, err := NormalizeKey(in); err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"ctrl+c", "CTRL+C", "ctrl+i", "ctrl+m", "ctrl+s", "control+g", "ctrl+gg", " ", "hello"} {
		if _, err := NormalizeKey(bad); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}

func TestLoadRefusesCtrlC(t *testing.T) {
	_, err := Load(write(t, "commands:\n  - {id: a, key: ctrl+c, label: a, run: x}"), spec)
	if err == nil || !strings.Contains(err.Error(), "always quits") {
		t.Fatalf("got %v", err)
	}
}

func TestPathIsConfigYAML(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	t.Setenv("HOME", t.TempDir())
	dir, _ := os.UserConfigDir()
	if got := Path("gitman"); got != filepath.Join(dir, "gitman", "config.yaml") || !strings.HasSuffix(got, filepath.Join("gitman", "config.yaml")) {
		t.Fatal(got)
	}
}

func TestLoadAcceptsOtherSettingsButNotUnknownCommandFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("theme: nord\nbackground: dark\ncommands:\n  - {id: a, label: A, run: echo}\n"), 0o644)
	if cs, err := Load(p, Spec{}); err != nil || len(cs) != 1 {
		t.Fatalf("%v %v", cs, err)
	}
	os.WriteFile(p, []byte("theme: nord\ncommands:\n  - {id: a, label: A, run: echo, colour: red}\n"), 0o644)
	if _, err := Load(p, Spec{}); err == nil {
		t.Fatal("unknown command field accepted")
	}
}

func TestMigrateRenamesWhenThereIsNoConfig(t *testing.T) {
	dir := t.TempDir()
	old := "# my comment\ncommands:\n  - {id: a, label: A, run: echo}\n"
	os.WriteFile(filepath.Join(dir, "commands.yaml"), []byte(old), 0o644)
	cs, err := Load(filepath.Join(dir, "config.yaml"), Spec{})
	if err != nil || len(cs) != 1 {
		t.Fatalf("%v %v", cs, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if string(b) != old {
		t.Fatalf("comments lost: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "commands.yaml")); !os.IsNotExist(err) {
		t.Fatal("old file still there")
	}
}

func TestMigrateAppendsToAConfigWithoutCommands(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfg, []byte("theme: gruvbox\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "commands.yaml"), []byte("commands:\n  - {id: a, label: A, run: echo}\n"), 0o644)
	did, err := Migrate(cfg)
	if err != nil || did == "" {
		t.Fatalf("%q %v", did, err)
	}
	b, _ := os.ReadFile(cfg)
	if !strings.HasPrefix(string(b), "theme: gruvbox\n\ncommands:") {
		t.Fatalf("merged: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "commands.yaml.migrated")); err != nil {
		t.Fatal("no .migrated copy")
	}
	if did, _ := Migrate(cfg); did != "" {
		t.Fatalf("migrated twice: %s", did)
	}
}

func TestMigrateLeavesAConfigWithCommandsAlone(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	mine := "commands:\n  - {id: mine, label: M, run: echo}\n"
	os.WriteFile(cfg, []byte(mine), 0o644)
	os.WriteFile(filepath.Join(dir, "commands.yaml"), []byte("commands:\n  - {id: old, label: O, run: echo}\n"), 0o644)
	if did, err := Migrate(cfg); err != nil || did != "" {
		t.Fatalf("%q %v", did, err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != mine {
		t.Fatalf("changed: %q", b)
	}
}

func TestInitAddsThemeLinesAfterSchemaLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	starter := []byte("# yaml-language-server: $schema=x\n# about\ncommands: []\n")
	if wrote, _, err := Init(p, starter, false); !wrote || err != nil {
		t.Fatal(wrote, err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if !strings.HasPrefix(s, "# yaml-language-server: $schema=x\n#\n# Colours") || !strings.Contains(s, "# theme: default\n") || !strings.HasSuffix(s, "# about\ncommands: []\n") {
		t.Fatalf("%q", s)
	}
}
