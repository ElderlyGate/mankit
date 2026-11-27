package licenses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/store"
)

// TrialOutcome is what StartTrial did, the word a program decides by (G-STACK-15).
type TrialOutcome string

// The outcomes of StartTrial.
const (
	TrialStarted TrialOutcome = "started" // no license: the built-in trial is written, from today
	TrialRenewed TrialOutcome = "renewed" // a new install: a fresh trial is written over the trial
	TrialKept    TrialOutcome = "kept"    // the trial this install started is left as it is, ended or not
	TrialSkipped TrialOutcome = "skipped" // a license, or a file it cannot take: nothing is written
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

// StartTrial is the call an app makes at start with its built-in trial key and its executable's path, as os.Executable gives it (ADR-L9): with no license it writes the key as the app's license file, started today and stamped with the binary's path and file time; a new install, whose stamp differs, gets a fresh trial over a trial, an old release-trial among them; a license is never touched.
func (c *Checker) StartTrial(ctx context.Context, key []byte, binary string) TrialStart {
	k, err := c.take(key, false)
	switch {
	case err != nil:
	case k.old != "":
		err = fmt.Errorf("it is a %s of the old license form %s, which mankit reads but never writes; a key is %s", k.old, APIVersion, FileAPIVersion)
	case k.Kind != Trial:
		err = fmt.Errorf("it is a %s, not a trial", k.Kind)
	}
	if err != nil {
		return failed("", "the built-in trial key is refused", err)
	}
	stamp, err := stampOf(binary)
	if err != nil {
		return failed("", "the app's binary cannot be stamped", err)
	}
	r, want := c.plan(k, stamp)
	if r.Outcome != "" {
		return r
	}
	stamp.Day = date(c.c.Now())
	body, err := withStarted(key, stamp)
	if err == nil {
		_, err = c.take(body, false)
	}
	if err != nil {
		return failed(r.File, "the trial cannot be written", err)
	}
	s, err := c.scope(false)
	if err != nil {
		return failed(r.File, "the app's folder cannot be opened", err)
	}
	defer s.Close()
	err = s.Change(ctx, name, "license trial", func(tx store.Tx) error { return tx.Put(name, body, want) })
	var p *problem.Problem
	if errors.As(err, &p) && p.Type == problem.TypeRefused && p.Reason == store.ReasonChanged {
		return c.again(k, stamp)
	}
	if err != nil {
		return failed(r.File, "the license file is not written and the old one is whole", err)
	}
	did := TrialStarted
	if want != store.Absent() {
		did = TrialRenewed
	}
	r.Outcome, r.Reason = did, fmt.Sprintf("a %d-day trial from %s", k.TrialDays, stamp.Day.Format(day))
	return r
}

// plan reads both places and decides: a TrialStart with an Outcome when nothing is to be written, else the app's file and what the write wants to find there.
func (c *Checker) plan(k License, stamp Started) (TrialStart, store.Want) {
	org, err := c.at(true)
	if err != nil {
		return failed("", "the org's license cannot be found", err), store.Want{}
	}
	own, err := c.at(false)
	if err != nil {
		return failed("", "the app's license cannot be found", err), store.Want{}
	}
	r := TrialStart{File: own.path}
	skip := func(why string) (TrialStart, store.Want) {
		r.Outcome, r.Reason = TrialSkipped, why
		return r, store.Want{}
	}
	for _, p := range []place{org, own} {
		if p.refused != nil {
			return skip(fmt.Sprintf("the license file %s is refused (%s: %s), so nothing is written", p.path, p.refused.Reason, p.refused.Detail))
		}
	}
	if org.license != nil {
		return skip(fmt.Sprintf("the license %s is kept", org.license.ID))
	}
	want := store.Absent()
	if l := own.license; l != nil {
		switch {
		case l.Kind != Trial:
			return skip(fmt.Sprintf("the %s license %s is kept", l.old, l.ID))
		case l.Started != nil && stamp.same(*l.Started):
			r.Outcome, r.Reason = TrialKept, fmt.Sprintf("this install started its trial on %s", l.Started.Day.Format(day))
			return r, store.Want{}
		}
		want = store.Holds(store.SumOf(own.raw))
	}
	return r, want
}

// again is what the places hold once another start, or license add, changed the file while this one wrote: read once more, never written.
func (c *Checker) again(k License, stamp Started) TrialStart {
	r, _ := c.plan(k, stamp)
	switch r.Outcome {
	case "":
		r.Outcome, r.Reason = TrialSkipped, "the license file changed while the trial was written, so it is left as another start wrote it"
	case TrialKept, TrialSkipped:
		r.Reason += "; another start wrote it"
	}
	return r
}

// failed is a TrialStart that failed with err, saying why.
func failed(file, why string, err error) TrialStart {
	return TrialStart{Outcome: TrialFailed, Reason: fmt.Sprintf("%s: %v", why, err), File: file, Err: err}
}

// stampOf is the install stamp of the binary at path: its absolute path with every link resolved, as macOS may give a link where Linux does not, and its file time to the second, as spec.started keeps it.
func stampOf(path string) (Started, error) {
	abs, err := filepath.Abs(path)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
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
