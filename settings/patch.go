package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/tugbayatilla/mankit/theme"
)

// errByHand is the end of every refusal to patch a value in place.
const errByHand = "change it in the file by hand"

// bom is the UTF-8 byte order mark, kept but left out of the column math.
var bom = []byte("\xef\xbb\xbf")

// patch is b with top-level key set to value ("" removes its line); every other byte stays.
func patch(b []byte, key, value string) ([]byte, error) {
	if bytes.HasPrefix(b, bom) {
		out, err := patch(b[len(bom):], key, value)
		if err != nil {
			return nil, err
		}
		return join(bom, out), nil
	}
	root, err := topMapping(b)
	if err != nil {
		return nil, err
	}
	var k, v *yaml.Node
	for i := 0; root != nil && i+1 < len(root.Content); i += 2 {
		if c := root.Content[i]; c.Kind == yaml.ScalarNode && c.Value == key {
			if k != nil {
				return nil, fmt.Errorf("%s is in the file twice; %s", key, errByHand)
			}
			k, v = c, root.Content[i+1]
		}
	}
	flow := root != nil && root.Style&yaml.FlowStyle != 0
	if k == nil {
		switch {
		case value == "":
			return b, nil
		case flow:
			return nil, fmt.Errorf("the keys are written in {braces}; %s", errByHand)
		}
		return appendKey(b, key, value), nil
	}
	switch {
	case v.Kind == yaml.AliasNode || k.Anchor != "" || v.Anchor != "":
		return nil, fmt.Errorf("%s has an anchor or alias; %s", key, errByHand)
	case v.Kind != yaml.ScalarNode:
		return nil, fmt.Errorf("%s is not a single value; %s", key, errByHand)
	case v.Style&(yaml.LiteralStyle|yaml.FoldedStyle|yaml.TaggedStyle) != 0:
		return nil, fmt.Errorf("%s is a block or tagged value; %s", key, errByHand)
	}
	start, end, next := lineBounds(b, k.Line)
	from, to, err := valueSpan(b[start:end], k, v)
	if err != nil {
		return nil, err
	}
	if value == "" {
		if flow || othersOnLine(root, k) {
			return nil, fmt.Errorf("%s shares its line with other keys; %s", key, errByHand)
		}
		return join(b[:start], b[next:]), nil
	}
	style := v.Style
	if flow && strings.ContainsAny(value, ",[]{}") {
		style = yaml.SingleQuotedStyle // a bare comma or bracket would end the value
	}
	text := []byte(quote(value, style))
	if from == to {
		text = append([]byte(" "), text...) // after the colon of an empty value
	}
	return join(b[:start+from], text, b[start+to:]), nil
}

// othersOnLine reports whether a key or value other than k's pair starts on k's line.
func othersOnLine(root, k *yaml.Node) bool {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i] != k && (root.Content[i].Line == k.Line || root.Content[i+1].Line == k.Line) {
			return true
		}
	}
	return false
}

// topMapping is the top-level mapping of b, nil when b has no keys; more than one document is refused.
func topMapping(b []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var doc yaml.Node
	if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the file has more than one YAML document; %s", errByHand)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("expected keys at the top")
	}
	return doc.Content[0], nil
}

// appendKey adds "key: value" as the last line in the file's line ending, keeping a missing final newline missing.
func appendKey(b []byte, key, value string) []byte {
	line, nl := key+": "+quote(value, 0), "\n"
	if i := bytes.IndexByte(b, '\n'); i > 0 && b[i-1] == '\r' {
		nl = "\r\n"
	}
	switch {
	case len(b) == 0:
		return []byte(line + nl)
	case b[len(b)-1] == '\n':
		return join(b, []byte(line+nl))
	}
	return join(b, []byte(nl+line))
}

// lineBounds is where line n (from 1) starts, ends before its line break, and where the next one starts.
func lineBounds(b []byte, n int) (start, end, next int) {
	for i := 1; i < n; i++ {
		start += bytes.IndexByte(b[start:], '\n') + 1
	}
	next = len(b)
	if i := bytes.IndexByte(b[start:], '\n'); i >= 0 {
		next = start + i + 1
	}
	end = next
	if end > start && b[end-1] == '\n' {
		end--
	}
	if end > start && b[end-1] == '\r' {
		end--
	}
	return start, end, next
}

