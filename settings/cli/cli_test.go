package cli_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/tugbayatilla/mankit/settings"
	"github.com/tugbayatilla/mankit/settings/cli"
)

func run(t *testing.T, root *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func app(path string) *cobra.Command {
	root := &cobra.Command{Use: "app"}
	root.AddCommand(&cobra.Command{Use: "get", Short: "the app's own get"})
	cli.AddTo(root, settings.Keys("app"), func() string { return path })
	return root
}

func TestSetThenGetConfigInEveryFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app", "config.yaml")
	if out, err := run(t, app(path), "set", "config", "theme", "nord"); err != nil || !strings.Contains(out, "nord") {
		t.Fatalf("set: %q %v", out, err)
	}

	out, err := run(t, app(path), "get", "config")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if lines[0] != "file: "+path || !strings.HasPrefix(lines[1], "KEY") || !strings.Contains(lines[2], "nord") || !strings.Contains(lines[2], "set") || !strings.Contains(lines[3], "default") {
		t.Fatalf("table:\n%s", out)
	}
	if strings.Index(lines[1], "VALUE") != strings.Index(lines[2], "nord") {
		t.Fatalf("columns not aligned:\n%s", out)
	}

	var rows []settings.Row
	out, _ = run(t, app(path), "get", "config", "-o", "json")
	if err := json.Unmarshal([]byte(out), &rows); err != nil || rows[0].Key != "theme" || rows[0].Value != "nord" || !rows[0].Set || len(rows[0].Values) == 0 {
		t.Fatalf("json: %q %v", out, err)
	}
	rows = nil
	out, _ = run(t, app(path), "get", "config", "-o", "yaml")
	if err := yaml.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 3 || rows[1].Key != "background" || rows[1].Set {
		t.Fatalf("yaml: %q %v", out, err)
	}
	if _, err := run(t, app(path), "get", "config", "-o", "xml"); err == nil {
		t.Fatal("unknown output accepted")
	}
}

func TestSetConfigRefusesABadValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := run(t, app(path), "set", "config", "background", "dim"); err == nil || !strings.Contains(err.Error(), "auto, dark, light") {
		t.Fatal(err)
	}
}

func TestAddToEWithAnExistingConfigCommandFails(t *testing.T) {
	root := &cobra.Command{Use: "app"}
	get := &cobra.Command{Use: "get"}
	get.AddCommand(&cobra.Command{Use: "config"})
	root.AddCommand(get)
	if err := cli.AddToE(root, settings.Keys("app"), func() string { return "" }); err == nil {
		t.Fatal("a second get config was added")
	}
	cli.AddTo(root, settings.Keys("app"), func() string { return "" })
	if n := len(get.Commands()); n != 1 {
		t.Fatalf("get has %d commands", n)
	}
}

func TestAddToLeavesOutATakenShorthand(t *testing.T) {
	root := &cobra.Command{Use: "app"}
	root.PersistentFlags().StringP("org", "o", "", "")
	if err := cli.AddToE(root, settings.Keys("app"), func() string { return "" }); err == nil {
		t.Fatal("-o clash not reported")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	cli.AddTo(root, settings.Keys("app"), func() string { return path })
	out, err := run(t, root, "get", "config", "--output", "json")
	if err != nil || !strings.HasPrefix(out, "[") {
		t.Fatalf("%q %v", out, err)
	}
}
