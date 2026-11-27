package licenses_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/licenses"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/store"
)

var all = []features.Feature{
	{ID: "edit", Title: "Change a design", Covers: "apply and edit"},
	{ID: "events", Title: "Live events", Covers: "the /api/events stream", Tier: features.Enterprise},
	{ID: "sync", Title: "Sync designs", Covers: "designs on several machines", Tier: features.Enterprise},
}

// org is the folder of the org's own files in a test (home.OrgDir), set through XDG_CONFIG_HOME as on a machine: <Org>-dev, as a test binary is a dev build.
func org(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	return filepath.Join(base, "elderlygate-dev")
}

// orgLicense is the one license file, in the org's folder.
func orgLicense(t *testing.T) string {
	t.Helper()
	dir, err := home.OrgDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "license")
}

// appLicense is the app's own license file, its trial, in its config folder (home.File).
func appLicense(t *testing.T) string {
	t.Helper()
	path, err := home.File("designer", "license")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// checker is designer's Checker in Designer 1 on the day.
func checker(t *testing.T, trust licenses.Trust, today string) *licenses.Checker {
	t.Helper()
	return checkerOfLine(t, trust, today, 1)
}

func checkerOfLine(t *testing.T, trust licenses.Trust, today string, line int) *licenses.Checker {
	t.Helper()
	c, err := licenses.New(licenses.Config{ID: "designer", Line: line, Trust: trust, Now: func() time.Time { return day(today).Add(15 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func put(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func list(c *licenses.Checker) *features.List {
	return features.New(all, features.Store{Off: func() ([]string, error) { return nil, nil }, Keep: func(string, bool) error { return nil }}, c.Closer(all))
}

func on(t *testing.T, l *features.List) []string {
	t.Helper()
	states, err := l.States()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range states {
		if s.On {
			ids = append(ids, s.ID)
		} else if s.By != features.License {
			t.Fatalf("%s off by %q", s.ID, s.By)
		}
	}
	return ids
}

func TestWithNoLicenseOnlyFreeFeaturesRun(t *testing.T) {
	org(t)
	_, release, _ := signer(t)
	if got := on(t, list(checker(t, release, "2026-11-19"))); !slices.Equal(got, []string{"edit"}) {
		t.Fatalf("on = %v", got)
	}
}

func TestALicenseOpensWhatItNamesUntilItsLastDay(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, orgLicense(t), sign(t, k, license))
	if got := on(t, list(checker(t, release, "2027-11-06"))); !slices.Equal(got, []string{"edit", "events"}) {
		t.Fatalf("on its last day = %v", got)
	}
	// Ended, its features lock again (ADR-L3).
	if got := on(t, list(checker(t, release, "2027-11-07"))); !slices.Equal(got, []string{"edit"}) {
		t.Fatalf("after its last day = %v", got)
	}
}

// A license that names lines opens them only in a build of one of them, and never ends (ADR-L10).
func TestALicenseThatNamesLinesOpensThoseLinesOnly(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, orgLicense(t), sign(t, k, perpetual))
	for line, want := range map[int][]string{1: {"edit", "events", "sync"}, 2: {"edit"}, 0: {"edit"}} {
		c := checkerOfLine(t, release, "2040-01-01", line)
		if got := on(t, list(c)); !slices.Equal(got, want) {
			t.Errorf("line %d: on = %v, want %v", line, got, want)
		}
		f, err := c.Read()
		if err != nil || len(f.Taken) != 1 || f.Taken[0].Ended || !f.Taken[0].LastDay.IsZero() || f.Taken[0].Elsewhere != (line != 1) {
			t.Errorf("line %d: found = %+v, %v", line, f, err)
		}
	}
}

// One license file serves every app: one that names only another app is taken and opens nothing here.
func TestALicenseOfAnotherAppOpensNothingHere(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	other := license
	other.Products = map[string][]string{"kanban": {"sync"}}
	put(t, orgLicense(t), sign(t, k, other))
	f, err := checker(t, release, "2026-12-01").Read()
	if err != nil || len(f.Taken) != 1 || f.Taken[0].Running() || !f.Taken[0].Elsewhere {
		t.Fatalf("found = %+v, %v", f, err)
	}
}

// A file that cannot be read or verified is refused with its file and why, never taken for no license (G-CODE-6).
func TestAFileItCannotTakeIsRefusedWithItsFile(t *testing.T) {
	org(t)
	k, _, dev := signer(t)
	put(t, orgLicense(t), sign(t, k, license))
	put(t, appLicense(t), []byte("garbage"))
	f, err := checker(t, dev, "2026-11-19").Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Taken) != 0 || len(f.Refused) != 2 ||
		f.Refused[0].File != orgLicense(t) || f.Refused[0].Reason != licenses.ReasonOtherBuild ||
		f.Refused[1].File != appLicense(t) || f.Refused[1].Reason != licenses.ReasonUnreadable {
		t.Fatalf("found = %+v", f)
	}
}

// A license is kept in the org's folder and a trial as its app's own; an old file keeps its old place (ADR-L8).
func TestALicenseInThePlaceOfAnotherKindIsRefused(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	otherTrial := trial
	otherTrial.For, otherTrial.Lines = "Kanban 1", map[string][]int{"kanban": {1}}
	otherProduct := strings.Replace(oldProduct, `"designer"`, `"kanban"`, 1)
	for name, tc := range map[string]struct {
		raw []byte
		at  func(*testing.T) string
	}{
		"a license as the app's own":          {sign(t, k, license), appLicense},
		"a trial in the org's folder":         {start(t, sign(t, k, trial), "2026-12-03"), orgLicense},
		"another app's trial":                 {start(t, sign(t, k, otherTrial), "2026-12-03"), appLicense},
		"an old org license as the app's":     {signOld(t, k, oldOrg), appLicense},
		"an old product license as the org's": {signOld(t, k, oldProduct), orgLicense},
		"another app's old product license":   {signOld(t, k, otherProduct), appLicense},
		"an old release-trial as the org's":   {start(t, signOld(t, k, oldReleaseTrial), "2026-12-03"), orgLicense},
	} {
		t.Run(name, func(t *testing.T) {
			org(t)
			put(t, tc.at(t), tc.raw)
			f, err := checker(t, release, "2026-12-03").Read()
			if err != nil || len(f.Taken) != 0 || len(f.Refused) != 1 || f.Refused[0].Reason != licenses.ReasonWrongPlace {
				t.Fatalf("found = %+v, %v", f, err)
			}
		})
	}
}

// The old kinds are read in their old places for one release, as a license and a trial (ADR-L8).
func TestTheOldKindsStillOpenWhatTheyOpened(t *testing.T) {
	k, release, _ := signer(t)
	for name, tc := range map[string]struct {
		raw   []byte
		at    func(*testing.T) string
		today string
		want  []string
	}{
		"an old org license":              {signOld(t, k, oldOrg), orgLicense, "2026-12-03", []string{"edit", "events"}},
		"an old product license":          {signOld(t, k, oldProduct), appLicense, "2026-12-03", []string{"edit", "sync"}},
		"an old release-trial, started":   {start(t, signOld(t, k, oldReleaseTrial), "2026-12-03"), appLicense, "2026-12-16", []string{"edit", "events", "sync"}},
		"an old release-trial, ended":     {start(t, signOld(t, k, oldReleaseTrial), "2026-12-03"), appLicense, "2026-12-17", []string{"edit"}},
		"an old release-trial, unstarted": {signOld(t, k, oldReleaseTrial), appLicense, "2026-12-03", []string{"edit"}},
	} {
		t.Run(name, func(t *testing.T) {
			org(t)
			put(t, tc.at(t), tc.raw)
			if got := on(t, list(checker(t, release, tc.today))); !slices.Equal(got, tc.want) {
				t.Fatalf("on = %v, want %v", got, tc.want)
			}
		})
	}
}

// Add keeps the license in the org's folder, for every app, whatever org says (ADR-L8).
func TestAddKeepsTheLicenseInTheOrgsFolder(t *testing.T) {
	for _, asOrg := range []bool{true, false} {
		org(t)
		k, release, _ := signer(t)
		c := checker(t, release, "2026-11-19")
		raw := sign(t, k, perpetual)
		taken, err := c.Add(context.Background(), raw, asOrg)
		if err != nil || taken.File != orgLicense(t) {
			t.Fatalf("org %t: taken = %+v, %v", asOrg, taken, err)
		}
		if got, err := os.ReadFile(orgLicense(t)); err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("org %t: %s = %q, %v", asOrg, orgLicense(t), got, err)
		}
		if _, err := os.Stat(appLicense(t)); !os.IsNotExist(err) {
			t.Fatalf("org %t: the app's own file was written", asOrg)
		}
		if got := on(t, list(c)); !slices.Equal(got, []string{"edit", "events", "sync"}) {
			t.Fatalf("org %t: on = %v", asOrg, got)
		}
	}
}

// Add keeps a license of today's form only: an old file is read but never kept, and a trial is built in (ADR-L8, L9).
func TestAddRefusesAnOldFileAndATrial(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	c := checker(t, release, "2026-11-19")
	for name, tc := range map[string]struct {
		raw  []byte
		want string
	}{
		"an old org license": {signOld(t, k, oldOrg), licenses.ReasonUnreadable},
		"a trial":            {sign(t, k, trial), licenses.ReasonWrongPlace},
	} {
		if _, err := c.Add(context.Background(), tc.raw, true); reason(err) != tc.want {
			t.Errorf("%s: err = %v, want %s", name, err, tc.want)
		}
	}
	if _, err := os.Stat(orgLicense(t)); !os.IsNotExist(err) {
		t.Fatal("a refused file was kept")
	}
}

// The license lands in a folder private to the user, made by the store when it is missing.
func TestAddKeepsTheLicenseInAFolderPrivateToTheUser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	k, release, _ := signer(t)
	if _, err := checker(t, release, "2026-11-19").Add(context.Background(), sign(t, k, license), true); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(os.Getenv("HOME"), ".config", home.Org+"-dev")
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "license"): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s is %v, want %v", path, got, want)
		}
	}
}

