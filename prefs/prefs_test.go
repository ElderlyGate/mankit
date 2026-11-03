package prefs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/ask"
	"github.com/tugbayatilla/mankit/home"
	settingscli "github.com/tugbayatilla/mankit/settings/cli"
)

// sandbox gives the test its own home and returns the app's config file.
func sandbox(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(h, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(h, ".local", "share"))
	return filepath.Join(h, ".config", "elderlygate", "app", "config.yaml")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, _ := os.ReadFile(path)
	return string(b)
}

func TestTheAppsStyleWinsOverTheDefaultWhichIsYesNoDeleteIncluded(t *testing.T) {
	config := sandbox(t)
	writeFile(t, config, "theme: nord\nconfirm:\n  delete: never\n")
	rows, err := Rows(config, []ask.Type{ask.Delete, ask.Edit, ask.Create}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{{ask.Delete, ask.Never, ask.FromProduct, ""}, {ask.Edit, ask.YesNo, ask.FromDefault, ""}, {ask.Create, ask.YesNo, ask.FromDefault, ""}}
	for i, w := range want {
		if rows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
}

func TestATypeWithChangesToTheSystemSaysTheyAskForTheName(t *testing.T) {
	config := sandbox(t)
	system := []ask.Type{ask.Delete}
	for _, tt := range []struct{ file, note string }{
		{"", "a change to the system itself asks for the name"},
		{"confirm:\n  delete: never\n", "a change to the system itself still asks for the name"},
		{"confirm:\n  delete: yes-no\n", ""},
	} {
		writeFile(t, config, tt.file)
		rows, err := Rows(config, []ask.Type{ask.Delete, ask.Edit}, system)
		if err != nil || rows[0].Note != tt.note || rows[1].Note != "" {
			t.Errorf("%q: notes %q, %q (%v), want %q", tt.file, rows[0].Note, rows[1].Note, err, tt.note)
		}
	}
	root := &cobra.Command{Use: "app"}
	settingscli.AddTo(root, nil, func() string { return config })
	if err := AddTo(root, Options{App: "app", Config: func() string { return config }, Types: []ask.Type{ask.Delete}, System: system,
		Asker: Asker{Config: func() string { return config }}}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, config, "confirm:\n  delete: never\n")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"get", "confirm"})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), "never — a change to the system itself still asks for the name") {
		t.Fatalf("get confirm (%v):\n%s", err, out.String())
	}
}

// command is a cobra command reading in, with --yes.
func command(in string) *cobra.Command {
	c := &cobra.Command{Use: "x"}
	ask.Flag(c)
	c.SetIn(strings.NewReader(in))
	c.SetErr(&bytes.Buffer{})
	return c
}

func TestRememberSavesNeverInTheAppsConfig(t *testing.T) {
	config := sandbox(t)
	var asked ask.Style
	k := Asker{Config: func() string { return config }, Change: func(_ *cobra.Command, _ ask.Action, s ask.Style) (ask.Answer, error) {
		asked = s
		return ask.Answer{Remember: true}, nil
	}}
	if err := k.Ask(command(""), ask.Action{Type: ask.Delete, What: "delete x", Name: "x"}); err != nil {
		t.Fatal(err)
	}
	if asked != ask.YesNo {
		t.Fatalf("asked in %q, want the default yes-no", asked)
	}
	if got := readFile(t, config); got != "confirm:\n  delete: never\n" {
		t.Fatalf("app config:\n%s", got)
	}
}

