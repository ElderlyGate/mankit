package licenses

import (
	"time"

	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/problem"
)

// ShowKind is the kind of the `<app> license show` answer; contracts mankit/v1 license-show says what each field of Show means.
const ShowKind = "LicenseShow"

// The words of Show.State.
const (
	StateNone       = "none"
	StateTrial      = "trial"
	StateTrialEnded = "trial-ended"
	StateLicensed   = "licensed"
	StateEnded      = "ended"
)

// Show is the `<app> license show` answer, the same for the CLI, its JSON and the License group of the app's Settings page (ADR-L8).
type Show struct {
	State    string       `json:"state" yaml:"state"`
	EndedOn  string       `json:"endedOn,omitempty" yaml:"endedOn,omitempty"`
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
	Kind     Kind    `json:"kind" yaml:"kind"`
	For      string  `json:"for" yaml:"for"`
	Until    string  `json:"until" yaml:"until"`
	DaysLeft int     `json:"daysLeft" yaml:"daysLeft"`
	Ended    bool    `json:"ended" yaml:"ended"`
	KeyID    string  `json:"keyId" yaml:"keyId"`
	Window   *Window `json:"window,omitempty" yaml:"window,omitempty"`
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

// Show is the answer for the features list holds, which the Checker's Closer closes: its licenses as read now, and each feature's state and where it is unlocked from.
func (c *Checker) Show(list *features.List) (Show, error) {
	states, err := list.States()
	if err != nil {
		return Show{}, err
	}
	f, err := c.Read()
	if err != nil {
		return Show{}, err
	}
	state, endedOn := f.state()
	out := Show{State: state, Build: Build{Release: c.Release()}, Licenses: []ShownFile{}, Refused: []RefusedRow{}, Features: []FeatureRow{}}
	if !endedOn.IsZero() {
		out.EndedOn = endedOn.Format(day)
	}
	for _, t := range f.Taken {
		row := ShownFile{Kind: t.Kind, For: t.For, Until: t.Until.Format(day), DaysLeft: t.DaysLeft, Ended: t.Ended, KeyID: t.KeyID}
		// A paid license's window waits for the threshold (ADR-S23); only a running trial key's shows.
		if t.Kind == Trial && t.Running() {
			row.Window = &Window{Of: t.Days()}
		}
		out.Licenses = append(out.Licenses, row)
	}
	for _, r := range f.Refused {
		out.Refused = append(out.Refused, RefusedRow{File: r.File, Type: problem.TypeRefused, Reason: r.Reason})
	}
	for _, s := range states {
		if s.Enterprise() {
			out.Counts.Enterprise++
		} else {
			s.Tier = features.Free
			out.Counts.Free++
		}
		if s.On {
			out.Counts.On++
		}
		if s.By == features.License {
			out.Counts.Locked++
		}
		row := FeatureRow{State: s, From: FromNone}
		if from, until := f.From(s.Feature); from != FromNone {
			row.From, row.Until = from, until.Format(day)
		}
		out.Features = append(out.Features, row)
	}
	return out, nil
}

// state is one word for the app: a running paid license, else an ended one, whose notice stays until it is replaced or removed (ADR-L3), else a running trial key, else an ended one, else none; one not begun counts as none. When it is ended, the day is the latest last day of the ended paid licenses.
func (f Found) state() (string, time.Time) {
	var paid, ended, trial, trialEnded bool
	var endedOn time.Time
	for _, t := range f.Taken {
		switch {
		case t.Kind == Trial && t.Ended:
			trialEnded = true
		case t.Kind == Trial && t.Running():
			trial = true
		case t.Ended:
			ended = true
			if t.Until.After(endedOn) {
				endedOn = t.Until
			}
		case t.Running():
			paid = true
		}
	}
	switch {
	case paid:
		return StateLicensed, time.Time{}
	case ended:
		return StateEnded, endedOn
	case trial:
		return StateTrial, time.Time{}
	case trialEnded:
		return StateTrialEnded, time.Time{}
	}
	return StateNone, time.Time{}
}