// A linked org folder is refused on write, and the refusal names the org folder's path.
func TestAddRefusesALinkedOrgFolderNamingItsPath(t *testing.T) {
	dir := org(t)
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	k, release, _ := signer(t)
	_, err := checker(t, release, "2026-11-19").Add(context.Background(), sign(t, k, license), true)
	var p *problem.Problem
	if !errors.As(err, &p) || !strings.Contains(p.Detail, "the folder "+dir) {
		t.Fatalf("err = %v", err)
	}
}

func TestAddWritesNothingItCannotTake(t *testing.T) {
	org(t)
	_, release, _ := signer(t)
	if _, err := checker(t, release, "2026-11-19").Add(context.Background(), []byte("garbage"), true); reason(err) != licenses.ReasonUnreadable {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(orgLicense(t)); !os.IsNotExist(err) {
		t.Fatal("a refused license was written")
	}
}

// A license before its first day opens nothing yet.
func TestALicenseBeforeItsFirstDayOpensNothing(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, orgLicense(t), sign(t, k, license))
	if got := on(t, list(checker(t, release, "2026-11-05"))); !slices.Equal(got, []string{"edit"}) {
		t.Fatalf("on before its first day = %v", got)
	}
}

// Fleet puts license files in from its secret store as links (ADR-L8): a link to a file is read, a link to anything else refused.
func TestALinkToALicenseIsReadAndToAFolderRefused(t *testing.T) {
	dir := org(t)
	k, release, _ := signer(t)
	secrets := t.TempDir()
	put(t, filepath.Join(secrets, "org"), sign(t, k, license))
	for _, d := range []string{dir, filepath.Dir(appLicense(t))} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(secrets, "org"), orgLicense(t)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secrets, appLicense(t)); err != nil {
		t.Fatal(err)
	}
	f, err := checker(t, release, "2026-11-19").Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Taken) != 1 || f.Taken[0].Kind != licenses.KindLicense || len(f.Refused) != 1 || f.Refused[0].Reason != licenses.ReasonUnreadable {
		t.Fatalf("found = %+v", f)
	}
}