func TestAChangeToTheSystemAsksForTheNameUnlessTheAppSetsItsType(t *testing.T) {
	config := sandbox(t)
	var asked ask.Style
	k := Asker{Config: func() string { return config }, Change: func(_ *cobra.Command, _ ask.Action, s ask.Style) (ask.Answer, error) {
		asked = s
		return ask.Answer{}, nil
	}}
	uninstall := ask.Action{Type: ask.Delete, What: "uninstall x", Name: "x", System: true}
	if err := k.Ask(command(""), uninstall); err != nil || asked != ask.Name {
		t.Fatalf("asked in %q (%v), want the name", asked, err)
	}
	writeFile(t, config, "confirm:\n  delete: yes-no\n")
	if err := k.Ask(command(""), uninstall); err != nil || asked != ask.YesNo {
		t.Fatalf("asked in %q (%v), want the app's yes-no", asked, err)
	}
	writeFile(t, config, "confirm:\n  delete: never\n")
	if err := k.Ask(command(""), uninstall); err != nil || asked != ask.Name {
		t.Fatalf("asked in %q (%v), want the name: never does not silence a change to the system", asked, err)
	}
}

// oldConfig writes body to the app's config.yaml where it was kept before the org's folder: <system config dir>/app/config.yaml.
func oldConfig(t *testing.T, body string) string {
	t.Helper()
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "app", "config.yaml")
	writeFile(t, path, body)
	return path
}

// homeAsker reads the styles where home.Current says and copies the old config with home.Migrate after the yes.
func homeAsker(config string) Asker {
	return Asker{
		Config:  func() string { return config },
		Read:    func() string { p, _ := home.Current("app", "config.yaml"); return p },
		Migrate: func() error { return home.Migrate("app", "config.yaml") },
	}
}

func TestARefusedChangeCopiesNothingAndWritesNothing(t *testing.T) {
	config := sandbox(t)
	oldConfig(t, "confirm:\n  edit: never\n")
	k := homeAsker(config)
	if err := k.Ask(command(""), ask.Action{Type: ask.Edit, What: "edit x"}); !errors.Is(err, ask.ErrRefused) {
		t.Fatalf("no terminal: %v, want refused", err)
	}
	k.Change = func(*cobra.Command, ask.Action, ask.Style) (ask.Answer, error) {
		return ask.Answer{}, fmt.Errorf("%w: edit x", ask.ErrRefused)
	}
	if err := k.Ask(command("n\n"), ask.Action{Type: ask.Edit, What: "edit x"}); !errors.Is(err, ask.ErrRefused) {
		t.Fatalf("a no: %v, want refused", err)
	}
	if _, err := os.Stat(filepath.Dir(config)); !os.IsNotExist(err) {
		t.Fatalf("a refused change wrote into the app's folder: %v", err)
	}
	dir, _ := home.Dir()
	if _, err := os.Stat(filepath.Join(dir, ".migrated")); !os.IsNotExist(err) {
		t.Fatalf("a refused change left a mark: %v", err)
	}
}

func TestTheOldStyleHoldsUntilTheYesThenTheOldFileIsCopied(t *testing.T) {
	config := sandbox(t)
	oldConfig(t, "confirm:\n  edit: never\n")
	if out, err := cliWith(t, config, homeAsker(config), "get", "confirm"); err != nil || !strings.Contains(out, "never") {
		t.Fatalf("get confirm before the first change shows the old style (%v):\n%s", err, out)
	}
	var asked ask.Style
	k := homeAsker(config)
	k.Change = func(_ *cobra.Command, _ ask.Action, s ask.Style) (ask.Answer, error) {
		if _, err := os.Stat(config); !os.IsNotExist(err) {
			t.Errorf("copied before the yes: %v", err)
		}
		asked = s
		return ask.Answer{}, nil
	}
	if err := k.Ask(command(""), ask.Action{Type: ask.Edit, What: "edit x"}); err != nil || asked != ask.Never {
		t.Fatalf("asked in %q (%v), want the old never", asked, err)
	}
	if got := readFile(t, config); got != "confirm:\n  edit: never\n" {
		t.Fatalf("after the yes the old file is copied; app config:\n%s", got)
	}
}

