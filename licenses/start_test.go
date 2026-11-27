package licenses_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.naywee.com/contracts/v2"

	"github.com/tugbayatilla/mankit/v4/licenses"
)

// installed is the time the throwaway binary was written; a reinstall writes it anew.
var installed = time.Date(2026, 11, 30, 8, 0, 0, 0, time.UTC)

// install is a temp HOME/XDG with a throwaway app binary written at installed.
func install(t *testing.T) string {
	t.Helper()
	org(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	bin := filepath.Join(real(t, t.TempDir()), "designer")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, bin, installed)
	return bin
}

// real is path with every link resolved, as the stamp keeps it: a temp folder on macOS lies behind a link.
func real(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func startTrial(t *testing.T, trust licenses.Trust, today, bin string) licenses.TrialStart {
	t.Helper()
	return checker(t, trust, today).StartTrial(context.Background(), nil, bin)
}

// notePath is designer's trial note.
func notePath(t *testing.T) string { t.Helper(); return appPath(t, "designer", "trial") }

// readNote is the trial note, and whether it is there.
func readNote(t *testing.T) ([]byte, bool) {
	t.Helper()
	raw, err := os.ReadFile(notePath(t))
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return raw, true
}

// trialOn is the trial the app reads on the day.
func trialOn(t *testing.T, trust licenses.Trust, today string) *licenses.TrialRun {
	t.Helper()
	return found(t, trust, today).Trial
}

func want(t *testing.T, got licenses.TrialStart, outcome licenses.TrialOutcome) {
	t.Helper()
	if got.Outcome != outcome {
		t.Fatalf("outcome = %s (%s), want %s", got.Outcome, got.Reason, outcome)
	}
	if (got.Err != nil) != (outcome == licenses.TrialFailed) {
		t.Fatalf("err = %v with outcome %s", got.Err, got.Outcome)
	}
}

// With no license the app writes its trial note, plain, of contracts mankit/v3 trial-started: today, the binary and its file time; the trial runs 14 days, its first day counted (ADR-L9).
func TestWithNoLicenseTheTrialStarts(t *testing.T) {
	bin := install(t)
	_, release, _ := signer(t)
	got := startTrial(t, release, "2026-12-03", bin)
	want(t, got, licenses.TrialStarted)
	if got.File != notePath(t) || !strings.Contains(got.Reason, "14-day") {
		t.Fatalf("start = %+v", got)
	}
	raw, _ := readNote(t)
	if err := contracts.Validate("mankit/v3/trial-started.schema.json", raw); err != nil {
		t.Fatalf("the note does not meet its schema: %v\n%s", err, raw)
	}
	tr := trialOn(t, release, "2026-12-03")
	if tr == nil || tr.Ended || !tr.Day.Equal(day("2026-12-03")) || tr.Binary != bin || !tr.FileTime.Equal(installed) || !tr.LastDay.Equal(day("2026-12-16")) || tr.DaysLeft != 14 {
		t.Fatalf("trial = %+v", tr)
	}
	if tr := trialOn(t, release, "2026-12-17"); tr == nil || !tr.Ended || tr.DaysLeft != 0 {
		t.Fatalf("trial after its last day = %+v", tr)
	}
}

// The install that started the trial keeps it, ended or not: starting again never stretches it.
func TestAMatchingStampKeepsTheTrial(t *testing.T) {
	bin := install(t)
	_, release, _ := signer(t)
	want(t, startTrial(t, release, "2026-12-03", bin), licenses.TrialStarted)
	before, _ := readNote(t)
	for _, today := range []string{"2026-12-04", "2026-12-30"} {
		want(t, startTrial(t, release, today, bin), licenses.TrialKept)
		if after, _ := readNote(t); !bytes.Equal(after, before) {
			t.Fatalf("%s: the note changed", today)
		}
	}
}

// A reinstall or an update writes the binary anew, at a new time or a new path, and gets a fresh trial from today (ADR-L9).
func TestAChangedStampRenewsTheTrial(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, bin string) string{
		"a new file time": func(t *testing.T, bin string) string {
			touch(t, bin, installed.Add(40*24*time.Hour))
			return bin
		},
		"a new path": func(t *testing.T, bin string) string {
			moved := filepath.Join(real(t, t.TempDir()), "designer")
			if err := os.Rename(bin, moved); err != nil {
				t.Fatal(err)
			}
			return moved
		},
	} {
		t.Run(name, func(t *testing.T) {
			bin := install(t)
			_, release, _ := signer(t)
			want(t, startTrial(t, release, "2026-12-03", bin), licenses.TrialStarted)
			bin = change(t, bin)
			want(t, startTrial(t, release, "2027-01-10", bin), licenses.TrialRenewed)
			if tr := trialOn(t, release, "2027-01-10"); tr == nil || !tr.Day.Equal(day("2027-01-10")) || tr.Binary != bin {
				t.Fatalf("trial = %+v", tr)
			}
		})
	}
}

