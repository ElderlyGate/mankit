package licenses

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/problem"
)

// ShowKind is the kind of the `<app> license show` answer; contracts mankit/v3 license-show says what each field of Show means.
const ShowKind = "LicenseShow"

// The words of Show.State.
const (
	StateFree     = "free"
	StateTrial    = "trial"
	StateLicensed = "licensed"
	// Deprecated: no license or trial runs is StateFree.
	StateNone = "none"
	// Deprecated: an ended trial is StateFree with Show.Warn.
	StateTrialEnded = "trial-ended"
	// Deprecated: a license ending is StateLicensed with Show.Warn.
	StateEnding = "ending"
	// Deprecated: an ended license is StateFree with Show.EndedOn and Show.Warn.
	StateEnded = "ended"
)

// Show is the `<app> license show` answer, the same for the CLI, its JSON and the License group of the app's Settings page (ADR-L8).
type Show struct {
	State    string       `json:"state" yaml:"state"`
	EndedOn  string       `json:"endedOn,omitempty" yaml:"endedOn,omitempty"`
	Sentence string       `json:"sentence,omitempty" yaml:"sentence,omitempty"`
	Warn     bool         `json:"warn" yaml:"warn"`
	Build    Build        `json:"build" yaml:"build"`
	Licenses []ShownFile  `json:"licenses" yaml:"licenses"`
	Refused  []RefusedRow `json:"refused" yaml:"refused"`
	Counts   Counts       `json:"counts" yaml:"counts"`
	Features []FeatureRow `json:"features" yaml:"features"`
}

// Build is whether the app's build is a release.
type Build struct {
	Release bool `json:"release" yaml:"release"`
}

// ShownFile is one license taken.
type ShownFile struct {
	Kind      Kind             `json:"kind" yaml:"kind"`
	For       string           `json:"for" yaml:"for"`
	Lines     map[string][]int `json:"lines,omitempty" yaml:"lines,omitempty"`
	Until     string           `json:"until,omitempty" yaml:"until,omitempty"`
	TrialEnds string           `json:"trialEnds,omitempty" yaml:"trialEnds,omitempty"`
	DaysLeft  int              `json:"daysLeft" yaml:"daysLeft"` // written only when Ends has a day
	Ended     bool             `json:"ended" yaml:"ended"`
	KeyID     string           `json:"keyId" yaml:"keyId"`
	Window    *Window          `json:"window,omitempty" yaml:"window,omitempty"`
	// Deprecated: a trial's offer never ends, so it is always empty and never written.
	OfferEnds string `json:"-" yaml:"-"`
}

// Ends is the last day DaysLeft counts to: a license's until, else a started trial's end; empty when there is none.
func (r ShownFile) Ends() string {
	if r.Until != "" {
		return r.Until
	}
	return r.TrialEnds
}

// MarshalJSON writes daysLeft only when the row has a last day, as contracts mankit/v3 license-show takes it.
func (r ShownFile) MarshalJSON() ([]byte, error) {
	type row ShownFile
	out := struct {
		row
		DaysLeft *int `json:"daysLeft,omitempty"`
	}{row: row(r)}
	if r.Ends() != "" {
		out.DaysLeft = &r.DaysLeft
	}
	return json.Marshal(out)
}

// Window is the countdown the License group's bar shows; its days left are the license's DaysLeft.
type Window struct {
	Of int `json:"of" yaml:"of"`
}

// RefusedRow is one license file refused.
type RefusedRow struct {
	File   string `json:"file" yaml:"file"`
	Type   string `json:"type" yaml:"type"`
	Reason string `json:"reason" yaml:"reason"`
}

// Counts is how many features there are of each tier, on and locked.
type Counts struct {
	Free       int `json:"free" yaml:"free"`
	Enterprise int `json:"enterprise" yaml:"enterprise"`
	On         int `json:"on" yaml:"on"`
	Locked     int `json:"locked" yaml:"locked"`
}

// FeatureRow is one feature in its feature state, its tier always written, and where it is unlocked from.
type FeatureRow struct {
	features.State `yaml:",inline"`
	From           From   `json:"from" yaml:"from"`
	Until          string `json:"until,omitempty" yaml:"until,omitempty"`
}

