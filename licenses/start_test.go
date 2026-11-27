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

// install is a temp HOME/XDG with a throwaway app binary written at installed, and the org's folder.
func install(t *testing.T) (bin, orgDir string) {
	t.Helper()
	orgDir = org(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	bin = filepath.Join(real(t, t.TempDir()), "designer")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, bin, installed)
	return bin, orgDir
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

func startTrial(t *testing.T, key []byte, release licenses.Trust, today, bin string) licenses.TrialStart {
	t.Helper()
	return checker(t, release, today).StartTrial(context.Background(), key, bin)
}

// read is the app's license file, and whether it is there.
func read(t *testing.T) ([]byte, bool) {
	t.Helper()
	raw, err := os.ReadFile(appLicense(t))
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return raw, true
}

// started is the trial the app's license file holds, read as the app reads it.
func started(t *testing.T, release licenses.Trust, today string) licenses.Taken {
	t.Helper()
	f, err := checker(t, release, today).Read()
	if err != nil || len(f.Taken) != 1 || f.Taken[0].Started == nil {
		t.Fatalf("found = %+v, %v", f, err)
	}
	return f.Taken[0]
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

// With no license the built-in key is written as the app's license file, started today and stamped with the binary (ADR-L9).
func TestWithNoLicenseTheBuiltInTrialStarts(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	got := startTrial(t, sign(t, k, trial), release, "2026-12-03", bin)
	want(t, got, licenses.TrialStarted)
	if got.File != appLicense(t) {
		t.Fatalf("file = %s", got.File)
	}
	raw, _ := read(t)
	if err := contracts.Validate("mankit/v3/license-file.schema.json", raw); err != nil {
		t.Fatalf("the written file does not meet its schema: %v\n%s", err, raw)
	}
	tk := started(t, release, "2026-12-03")
	if !tk.Running() || !tk.Started.Day.Equal(day("2026-12-03")) || tk.Started.Binary != bin || !tk.Started.FileTime.Equal(installed) || !tk.LastDay.Equal(day("2026-12-16")) {
		t.Fatalf("taken = %+v, started = %+v", tk, tk.Started)
	}
}

// The install that started the trial keeps it, ended or not: starting again never stretches it.
func TestAMatchingStampKeepsTheTrial(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	key := sign(t, k, trial)
	want(t, startTrial(t, key, release, "2026-12-03", bin), licenses.TrialStarted)
	before, _ := read(t)
	for _, today := range []string{"2026-12-04", "2026-12-30"} {
		want(t, startTrial(t, key, release, today, bin), licenses.TrialKept)
		if after, _ := read(t); !bytes.Equal(after, before) {
			t.Fatalf("%s: the file changed", today)
		}
	}
	if tk := started(t, release, "2026-12-30"); !tk.Ended {
		t.Fatalf("taken = %+v, want ended", tk)
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
			bin, _ := install(t)
			k, release, _ := signer(t)
			key := sign(t, k, trial)
			want(t, startTrial(t, key, release, "2026-12-03", bin), licenses.TrialStarted)
			bin = change(t, bin)
			want(t, startTrial(t, key, release, "2027-01-10", bin), licenses.TrialRenewed)
			info, _ := os.Stat(bin)
			tk := started(t, release, "2027-01-10")
			if !tk.Running() || !tk.Started.Day.Equal(day("2027-01-10")) || tk.Started.Binary != bin || !tk.Started.FileTime.Equal(info.ModTime().UTC().Truncate(time.Second)) {
				t.Fatalf("taken = %+v, started = %+v", tk, tk.Started)
			}
		})
	}
}

// An old product license, or an old person's trial key, which is refused, at the app's place is never touched and no trial is written.
func TestTheAppsOwnLicenseIsNeverTouched(t *testing.T) {
	k, release, _ := signer(t)
	for name, body := range map[string]string{"an old product license": oldProduct, "an old person's trial key": oldPersonTrial} {
		t.Run(name, func(t *testing.T) {
			bin, _ := install(t)
			raw := signOld(t, k, body)
			put(t, appLicense(t), raw)
			want(t, startTrial(t, sign(t, k, trial), release, "2026-12-03", bin), licenses.TrialSkipped)
			if after, _ := read(t); !bytes.Equal(after, raw) {
				t.Fatal("the license was touched")
			}
		})
	}
}