// A dev build keeps the license in <Org>-dev (ADR-L5): it never reads the release's license, and license add never writes over it.
func TestADevBuildNeverTouchesTheReleasesLicense(t *testing.T) {
	dir := org(t)
	releases := filepath.Join(filepath.Dir(dir), "elderlygate", "license")
	k, _, dev := signer(t)
	prodRaw := sign(t, k, license)
	put(t, releases, prodRaw)
	f, err := checker(t, dev, "2026-11-19").Read()
	if err != nil || len(f.Taken) != 0 || len(f.Refused) != 0 {
		t.Fatalf("a dev build read the release's license: %+v, %v", f, err)
	}
	devLicense := license
	devLicense.KeyID = "dev-2026"
	devRaw := sign(t, k, devLicense)
	taken, err := checker(t, dev, "2026-11-19").Add(context.Background(), devRaw, true)
	if err != nil || taken.File != filepath.Join(dir, "license") {
		t.Fatalf("add = %+v, %v; want it in %s", taken, err, dir)
	}
	if got, _ := os.ReadFile(releases); !bytes.Equal(got, prodRaw) {
		t.Fatal("add in a dev build wrote over the release's license")
	}
}

// A started trial opens every enterprise feature of its app, counted from its started day for its days: started 3 Dec for 14 days, its last day is 16 Dec (ADR-L9).
func TestATrialRunsItsDaysFromItsStartedDay(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, appLicense(t), start(t, sign(t, k, trial), "2026-12-03"))
	for _, c := range []struct {
		today string
		on    []string
		left  int
		early bool
	}{
		{"2026-12-02", []string{"edit"}, 15, true},
		{"2026-12-03", []string{"edit", "events", "sync"}, 14, false},
		{"2026-12-16", []string{"edit", "events", "sync"}, 1, false},
		{"2026-12-17", []string{"edit"}, 0, false},
	} {
		c2 := checker(t, release, c.today)
		if got := on(t, list(c2)); !slices.Equal(got, c.on) {
			t.Errorf("%s: on = %v, want %v", c.today, got, c.on)
		}
		f, err := c2.Read()
		if err != nil || len(f.Taken) != 1 {
			t.Fatalf("%s: found = %+v, %v", c.today, f, err)
		}
		if got := f.Taken[0]; !got.LastDay.Equal(day("2026-12-16")) || got.DaysLeft != c.left || got.Ended != (c.left == 0) || got.Early != c.early {
			t.Errorf("%s: taken = %+v", c.today, got)
		}
	}
}

