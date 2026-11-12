// Package prefs is what an app keeps beside mankit's settings: asking in the confirm styles of its config.yaml, and where its files are.
package prefs

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v2/ask"
)

// UseDefault, given as a style, removes the type, so its default holds.
const UseDefault = "default"

// Asker asks before a change, in the style resolved for its type, and saves never in the app's config when the user asks to stop asking.
type Asker struct {
	Config  func() string                                                   // the app's config.yaml, where a remembered answer is saved
	Read    func() string                                                   // where the styles are read until Migrate ran, e.g. home.Current; nil is Config
	Change  func(*cobra.Command, ask.Action, ask.Style) (ask.Answer, error) // nil is ask.Change
	Migrate func() error                                                    // copies the app's old files once, after the yes and before anything is written; nil for none
}

// Ask asks before cmd makes a: the app's style for its type, else a's default; a no is an ask.ErrRefused and writes nothing.
// A dry run (--dry-run) asks nothing and writes nothing, as the change it is about is not made.
func (k Asker) Ask(cmd *cobra.Command, a ask.Action) error {
	if dryRun(cmd) {
		return nil
	}
	style, _, err := resolve(k.read(), a)
	if err != nil {
		return err
	}
	change := k.Change
	if change == nil {
		change = ask.Change
	}
	answer, err := change(cmd, a, style)
	if err != nil {
		return err
	}
	if k.Migrate != nil {
		if err := k.Migrate(); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: old files not copied: %v\n", err)
		}
	}
	if !answer.Remember {
		return nil
	}
	path := k.Config()
	if err := ask.Remember(path, a.Type); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s will still ask: %v\n", a.Type, err)
		return nil
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s no longer asks (set in %s)\n", a.Type, path)
	return nil
}

// dryRun reports whether cmd runs with --dry-run, so it changes nothing.
func dryRun(cmd *cobra.Command) bool {
	f := cmd.Flag("dry-run")
	return f != nil && f.Value.String() == "true"
}

// read is the file the styles are read from now.
func (k Asker) read() string {
	if k.Read != nil {
		return k.Read()
	}
	return k.Config()
}

// Row is one type's style in use, where it comes from, and how a change to the system itself of that type asks, when it differs.
type Row struct {
	Type   ask.Type   `json:"type" yaml:"type"`
	Style  ask.Style  `json:"style" yaml:"style"`
	Source ask.Source `json:"source" yaml:"source"`
	Note   string     `json:"note,omitempty" yaml:"note,omitempty"`
}

// Rows is the style in use for each of types: the app's config at path, else the default; system are the types some verb of which changes the system itself.
func Rows(path string, types, system []ask.Type) ([]Row, error) {
	rows := make([]Row, 0, len(types))
	for _, t := range types {
		s, src, err := resolve(path, ask.Action{Type: t})
		if err != nil {
			return nil, err
		}
		rows = append(rows, Row{Type: t, Style: s, Source: src, Note: systemNote(slices.Contains(system, t), s, src)})
	}
	return rows, nil
}

// systemNote is how a change to the system itself asks when the type's style does not hold for it: by default, and over a never.
func systemNote(system bool, s ask.Style, src ask.Source) string {
	switch {
	case !system:
		return ""
	case s == ask.Never:
		return "a change to the system itself still asks for the name"
	case src == ask.FromDefault:
		return "a change to the system itself asks for the name"
	}
	return ""
}

// text is the style as a table shows it, with its note.
func (r Row) text() string {
	if r.Note == "" {
		return string(r.Style)
	}
	return string(r.Style) + " — " + r.Note
}

// resolve is the style in use for a and where it comes from.
func resolve(path string, a ask.Action) (ask.Style, ask.Source, error) {
	product, err := ask.Load(path)
	if err != nil {
		return "", "", err
	}
	s, src := ask.Resolve(a, product)
	return s, src, nil
}
