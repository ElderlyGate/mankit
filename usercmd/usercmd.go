// Package usercmd is the commands a user adds to a dashboard in
// <config dir>/<app>/commands.yaml: each gets a palette entry, a key if it
// is free, and runs a shell line with the selection filled in.
//
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
}

// Path is <user config dir>/<app>/commands.yaml, or "" when there is none.
func Path(app string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, app, "commands.yaml")
}

// Spec is what an app accepts: its view names and its placeholders.
type Spec struct {
	Views        []string // besides "all"
	Placeholders []string // besides "input"
}

var placeholderRe = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)

// Load reads path; a missing file is no commands. Unknown fields, bad views,
// unknown placeholders and duplicate ids are errors naming the command.
func Load(path string, s Spec) ([]Command, error) {
	if path == "" {
		return nil, nil
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
	return "commands.yaml: key already taken, palette only: " + strings.Join(parts, ", ")
}