// A trial opens in every line's build: its line names what it was signed for, not a lock (ADR-L9).
func TestATrialOpensWhateverTheBuildsLine(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, appLicense(t), start(t, sign(t, k, trial), "2026-12-03"))
	if got := on(t, list(checkerOfLine(t, release, "2026-12-03", 2))); !slices.Equal(got, []string{"edit", "events", "sync"}) {
		t.Fatalf("on = %v", got)
	}
}

// A started day edited to before the key's issued day opens nothing before that issued day.
func TestATrialStartedBeforeItsIssuedDayOpensNothingEarly(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, appLicense(t), start(t, sign(t, k, trial), "2026-11-20"))
	for today, want := range map[string][]string{"2026-11-22": {"edit"}, "2026-11-23": {"edit", "events", "sync"}} {
		if got := on(t, list(checker(t, release, today))); !slices.Equal(got, want) {
			t.Errorf("%s: on = %v, want %v", today, got, want)
		}
	}
}

// A trial with no started record is the key as signed: it opens nothing, and as its offer never ends it never ends either.
func TestATrialNotStartedOpensNothing(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, appLicense(t), sign(t, k, trial))
	for _, today := range []string{"2026-12-03", "2040-01-01"} {
		c := checker(t, release, today)
		if got := on(t, list(c)); !slices.Equal(got, []string{"edit"}) {
			t.Errorf("%s: on = %v", today, got)
		}
		f, err := c.Read()
		if err != nil || len(f.Taken) != 1 {
			t.Fatalf("%s: found = %+v, %v", today, f, err)
		}
		if got := f.Taken[0]; got.Running() || got.Ended || !got.Early || !got.LastDay.IsZero() || got.DaysLeft != 0 {
			t.Errorf("%s: taken = %+v", today, got)
		}
	}
}

