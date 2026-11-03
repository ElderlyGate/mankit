// Package settings lists the keys of an app's config.yaml and changes one at a time, leaving the rest of the file as it was.
package settings

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/tugbayatilla/mankit/ask"
	"github.com/tugbayatilla/mankit/internal/configfile"
	"github.com/tugbayatilla/mankit/theme"
)

// Key is one setting of config.yaml.
type Key struct {
	Name     string
	Default  string
	Values   []string // the allowed values; empty is free text
	Help     string
	ReadOnly bool // shown, but changed elsewhere
}

// Row is a key as config.yaml has it now.
type Row struct {
	Key      string   `json:"key" yaml:"key"`
	Value    string   `json:"value" yaml:"value"`
	Set      bool     `json:"set" yaml:"set"` // in the file; else the default
	Default  string   `json:"default" yaml:"default"`
	Values   []string `json:"values,omitempty" yaml:"values,omitempty"`
	ReadOnly bool     `json:"readOnly" yaml:"readOnly"`
	Help     string   `json:"help" yaml:"help"`
}

// Path is <dir>/<app>/config.yaml (dir "" is the user config dir), or "" without a config dir.
func Path(app, dir string) string {
	path, err := configfile.Path(app, dir)
	if err != nil {
		return ""
	}
	return path
}

// Theme and Background are read-only templates of the colour keys; Keys hands out copies.
var (
	Theme      = Key{Name: "theme", Default: "default", Values: theme.Names(), Help: "the colours (--theme wins)"}
	Background = Key{Name: "background", Default: "auto", Values: []string{"auto", "dark", "light"}, Help: "which variant of the theme"}
)

// Commands is the commands: block of the loaded file, shown as a count.
func Commands(app string) Key {
	return Key{Name: "commands", Help: "edit with `" + app + " commands edit`", ReadOnly: true}
}

// Keys is mankit's keys, then the app's own, each with its own copy of Values.
func Keys(app string, own ...Key) []Key {
	keys := append([]Key{Theme, Background, Commands(app)}, own...)
	for i := range keys {
		keys[i].Values = slices.Clone(keys[i].Values)
	}
	return keys
}

// Confirm is the key confirm.<t>, how a change of type t asks; Set and Find know it without being in an app's keys.
func Confirm(t ask.Type) Key {
	return Key{Name: ask.Section + "." + string(t), Default: string(ask.YesNo), Values: []string{string(ask.Name), string(ask.YesNo), string(ask.Never)},
		Help: "how a " + string(t) + " asks (a change to the system itself asks the name unless set)"}
}

// Find is the key named name: one of keys, or confirm.<type>.
func Find(keys []Key, name string) (Key, bool) {
	if i := slices.IndexFunc(keys, func(k Key) bool { return k.Name == name }); i >= 0 {
		return keys[i], true
	}
	if t, ok := strings.CutPrefix(name, ask.Section+"."); ok && ask.ValidType(t) {
		return Confirm(ask.Type(t)), true
	}
	return Key{}, false
}

// Load reads path into one row per key; a missing file is all defaults.
func Load(path string, keys []Key) ([]Row, error) {
	doc, err := read(path)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(keys))
	for _, k := range keys {
		r := Row{Key: k.Name, Value: k.Default, Default: k.Default, Values: slices.Clone(k.Values), ReadOnly: k.ReadOnly, Help: k.Help}
		v := value(doc, k.Name)
		empty := v == nil || v.Kind == yaml.ScalarNode && (v.Tag == "!!null" || v.Value == "")
		switch {
		case k.ReadOnly:
			r.Set = !empty
			r.Value = fmt.Sprintf("%d %s", count(v), k.Name)
		case empty:
		case v.Kind != yaml.ScalarNode:
			return nil, fmt.Errorf("%s: %s is not a single value", path, k.Name)
		default:
			r.Value, r.Set = v.Value, true
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// Set writes value to key in path, changing only that key's line; "" removes the key.
func Set(path string, keys []Key, key, value string) error {
	if path == "" {
		return errors.New("no config folder on this system")
	}
	k, ok := Find(keys, key)
	if !ok {
		return fmt.Errorf("unknown key %q (have: %s, %s.<type>)", key, strings.Join(names(keys), ", "), ask.Section)
	}
	if err := check(k, value); err != nil {
		return err
	}
	if t, ok := strings.CutPrefix(key, ask.Section+"."); ok {
		return ask.Set(path, ask.Type(t), ask.Style(value))
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	out, err := patch(b, key, value)
	if err == nil {
		err = verify(b, out, key, value)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if b != nil && string(out) == string(b) {
		return nil
	}
	if b == nil && len(out) == 0 {
		return nil // removing a key from a missing file
	}
	return configfile.WriteFile(path, out)
}

// Next is the value after current in k's allowed values, wrapping around; free text keeps current.
func Next(k Key, current string) string {
	if len(k.Values) == 0 {
		return current
	}
	if current == "" {
		current = k.Default
	}
	i := slices.Index(k.Values, current)
	return k.Values[(i+1)%len(k.Values)]
}

// check refuses a value key does not allow, naming the allowed ones.
func check(k Key, value string) error {
	switch {
	case k.ReadOnly:
		return fmt.Errorf("%s cannot be set here: %s", k.Name, k.Help)
	case value == "":
		return nil
	case len(k.Values) > 0 && !slices.Contains(k.Values, value):
		return fmt.Errorf("%s: %q is not one of %s", k.Name, value, strings.Join(k.Values, ", "))
	case strings.IndexFunc(value, func(r rune) bool { return r != ' ' && !unicode.IsGraphic(r) }) >= 0:
		return fmt.Errorf("%s: the value has a control or invisible character", k.Name)
	}
	return nil
}

// names is the keys Set accepts.
func names(keys []Key) []string {
	var out []string
	for _, k := range keys {
		if !k.ReadOnly {
			out = append(out, k.Name)
		}
	}
	return out
}

// read is config.yaml as a node tree, nil for a missing file or one without keys.
func read(path string) (*yaml.Node, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: expected keys at the top", path)
	}
	return &doc, nil
}

// value is the node of key, nil when the file does not have it; confirm.<type> is <type> inside confirm:.
func value(doc *yaml.Node, key string) *yaml.Node {
	if doc == nil {
		return nil
	}
	m := doc.Content[0]
	if t, ok := strings.CutPrefix(key, ask.Section+"."); ok {
		if m = child(m, ask.Section); m == nil || m.Kind != yaml.MappingNode {
			return nil
		}
		key = t
	}
	return child(m, key)
}

// child is the value of key in mapping m, nil when m does not have it.
func child(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// count is the number of entries of a list node.
func count(n *yaml.Node) int {
	if n == nil || n.Kind != yaml.SequenceNode {
		return 0
	}
	return len(n.Content)
}
