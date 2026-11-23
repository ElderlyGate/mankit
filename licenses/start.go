package licenses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tugbayatilla/mankit/v4/store"
)

// TrialOutcome is what StartTrial did, the word a program decides by (G-STACK-15).
type TrialOutcome string

// The outcomes of StartTrial.
const (
	TrialStarted TrialOutcome = "started" // no license: the built-in trial is written, from today
	TrialRenewed TrialOutcome = "renewed" // a new install: a fresh trial is written over the release-trial
	TrialKept    TrialOutcome = "kept"    // the release-trial this install started is left as it is, ended or not
	TrialSkipped TrialOutcome = "skipped" // another license, a file it cannot take, or the key's last day to start a trial has passed: nothing is written
	TrialFailed  TrialOutcome = "failed"  // the key, the binary or the write failed: nothing is written and the old file is whole
)

// TrialStart is what StartTrial did and why, for the app's start log; a failed one never stops the app.
type TrialStart struct {
	Outcome TrialOutcome
	Reason  string
	File    string // the app's license file
	Err     error  // set when Outcome is TrialFailed
}

func (s TrialStart) String() string { return fmt.Sprintf("license: trial %s: %s", s.Outcome, s.Reason) }

// StartTrial is the call an app makes at start with its built-in release-trial key and its executable's path (ADR-L9): with no license it writes the key as the app's license file, started today and stamped with the binary's path and file time; a new install, whose stamp differs, gets a fresh trial over a release-trial; a bought license, an org license or a person's trial key is never touched.
func (c *Checker) StartTrial(ctx context.Context, key []byte, binary string) TrialStart {
	r := TrialStart{}
	fail := func(why string, err error) TrialStart {
		r.Outcome, r.Reason, r.Err = TrialFailed, fmt.Sprintf("%s: %v", why, err), err
		return r
	}
	k, err := c.take(key, false)
	if err == nil && k.Kind != ReleaseTrial {
		err = fmt.Errorf("it is a %s license, not a release-trial", k.Kind)
	}
	if err != nil {
		return fail("the built-in trial key is refused", err)
	}
	stamp, err := stampOf(binary)
	if err != nil {
		return fail("the app's binary cannot be stamped", err)
	}
	org, err := c.at(true)
	if err != nil {
		return fail("the org's license cannot be found", err)
	}
	own, err := c.at(false)
	if err != nil {
		return fail("the app's license cannot be found", err)
	}
	r.File = own.path
	skip := func(why string) TrialStart {
		r.Outcome, r.Reason = TrialSkipped, why
		return r
	}
	for _, p := range []place{org, own} {
		if p.refused != nil {
			return skip(fmt.Sprintf("the license file %s is refused (%s: %s), so nothing is written", p.path, p.refused.Reason, p.refused.Detail))
		}
	}
	if org.license != nil {
		return skip(fmt.Sprintf("the org license %s is kept", org.license.ID))
	}
	want, did := store.Absent(), TrialStarted
	if l := own.license; l != nil {
		switch {
		case l.Kind != ReleaseTrial:
			return skip(fmt.Sprintf("the %s license %s is kept", l.Kind, l.ID))
		case l.Started != nil && stamp.same(*l.Started):
			r.Outcome, r.Reason = TrialKept, fmt.Sprintf("this install started its trial on %s", l.Started.Day.Format(day))
			return r
		}
		want, did = store.Holds(store.SumOf(own.raw)), TrialRenewed
	}
	today := date(c.c.Now())
	if today.After(date(k.Until)) {
		return skip(fmt.Sprintf("the built-in key's last day to start a trial, %s, has passed", k.Until.Format(day)))
	}
	stamp.Day = today
	body, err := withStarted(key, stamp)
	if err == nil {
		_, err = c.take(body, false)
	}
	if err != nil {
		return fail("the trial cannot be written", err)
	}
	s, err := c.scope(false)
	if err != nil {
		return fail("the app's folder cannot be opened", err)
	}
	defer s.Close()
	if err := s.Change(ctx, name, "license trial", func(tx store.Tx) error { return tx.Put(name, body, want) }); err != nil {
		return fail("the license file is not written and the old one is whole", err)
	}
	r.Outcome, r.Reason = did, fmt.Sprintf("a %d-day trial from %s", k.TrialDays, today.Format(day))
	return r
}

// stampOf is the install stamp of the binary at path: its absolute path and its file time to the second, as spec.started keeps it.
func stampOf(path string) (Started, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Started{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Started{}, err
	}
	if !info.Mode().IsRegular() {
		return Started{}, errors.New(abs + " is not a regular file")
	}
	return Started{Binary: abs, FileTime: info.ModTime().UTC().Truncate(time.Second)}, nil
}

// same reports whether s and o stamp the same install: the same binary path and file time.
func (s Started) same(o Started) bool {
	return s.Binary == o.Binary && s.FileTime.Equal(o.FileTime.Truncate(time.Second))
}

// withStarted is the license file key with s as its spec.started, every other field kept as it is.
func withStarted(key []byte, s Started) ([]byte, error) {
	var f, spec map[string]json.RawMessage
	if err := json.Unmarshal(key, &f); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(f["spec"], &spec); err != nil {
		return nil, err
	}
	started, err := json.Marshal(startedJSON{Day: s.Day.Format(day), Binary: s.Binary, FileTime: s.FileTime.UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	spec["started"] = started
	if f["spec"], err = json.Marshal(spec); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(f, "", "  ")
	return append(out, '\n'), err
}