// remove takes away what Removing shows, as license remove does, and says which files.
func remove(t *testing.T, c *licenses.Checker) ([]string, error) {
	t.Helper()
	shown, err := c.Removing()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, r := range shown {
		files = append(files, r.File)
	}
	return files, c.Remove(context.Background(), shown)
}

// Remove takes away the license in the org's folder, an old product license, and a file refused in the app's folder, and leaves the trial.
func TestRemoveTakesTheLicenseAndLeavesTheTrial(t *testing.T) {
	k, release, _ := signer(t)
	for name, tc := range map[string]struct {
		at  func(*testing.T) string
		raw []byte
	}{
		"a license":                       {orgLicense, sign(t, k, license)},
		"a refused file":                  {orgLicense, []byte("garbage")},
		"an old product license":          {appLicense, signOld(t, k, oldProduct)},
		"an old org license":              {orgLicense, signOld(t, k, oldOrg)},
		"a file refused as the app's own": {appLicense, []byte("garbage")},
		"a license as the app's own":      {appLicense, sign(t, k, license)},
	} {
		t.Run(name, func(t *testing.T) {
			org(t)
			put(t, tc.at(t), tc.raw)
			if tc.at(t) == orgLicense(t) {
				put(t, appLicense(t), start(t, sign(t, k, trial), "2026-12-03"))
			}
			c := checker(t, release, "2026-12-05")
			files, err := remove(t, c)
			if err != nil || !slices.Equal(files, []string{tc.at(t)}) {
				t.Fatalf("removed %v, %v", files, err)
			}
			if _, err := os.Stat(tc.at(t)); !os.IsNotExist(err) {
				t.Fatal("the license is still there")
			}
			f, err := c.Read()
			if err != nil || len(f.Refused) != 0 {
				t.Fatalf("found = %+v, %v", f, err)
			}
			for _, tk := range f.Taken {
				if tk.Kind != licenses.Trial || !tk.Running() {
					t.Fatalf("left = %+v", tk)
				}
			}
		})
	}
}