func TestADryRunAsksNothingAndWritesNothing(t *testing.T) {
	config := sandbox(t)
	oldConfig(t, "confirm:\n  edit: name\n")
	k := homeAsker(config)
	k.Change = func(*cobra.Command, ask.Action, ask.Style) (ask.Answer, error) {
		t.Error("a dry run asked")
		return ask.Answer{Remember: true}, nil
	}
	c := command("")
	c.Flags().Bool("dry-run", true, "")
	if err := k.Ask(c, ask.Action{Type: ask.Edit, What: "edit x"}); err != nil {
		t.Fatal(err)
	}
	if out, err := cliWith(t, config, homeAsker(config), "set", "config", "confirm.delete", "never", "--yes", "--dry-run"); err != nil || !strings.Contains(out, "dry run") {
		t.Fatalf("set config --dry-run (%v):\n%s", err, out)
	}
	dir, _ := home.Dir()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a dry run wrote into the org's config folder: %v", err)
	}
}

func TestWithoutATerminalAChangeIsRefusedUnlessYes(t *testing.T) {
	config := sandbox(t)
	writeFile(t, config, "confirm:\n  edit: never\n")
	k := Asker{Config: func() string { return config }}
	c := command("y\n")
	if err := k.Ask(c, ask.Action{Type: ask.Edit, What: "edit x"}); !errors.Is(err, ask.ErrRefused) {
		t.Fatalf("never still refuses without a terminal: %v", err)
	}
	c.Flags().Set("yes", "true")
	if err := k.Ask(c, ask.Action{Type: ask.Edit, What: "edit x"}); err != nil {
		t.Fatalf("--yes: %v", err)
	}
}

// cli is a root with mankit's settings verbs and AddTo, run with args and no terminal.
func cli(t *testing.T, config string, args ...string) (string, error) {
	t.Helper()
	return cliWith(t, config, Asker{Config: func() string { return config }}, args...)
}

// cliWith is cli with asker.
func cliWith(t *testing.T, config string, asker Asker, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "app"}
	root.PersistentFlags().Bool("dry-run", false, "change nothing")
	settingscli.AddTo(root, nil, func() string { return config })
	if err := AddTo(root, Options{App: "app", Config: func() string { return config }, Types: []ask.Type{ask.Delete, ask.Edit},
		Asker: asker}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestSetConfigWritesTheAppsConfig(t *testing.T) {
	config := sandbox(t)
	if _, err := cli(t, config, "set", "config", "confirm.delete", "yes-no"); !errors.Is(err, ask.ErrRefused) {
		t.Fatalf("set config asks first: %v", err)
	}
	if out, err := cli(t, config, "set", "config", "confirm.delete", "name", "--yes"); err != nil || !strings.Contains(out, "name (product)") {
		t.Fatalf("set: %v\n%s", err, out)
	}
	if got := readFile(t, config); got != "confirm:\n  delete: name\n" {
		t.Fatalf("app config:\n%s", got)
	}
	if _, err := cli(t, config, "set", "config", "confirm.delete", UseDefault, "-y"); err != nil {
		t.Fatal(err)
	}
	var rows []Row
	out, _ := cli(t, config, "get", "confirm", "-o", "json")
	if err := json.Unmarshal([]byte(out), &rows); err != nil || rows[0] != (Row{ask.Delete, ask.YesNo, ask.FromDefault, ""}) || rows[1].Source != ask.FromDefault {
		t.Fatalf("default falls back to the default: %v\n%s", err, out)
	}
}

func TestSetConfigRefusesWhatItCannotUse(t *testing.T) {
	config := sandbox(t)
	for _, args := range [][]string{
		{"confirm.archive", "never"},    // a type this app does not use
		{"confirm.delete", "sometimes"}, // not a style
	} {
		if _, err := cli(t, config, append([]string{"set", "config", "--yes"}, args...)...); err == nil || errors.Is(err, ask.ErrRefused) {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}

func TestGetLocationsShowsTheAppsConfigAndItsData(t *testing.T) {
	config := sandbox(t)
	out, err := cli(t, config, "get", "locations")
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(os.Getenv("XDG_DATA_HOME"), "elderlygate", "app")
	for _, want := range []string{config, data} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "global-config.yaml") {
		t.Errorf("a shared config is shown:\n%s", out)
	}
}
