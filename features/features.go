// Package features is an app's list of switchable features and what closes each; it knows no app, so the list and where switches are kept are handed in.
package features

import (
	"fmt"
	"slices"
	"strings"
)

// Feature is one part of an app that can be closed, and what it covers.
type Feature struct {
	ID     string `json:"id" yaml:"id"`
	Title  string `json:"title" yaml:"title"`
	Covers string `json:"covers" yaml:"covers"`
}

// State is a feature, whether it is on, and when it is not, what closed it.
type State struct {
	Feature `yaml:",inline"`
	On      bool   `json:"on" yaml:"on"`
	By      string `json:"by,omitempty" yaml:"by,omitempty"`
}

// Switch is the closer the owner sets with `<app> disable feature`.
const Switch = "switch"

// Store keeps the switched-off ids; the app says where.
type Store struct {
	// Off is the ids switched off now.
	Off func() ([]string, error)
	// Keep switches id off, or back on.
	Keep func(id string, off bool) error
}

// Closer is one more source that can close features, such as public mode or a license; it closes and never opens.
type Closer struct {
	By     string
	Closed func() ([]string, error)
}

// List is an app's features with what can close them: the owner's switch first, then each closer in order.
type List struct {
	all     []Feature
	store   Store
	closers []Closer
	parts   map[string]any
}

// Part is the module attached to a feature, for the app's doors to reach while the feature is on.
type Part struct {
	ID   string
	Part any
}

// New is the list all, switched in store, closed further by closers.
func New(all []Feature, store Store, closers ...Closer) *List {
	return &List{all: all, store: store, closers: append([]Closer{{By: Switch, Closed: store.Off}}, closers...)}
}

// States is every feature in the list's order, each on unless something closes it; the first closer to close it is named.
func (l *List) States() ([]State, error) {
	by := map[string]string{}
	for _, c := range l.closers {
		ids, err := c.Closed()
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, named := by[id]; !named {
				by[id] = c.By
			}
		}
	}
	out := make([]State, len(l.all))
	for i, f := range l.all {
		out[i] = State{Feature: f, On: by[f.ID] == "", By: by[f.ID]}
	}
	return out, nil
}

// Set switches the feature id off, or back on; another closer still closes it, since a switch never opens what another closed.
func (l *List) Set(id string, on bool) error {
	if err := l.Known(id); err != nil {
		return err
	}
	if err := l.store.Keep(id, !on); err != nil {
		return err
	}
	if m, ok := l.parts[id].(SwitchedOff); ok && !on {
		m.SwitchedOff()
	}
	return nil
}

// SwitchedOff is a module told at once when the owner switches its feature off, so it can let go of what it holds;
// a closer such as a license is not a switch, so the module still asks On before each use.
type SwitchedOff interface {
	SwitchedOff()
}

// Known refuses an id the list does not hold, naming the ones it does.
func (l *List) Known(id string) error {
	if slices.ContainsFunc(l.all, func(f Feature) bool { return f.ID == id }) {
		return nil
	}
	ids := make([]string, len(l.all))
	for i, f := range l.all {
		ids[i] = f.ID
	}
	return fmt.Errorf("unknown feature %q; the features are %s", id, strings.Join(ids, ", "))
}

// On reports whether the feature id is on now, for a part of the app that asks before each use.
func (l *List) On(id string) (bool, error) {
	if err := l.Known(id); err != nil {
		return false, err
	}
	states, err := l.States()
	if err != nil {
		return false, err
	}
	for _, s := range states {
		if s.ID == id {
			return s.On, nil
		}
	}
	return false, nil
}

// Attach hands the list the module that serves feature id, so the rest of the app reaches it only through the list; one per feature.
func (l *List) Attach(id string, part any) error {
	if err := l.Known(id); err != nil {
		return err
	}
	if _, taken := l.parts[id]; taken {
		return fmt.Errorf("feature %q already has its module", id)
	}
	if l.parts == nil {
		l.parts = map[string]any{}
	}
	l.parts[id] = part
	return nil
}

// Parts is every attached module in the list's order, on or off: the door that uses one asks On before each use.
func (l *List) Parts() []Part {
	var out []Part
	for _, f := range l.all {
		if p, ok := l.parts[f.ID]; ok {
			out = append(out, Part{ID: f.ID, Part: p})
		}
	}
	return out
}