// Show is the answer for the features list holds, which the Checker's Closer closes: its licenses as read now, the state and warn as verdict works them out, and each feature's state and where it is unlocked from.
func (c *Checker) Show(list *features.List) (Show, error) {
	states, err := list.States()
	if err != nil {
		return Show{}, err
	}
	f, err := c.Read()
	if err != nil {
		return Show{}, err
	}
	v := f.verdict()
	out := Show{State: v.state, Warn: v.warn, Build: Build{Release: c.Release()}, Licenses: []ShownFile{}, Refused: []RefusedRow{}, Features: []FeatureRow{}}
	if !v.endedOn.IsZero() {
		out.EndedOn = v.endedOn.Format(day)
	}
	for _, t := range f.Taken {
		row := ShownFile{Kind: t.Kind, For: t.For, Lines: t.Lines, DaysLeft: t.DaysLeft, Ended: t.Ended, KeyID: t.KeyID}
		switch {
		case t.Kind == KindLicense && !t.Until.IsZero():
			row.Until = t.Until.Format(day)
		case t.Kind == Trial && t.Started != nil:
			row.TrialEnds = t.LastDay.Format(day)
		}
		switch {
		case t.Kind == Trial && t.Running():
			row.Window = &Window{Of: t.Days()}
		case t.Running() && !t.LastDay.IsZero() && LicenseExpiring.Warns(t.Kind, t.DaysLeft):
			row.Window = &Window{Of: LicenseExpiring.Paid}
		}
		out.Licenses = append(out.Licenses, row)
	}
	for _, r := range f.Refused {
		out.Refused = append(out.Refused, RefusedRow{File: r.File, Type: problem.TypeRefused, Reason: r.Reason})
	}
	for _, s := range states {
		if s.Tier = s.TierOrFree(); s.Enterprise() {
			out.Counts.Enterprise++
		} else {
			out.Counts.Free++
		}
		if s.On {
			out.Counts.On++
		}
		if s.By == features.License {
			out.Counts.Locked++
		}
		from, until := f.From(s.Feature)
		row := FeatureRow{State: s, From: from}
		if !until.IsZero() {
			row.Until = until.Format(day)
		}
		out.Features = append(out.Features, row)
	}
	out.Sentence = v.says(c.name())
	return out, nil
}

// name is the app as the sentence names it: its title and, when it has one, its line, like Designer 1 (V-10).
func (c *Checker) name() string {
	if c.c.Line > 0 {
		return c.c.Title + " " + strconv.Itoa(c.c.Line)
	}
	return c.c.Title
}

// verdict is the app's license state, worked out once for every surface (ADR-L3): the state and whether it warns, the day a license ended, the running license that ends last and the running trial, and whether a trial has ended.
type verdict struct {
	state      string
	warn       bool
	endedOn    time.Time
	license    *Taken
	trial      *Taken
	trialEnded bool
}

// verdict is the state contracts mankit/v3 license-show names: licensed while a license runs, warning once the one that ends last is within LicenseExpiring; else trial while a trial runs, warning in its last days; else free, warning once a trial or a license has ended, until it is replaced or removed, with the latest day an ended license ran.
func (f Found) verdict() verdict {
	var v verdict
	licenseEnded := false
	for i := range f.Taken {
		t := &f.Taken[i]
		switch {
		case t.Kind == Trial && t.Running():
			v.trial = t
		case t.Kind == Trial:
			v.trialEnded = v.trialEnded || t.Ended
		case t.Running():
			if v.license == nil || endsLater(*t, *v.license) {
				v.license = t
			}
		case t.Ended && !t.Elsewhere:
			licenseEnded = true
			if t.Until.After(v.endedOn) {
				v.endedOn = t.Until
			}
		}
	}
	switch {
	case v.license != nil:
		v.state = StateLicensed
		v.warn = !v.license.LastDay.IsZero() && LicenseExpiring.Warns(KindLicense, v.license.DaysLeft)
		v.endedOn = time.Time{}
	case v.trial != nil:
		v.state = StateTrial
		v.warn = LicenseExpiring.Warns(Trial, v.trial.DaysLeft)
		v.endedOn = time.Time{}
	default:
		v.state = StateFree
		v.warn = v.trialEnded || licenseEnded
	}
	return v
}

// endsLater reports whether a ends after b, a license that never ends after every other.
func endsLater(a, b Taken) bool {
	switch {
	case b.LastDay.IsZero():
		return false
	case a.LastDay.IsZero():
		return true
	}
	return a.LastDay.After(b.LastDay)
}

// says is the owner's sentence for the verdict, naming the app as name: a running license, a running trial and an ended trial have one, every other state none.
func (v verdict) says(name string) string {
	switch {
	case v.state == StateLicensed:
		return fmt.Sprintf("Licensed to %s · %s", v.license.For, name)
	case v.state == StateTrial:
		days := "days"
		if v.trial.DaysLeft == 1 {
			days = "day"
		}
		return fmt.Sprintf("Trial: %d %s left (ends %s)", v.trial.DaysLeft, days, v.trial.LastDay.Format(day))
	case v.trialEnded:
		return "Your trial has ended. Buy a license to keep the paid features."
	}
	return ""
}
