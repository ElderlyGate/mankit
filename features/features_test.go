package features_test

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/tugbayatilla/mankit/v2/features"
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

func TestOnSaysWhetherOneFeatureIsOnNow(t *testing.T) {
	l := features.New(all, memory("export"))
	edit, err := l.On("edit")
	if err != nil || !edit {
		t.Fatalf("edit on = %v, %v", edit, err)
	}
	if export, err := l.On("export"); err != nil || export {
		t.Fatalf("export on = %v, %v", export, err)
	}
	if _, err := l.On("nope"); err == nil {
		t.Fatal("an unknown id is on")
	}
}

func TestAModuleIsAttachedToItsFeatureOnce(t *testing.T) {
	l := features.New(all, memory())
	if err := l.Attach("export", "exporter"); err != nil {
		t.Fatal(err)
	}
	if got := l.Parts(); !reflect.DeepEqual(got, []features.Part{{ID: "export", Part: "exporter"}}) {
		t.Fatalf("parts = %+v", got)
	}
	if err := l.Attach("export", "another"); err == nil {
		t.Fatal("a second module was attached to export")
	}
	if err := l.Attach("nope", "x"); err == nil {
		t.Fatal("a module was attached to an unknown feature")
	}
}

// held is a module that lets go when its feature is switched off.
type held struct{ off int }

func (h *held) SwitchedOff() { h.off++ }

func TestSwitchingOffTellsTheModuleAtOnce(t *testing.T) {
	l := features.New(all, memory())
	m := &held{}
	if err := l.Attach("export", m); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		id string
		on bool
	}{{"export", true}, {"edit", false}, {"export", false}} {
		if err := l.Set(step.id, step.on); err != nil {
			t.Fatal(err)
		}
	}
	if m.off != 1 {
		t.Fatalf("the module was told %d times, want once", m.off)
	}
}

// keeping is a store that keeps on as well as off, as an app's config would, safe for concurrent use as serving needs.
type keeping struct {
	mu      sync.Mutex
	off, on []string
}

func (k *keeping) store() features.Store {
	read := func(ids *[]string) func() ([]string, error) {
		return func() ([]string, error) {
			k.mu.Lock()
			defer k.mu.Unlock()
			return slices.Clone(*ids), nil
		}
	}
	return features.Store{
		Off: read(&k.off),
		On:  read(&k.on),
		Keep: func(id string, off bool) error {
			k.mu.Lock()
			defer k.mu.Unlock()
			k.off = slices.DeleteFunc(k.off, func(o string) bool { return o == id })
			k.on = slices.DeleteFunc(k.on, func(o string) bool { return o == id })
			if off {
				k.off = append(k.off, id)
			} else {
				k.on = append(k.on, id)
			}
			return nil
		},
	}
}

// needy is a feature that needs two values and starts off, beside one that needs a value and starts on.
var needy = []features.Feature{
	{ID: "telemetry", Title: "Telemetry", Covers: "traces", StartsOff: true, Needs: []features.Value{
		{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Purpose: "where traces go"},
		{Name: "OTEL_SERVICE_NAME", Purpose: "the name traces carry"},
	}},
	{ID: "mail", Title: "Mail", Covers: "notices", Needs: []features.Value{{Name: "SMTP_HOST", Purpose: "the mail server"}}},
	{ID: "edit", Title: "Edit", Covers: "changes"},
}

// set is a lookup over values held in the test, as an app's environment would hold them.
func set(values map[string]bool) features.Lookup {
	var mu sync.Mutex
	return func(name string) bool {
		mu.Lock()
		defer mu.Unlock()
		return values[name]
	}
}

var everyValue = map[string]bool{"OTEL_EXPORTER_OTLP_ENDPOINT": true, "OTEL_SERVICE_NAME": true, "SMTP_HOST": true}