// A license, of today's form or an old org license, means no trial is written at the app's place.
func TestALicenseMeansNoTrial(t *testing.T) {
	k, release, _ := signer(t)
	for _, raw := range [][]byte{sign(t, k, license), signOld(t, k, oldOrg)} {
		bin, dir := install(t)
		put(t, filepath.Join(dir, "license"), raw)
		want(t, startTrial(t, sign(t, k, trial), release, "2026-12-03", bin), licenses.TrialSkipped)
		if _, there := read(t); there {
			t.Fatal("a trial was written beside the license")
		}
	}
}

// A removed license leaves the trial this install started as it was: kept, never a fresh one.
func TestARemovedLicenseKeepsTheTrial(t *testing.T) {
	bin, dir := install(t)
	k, release, _ := signer(t)
	key := sign(t, k, trial)
	want(t, startTrial(t, key, release, "2026-12-03", bin), licenses.TrialStarted)
	put(t, filepath.Join(dir, "license"), sign(t, k, license))
	if _, err := remove(t, checker(t, release, "2026-12-05")); err != nil {
		t.Fatal(err)
	}
	want(t, startTrial(t, key, release, "2026-12-30", bin), licenses.TrialKept)
	if tk := started(t, release, "2026-12-30"); !tk.Ended || !tk.Started.Day.Equal(day("2026-12-03")) {
		t.Fatalf("taken = %+v", tk)
	}
}

// An old release-trial this install started is kept as it is; a new install renews it in today's form.
func TestAnOldReleaseTrialIsKeptOrRenewed(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	old := edit(t, signOld(t, k, oldReleaseTrial), func(f map[string]any) {
		f["spec"].(map[string]any)["started"] = map[string]any{"day": "2026-12-03", "binary": bin, "fileTime": installed.Format(time.RFC3339)}
	})
	put(t, appLicense(t), old)
	want(t, startTrial(t, sign(t, k, trial), release, "2026-12-05", bin), licenses.TrialKept)
	if after, _ := read(t); !bytes.Equal(after, old) {
		t.Fatal("the old trial was touched")
	}
	touch(t, bin, installed.Add(time.Hour))
	want(t, startTrial(t, sign(t, k, trial), release, "2027-01-10", bin), licenses.TrialRenewed)
	raw, _ := read(t)
	if err := contracts.Validate("mankit/v3/license-file.schema.json", raw); err != nil {
		t.Fatalf("the renewed file is not of today's form: %v\n%s", err, raw)
	}
}

// A file it cannot take is never taken for no license, so nothing is written over it (G-CODE-6).
func TestAFileItCannotTakeIsLeftAlone(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	put(t, appLicense(t), []byte("not a license\n"))
	got := startTrial(t, sign(t, k, trial), release, "2026-12-03", bin)
	want(t, got, licenses.TrialSkipped)
	if after, _ := read(t); string(after) != "not a license\n" {
		t.Fatalf("the file was touched: %q", after)
	}
}

// A file refused in the app's folder keeps the trial from starting until license remove takes it away; then the trial starts.
func TestARemovedRefusedFileLetsTheTrialStart(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	key := sign(t, k, trial)
	put(t, appLicense(t), []byte("not a license\n"))
	want(t, startTrial(t, key, release, "2026-12-03", bin), licenses.TrialSkipped)
	if files, err := remove(t, checker(t, release, "2026-12-03")); err != nil || len(files) != 1 || files[0] != appLicense(t) {
		t.Fatalf("removed %v, %v", files, err)
	}
	want(t, startTrial(t, key, release, "2026-12-03", bin), licenses.TrialStarted)
}

// A key of another line than the build's fails and writes nothing; a dev build with no line takes the key's, a release with none starts no trial.
func TestAKeyOfAnotherLineFails(t *testing.T) {
	k, release, dev := signer(t)
	key := sign(t, k, trial)
	bin, _ := install(t)
	if got := checkerOfLine(t, release, "2026-12-03", 2).StartTrial(context.Background(), key, bin); got.Outcome != licenses.TrialFailed {
		t.Fatalf("line 2: %v", got)
	}
	if _, there := read(t); there {
		t.Fatal("a key of another line was written")
	}
	if got := checkerOfLine(t, release, "2026-12-03", 0).StartTrial(context.Background(), key, bin); got.Outcome != licenses.TrialFailed || !strings.Contains(got.Reason, "no product line") {
		t.Fatalf("a release with no line: %v", got)
	}
	if _, there := read(t); there {
		t.Fatal("a release with no line wrote a trial")
	}
	devKey := trial
	devKey.KeyID = "dev-2026"
	want(t, checkerOfLine(t, dev, "2026-12-03", 0).StartTrial(context.Background(), sign(t, k, devKey), bin), licenses.TrialStarted)
}

