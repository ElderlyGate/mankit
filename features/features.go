// Package features is an app's list of features, the parts it can run without and so can switch off, with the values each needs and what closes each; it knows no app, so the list, where switches are kept and how values are found are handed in.
package features

import (
	"fmt"
	"slices"
	"strings"
)

// Feature is one part of an app that can be switched off, what it covers, the values it needs and whether it starts off.
type Feature struct {
	ID        string  `json:"id" yaml:"id"`
	Title     string  `json:"title" yaml:"title"`
	Covers    string  `json:"covers" yaml:"covers"`
	Needs     []Value `json:"needs,omitempty" yaml:"needs,omitempty"`
	StartsOff bool    `json:"startsOff,omitempty" yaml:"startsOff,omitempty"`
}

// Value is one value a feature needs, such as an environment variable or a settings row, and what it is for.
type Value struct {
	Name    string `json:"name" yaml:"name"`
	Purpose string `json:"purpose" yaml:"purpose"`
}

// State is a feature, whether it is on, what keeps it off, and for one that is on, the values it needs and cannot find.
type State struct {
	Feature `yaml:",inline"`
	On      bool    `json:"on" yaml:"on"`
	By      string  `json:"by,omitempty" yaml:"by,omitempty"`
	Missing []Value `json:"missing,omitempty" yaml:"missing,omitempty"`
}

// Broken is a feature that is on but cannot run, since values it needs are missing.
func (s State) Broken() bool { return s.On && len(s.Missing) > 0 }

// Switch is the closer the owner sets with `<app> disable feature`.
const Switch = "switch"

// Default is what keeps off a feature that starts off and that the owner has not switched on.
const Default = "default"

// CodeNeedsValues is the code of NeedsValues; a program decides by the code, never by the message (G-STACK-15).
const CodeNeedsValues = "feature_needs_values"

// NeedsValues is a feature switched on, or on, without values it needs; Missing names every one.
type NeedsValues struct {
	ID      string
	Missing []Value
}

func (e *NeedsValues) Error() string {
	names := make([]string, len(e.Missing))
	for i, v := range e.Missing {
		names[i] = fmt.Sprintf("%s (%s)", v.Name, v.Purpose)
	}
	return fmt.Sprintf("the feature %s needs values that are not set: %s", e.ID, strings.Join(names, ", "))
}

// Store keeps the owner's switches; the app says where. It holds on as well as off, so a feature that starts off can be switched on.
type Store struct {
	// Switched is each id the owner switched, true for on and false for off; an id not in it is at its default.
	Switched func() (map[string]bool, error)
	// Keep switches id on or off.
	Keep func(id string, on bool) error
}

// Lookup says whether the value name is set now; the app says where values live. Nil finds none.
type Lookup func(name string) bool

// Closer is one more source that can close features, such as public mode or a license; it closes and never opens.
type Closer struct {
	By     string
	Closed func() ([]string, error)
}

// List is an app's features with what can close them: the owner's switch or the feature's default first, then each closer in order.
type List struct {
	all     []Feature
	store   Store
	lookup  Lookup
	closers []Closer
	parts   map[string]any
}

// Part is the module attached to a feature, for the app's doors to reach while the feature is on.
type Part struct {
	ID   string
	Part any
}

// New is the list all, switched in store, its needed values found by lookup, closed further by closers.
func New(all []Feature, store Store, lookup Lookup, closers ...Closer) *List {
	return &List{all: all, store: store, lookup: lookup, closers: closers}
}

// States is every feature in the list's order: off by the owner's switch or its default, else by the first closer that closes it, else on, and broken when it misses a value.
func (l *List) States() ([]State, error) {
	switched, err := l.store.Switched()
	if err != nil {
		return nil, err
	}
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
		s := State{Feature: f}
		on, set := switched[f.ID]
		switch {
		case set && !on:
			s.By = Switch
		case !set && f.StartsOff:
			s.By = Default
		default:
			s.By = by[f.ID]
		}
		s.On = s.By == ""
		if s.On {
			s.Missing = l.missing(f)
		}
		out[i] = s
	}
	return out, nil
}

// missing is every value f needs that lookup cannot find now.
func (l *List) missing(f Feature) []Value {
	var out []Value
	for _, v := range f.Needs {
		if l.lookup == nil || !l.lookup(v.Name) {
			out = append(out, v)
		}
	}
	return out
}

// Broken is every feature that is on but misses values it needs, with the missing values, for the app to log at start.
func (l *List) Broken() ([]State, error) {
	states, err := l.States()
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(states, func(s State) bool { return !s.Broken() }), nil
}

// Set switches the feature id on or off; switching on without every needed value is refused as NeedsValues and nothing is kept,
// and another closer still closes it, since a switch never opens what another closed.
func (l *List) Set(id string, on bool) error {
	f, err := l.feature(id)
	if err != nil {
		return err
	}
	if missing := l.missing(f); on && len(missing) > 0 {
		return &NeedsValues{ID: id, Missing: missing}
	}
	if err := l.store.Keep(id, on); err != nil {
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
	_, err := l.feature(id)
	return err
}

// feature is the feature id, or the refusal Known gives.
func (l *List) feature(id string) (Feature, error) {
	if i := slices.IndexFunc(l.all, func(f Feature) bool { return f.ID == id }); i >= 0 {
		return l.all[i], nil
	}
	ids := make([]string, len(l.all))
	for i, f := range l.all {
		ids[i] = f.ID
	}
	return Feature{}, fmt.Errorf("unknown feature %q; the features are %s", id, strings.Join(ids, ", "))
}

// On reports whether the feature id is on now, for a part of the app that asks before each use; on but broken, it is not, and the error is NeedsValues.
func (l *List) On(id string) (bool, error) {
	if err := l.Known(id); err != nil {
		return false, err
	}
	states, err := l.States()
	if err != nil {
		return false, err
	}
	s := states[slices.IndexFunc(states, func(s State) bool { return s.ID == id })]
	if s.Broken() {
		return false, &NeedsValues{ID: id, Missing: s.Missing}
	}
	return s.On, nil
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

// Parts is every attached module in the list's order, on, off or broken: the door that uses one asks On before each use.
func (l *List) Parts() []Part {
	var out []Part
	for _, f := range l.all {
		if p, ok := l.parts[f.ID]; ok {
			out = append(out, Part{ID: f.ID, Part: p})
		}
	}
	return out
}
