package ask

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/tugbayatilla/mankit/internal/configfile"
)

// Section is the key of an app's config.yaml that holds a style per type: confirm: {delete: name, move: never}.
const Section = "confirm"

// Load reads the confirm: styles of the config file at path; a missing file or section sets nothing.
func Load(path string) (map[Type]Style, error) {
	doc, err := read(path)
	if err != nil {
		return nil, err
	}
	set := map[Type]Style{}
	confirm := lookup(doc.Content[0], Section)
	if confirm == nil || confirm.Tag == "!!null" {
		return set, nil
	}
	if confirm.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: %s is not a map of type: style", path, Section)
	}
	for i := 0; i+1 < len(confirm.Content); i += 2 {
		k, v := confirm.Content[i].Value, confirm.Content[i+1].Value
		if !ValidType(k) {
			return nil, fmt.Errorf("%s: %s.%s is not a type of change", path, Section, k)
		}
		s, err := ParseStyle(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %s.%s: %w", path, Section, k, err)
		}
		set[Type(k)] = s
	}
	return set, nil
}

// Remember saves never for t in the config file at path, after the user asked to stop asking for it.
func Remember(path string, t Type) error { return Set(path, t, Never) }

// Set writes style s for t into path's confirm: section, keeping every other key; an empty s removes t, so its default holds.
func Set(path string, t Type, s Style) error {
	if !ValidType(string(t)) {
		return fmt.Errorf("%q is not a type of change", t)
	}
	if s != "" {
		if _, err := ParseStyle(string(s)); err != nil {
			return err
		}
	}
	doc, err := read(path)
	if err != nil {
		return err
	}
	root := doc.Content[0]
	confirm := lookup(root, Section)
	switch {
	case confirm == nil && s == "":
		return nil
	case confirm == nil:
		confirm = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, scalar(Section), confirm)
	case confirm.Tag == "!!null":
		*confirm = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	case confirm.Kind != yaml.MappingNode:
		return fmt.Errorf("%s: %s is not a map of type: style", path, Section)
	}
	at := keyAt(confirm, string(t))
	switch {
	case s == "" && at >= 0:
		confirm.Content = slices.Delete(confirm.Content, at, at+2)
	case s == "":
	case at >= 0:
		confirm.Content[at+1] = scalar(string(s))
	default:
		confirm.Content = append(confirm.Content, scalar(string(t)), scalar(string(s)))
	}
	if len(confirm.Content) == 0 {
		i := slices.Index(root.Content, confirm)
		root.Content = slices.Delete(root.Content, i-1, i+1)
	}
	var b bytes.Buffer
	if len(root.Content) > 0 {
		enc := yaml.NewEncoder(&b)
		enc.SetIndent(2)
		if err := enc.Encode(doc); err != nil {
			return err
		}
		if err := enc.Close(); err != nil {
			return err
		}
	}
	return configfile.WriteFile(path, b.Bytes())
}

// read parses path into a document whose first node is a mapping; a missing or empty file is an empty one.
func read(path string) (*yaml.Node, error) {
	if path == "" {
		return nil, errors.New("no config folder on this system")
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	doc := &yaml.Node{}
	if err := yaml.Unmarshal(b, doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: expected keys at the top", path)
	}
	return doc, nil
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
