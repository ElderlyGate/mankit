package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tugbayatilla/mankit/usercmd"
)

const starter = "commands:\n  - {id: hello, key: h, label: say hello, run: echo hi}\n  - {id: quit2, key: q, label: clash, run: echo}\n"

var spec = usercmd.Spec{Views: []string{"log"}}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	c := Command("testapp", []byte(starter), spec, func() map[string][]string { return map[string][]string{"all": {"q"}} })
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs(args)
	err := c.Execute()
	return out.String(), err
}

func TestInitPathCheck(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	path := usercmd.Path("testapp")

	out, _ := run(t, "path")
	if !strings.Contains(out, path) || !strings.Contains(out, "does not exist") {
		t.Fatalf("path: %q", out)
	}
	if _, err := run(t, "check"); err == nil {
		t.Fatal("check without a file fails")
	}
	if out, err := run(t, "init"); err != nil || !strings.Contains(out, "wrote the starter") {
		t.Fatalf("init: %q %v", out, err)
	}
	os.WriteFile(path, []byte("commands: []\n"), 0o644)
	if out, _ := run(t, "init"); !strings.Contains(out, "kept your") {
		t.Fatalf("init keeps an existing file: %q", out)
	}
	if out, _ := run(t, "init", "--force"); !strings.Contains(out, ".bak") {
		t.Fatalf("init --force: %q", out)
	}
	if b, _ := os.ReadFile(path + ".bak"); string(b) != "commands: []\n" {
		t.Fatalf("the old file is the backup: %q", b)
	}
	out, err := run(t, "check")
	if err != nil || !strings.Contains(out, "say hello") || !strings.Contains(out, "quit2 (q)") {
		t.Fatalf("check: %q %v", out, err)
	}
	if filepath.Base(path) != "commands.yaml" {
		t.Fatal(path)
	}
}