func TestAFeatureThatStartsOffIsOffUntilSwitchedOnAndTheOnIsKept(t *testing.T) {
	k := &keeping{}
	l := features.New(needy, k.store()).WithLookup(set(everyValue))
	if got := states(t, l); got[0].On || got[0].By != features.Default || !got[1].On || !got[2].On {
		t.Fatalf("states = %+v, want telemetry off by default and the rest on", got)
	}
	if err := l.Set("telemetry", true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(k.on, []string{"telemetry"}) {
		t.Fatalf("kept on %v, want telemetry", k.on)
	}
	fresh := features.New(needy, k.store()).WithLookup(set(everyValue))
	if got := states(t, fresh); !got[0].On || got[0].Broken() {
		t.Fatalf("telemetry read by a fresh list = %+v, want on", got[0])
	}
	if err := fresh.Set("telemetry", false); err != nil {
		t.Fatal(err)
	}
	if got := states(t, fresh); got[0].On || got[0].By != features.Switch {
		t.Fatalf("telemetry = %+v, want off by switch", got[0])
	}
}

func TestSwitchingOnWithoutItsValuesIsRefusedNamingEveryOneAndNothingIsKept(t *testing.T) {
	kept := false
	store := features.Store{
		Off:  func() ([]string, error) { return nil, nil },
		On:   func() ([]string, error) { return nil, nil },
		Keep: func(string, bool) error { kept = true; return nil },
	}
	l := features.New(needy, store).WithLookup(set(map[string]bool{}))
	err := l.Set("telemetry", true)
	var nv *features.NeedsValues
	if !errors.As(err, &nv) || nv.ID != "telemetry" || !reflect.DeepEqual(nv.Missing, needy[0].Needs) || kept {
		t.Fatalf("err = %v, kept = %v, want both values named and nothing kept", err, kept)
	}
	if want := "the feature telemetry needs values that are not set: OTEL_EXPORTER_OTLP_ENDPOINT (where traces go), OTEL_SERVICE_NAME (the name traces carry)"; err.Error() != want {
		t.Fatalf("message = %q", err)
	}
	if err := l.Set("telemetry", false); err != nil || !kept {
		t.Fatalf("switching off was refused: %v", err)
	}
}

func TestSwitchingOnSucceedsOnceItsValuesAreSet(t *testing.T) {
	values := map[string]bool{"OTEL_EXPORTER_OTLP_ENDPOINT": true}
	l := features.New(needy, (&keeping{}).store()).WithLookup(set(values))
	if err := l.Set("telemetry", true); err == nil {
		t.Fatal("switched on with a value missing")
	}
	values["OTEL_SERVICE_NAME"] = true
	if err := l.Set("telemetry", true); err != nil {
		t.Fatal(err)
	}
	if on, err := l.On("telemetry"); err != nil || !on {
		t.Fatalf("telemetry on = %v, %v", on, err)
	}
}

func TestAFeatureOnThatLosesAValueIsBrokenAloneAndSaysWhatItMisses(t *testing.T) {
	values := map[string]bool{"SMTP_HOST": true}
	l := features.New(needy, (&keeping{}).store()).WithLookup(set(values))
	if broken, err := l.Broken(); err != nil || len(broken) != 0 {
		t.Fatalf("broken = %+v, %v with every value set", broken, err)
	}
	delete(values, "SMTP_HOST")
	broken, err := l.Broken()
	if err != nil || len(broken) != 1 || broken[0].ID != "mail" || !reflect.DeepEqual(broken[0].Missing, needy[1].Needs) {
		t.Fatalf("broken = %+v, %v, want mail missing SMTP_HOST", broken, err)
	}
	on, err := l.On("mail")
	var nv *features.NeedsValues
	if on || !errors.As(err, &nv) || !reflect.DeepEqual(nv.Missing, needy[1].Needs) {
		t.Fatalf("mail on = %v, %v, want refused naming SMTP_HOST", on, err)
	}
	if on, err := l.On("edit"); err != nil || !on {
		t.Fatalf("edit on = %v, %v, want on beside the broken mail", on, err)
	}
	if got := states(t, l); got[0].Missing != nil {
		t.Fatalf("telemetry = %+v: a feature that is off is not broken", got[0])
	}
}

func TestAFeatureClosedAndMissingAValueIsOffByTheCloserNotBroken(t *testing.T) {
	public := features.Closer{By: "public", Closed: func() ([]string, error) { return []string{"mail"}, nil }}
	l := features.New(needy, (&keeping{}).store(), public).WithLookup(set(map[string]bool{}))
	if got := states(t, l); got[1].On || got[1].By != "public" || got[1].Missing != nil {
		t.Fatalf("mail = %+v, want off by public and not broken", got[1])
	}
	if on, err := l.On("mail"); on || err != nil {
		t.Fatalf("mail on = %v, %v, want off and no error", on, err)
	}
}

func TestACloserStillClosesAFeatureSwitchedOnWithItsValues(t *testing.T) {
	license := features.Closer{By: "license", Closed: func() ([]string, error) { return []string{"telemetry"}, nil }}
	l := features.New(needy, (&keeping{}).store(), license).WithLookup(set(everyValue))
	if err := l.Set("telemetry", true); err != nil {
		t.Fatal(err)
	}
	if got := states(t, l); got[0].On || got[0].By != "license" {
		t.Fatalf("telemetry = %+v, want off by license", got[0])
	}
}

func TestBrokenSaysAStoreThatCannotBeRead(t *testing.T) {
	broken := errors.New("cannot read")
	if _, err := features.New(all, features.Store{Off: func() ([]string, error) { return nil, broken }}).Broken(); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
}

func TestAListThatCannotAnswerForItsFeaturesIsRefusedAtAttach(t *testing.T) {
	if err := features.New(needy[1:], memory()).Attach("mail", "mailer"); err == nil {
		t.Fatal("attached a feature that needs a value to a list without a lookup")
	}
	if err := features.New(needy[:1], memory()).WithLookup(set(everyValue)).Attach("telemetry", "tracer"); err == nil {
		t.Fatal("attached a feature that starts off to a store that keeps no on")
	}
}

func TestSwitchingAndAskingAtOnceIsSafe(t *testing.T) {
	l := features.New(needy, (&keeping{}).store()).WithLookup(set(everyValue))
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if err := l.Set("mail", i%2 == 0); err != nil {
					t.Error(err)
				}
				if _, err := l.On("mail"); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestAFeatureInBothOffAndOnIsOff(t *testing.T) {
	k := &keeping{off: []string{"telemetry"}, on: []string{"telemetry"}}
	if got := states(t, features.New(needy, k.store()).WithLookup(set(everyValue))); got[0].On || got[0].By != features.Switch {
		t.Fatalf("telemetry = %+v, want off by switch", got[0])
	}
}

func TestAFeatureThatStartsOffIsOffByItsDefaultBeforeACloser(t *testing.T) {
	license := features.Closer{By: "license", Closed: func() ([]string, error) { return []string{"telemetry"}, nil }}
	if got := states(t, features.New(needy, (&keeping{}).store(), license).WithLookup(set(everyValue))); got[0].On || got[0].By != features.Default {
		t.Fatalf("telemetry = %+v, want off by default", got[0])
	}
}
