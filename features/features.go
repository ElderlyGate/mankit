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

// String is the value as people read it in a message: its name and what it is for.
func (v Value) String() string { return v.Name + " (" + v.Purpose + ")" }

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

// ReasonNeedsValues is the reason of NeedsValues, with the type failed; a program decides by type and reason, never by the message (G-STACK-15).
const ReasonNeedsValues = "feature_needs_values"

// NeedsValues is a feature switched on, or on, without values it needs; Missing names every one.
type NeedsValues struct {
	ID      string
	Missing []Value
}

func (e *NeedsValues) Error() string {
	return fmt.Sprintf("the feature %s needs values that are not set: %s", e.ID, Names(e.Missing))
}

// Names is values as a message lists them.
func Names(values []Value) string {
	names := make([]string, len(values))
	for i, v := range values {
		names[i] = v.String()
	}
	return strings.Join(names, ", ")
}

// Missing is every value of needs that lookup cannot find now; a nil lookup finds none.
func Missing(needs []Value, lookup Lookup) []Value {
	var out []Value
	for _, v := range needs {
		if lookup == nil || !lookup(v.Name) {
			out = append(out, v)
		}
	}
	return out
}

// Store keeps the owner's switches; the app says where. Its funcs are called from every request, so they must be safe for concurrent use.
//
// A feature that starts off is on only while the owner's on is kept, so a list holding one needs On,
// and its Keep(id, false) keeps id in On as well as taking it out of Off; Keep(id, true) takes it out of On.
// An id in both Off and On is off: the owner's off wins.
type Store struct {
	// Off is the ids switched off now.
	Off func() ([]string, error)
	// Keep switches id off, or back on.
	Keep func(id string, off bool) error
	// On is the ids switched on now; needed only for a feature that starts off.
	On func() ([]string, error)
}

// Lookup says whether the value name is set now; the app says where values live. It must be safe for concurrent use.
type Lookup func(name string) bool

// Closer is one more source that can close features, such as public mode or a license; it closes and never opens.
type Closer struct {
	By     string
	Closed func() ([]string, error)
}

// List is an app's features with what can close them: the owner's switch or the feature's default first, then each closer in order.
// Attach and WithLookup come before serving; after that the list is safe for concurrent use.
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

// New is the list all, switched in store, closed further by closers.
func New(all []Feature, store Store, closers ...Closer) *List {
	return &List{all: all, store: store, closers: append([]Closer{{By: Switch, Closed: store.Off}}, closers...)}
}

// WithLookup hands the list how the app finds a value, needed as soon as a feature needs one, before Attach; it returns the list.
func (l *List) WithLookup(lookup Lookup) *List {
	l.lookup = lookup
	return l
}

// wired refuses a list that cannot answer for its features: one that needs values without a lookup, or starts off without a store that keeps on.
func (l *List) wired() error {
	for _, f := range l.all {
		if len(f.Needs) > 0 && l.lookup == nil {
			return fmt.Errorf("features: %s needs values, and the list has no lookup (WithLookup)", f.ID)
		}
		if f.StartsOff && l.store.On == nil {
			return fmt.Errorf("features: %s starts off, and the store keeps no on (Store.On)", f.ID)
		}
	}
	return nil
}

// States is every feature in the list's order: off by the owner's switch, else by its default, else by the first closer that closes it,
// else on, and broken when it misses a value.
func (l *List) States() ([]State, error) {
	if err := l.wired(); err != nil {
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
	var on []string
	if l.store.On != nil {
		var err error
		if on, err = l.store.On(); err != nil {
			return nil, err
		}
	}
	out := make([]State, len(l.all))
	for i, f := range l.all {
		s := State{Feature: f, By: by[f.ID]}
		if s.By != Switch && f.StartsOff && !slices.Contains(on, f.ID) {
			s.By = Default
		}
		s.On = s.By == ""
		if s.On {
			s.Missing = Missing(f.Needs, l.lookup)
		}
		out[i] = s
	}
	return out, nil
}

// Broken is every feature that is on but misses values it needs, with the missing values, for the app to log at start.
func (l *List) Broken() ([]State, error) {
	states, err := l.States()
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(states, func(s State) bool { return !s.Broken() }), nil
}

// Set switches the feature id off, or back on; switching on without every needed value is refused as NeedsValues and nothing is kept,
// and another closer still closes it, since a switch never opens what another closed.
func (l *List) Set(id string, on bool) error {
	f, err := l.feature(id)
	if err != nil {
		return err
	}
	if err := l.wired(); err != nil {
		return err
	}
	if missing := Missing(f.Needs, l.lookup); on && len(missing) > 0 {
		return &NeedsValues{ID: id, Missing: missing}
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
// It refuses a list that cannot answer for its features, so a wrong wiring shows at start.
func (l *List) Attach(id string, part any) error {
	if err := l.Known(id); err != nil {
		return err
	}
	if err := l.wired(); err != nil {
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