// With no license, Remove removes nothing and says so; a trial alone is never removed.
func TestRemoveWithNoLicenseIsNotFound(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	raw := start(t, sign(t, k, trial), "2026-12-03")
	put(t, appLicense(t), raw)
	_, err := remove(t, checker(t, release, "2026-12-05"))
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeNotFound {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(appLicense(t)); !bytes.Equal(got, raw) {
		t.Fatal("the trial was touched")
	}
}

// Remove takes away only what was shown: a file changed since it was shown is left as it is, a Problem refused.
func TestRemoveTakesOnlyWhatWasShown(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, orgLicense(t), sign(t, k, license))
	c := checker(t, release, "2026-12-05")
	shown, err := c.Removing()
	if err != nil {
		t.Fatal(err)
	}
	other := sign(t, k, perpetual)
	put(t, orgLicense(t), other)
	err = c.Remove(context.Background(), shown)
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeRefused {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(orgLicense(t)); !bytes.Equal(got, other) {
		t.Fatal("the license put since was touched")
	}
	if err := c.Remove(context.Background(), []licenses.Removal{{File: filepath.Join(t.TempDir(), "license"), Sum: shown[0].Sum}}); err == nil {
		t.Fatal("a file that is no license file of the app was taken")
	}
}

// Removing never offers a trial in the app's folder, taken or refused, whatever the refusal; a file that is no trial is offered.
func TestRemovingNeverOffersATrialInTheAppsFolder(t *testing.T) {
	k, release, _ := signer(t)
	started := start(t, sign(t, k, trial), "2026-12-03")
	forOther := trial
	forOther.Lines = map[string][]int{"kanban": {1}}
	forOther.ID = "kanban-trial"
	untrusted := trial
	untrusted.KeyID = "dev-2026"
	for name, tc := range map[string]struct {
		raw     []byte
		offered bool
	}{
		"a started trial":                     {started, false},
		"a trial of another product":          {start(t, sign(t, k, forOther), "2026-12-03"), false},
		"a trial of an untrusted key":         {sign(t, k, untrusted), false},
		"an old release-trial":                {start(t, signOld(t, k, oldReleaseTrial), "2026-12-03"), false},
		"a trial whose signature is tampered": {edit(t, started, func(f map[string]any) { f["spec"].(map[string]any)["signature"] = "AAAA" }), false},
		"a license in the app's folder":       {sign(t, k, license), true},
		"garbage":                             {[]byte("garbage"), true},
		"a trial too big to read":             {bytes.Repeat([]byte("x"), 70<<10), true},
	} {
		t.Run(name, func(t *testing.T) {
			org(t)
			put(t, appLicense(t), tc.raw)
			put(t, orgLicense(t), sign(t, k, license))
			shown, err := checker(t, release, "2026-12-05").Removing()
			if err != nil {
				t.Fatal(err)
			}
			own := slices.ContainsFunc(shown, func(r licenses.Removal) bool { return r.File == appLicense(t) })
			if own != tc.offered {
				t.Fatalf("shown = %+v, offered the app's file = %v", shown, own)
			}
		})
	}
}

// Remove refuses the app's own file when it is a trial now, as a guard for a caller that hands in a Removal itself, and removes nothing.
func TestRemoveRefusesATrialAsTheAppsOwn(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, orgLicense(t), sign(t, k, license))
	put(t, appLicense(t), []byte("garbage"))
	c := checker(t, release, "2026-12-05")
	shown, err := c.Removing()
	if err != nil || len(shown) != 2 {
		t.Fatalf("shown = %+v, %v", shown, err)
	}
	trialFile := start(t, sign(t, k, trial), "2026-12-03")
	put(t, appLicense(t), trialFile)
	for i, r := range shown {
		if r.File == appLicense(t) {
			shown[i].Sum = store.SumOf(trialFile)
		}
	}
	err = c.Remove(context.Background(), shown)
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeRefused {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(appLicense(t)); !bytes.Equal(got, trialFile) {
		t.Fatal("the trial was touched")
	}
	if _, err := os.Stat(orgLicense(t)); err != nil {
		t.Fatal("the org's license was removed beside a refused trial")
	}
}

// Remove leaves every file when one of them cannot be removed: no partial removal.
func TestRemoveRemovesAllOrNone(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, orgLicense(t), sign(t, k, license))
	put(t, appLicense(t), []byte("garbage"))
	c := checker(t, release, "2026-12-05")
	shown, err := c.Removing()
	if err != nil || len(shown) != 2 {
		t.Fatalf("shown = %+v, %v", shown, err)
	}
	put(t, orgLicense(t), sign(t, k, perpetual))
	err = c.Remove(context.Background(), shown)
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeRefused {
		t.Fatalf("err = %v", err)
	}
	for _, f := range []string{orgLicense(t), appLicense(t)} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s was removed by a refused Remove", f)
		}
	}
}
