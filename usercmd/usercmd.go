// Package usercmd is the commands a user adds to a dashboard in
// <config dir>/<app>/config.yaml, the one file of each app a person edits:
// each gets a palette entry, a key if it is free, and runs a shell line
// with the selection filled in. The same file holds theme: and background:
// (see the theme package).
//
//	theme: nord
//	commands:
//	  - id: cherry-pick
//	    key: C
//	    label: cherry-pick this commit
//	    view: log
//	    run: git cherry-pick {hash}
//	    confirm: true
//
// Placeholders such as {hash} are filled with shell-quoted values, so a
// name with spaces or quotes stays one argument; never quote them yourself.
// {input} is the answer to the ask question. The same schema serves every
// app; only the views and placeholders differ (see commands.schema.json).
package usercmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Command is one entry of the commands file.
type Command struct {
	ID       string `yaml:"id"`       // unique; also what the favorites file stores
	Key      string `yaml:"key"`      // optional; a key a built-in command has is refused
	Label    string `yaml:"label"`    // shown in the palette and the footer
	View     string `yaml:"view"`     // a view name of the app, or "all" (the default)
	Run      string `yaml:"run"`      // shell line; {name} placeholders are filled in
	Ask      string `yaml:"ask"`      // asks this first; the answer is {input}
	Confirm  bool   `yaml:"confirm"`  // asks "run …?" before running
	Terminal bool   `yaml:"terminal"` // hands over the terminal, for interactive commands
}

type file struct {
	Commands []Command `yaml:"commands"`
	// The other settings of config.yaml (theme, background, …) belong to
	// other packages; commands themselves are still read strictly.
	Other map[string]any `yaml:",inline"`
}

// Path is <user config dir>/<app>/config.yaml, or "" when there is none.
func Path(app string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, app, "config.yaml")
}

// Old is the file commands lived in before they moved into config.yaml.
const Old = "commands.yaml"

// Migrate moves <dir>/commands.yaml into path (config.yaml next to it):
// renamed when there is no config.yaml yet, which keeps its comments, else
// appended when config.yaml has no commands yet. The old file stays as
// commands.yaml.migrated. It reports what it did, "" for nothing.
func Migrate(path string) (string, error) {
	if path == "" || filepath.Base(path) != "config.yaml" {
		return "", nil
	}
	old := filepath.Join(filepath.Dir(path), Old)
	b, err := os.ReadFile(old)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	cur, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Rename(old, path); err != nil {
			return "", err
		}
		return fmt.Sprintf("moved %s to %s", old, path), nil
	case err != nil:
		return "", err
	}
	var f file
	if err := yaml.Unmarshal(cur, &f); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Commands) > 0 {
		return "", nil // config.yaml already has its own commands
	}
	if bytes.Contains(cur, []byte("\ncommands:")) || bytes.HasPrefix(cur, []byte("commands:")) {
		return "", nil // an empty commands: key; leave both files to the user
	}
	merged := append(bytes.TrimRight(cur, "\n"), '\n', '\n')
	merged = append(merged, b...)
	if err := writeFile(path, merged); err != nil {
		return "", err
	}
	if err := os.Rename(old, old+".migrated"); err != nil {
		return "", err
	}
	return fmt.Sprintf("added the commands of %s to %s (the old file is %s.migrated)", old, path, Old), nil
}

// writeFile replaces path through a temp file and a rename.
func writeFile(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Spec is what an app accepts: its view names and its placeholders.
type Spec struct {
	Views        []string // besides "all"
	Placeholders []string // besides "input"
}

var placeholderRe = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)

