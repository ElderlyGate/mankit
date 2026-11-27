package licenses

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/problem"
)

// ShowKind is the kind of the `<app> license show` answer; contracts mankit/v3 license-show says what each field of Show means.
const ShowKind = "LicenseShow"

// ListKind is the kind of the `<app> license list` answer; contracts mankit/v3 license-list says what each field of List means.
const ListKind = "LicenseList"

// LicenseKind is the kind of the `<app> license show <id>` answer, one row of List.Licenses; contracts mankit/v3 license says what it holds.
const LicenseKind = "License"

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
	License  *ShownFile   `json:"license,omitempty" yaml:"license,omitempty"` // the active license, List's row of it; nil when none is active
	Trial    *ShownTrial  `json:"trial,omitempty" yaml:"trial,omitempty"`
	Counts   Counts       `json:"counts" yaml:"counts"`
	Features []FeatureRow `json:"features" yaml:"features"`
	// Deprecated: every license is List's (Checker.List); still filled with List's rows, never written.
	Licenses []ShownFile `json:"-" yaml:"-"`
	// Deprecated: every refused file is List's (Checker.List); still filled with List's rows, never written.
	Refused []RefusedRow `json:"-" yaml:"-"`
}

// List is the `<app> license list` answer: every license the app took, the active one marked, and every file it refused, the one home of a license's fields (ADR-L8).
type List struct {
	Licenses []ShownFile  `json:"licenses" yaml:"licenses"`
	Refused  []RefusedRow `json:"refused" yaml:"refused"`
}

// Build is whether the app's build is a release.
type Build struct {
	Release bool `json:"release" yaml:"release"`
}

// ShownFile is one license taken.
type ShownFile struct {
	ID     string            `json:"id" yaml:"id"`
	Place  Place             `json:"place" yaml:"place"`
	For    string            `json:"for" yaml:"for"`
	Lines  *map[string][]int `json:"lines,omitempty" yaml:"lines,omitempty"` // a pointer, so a row stays comparable
	Issued string            `json:"issued" yaml:"issued"`
	Until  string            `json:"until,omitempty" yaml:"until,omitempty"`
	// Deprecated: a license has one kind, KindLicense; never written.
	Kind     Kind    `json:"-" yaml:"-"`
	DaysLeft int     `json:"daysLeft" yaml:"daysLeft"` // written only when Until has a day
	Ended    bool    `json:"ended" yaml:"ended"`
	KeyID    string  `json:"keyId" yaml:"keyId"`
	Window   *Window `json:"window,omitempty" yaml:"window,omitempty"`
	Active   bool    `json:"active" yaml:"active"`
	// Deprecated: the trial is Show.Trial, not a license; always empty and never written.
	TrialEnds string `json:"-" yaml:"-"`
	// Deprecated: a trial's offer never ends, so it is always empty and never written.
	OfferEnds string `json:"-" yaml:"-"`
}

// Ends is the last day DaysLeft counts to, the license's until; empty when there is none.
func (r ShownFile) Ends() string { return r.Until }

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

// MarshalYAML writes the row as MarshalJSON does, so -o yaml and any YAML encoder leave daysLeft out alike.
func (r ShownFile) MarshalYAML() (any, error) {
	raw, err := r.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(raw, &out)
	return out, err
}

// ShownTrial is the app's trial, a rule and not a license (ADR-L9).
type ShownTrial struct {
	Until    string  `json:"until" yaml:"until"`
	DaysLeft int     `json:"daysLeft" yaml:"daysLeft"`
	Ended    bool    `json:"ended" yaml:"ended"`
	Window   *Window `json:"window,omitempty" yaml:"window,omitempty"`
}

// Window is the countdown the License group's bar shows; its days left are the DaysLeft beside it.
type Window struct {
	Of int `json:"of" yaml:"of"`
}