// A trial's offer never ends: a key starts a trial on any day (ADR-L9).
func TestATrialKeyStartsATrialOnAnyDay(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	want(t, startTrial(t, sign(t, k, trial), release, "2040-01-01", bin), licenses.TrialStarted)
}

// A write that fails leaves the old file whole and is reported, never a panic or an exit.
func TestAFailedWriteLeavesTheOldFileWhole(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	key := sign(t, k, trial)
	want(t, startTrial(t, key, release, "2026-12-03", bin), licenses.TrialStarted)
	before, _ := read(t)
	folder := filepath.Dir(appLicense(t))
	if err := os.Chmod(folder, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(folder, 0o700) })
	touch(t, bin, installed.Add(time.Hour))
	want(t, startTrial(t, key, release, "2027-01-10", bin), licenses.TrialFailed)
	if after, _ := read(t); !bytes.Equal(after, before) {
		t.Fatal("the old file is not whole")
	}
}

// A key the build does not trust, one that is no trial, or one of the old form, which mankit never writes, is refused; nothing is written.
func TestABuiltInKeyIsCheckedAsAnyLicense(t *testing.T) {
	k, release, dev := signer(t)
	devKey := trial
	devKey.KeyID = "dev-2026"
	other := trial
	other.Lines = map[string][]int{"kanban": {1}}
	for name, c := range map[string]struct {
		key   []byte
		trust licenses.Trust
	}{
		"a dev key in a release":    {sign(t, k, devKey), release},
		"a prod key in a dev build": {sign(t, k, trial), dev},
		"a license":                 {sign(t, k, license), release},
		"another app's trial":       {sign(t, k, other), release},
		"an old release-trial key":  {signOld(t, k, oldReleaseTrial), release},
		"an old person's trial key": {signOld(t, k, oldPersonTrial), release},
	} {
		t.Run(name, func(t *testing.T) {
			bin, _ := install(t)
			want(t, startTrial(t, c.key, c.trust, "2026-12-03", bin), licenses.TrialFailed)
			if _, there := read(t); there {
				t.Fatal("a refused key was written")
			}
		})
	}
}

// A binary that is not there cannot be stamped: reported, nothing written.
func TestABinaryThatIsNotThereIsReported(t *testing.T) {
	install(t)
	k, release, _ := signer(t)
	want(t, startTrial(t, sign(t, k, trial), release, "2026-12-03", filepath.Join(t.TempDir(), "gone")), licenses.TrialFailed)
	if _, there := read(t); there {
		t.Fatal("a trial was written without a stamp")
	}
}

// A link to the binary, as macOS's os.Executable may give, stamps the same install as its real path, so switching between them keeps the trial.
func TestALinkToTheBinaryIsTheSameInstall(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	key := sign(t, k, trial)
	link := filepath.Join(t.TempDir(), "designer-link")
	if err := os.Symlink(bin, link); err != nil {
		t.Fatal(err)
	}
	want(t, startTrial(t, key, release, "2026-12-03", link), licenses.TrialStarted)
	want(t, startTrial(t, key, release, "2026-12-04", bin), licenses.TrialKept)
	want(t, startTrial(t, key, release, "2026-12-05", link), licenses.TrialKept)
	if tk := started(t, release, "2026-12-05"); tk.Started.Binary != bin {
		t.Fatalf("binary = %s, want %s", tk.Started.Binary, bin)
	}
}

// A file at the org's place that it cannot take is never taken for no license, so no trial is written (G-CODE-6).
func TestAFileAtTheOrgsPlaceItCannotTakeMeansNoTrial(t *testing.T) {
	bin, dir := install(t)
	k, release, _ := signer(t)
	put(t, filepath.Join(dir, "license"), []byte("not a license\n"))
	want(t, startTrial(t, sign(t, k, trial), release, "2026-12-03", bin), licenses.TrialSkipped)
	if _, there := read(t); there {
		t.Fatal("a trial was written beside an org file it cannot take")
	}
}

// Starts at the same moment write one trial: the first starts it, the others find it there.
func TestStartsAtOnceWriteOneTrial(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	key := sign(t, k, trial)
	c := checker(t, release, "2026-12-03")
	got := make([]licenses.TrialStart, 8)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = c.StartTrial(context.Background(), key, bin)
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
	if tk := started(t, release, "2026-12-03"); !tk.Running() {
		t.Fatalf("taken = %+v", tk)
	}
}