// A missing, broken, future-dated note, or one of another app, counts as none: the app reads no trial from it, and the next start writes a fresh note over it (ADR-L9).
func TestANoteThatDoesNotCountGivesAFreshTrial(t *testing.T) {
	for name, raw := range map[string][]byte{
		"broken":              []byte("not json"),
		"future-dated":        noteOn("designer", "2026-12-24"),
		"of another app":      noteOn("kanban", "2026-12-01"),
		"of another form":     bytes.Replace(noteOn("designer", "2026-12-01"), []byte("trial/v1"), []byte("trial/v2"), 1),
		"with a closed field": bytes.Replace(noteOn("designer", "2026-12-01"), []byte(`"kind"`), []byte(`"extra":1,"kind"`), 1),
		"without a binary":    bytes.Replace(noteOn("designer", "2026-12-01"), []byte("/home/ada/.local/bin/designer"), nil, 1),
	} {
		t.Run(name, func(t *testing.T) {
			bin := install(t)
			_, release, _ := signer(t)
			put(t, notePath(t), raw)
			if tr := trialOn(t, release, "2026-12-03"); tr != nil {
				t.Fatalf("a note that counts as none gave the trial %+v", tr)
			}
			want(t, startTrial(t, release, "2026-12-03", bin), licenses.TrialRenewed)
			if tr := trialOn(t, release, "2026-12-03"); tr == nil || !tr.Day.Equal(day("2026-12-03")) {
				t.Fatalf("trial = %+v", tr)
			}
		})
	}
}

// A field a newer app adds to the note's spec is passed over (CT-RULE-4).
func TestANoteWithANewerSpecFieldCounts(t *testing.T) {
	install(t)
	_, release, _ := signer(t)
	put(t, notePath(t), bytes.Replace(noteOn("designer", "2026-12-01"), []byte(`"day"`), []byte(`"newer":true,"day"`), 1))
	if tr := trialOn(t, release, "2026-12-03"); tr == nil || !tr.Day.Equal(day("2026-12-01")) {
		t.Fatalf("trial = %+v", tr)
	}
}

// An active license that has not ended means no trial: no note is written, and an existing one is left (ADR-L9). Once it has ended, the trial starts.
func TestAnActiveLicenseMeansNoTrial(t *testing.T) {
	for name, place := range map[string]func(t *testing.T) string{
		"of the org":           func(t *testing.T) string { return orgFile(t, "acme-2026") },
		"of the app":           func(t *testing.T) string { return appFile(t, "designer", "acme-2026") },
		"in the old org place": oldOrgFile,
		"in the old app place": oldAppFile,
	} {
		t.Run(name, func(t *testing.T) {
			bin := install(t)
			k, release, _ := signer(t)
			put(t, place(t), sign(t, k, license))
			got := startTrial(t, release, "2026-12-03", bin)
			want(t, got, licenses.TrialSkipped)
			if !strings.Contains(got.Reason, "acme-2026") {
				t.Fatalf("reason = %s", got.Reason)
			}
			if _, there := readNote(t); there {
				t.Fatal("a note was written beside an active license")
			}
			want(t, startTrial(t, release, "2027-11-07", bin), licenses.TrialStarted)
		})
	}
}

// A license refused, or an old trial file, plays no part: the trial starts beside it.
func TestARefusedFileOrOldTrialDoesNotStopTheTrial(t *testing.T) {
	bin := install(t)
	k, release, _ := signer(t)
	old := signOld(t, k, oldReleaseTrial)
	put(t, oldAppFile(t), old)
	put(t, orgFile(t, "broken"), []byte("not json"))
	want(t, startTrial(t, release, "2026-12-03", bin), licenses.TrialStarted)
	if raw, _ := os.ReadFile(oldAppFile(t)); !bytes.Equal(raw, old) {
		t.Fatal("the old trial file was touched")
	}
}