// valueSpan is where v's text sits on k's line; an empty value is the point after the colon.
func valueSpan(line []byte, k, v *yaml.Node) (from, to int, err error) {
	refuse := fmt.Errorf("%s does not sit on one line; %s", k.Value, errByHand)
	if v.Tag == "!!null" && v.Value == "" {
		ks := column(line, k.Column)
		ke := scalarEnd(line, ks, k)
		if ke < 0 || source(line[ks:ke], k.Style) != k.Value {
			return 0, 0, refuse
		}
		colon := ke + len(line[ke:]) - len(bytes.TrimLeft(line[ke:], " \t"))
		if colon >= len(line) || line[colon] != ':' {
			return 0, 0, refuse
		}
		return colon + 1, colon + 1, nil
	}
	if v.Line != k.Line {
		return 0, 0, refuse
	}
	from = column(line, v.Column)
	to = scalarEnd(line, from, v)
	if to < 0 || source(line[from:to], v.Style) != v.Value {
		return 0, 0, refuse
	}
	return from, to, nil
}

// column is the byte offset of a 1-based character column of line.
func column(line []byte, col int) int {
	i := 0
	for c := 1; c < col && i < len(line); c++ {
		_, size := utf8.DecodeRune(line[i:])
		i += size
	}
	return i
}

// scalarEnd is where the scalar n starting at line[start] ends, -1 when not on this line.
func scalarEnd(line []byte, start int, n *yaml.Node) int {
	switch {
	case n.Style&yaml.DoubleQuotedStyle != 0:
		for i := start + 1; i < len(line); i++ {
			switch line[i] {
			case '\\':
				i++
			case '"':
				return i + 1
			}
		}
		return -1
	case n.Style&yaml.SingleQuotedStyle != 0:
		for i := start + 1; i < len(line); i++ {
			if line[i] != '\'' {
				continue
			}
			if i+1 < len(line) && line[i+1] == '\'' {
				i++
				continue
			}
			return i + 1
		}
		return -1
	}
	if start+len(n.Value) <= len(line) && string(line[start:start+len(n.Value)]) == n.Value {
		return start + len(n.Value)
	}
	return -1
}

// source is the value a scalar's text stands for.
func source(text []byte, style yaml.Style) string {
	if style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) == 0 {
		return string(text)
	}
	var s string
	if err := yaml.Unmarshal(text, &s); err != nil {
		return "\x00" // never a value
	}
	return s
}

// quote writes value in style when it can, else plain when that reads back as the same string, else single-quoted.
func quote(value string, style yaml.Style) string {
	switch {
	case style&yaml.DoubleQuotedStyle != 0:
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
	case style&yaml.SingleQuotedStyle == 0 && plain(value):
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// plain reports whether value written bare reads back as the same string.
func plain(value string) bool {
	var m map[string]yaml.Node
	if err := yaml.Unmarshal([]byte("k: "+value+"\n"), &m); err != nil || len(m) != 1 {
		return false
	}
	n := m["k"]
	return n.Kind == yaml.ScalarNode && n.Tag == "!!str" && n.Style == 0 && n.Value == value
}

// verify parses old and out the way the apps do: key reads back as value and every other key is unchanged.
func verify(old, out []byte, key, value string) error {
	var before, m map[string]any
	if err := yaml.Unmarshal(old, &before); err != nil {
		return err
	}
	if err := yaml.Unmarshal(out, &m); err != nil {
		return fmt.Errorf("the change would break the file (%v); %s", err, errByHand)
	}
	delete(before, key)
	after := maps.Clone(m)
	delete(after, key)
	if !reflect.DeepEqual(before, after) && len(before)+len(after) > 0 {
		return fmt.Errorf("the change would alter other keys; %s", errByHand)
	}
	var c theme.Config
	if err := yaml.Unmarshal(out, &c); err != nil {
		return fmt.Errorf("the change would break the colours (%v); %s", err, errByHand)
	}
	got, ok := m[key]
	if value == "" && ok || value != "" && got != value {
		return fmt.Errorf("%s would not read back as %q; %s", key, value, errByHand)
	}
	return nil
}

// target is the file path names after following symlinks, even to a target that does not exist yet.
func target(path string) (string, error) {
	for range 40 {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			return real, nil
		}
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			return path, nil
		}
		link, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(path), link)
		}
		path = link
	}
	return "", fmt.Errorf("%s: too many symlinks", path)
}

// join joins parts into a new slice.
func join(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

// writeFile replaces path's real file through a synced temp file and a rename, keeping its mode.
func writeFile(path string, b []byte) error {
	mode := fs.FileMode(0o644)
	path, err := target(path)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync() // best effort: the rename survives a crash
		d.Close()
	}
	return nil
}
