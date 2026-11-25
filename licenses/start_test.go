package licenses_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"

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
	raw, err := os.ReadFile(productLicense(t))
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
	got := startTrial(t, sign(t, k, releaseTrial), release, "2026-12-03", bin)
	want(t, got, licenses.TrialStarted)
	if got.File != productLicense(t) {
		t.Fatalf("file = %s", got.File)
	}
	raw, _ := read(t)
	if err := contracts.Validate("mankit/v1/license-file.schema.json", raw); err != nil {
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
	key := sign(t, k, releaseTrial)
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
			key := sign(t, k, releaseTrial)
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

// A bought product license, or a person's trial key, at the app's place is never touched and no trial is written.
func TestTheAppsOwnLicenseIsNeverTouched(t *testing.T) {
	k, release, _ := signer(t)
	product := licenses.License{ID: "acme-designer", Kind: licenses.Product, For: "Acme Ltd", Products: map[string][]string{"designer": {"events"}}, Issued: day("2026-11-06"), Until: day("2027-11-06"), KeyID: "test-2026"}
	person := licenses.License{ID: "ada-trial", Kind: licenses.Trial, For: "ada@acme.example", Issued: day("2026-11-19"), Until: day("2026-12-02"), KeyID: "test-2026"}
	for name, l := range map[string]licenses.License{"a bought product license": product, "a person's trial key, ended": person} {
		t.Run(name, func(t *testing.T) {
			bin, _ := install(t)
			raw := sign(t, k, l)
			put(t, productLicense(t), raw)
			want(t, startTrial(t, sign(t, k, releaseTrial), release, "2026-12-03", bin), licenses.TrialSkipped)
			if after, _ := read(t); !bytes.Equal(after, raw) {
				t.Fatal("the license was touched")
			}
		})
	}
}

// An org license means no trial is written at the app's place.
func TestAnOrgLicenseMeansNoTrial(t *testing.T) {
	bin, dir := install(t)
	k, release, _ := signer(t)
	put(t, filepath.Join(dir, "license"), sign(t, k, orgLicense))
	want(t, startTrial(t, sign(t, k, releaseTrial), release, "2026-12-03", bin), licenses.TrialSkipped)
	if _, there := read(t); there {
		t.Fatal("a trial was written beside the org license")
	}
}

// A file it cannot take is never taken for no license, so nothing is written over it (G-CODE-6).
func TestAFileItCannotTakeIsLeftAlone(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	put(t, productLicense(t), []byte("not a license\n"))
	got := startTrial(t, sign(t, k, releaseTrial), release, "2026-12-03", bin)
	want(t, got, licenses.TrialSkipped)
	if after, _ := read(t); string(after) != "not a license\n" {
		t.Fatalf("the file was touched: %q", after)
	}
}

// After the key's until it starts no trial, and renews none.
func TestAKeyPastItsUntilStartsNoTrial(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	key := sign(t, k, releaseTrial)
	want(t, startTrial(t, key, release, "2027-11-24", bin), licenses.TrialSkipped)
	if _, there := read(t); there {
		t.Fatal("a trial was written after the key's until")
	}
	want(t, startTrial(t, key, release, "2027-11-23", bin), licenses.TrialStarted)
	before, _ := read(t)
	touch(t, bin, installed.Add(time.Hour))
	want(t, startTrial(t, key, release, "2027-11-24", bin), licenses.TrialSkipped)
	if after, _ := read(t); !bytes.Equal(after, before) {
		t.Fatal("the trial was renewed after the key's until")
	}
}

// A write that fails leaves the old file whole and is reported, never a panic or an exit.
func TestAFailedWriteLeavesTheOldFileWhole(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	key := sign(t, k, releaseTrial)
	want(t, startTrial(t, key, release, "2026-12-03", bin), licenses.TrialStarted)
	before, _ := read(t)
	folder := filepath.Dir(productLicense(t))
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

// A key the build does not trust, or one that is no release-trial, is refused as any license is; nothing is written.
func TestABuiltInKeyIsCheckedAsAnyLicense(t *testing.T) {
	k, release, dev := signer(t)
	devKey := releaseTrial
	devKey.KeyID = "dev-2026"
	person := licenses.License{ID: "ada-trial", Kind: licenses.Trial, For: "ada@acme.example", Issued: day("2026-11-19"), Until: day("2026-12-02"), KeyID: "test-2026"}
	for name, c := range map[string]struct {
		key   []byte
		trust licenses.Trust
	}{
		"a dev key in a release":    {sign(t, k, devKey), release},
		"a prod key in a dev build": {sign(t, k, releaseTrial), dev},
		"a person's trial key":      {sign(t, k, person), release},
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
	want(t, startTrial(t, sign(t, k, releaseTrial), release, "2026-12-03", filepath.Join(t.TempDir(), "gone")), licenses.TrialFailed)
	if _, there := read(t); there {
		t.Fatal("a trial was written without a stamp")
	}
}

// A link to the binary, as macOS's os.Executable may give, stamps the same install as its real path, so switching between them keeps the trial.
func TestALinkToTheBinaryIsTheSameInstall(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	key := sign(t, k, releaseTrial)
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
	want(t, startTrial(t, sign(t, k, releaseTrial), release, "2026-12-03", bin), licenses.TrialSkipped)
	if _, there := read(t); there {
		t.Fatal("a trial was written beside an org file it cannot take")
	}
}

// Starts at the same moment write one trial: the first starts it, the others find it there.
func TestStartsAtOnceWriteOneTrial(t *testing.T) {
	bin, _ := install(t)
	k, release, _ := signer(t)
	key := sign(t, k, releaseTrial)
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