// RefusedRow is one license file refused.
type RefusedRow struct {
	ID     string `json:"id,omitempty" yaml:"id,omitempty"` // the id show <id> and license remove take for it: old-org or old-app for an old single file too broken to give one
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

// Show is the answer for the features list holds, which the Checker's Closer closes: its active license as List's row, its trial as read now, the state and warn as verdict works them out once, and each feature's state and where it is unlocked from.
func (c *Checker) Show(list *features.List) (Show, error) {
	states, err := list.States()
	if err != nil {
		return Show{}, err
	}
	f, err := c.Read()
	if err != nil {
		return Show{}, err
	}
	v, rows := f.verdict(), f.list()
	out := Show{State: v.state, Warn: v.warn, Build: Build{Release: c.Release()}, Licenses: rows.Licenses, Refused: rows.Refused, Features: []FeatureRow{}}
	if !v.endedOn.IsZero() {
		out.EndedOn = v.endedOn.Format(day)
	}
	for _, row := range rows.Licenses {
		if row.Active {
			out.License = &row
			break
		}
	}
	if t := f.Trial; t != nil {
		out.Trial = &ShownTrial{Until: t.LastDay.Format(day), DaysLeft: t.DaysLeft, Ended: t.Ended}
		if !t.Ended {
			out.Trial.Window = &Window{Of: trialDays}
		}
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
	out.Sentence = f.says(v, c.c.Title, c.name())
	return out, nil
}

// List is the `<app> license list` answer: every license the app took and every file it refused, as read now.
func (c *Checker) List() (List, error) {
	f, err := c.Read()
	if err != nil {
		return List{}, err
	}
	return f.list(), nil
}

// License is the `<app> license show <id>` answer, List's row of the license id, or the Problem why there is none: refused when the file of that id is refused, not_found when there is no such license.
func (c *Checker) License(id string) (ShownFile, error) {
	f, err := c.Read()
	if err != nil {
		return ShownFile{}, err
	}
	for _, row := range f.list().Licenses {
		if row.ID == id {
			return row, nil
		}
	}
	return ShownFile{}, f.missing(id)
}

// list is the rows of f's licenses and refused files, built here alone, so license list, show and show <id> give a license alike (ADR-L8): a refused file's id is the one refusal gives it.
func (f Found) list() List {
	out := List{Licenses: []ShownFile{}, Refused: []RefusedRow{}}
	for _, t := range f.Taken {
		row := ShownFile{ID: t.ID, Place: t.Place, For: t.For, Issued: t.Issued.Format(day), DaysLeft: t.DaysLeft, Ended: t.Ended, KeyID: t.KeyID, Active: t.Active}
		if len(t.Lines) > 0 {
			row.Lines = &t.Lines
		}
		if !t.Until.IsZero() {
			row.Until = t.Until.Format(day)
		}
		if t.Running() && !t.LastDay.IsZero() && t.DaysLeft <= LicenseExpiring.Paid {
			row.Window = &Window{Of: LicenseExpiring.Paid}
		}
		out.Licenses = append(out.Licenses, row)
	}
	for _, r := range f.Refused {
		out.Refused = append(out.Refused, RefusedRow{ID: r.ID, File: r.File, Type: problem.TypeRefused, Reason: r.Reason})
	}
	return out
}

// name is the app as the sentence names it: its title and, when it has one, its line, like Designer 1 (V-10).
func (c *Checker) name() string {
	if c.c.Line > 0 {
		return c.c.Title + " " + strconv.Itoa(c.c.Line)
	}
	return c.c.Title
}

// verdict is the app's license state, worked out once for every surface (ADR-L3): the state, whether it warns and the day the active license ended.
type verdict struct {
	state   string
	warn    bool
	endedOn time.Time
}

// verdict is the license state of f; contracts mankit/v3 license-show says what each state and warn mean (CT-RULE-5). Only the active license counts, as the app never takes another by itself (ADR-L8).
func (f Found) verdict() verdict {
	a, t := f.Active(), f.Trial
	switch {
	case a != nil && a.Running():
		return verdict{state: StateLicensed, warn: !a.LastDay.IsZero() && a.DaysLeft <= LicenseExpiring.Paid}
	case t != nil && !t.Ended:
		return verdict{state: StateTrial, warn: t.DaysLeft <= LicenseExpiring.Trial}
	}
	v := verdict{state: StateFree, warn: t != nil && t.Ended}
	if a != nil && a.Ended {
		v.warn, v.endedOn = true, a.Until
	}
	return v
}

// says is the sentence for the verdict v of f, naming the app as name and its lines by title: licensed names whom for; otherwise why the active license does not run when it has ended, has not started or is for another line, or why none is active when the chosen one is refused or gone or the choice cannot be read, then the trial while it runs, or that it ended.
func (f Found) says(v verdict, title, name string) string {
	a, t := f.Active(), f.Trial
	if v.state == StateLicensed {
		return fmt.Sprintf("Licensed to %s · %s", a.For, name)
	}
	var parts []string
	switch {
	case a != nil && a.Ended:
		parts = append(parts, fmt.Sprintf("The license for %s ended on %s", a.For, a.Until.Format(day)))
	case a != nil && a.Early:
		parts = append(parts, fmt.Sprintf("The license %s starts on %s", a.ID, a.Issued.Format(day)))
	case a != nil && a.Elsewhere && len(a.Lines[f.product]) == 0:
		parts = append(parts, fmt.Sprintf("The license %s names no line of %s", a.ID, title))
	case a != nil && a.Elsewhere:
		lines := make([]string, len(a.Lines[f.product]))
		for i, n := range a.Lines[f.product] {
			lines[i] = strconv.Itoa(n)
		}
		parts = append(parts, fmt.Sprintf("The license %s is for %s %s, not this build", a.ID, title, strings.Join(lines, ", ")))
	case f.unread != "":
		parts = append(parts, "The choice of license cannot be read ("+f.unread+"), so no license is active")
	case a == nil && f.Chosen != "":
		why := "is not among this app's licenses"
		for _, r := range f.Refused {
			if r.ID == f.Chosen {
				why = "was refused: " + r.Detail
				break
			}
		}
		parts = append(parts, fmt.Sprintf("The chosen license %s %s", f.Chosen, why))
	}
	switch {
	case t != nil && !t.Ended:
		days := "days"
		if t.DaysLeft == 1 {
			days = "day"
		}
		parts = append(parts, fmt.Sprintf("Trial: %d %s left, until %s", t.DaysLeft, days, t.LastDay.Format(day)))
	case t != nil && len(parts) == 0:
		parts = append(parts, "The trial ended on "+t.LastDay.Format(day))
	}
	return strings.Join(parts, " · ")
}
