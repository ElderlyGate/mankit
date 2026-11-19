package store

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/tugbayatilla/mankit/v4/internal/disk"
	"github.com/tugbayatilla/mankit/v4/problem"
)

// Command is the name of the CLI the scope's app runs as, which the unfinished_change refusal and store unfinished name: App.Command, else its ID.
func (s *Scope) Command() string { return s.app.command() }

// EveryUnfinished is Unfinished of thing, or with no thing of the folder's own turn and of every thing: those in the scope, and those the folder's marks and this user's journals name, so a mark on a thing gone is found too. Each change once.
func (s *Scope) EveryUnfinished(thing Key) ([]Unfinished, error) {
	first, err := s.Unfinished(thing)
	if err != nil || thing != "" {
		return order(first), err
	}
	var things []Key
	add := func(k Key) {
		if t := k.thing(); !slices.Contains(things, t) {
			things = append(things, t)
		}
	}
	for _, u := range first {
		for _, k := range u.Keys {
			add(k)
		}
	}
	keys, err := s.List("")
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		add(k)
	}
	all := first
	for _, t := range things {
		got, err := s.Unfinished(t)
		if err != nil {
			return nil, err
		}
		all = append(all, got...)
	}
	return order(all), nil
}

// once tells one change from another: a mark, or a left journal, which may share its mark's id.
type once struct {
	left        bool
	id, journal string
}

// order is each change once, those with marks first, then the left journals.
func order(all []Unfinished) []Unfinished {
	var held, left []Unfinished
	seen := map[once]bool{}
	for _, u := range all {
		k := once{u.Left, u.ID, u.Journal}
		if seen[k] {
			continue
		}
		seen[k] = true
		if u.Left {
			left = append(left, shown(u))
		} else {
			held = append(held, shown(u))
		}
	}
	return append(append([]Unfinished{}, held...), left...)
}

// shown is u as an answer gives it: keys a list, never null.
func shown(u Unfinished) Unfinished {
	if u.Keys == nil {
		u.Keys = []Key{}
	}
	return u
}

// Doing is what u's journal on this machine says its change was making, one write a line, read through the safety core as the store reads it; a change with no File has nothing to say here.
func (u Unfinished) Doing() ([]string, error) {
	file := u.File
	r, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return nil, fmt.Errorf("cannot be read: %v", err)
	}
	defer r.Close()
	raw, _, err := disk.Read(r, filepath.Base(file))
	if err != nil {
		return nil, fmt.Errorf("cannot be read: %v", err)
	}
	_, lines, err := parseJournal(raw)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		switch {
		case l.Dir && l.From != "":
			out = append(out, fmt.Sprintf("move the folder %s to %s", l.From, l.Key))
		case l.Dir && l.Gone:
			out = append(out, "take away the folder "+string(l.Key))
		case l.Dir:
			out = append(out, "make the folder "+string(l.Key))
		case l.Before == nil:
			out = append(out, "add "+string(l.Key))
		case l.After == nil:
			out = append(out, "remove "+string(l.Key))
		default:
			out = append(out, "rewrite "+string(l.Key))
		}
	}
	return out, nil
}

// ParseThing is s as a thing, a key's first step, the way the unfinished_change refusal names it.
func ParseThing(s string) (Key, error) {
	k, err := ParseKey(s)
	if err != nil {
		return "", err
	}
	if k == "" || k.thing() != k {
		return "", problem.FailAt(problem.TypeRefused, "", s, fmt.Sprintf("%q is not a thing: a thing is a key's first step, such as %s", s, k.thing()))
	}
	return k, nil
}