// A release with no product line stamped starts no trial and reads none, failing closed (ADR-L9); a dev build with none still runs one.
func TestAReleaseWithNoLineHasNoTrial(t *testing.T) {
	bin := install(t)
	_, release, dev := signer(t)
	put(t, notePath(t), noteOn("designer", "2026-12-01"))
	noLine := checkerOf(t, "designer", release, "2026-12-03", 0)
	want(t, noLine.StartTrial(context.Background(), nil, bin), licenses.TrialFailed)
	if f, err := noLine.Read(); err != nil || f.Trial != nil {
		t.Fatalf("found = %+v, %v", f, err)
	}
	if f, err := checkerOf(t, "designer", dev, "2026-12-03", 0).Read(); err != nil || f.Trial == nil {
		t.Fatalf("a dev build with no line: %+v, %v", f, err)
	}
}

// The key StartTrial took is ignored: any bytes, or none, start the same trial.
func TestTheKeyIsIgnored(t *testing.T) {
	bin := install(t)
	_, release, _ := signer(t)
	got := checker(t, release, "2026-12-03").StartTrial(context.Background(), []byte("anything"), bin)
	want(t, got, licenses.TrialStarted)
}

func TestAFailedWriteLeavesTheOldNoteWhole(t *testing.T) {
	bin := install(t)
	_, release, _ := signer(t)
	want(t, startTrial(t, release, "2026-12-03", bin), licenses.TrialStarted)
	before, _ := readNote(t)
	folder := filepath.Dir(notePath(t))
	if err := os.Chmod(folder, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(folder, 0o700) })
	touch(t, bin, installed.Add(time.Hour))
	want(t, startTrial(t, release, "2027-01-10", bin), licenses.TrialFailed)
	if after, _ := readNote(t); !bytes.Equal(after, before) {
		t.Fatal("the old note is not whole")
	}
}

func TestABinaryThatIsNotThereIsReported(t *testing.T) {
	install(t)
	_, release, _ := signer(t)
	want(t, startTrial(t, release, "2026-12-03", filepath.Join(t.TempDir(), "gone")), licenses.TrialFailed)
	if _, there := readNote(t); there {
		t.Fatal("a note was written without a stamp")
	}
}

func TestALinkToTheBinaryIsTheSameInstall(t *testing.T) {
	bin := install(t)
	_, release, _ := signer(t)
	link := filepath.Join(t.TempDir(), "designer-link")
	if err := os.Symlink(bin, link); err != nil {
		t.Fatal(err)
	}
	want(t, startTrial(t, release, "2026-12-03", link), licenses.TrialStarted)
	want(t, startTrial(t, release, "2026-12-04", bin), licenses.TrialKept)
	want(t, startTrial(t, release, "2026-12-05", link), licenses.TrialKept)
	if tr := trialOn(t, release, "2026-12-05"); tr.Binary != bin {
		t.Fatalf("binary = %s, want %s", tr.Binary, bin)
	}
}

// Starts at once write one note: the others find it as they wrote theirs, through the store's compare-and-put, and keep it.
func TestStartsAtOnceWriteOneTrial(t *testing.T) {
	bin := install(t)
	_, release, _ := signer(t)
	c := checker(t, release, "2026-12-03")
	got := make([]licenses.TrialStart, 8)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = c.StartTrial(context.Background(), nil, bin)
		}()
	}
	wg.Wait()
	n := 0
	for _, r := range got {
		switch r.Outcome {
		case licenses.TrialStarted:
			n++
		case licenses.TrialKept:
		default:
			t.Errorf("outcome = %s (%s)", r.Outcome, r.Reason)
		}
	}
	if n != 1 {
		t.Fatalf("%d started, want 1: %+v", n, got)
	}
	if tr := trialOn(t, release, "2026-12-03"); tr == nil || tr.Ended {
		t.Fatalf("trial = %+v", tr)
	}
}

// Only an active license that runs in this build means no trial: one of another line, as after an upgrade, or one not begun leaves the trial to start (ADR-L9, L10).
func TestAnActiveLicenseThatDoesNotRunHereLeavesTheTrial(t *testing.T) {
	for name, c := range map[string]struct {
		l     licenses.License
		line  int
		today string
	}{
		"of an older line": {perpetual, 2, "2026-12-03"},
		"not begun":        {license, 1, "2026-11-01"},
	} {
		t.Run(name, func(t *testing.T) {
			bin := install(t)
			k, release, _ := signer(t)
			put(t, appFile(t, "designer", c.l.ID), sign(t, k, c.l))
			ch := checkerOf(t, "designer", release, c.today, c.line)
			if f, _ := ch.Read(); active(f) != c.l.ID {
				t.Fatalf("active = %q", active(f))
			}
			want(t, ch.StartTrial(context.Background(), nil, bin), licenses.TrialStarted)
		})
	}
}
