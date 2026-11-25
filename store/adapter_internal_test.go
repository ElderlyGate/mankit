package store

import (
	"errors"
	"testing"

	"go.naywee.com/mankit/v5/features"
)

// other stands for an enterprise adapter's module: it is picked only through the feature it is attached to.
type other struct{ files }

func (o *other) open(s *Scope) (adapter, error) {
	a, err := openFiles(s)
	if err != nil {
		return nil, err
	}
	o.files = *a.(*files)
	return o, nil
}

func listOf(t *testing.T, off bool, set bool) *features.List {
	t.Helper()
	l := features.New([]features.Feature{{ID: "store-other", Title: "other", Covers: "scopes kept elsewhere", Needs: []features.Value{{Name: "OTHER_URL", Purpose: "where"}}}},
		features.Store{
			Off:  func() ([]string, error) { return map[bool][]string{true: {"store-other"}}[off], nil },
			Keep: func(string, bool) error { return nil },
		}).WithLookup(func(string) bool { return set })
	if err := l.Attach("store-other", &other{}); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestTheAdapterIsPickedInOnePlaceThroughTheFeatureList(t *testing.T) {
	s := &Scope{root: t.TempDir()}
	if a, err := pick(nil, s); err != nil || a.(*files) == nil {
		t.Fatalf("no feature list: %T %v, want files", a, err)
	}
	if a, err := pick(listOf(t, true, true), s); err != nil {
		t.Fatal(err)
	} else if _, ok := a.(*files); !ok {
		t.Fatalf("the feature off: %T, want files", a)
	}
	if a, err := pick(listOf(t, false, true), s); err != nil {
		t.Fatal(err)
	} else if _, ok := a.(*other); !ok {
		t.Fatalf("the feature on: %T, want its module", a)
	}
	var nv *features.NeedsValues
	if a, err := pick(listOf(t, false, false), s); !errors.As(err, &nv) || a != nil {
		t.Fatalf("the feature on without its values: %T %v, want refused, never files", a, err)
	}
}
