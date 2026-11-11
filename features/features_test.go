package features_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/tugbayatilla/mankit/features"
)

var all = []features.Feature{{ID: "edit", Title: "Edit", Covers: "changes"}, {ID: "export", Title: "Export", Covers: "files"}}

// memory is a store held in the test, as an app's config would hold it.
func memory(off ...string) features.Store {
	return features.Store{
		Off: func() ([]string, error) { return slices.Clone(off), nil },
		Keep: func(id string, shut bool) error {
			off = slices.DeleteFunc(off, func(o string) bool { return o == id })
			if shut {
				off = append(off, id)
			}
			return nil
		},
	}
}

func states(t *testing.T, l *features.List) []features.State {
	t.Helper()
	got, err := l.States()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestEveryFeatureIsOnUntilSomethingClosesIt(t *testing.T) {
	want := []features.State{{Feature: all[0], On: true}, {Feature: all[1], On: true}}
	if got := states(t, features.New(all, memory())); !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %+v", got)
	}
}

func TestTheSwitchClosesAndOpensOnlyWhatItClosed(t *testing.T) {
	public := features.Closer{By: "public", Closed: func() ([]string, error) { return []string{"export"}, nil }}
	l := features.New(all, memory(), public)
	if err := l.Set("edit", false); err != nil {
		t.Fatal(err)
	}
	if err := l.Set("export", true); err != nil {
		t.Fatal(err)
	}
	got := states(t, l)
	if got[0].On || got[0].By != features.Switch || got[1].On || got[1].By != "public" {
		t.Fatalf("states = %+v, want edit off by switch and export still off by public", got)
	}
}

func TestTheSwitchIsNamedFirstWhenSeveralClose(t *testing.T) {
	public := features.Closer{By: "public", Closed: func() ([]string, error) { return []string{"edit"}, nil }}
	if got := states(t, features.New(all, memory("edit"), public)); got[0].By != features.Switch {
		t.Fatalf("edit closed by %q", got[0].By)
	}
}

func TestAnUnknownIdIsRefusedWithTheListAndNothingIsKept(t *testing.T) {
	kept := false
	l := features.New(all, features.Store{Off: func() ([]string, error) { return nil, nil }, Keep: func(string, bool) error { kept = true; return nil }})
	if err := l.Set("nope", false); err == nil || err.Error() != `unknown feature "nope"; the features are edit, export` || kept {
		t.Fatalf("err = %v, kept = %v", err, kept)
	}
}

func TestAStoreThatCannotBeReadIsSaid(t *testing.T) {
	broken := errors.New("cannot read")
	if _, err := features.New(all, features.Store{Off: func() ([]string, error) { return nil, broken }}).States(); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
}