// Load reads the commands of path, moving an old commands.yaml in first;
// a missing file is no commands. Unknown fields of a command, bad views,
// unknown placeholders and duplicate ids are errors naming the command.
func Load(path string, s Spec) ([]Command, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := Migrate(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var f file
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for i := range f.Commands {
		c := &f.Commands[i]
		if c.View == "" {
			c.View = "all"
		}
		key, err := NormalizeKey(c.Key)
		if err != nil {
			return nil, fmt.Errorf("%s: command %d (%s): %w", path, i+1, c.ID, err)
		}
		c.Key = key
		if err := s.check(*c); err != nil {
			return nil, fmt.Errorf("%s: command %d (%s): %w", path, i+1, c.ID, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("%s: command %d: id %q is used twice", path, i+1, c.ID)
		}
		seen[c.ID] = true
	}
	return f.Commands, nil
}

func (s Spec) check(c Command) error {
	switch {
	case c.ID == "":
		return errors.New("id is missing")
	case strings.ContainsAny(c.ID, " \t\n"):
		return errors.New("id has spaces")
	case c.Label == "":
		return errors.New("label is missing")
	case strings.TrimSpace(c.Run) == "":
		return errors.New("run is missing")
	case c.View != "all" && !slices.Contains(s.Views, c.View):
		return fmt.Errorf("view %q is not one of all, %s", c.View, strings.Join(s.Views, ", "))
	}
	for _, m := range placeholderRe.FindAllStringSubmatch(c.Run, -1) {
		name := m[1]
		if name == "input" {
			if c.Ask == "" {
				return errors.New("{input} needs an ask question")
			}
			continue
		}
		if !slices.Contains(s.Placeholders, name) {
			return fmt.Errorf("{%s} is not a placeholder; use %s", name, strings.Join(append(slices.Clone(s.Placeholders), "input"), ", "))
		}
	}
	return nil
}

// Script fills the placeholders in c.Run from vars, shell-quoted. A
// placeholder without a value is an error that says which one is missing.
func (c Command) Script(vars map[string]string) (string, error) {
	var missing []string
	out := placeholderRe.ReplaceAllStringFunc(c.Run, func(p string) string {
		name := p[1 : len(p)-1]
		v, ok := vars[name]
		if !ok || v == "" {
			missing = append(missing, p)
			return p
		}
		return Quote(v)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%s needs %s; select one first", c.Label, strings.Join(missing, ", "))
	}
	return out, nil
}

var plainRe = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,^~-]+$`)

// Quote makes s one shell word.
func Quote(s string) string {
	if plainRe.MatchString(s) {
		return s
	}
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Shell runs script in dir with the system shell.
func Shell(ctx context.Context, dir, script string) *exec.Cmd {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", script)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", script)
	}
	cmd.Dir = dir
	return cmd
}

// unreachable are keys a terminal cannot tell apart from another key, or
// keeps for itself; ctrl+c always quits the dashboard.
var unreachable = map[string]string{
	"ctrl+c": "ctrl+c always quits",
	"ctrl+i": "the terminal sends ctrl+i as tab",
	"ctrl+m": "the terminal sends ctrl+m as enter",
	"ctrl+[": "the terminal sends ctrl+[ as esc",
	"ctrl+s": "the terminal may keep ctrl+s for flow control",
	"ctrl+q": "the terminal may keep ctrl+q for flow control",
	"ctrl+b": "tmux keeps ctrl+b as its prefix",
}

var named = []string{
	"enter", "tab", "esc", "backspace", "delete", "insert", "home", "end",
	"pgup", "pgdown", "up", "down", "left", "right", "shift+tab",
	"f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12",
}

// NormalizeKey checks k is a key a terminal can send and writes it the way
// bubbletea names it: one character (Z, ?), ctrl+<letter>, alt+<character>,
// or a named key such as f5. "" stays "": the command is palette only.
func NormalizeKey(k string) (string, error) {
	if k == "" {
		return "", nil
	}
	lower := strings.ToLower(k)
	switch {
	case len([]rune(k)) == 1:
		if k == " " {
			return "", errors.New(`key " " is space; built-in commands use it`)
		}
		return k, nil
	case strings.HasPrefix(lower, "ctrl+") && len([]rune(lower)) == 6:
		k = lower // ctrl+G and ctrl+g are the same key
	case strings.HasPrefix(lower, "alt+") && len([]rune(k)) == 5:
		k = "alt+" + k[4:] // alt+X keeps its case: it is alt+shift+x
	case slices.Contains(named, lower):
		k = lower
	default:
		return "", fmt.Errorf("key %q is not a key: use one character, ctrl+<letter>, alt+<character> or one of %s", k, strings.Join(named, ", "))
	}
	if why, ok := unreachable[k]; ok {
		return "", fmt.Errorf("key %q cannot be used: %s", k, why)
	}
	return k, nil
}

// Clash is a user command whose key a built-in already has in its view.
type Clash struct {
	ID, Key string
}

// FreeKeys drops the keys that clash with taken (keys per view, "all" for
// every view) and reports the clashes; the commands stay, palette only.
func FreeKeys(cmds []Command, taken map[string][]string) ([]Command, []Clash) {
	var clashes []Clash
	out := slices.Clone(cmds)
	for i, c := range out {
		if c.Key == "" {
			continue
		}
		busy := slices.Contains(taken["all"], c.Key)
		if c.View == "all" {
			for _, keys := range taken {
				busy = busy || slices.Contains(keys, c.Key)
			}
		} else {
			busy = busy || slices.Contains(taken[c.View], c.Key)
		}
		if busy {
			clashes = append(clashes, Clash{c.ID, c.Key})
			out[i].Key = ""
		}
	}
	return out, clashes
}

// ClashMessage is the status line for clashes; "" without any.
func ClashMessage(clashes []Clash) string {
	if len(clashes) == 0 {
		return ""
	}
	var parts []string
	for _, c := range clashes {
		parts = append(parts, fmt.Sprintf("%s (%s)", c.ID, c.Key))
	}
	return "config.yaml: key already taken, palette only: " + strings.Join(parts, ", ")
}

// themeLines go into a new config.yaml, so the theme setting is easy to find.
const themeLines = `#
# Colours (or pass --theme):
#   theme       default, nord, gruvbox, solarized, high-contrast or mono
#   background  auto, dark or light: which variant of the theme
# theme: default
# background: auto
`

// Init writes the starter commands (and the theme settings, commented out)
// to path when it is missing, and reports whether it wrote. An old
// commands.yaml is moved in first. force replaces an existing file, kept
// first as path + ".bak".
func Init(path string, starter []byte, force bool) (wrote bool, backup string, err error) {
	if path == "" {
		return false, "", errors.New("no config folder on this system")
	}
	if _, err := Migrate(path); err != nil {
		return false, "", err
	}
	if _, err := os.Stat(path); err == nil {
		if !force {
			return false, "", nil
		}
		backup = path + ".bak"
		if err := os.Rename(path, backup); err != nil {
			return false, "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, backup, err
	}
	return true, backup, os.WriteFile(path, withThemeLines(starter), 0o644)
}

// withThemeLines puts themeLines after the starter's first comment block
// line (the schema line), or first when it has none.
func withThemeLines(starter []byte) []byte {
	first, rest, ok := bytes.Cut(starter, []byte("\n"))
	if !ok || !bytes.HasPrefix(first, []byte("# yaml-language-server")) {
		return append([]byte(themeLines[2:]+"\n"), starter...)
	}
	out := append(append([]byte{}, first...), '\n')
	out = append(out, themeLines...)
	return append(out, rest...)
}

// Editor is $VISUAL, else $EDITOR, else vi (notepad on Windows), opening path.
func Editor(path string) *exec.Cmd {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if e := strings.Fields(os.Getenv(env)); len(e) > 0 {
			return exec.Command(e[0], append(e[1:], path)...)
		}
	}
	if runtime.GOOS == "windows" {
		return exec.Command("notepad", path)
	}
	return exec.Command("vi", path)
}
