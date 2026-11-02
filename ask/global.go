package ask

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/tugbayatilla/mankit/home"
	"github.com/tugbayatilla/mankit/internal/configfile"
)

// confirmKey is the section of the global file that holds a style per type.
const confirmKey = "confirm"

// Global reads the styles set in the global file (home.Global); a missing file or section sets nothing.
func Global() (map[Type]Style, error) {
	path, doc, err := readGlobal()
	if err != nil {
		return nil, err
	}
	set := map[Type]Style{}
	confirm := lookup(doc.Content[0], confirmKey)
	if confirm == nil || confirm.Tag == "!!null" {
		return set, nil
	}
	if confirm.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: %s is not a map of type: style", path, confirmKey)
	}
	for i := 0; i < len(confirm.Content); i += 2 {
		k, v := confirm.Content[i].Value, confirm.Content[i+1].Value
		if !ValidType(k) {
			return nil, fmt.Errorf("%s: %s.%s: %w", path, confirmKey, k, unknownType(k))
		}
		s, err := ParseStyle(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %s.%s: %w", path, confirmKey, k, err)
		}
		set[Type(k)] = s
	}
	return set, nil
}

// SetGlobal writes style s for type t into the global file, keeping every other key, section and comment.
func SetGlobal(t Type, s Style) error {
	if _, err := ParseStyle(string(s)); err != nil {
		return err
	}
	return editGlobal(t, func(confirm *yaml.Node, at int) {
		if at >= 0 {
			confirm.Content[at+1] = scalar(string(s))
			return
		}
		confirm.Content = append(confirm.Content, scalar(string(t)), scalar(string(s)))
	})
}

// ClearGlobal removes type t from the global file, so every app falls back to its own value or the default.
func ClearGlobal(t Type) error {
	return editGlobal(t, func(confirm *yaml.Node, at int) {
		if at >= 0 {
			confirm.Content = slices.Delete(confirm.Content, at, at+2)
		}
	})
}

// editGlobal hands change the confirm section and the index of t's key in it (-1 if absent), then writes the file if it changed.
func editGlobal(t Type, change func(confirm *yaml.Node, at int)) error {
	if !ValidType(string(t)) {
		return unknownType(string(t))
	}
	path, doc, err := readGlobal()
	if err != nil {
		return err
	}
	before, err := encode(doc)
	if err != nil {
		return err
	}
	root := doc.Content[0]
	confirm := lookup(root, confirmKey)
	switch {
	case confirm == nil:
		confirm = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, scalar(confirmKey), confirm)
	case confirm.Tag == "!!null":
		*confirm = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	case confirm.Kind != yaml.MappingNode:
		return fmt.Errorf("%s: %s is not a map of type: style", path, confirmKey)
	}
	at := keyAt(confirm, string(t))
	change(confirm, at)
	if len(confirm.Content) == 0 {
		i := slices.Index(root.Content, confirm)
		root.Content = slices.Delete(root.Content, i-1, i+1)
	}
	after, err := encode(doc)
	if err != nil || bytes.Equal(before, after) {
		return err
	}
	return configfile.WriteFile(path, after)
}

// readGlobal parses the global file into a document whose first node is a mapping; a missing or empty file is an empty one.
func readGlobal() (string, *yaml.Node, error) {
	path, err := home.Global()
	if err != nil {
		return "", nil, err
	}
	doc := &yaml.Node{}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", nil, err
	}
	if err := yaml.Unmarshal(b, doc); err != nil {
		return "", nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return "", nil, fmt.Errorf("%s: not a map of keys", path)
	}
	return path, doc, nil
}

// keyAt is the index of key in mapping m, or -1.
func keyAt(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// lookup is the value of key in mapping m, or nil.
func lookup(m *yaml.Node, key string) *yaml.Node {
	if i := keyAt(m, key); i >= 0 {
		return m.Content[i+1]
	}
	return nil
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func encode(doc *yaml.Node) ([]byte, error) {
	if len(doc.Content[0].Content) == 0 {
		return nil, nil
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return b.Bytes(), enc.Close()
}

func unknownType(s string) error {
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = string(t)
	}
	return fmt.Errorf("unknown type %q: use %s", s, strings.Join(names, ", "))
}
