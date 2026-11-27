package licenses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/store"
)

// trialDays is the trial's length, the one home of ADR-L9's 14 days.
const trialDays = 14

// The trial note's form and kind (contracts mankit/v3 trial-started).
const (
	noteAPIVersion = "elderlygate/mankit/trial/v1"
	noteKind       = "TrialStarted"
)

// TrialOutcome is what StartTrial did, the word a program decides by (G-STACK-15).
type TrialOutcome string

// The outcomes of StartTrial.
const (
	TrialStarted TrialOutcome = "started" // no note: the trial's note is written, from today
	TrialRenewed TrialOutcome = "renewed" // a note that is not this install's: a fresh note is written over it
	TrialKept    TrialOutcome = "kept"    // the note this install wrote is left as it is, its trial ended or not
	TrialSkipped TrialOutcome = "skipped" // an active license that has not ended: no trial, and nothing is written
	TrialFailed  TrialOutcome = "failed"  // a release with no line, the binary or the write failed: nothing is written and the old note is whole
)

// TrialStart is what StartTrial did and why, for the app's start log; a failed one never stops the app.
type TrialStart struct {
	Outcome TrialOutcome
	Reason  string
	File    string // the app's trial note
	Err     error  // set when Outcome is TrialFailed
}

func (s TrialStart) String() string { return fmt.Sprintf("license: trial %s: %s", s.Outcome, s.Reason) }

// StartTrial is the call an app makes at start with its executable's path, as os.Executable gives it (ADR-L9). An active license that has not ended means no trial. Else a note of this app whose stamp is this binary's is kept, and any other, missing, broken or future-dated, gives way to a new note from today, stamped with the binary's path and file time. A release build with no line stamped starts no trial.
// The key parameter is deprecated and ignored, as the trial is a rule in mankit and nothing about it is signed; pass nil.
func (c *Checker) StartTrial(ctx context.Context, key []byte, binary string) TrialStart {
	path, err := home.File(c.c.Folder, note)
	if err != nil {
		return failed("", "the app's folder cannot be found", err)
	}
	if c.c.Trust.Release && c.c.Line == 0 {
		return failed(path, "no trial", errors.New("this release build has no product line stamped (build.line), so it starts no trial"))
	}
	stamp, err := stampOf(binary)
	if err != nil {
		return failed(path, "the app's binary cannot be stamped", err)
	}
	r, want := c.plan(path, stamp)
	if r.Outcome != "" {
		return r
	}
	stamp.Day = date(c.c.Now())
	body, err := c.noteOf(stamp)
	if err != nil {
		return failed(path, "the trial's note cannot be written", err)
	}
	s, err := c.scope(false)
	if err != nil {
		return failed(path, "the app's folder cannot be opened", err)
	}
	defer s.Close()
	err = s.Change(ctx, note, "license trial", func(tx store.Tx) error { return tx.Put(note, body, want) })
	var p *problem.Problem
	if errors.As(err, &p) && p.Type == problem.TypeRefused && p.Reason == store.ReasonChanged {
		return c.again(path, stamp)
	}
	if err != nil {
		return failed(path, "the trial's note is not written and the old one is whole", err)
	}
	did := TrialStarted
	if want != store.Absent() {
		did = TrialRenewed
	}
	r.Outcome, r.Reason = did, fmt.Sprintf("a %d-day trial from %s", trialDays, stamp.Day.Format(day))
	return r
}

// plan reads the licenses and the note and decides: a TrialStart with an Outcome when nothing is to be written, else what the write wants to find at the note.
func (c *Checker) plan(path string, stamp Started) (TrialStart, store.Want) {
	r := TrialStart{File: path}
	f, err := c.Read()
	if err != nil {
		return failed(path, "the app's licenses cannot be read", err), store.Want{}
	}
	if a := f.Active(); a != nil && !a.Ended {
		r.Outcome, r.Reason = TrialSkipped, fmt.Sprintf("the license %s is active, so there is no trial", a.ID)
		return r, store.Want{}
	}
	raw, err := readFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return r, store.Absent()
	case err != nil:
		return failed(path, "the trial's note cannot be read", err), store.Want{}
	}
	if n, ok := c.note(raw); ok && stamp.same(n) {
		r.Outcome, r.Reason = TrialKept, fmt.Sprintf("this install started its trial on %s", n.Day.Format(day))
		return r, store.Want{}
	}
	return r, store.Holds(store.SumOf(raw))
}

// again is what the note and licenses hold once another start, or license add, changed them while this one wrote: read once more, never written.
func (c *Checker) again(path string, stamp Started) TrialStart {
	r, _ := c.plan(path, stamp)
	switch r.Outcome {
	case "":
		r.Outcome, r.Reason = TrialSkipped, "the trial's note changed while it was written, so it is left as another start wrote it"
	case TrialKept, TrialSkipped:
		r.Reason += "; another start wrote it"
	}
	return r
}

// failed is a TrialStart that failed with err, saying why.
func failed(file, why string, err error) TrialStart {
	return TrialStart{Outcome: TrialFailed, Reason: fmt.Sprintf("%s: %v", why, err), File: file, Err: err}
}

// noteFile is the trial note on disk (contracts mankit/v3 trial-started): closed but for spec, which a newer app may grow.
type noteFile struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		ID string `json:"id"`
	} `json:"metadata"`
	Spec json.RawMessage `json:"spec"`
}

// noteSpec is the note's spec.
type noteSpec struct {
	Day      string `json:"day"`
	Binary   string `json:"binary"`
	FileTime string `json:"fileTime"`
}

// note is the trial the note raw says started, and whether it counts: a note of this app's id with a day, a binary and a file time, and a day not after today (ADR-L9).
func (c *Checker) note(raw []byte) (Started, bool) {
	var n noteFile
	var s noteSpec
	if decode(raw, &n, true) != nil || n.APIVersion != noteAPIVersion || n.Kind != noteKind || n.Metadata.ID != c.c.ID || decode(n.Spec, &s, false) != nil {
		return Started{}, false
	}
	d, derr := time.Parse(day, s.Day)
	ft, ferr := time.Parse(time.RFC3339, s.FileTime)
	if derr != nil || ferr != nil || s.Binary == "" || date(d).After(date(c.c.Now())) {
		return Started{}, false
	}
	return Started{Day: d, Binary: s.Binary, FileTime: ft}, true
}

// noteOf is the note of the trial s started, as note reads it.
func (c *Checker) noteOf(s Started) ([]byte, error) {
	spec, err := json.Marshal(noteSpec{Day: s.Day.Format(day), Binary: s.Binary, FileTime: s.FileTime.UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	n := noteFile{APIVersion: noteAPIVersion, Kind: noteKind, Spec: spec}
	n.Metadata.ID = c.c.ID
	out, err := json.MarshalIndent(n, "", "  ")
	return append(out, '\n'), err
}

// stampOf is the install stamp of the binary at path: its absolute path with every link resolved, as macOS may give a link where Linux does not, and its file time to the second, as the note keeps it.
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
