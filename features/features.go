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
	return l.store.Keep(id, !on)
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
