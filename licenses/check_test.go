package licenses_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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

// orgFile is the license file of the id in the org's licenses folder.
func orgFile(t *testing.T, id string) string {
	t.Helper()
	dir, err := home.OrgDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "licenses", id+".license")
}

// appFile is the license file of the id in app's own licenses folder.
func appFile(t *testing.T, app, id string) string {
	t.Helper()
	return appPath(t, app, filepath.Join("licenses", id+".license"))
}

// appPath is name in app's config folder (home.File).
func appPath(t *testing.T, app, name string) string {
	t.Helper()
	path, err := home.File(app, name)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// oldOrgFile and oldAppFile are the old single files, read as candidates for one release.
func oldOrgFile(t *testing.T) string {
	t.Helper()
	dir, err := home.OrgDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "license")
}

func oldAppFile(t *testing.T) string { t.Helper(); return appPath(t, "designer", "license") }

// checker is designer's Checker in Designer 1 on the day.
func checker(t *testing.T, trust licenses.Trust, today string) *licenses.Checker {
	t.Helper()
	return checkerOf(t, "designer", trust, today, 1)
}

func checkerOf(t *testing.T, app string, trust licenses.Trust, today string, line int) *licenses.Checker {
	t.Helper()
	c, err := licenses.New(licenses.Config{ID: app, Line: line, Trust: trust, Now: func() time.Time { return day(today).Add(15 * time.Hour) }})
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

// read is what designer's Checker reads on the day.
func found(t *testing.T, trust licenses.Trust, today string) licenses.Found {
	t.Helper()
	f, err := checker(t, trust, today).Read()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// active is the id of the active license, empty with none.
func active(f licenses.Found) string {
	if a := f.Active(); a != nil {
		return a.ID
	}
	return ""
}

// named is license with its id, issued day and features.
func named(id, issued string, ids ...string) licenses.License {
	l := license
	l.ID, l.Issued, l.Products = id, day(issued), map[string][]string{"designer": ids}
	return l
}

// noteOn is a trial note of app started on the day, as the app writes it.
func noteOn(app, on string) []byte {
	return []byte(`{"apiVersion":"elderlygate/mankit/trial/v1","kind":"TrialStarted","metadata":{"id":"` + app + `"},"spec":{"day":"` + on + `","binary":"/home/ada/.local/bin/designer","fileTime":"` + on + `T09:14:05Z"}}`)
}

func TestWithNoLicenseOnlyFreeFeaturesRun(t *testing.T) {
	org(t)
	_, release, _ := signer(t)
	if got := on(t, list(checker(t, release, "2026-11-19"))); !slices.Equal(got, []string{"edit"}) {
		t.Fatalf("on = %v", got)
	}
}

// An org license that names the app opens what it names until its last day (ADR-L3).
func TestALicenseOpensWhatItNamesUntilItsLastDay(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, orgFile(t, "acme-2026"), sign(t, k, license))
	if got := on(t, list(checker(t, release, "2026-11-19"))); !slices.Equal(got, []string{"edit", "events"}) {
		t.Fatalf("on = %v", got)
	}
	if got := on(t, list(checker(t, release, "2027-11-07"))); !slices.Equal(got, []string{"edit"}) {
		t.Fatalf("on after its last day = %v", got)
	}
	f := found(t, release, "2026-11-19")
	if len(f.Taken) != 1 || f.Taken[0].Place != licenses.PlaceOrg || !f.Taken[0].Active {
		t.Fatalf("found = %+v", f)
	}
}

// A license that names lines opens only in a build of one of them (ADR-L10), and stays the active one in another.
func TestALicenseThatNamesLinesOpensThoseLinesOnly(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, appFile(t, "designer", "acme-perpetual"), sign(t, k, perpetual))
	if got := on(t, list(checkerOf(t, "designer", release, "2030-01-01", 1))); !slices.Equal(got, []string{"edit", "events", "sync"}) {
		t.Fatalf("on in Designer 1 = %v", got)
	}
	c := checkerOf(t, "designer", release, "2030-01-01", 2)
	if got := on(t, list(c)); !slices.Equal(got, []string{"edit"}) {
		t.Fatalf("on in Designer 2 = %v", got)
	}
	if f, _ := c.Read(); active(f) != "acme-perpetual" || f.Taken[0].Running() {
		t.Fatalf("found in Designer 2 = %+v", f)
	}
}

// An org license that does not name the app is not one of its licenses; an app's own that does not name it is refused (ADR-L8).
func TestALicenseOfAnotherAppIsNoneOfThisApps(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	other := license
	other.ID, other.Products = "acme-kanban", map[string][]string{"kanban": {"sync"}}
	put(t, orgFile(t, "acme-kanban"), sign(t, k, other))
	f := found(t, release, "2026-11-19")
	if len(f.Taken) != 0 || len(f.Refused) != 0 {
		t.Fatalf("found = %+v", f)
	}
	put(t, appFile(t, "designer", "acme-kanban"), sign(t, k, other))
	f = found(t, release, "2026-11-19")
	if len(f.Taken) != 0 || len(f.Refused) != 1 || f.Refused[0].Reason != licenses.ReasonWrongPlace || f.Refused[0].ID != "acme-kanban" {
		t.Fatalf("found = %+v", f)
	}
}

// A file it cannot take is refused with its file and the id it names, never taken for no license, and is never the active one.
func TestAFileItCannotTakeIsRefusedWithItsFile(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	bad := edit(t, sign(t, k, license), func(f map[string]any) { f["spec"].(map[string]any)["signature"] = "AAAA" })
	put(t, orgFile(t, "acme-2026"), bad)
	put(t, appFile(t, "designer", "garbage"), []byte("not json"))
	f := found(t, release, "2026-11-19")
	if len(f.Taken) != 0 || len(f.Refused) != 2 || active(f) != "" {
		t.Fatalf("found = %+v", f)
	}
	want := map[string]licenses.Refused{
		"garbage":   {File: appFile(t, "designer", "garbage"), ID: "garbage", Reason: licenses.ReasonUnreadable},
		"acme-2026": {File: orgFile(t, "acme-2026"), ID: "acme-2026", Reason: licenses.ReasonUnreadable},
	}
	for _, r := range f.Refused {
		w := want[r.ID]
		if r.File != w.File || r.Detail == "" {
			t.Fatalf("refused = %+v, want %+v", r, w)
		}
	}
}

// With no choice made, the newest license by issued that names the app is active, wherever it sits (ADR-L8).
func TestWithNoChoiceTheNewestIsActive(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, orgFile(t, "newest"), sign(t, k, named("newest", "2026-11-10", "events")))
	put(t, appFile(t, "designer", "older"), sign(t, k, named("older", "2026-11-01", "sync")))
	f := found(t, release, "2026-11-19")
	if active(f) != "newest" || f.Chosen != "" || len(f.Taken) != 2 || f.Taken[0].ID != "older" || f.Taken[0].Place != licenses.PlaceApp {
		t.Fatalf("found = %+v", f)
	}
	if got := on(t, list(checker(t, release, "2026-11-19"))); !slices.Equal(got, []string{"edit", "events"}) {
		t.Fatalf("on = %v, want only what the active license opens", got)
	}
}

// The chosen license stays active once it has ended or when its file is refused or gone: the app runs on its trial or free and never switches by itself (ADR-L8).
func TestTheChosenLicenseNeverSwitchesByItself(t *testing.T) {
	for name, c := range map[string]struct {
		put  func(t *testing.T, k keys)
		want string
	}{
		"ended": {func(t *testing.T, k keys) {
			l := named("chosen", "2026-01-01", "events")
			l.Until = day("2026-11-01")
			put(t, appFile(t, "designer", "chosen"), sign(t, k, l))
		}, "chosen"},
		"refused": {func(t *testing.T, k keys) {
			put(t, appFile(t, "designer", "chosen"), edit(t, sign(t, k, named("chosen", "2026-01-01", "events")), func(f map[string]any) {
				f["spec"].(map[string]any)["signature"] = "AAAA"
			}))
		}, ""},
		"gone": {func(*testing.T, keys) {}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			org(t)
			k, release, _ := signer(t)
			put(t, orgFile(t, "other"), sign(t, k, named("other", "2026-11-10", "events", "sync")))
			c.put(t, k)
			put(t, appPath(t, "designer", "license-active"), []byte("chosen\n"))
			put(t, appPath(t, "designer", "trial"), noteOn("designer", "2026-11-15"))
			f := found(t, release, "2026-11-19")
			if f.Chosen != "chosen" || active(f) != c.want {
				t.Fatalf("found = %+v", f)
			}
			if got := on(t, list(checker(t, release, "2026-11-19"))); !slices.Equal(got, []string{"edit", "events", "sync"}) {
				t.Fatalf("on = %v, want the trial's", got)
			}
			for _, ft := range all[1:] {
				if from, until := f.From(ft); from != licenses.FromTrial || !until.Equal(day("2026-11-28")) {
					t.Fatalf("%s from %s until %s", ft.ID, from, until)
				}
			}
		})
	}
}

// The old single files and the old form are read for one release as candidates, in their places (ADR-L8); an old trial there is refused as retired_kind and opens nothing.
func TestTheOldPlacesAndFormAreReadForOneRelease(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, oldOrgFile(t), signOld(t, k, oldOrg))
	put(t, oldAppFile(t), signOld(t, k, oldProduct))
	f := found(t, release, "2026-11-19")
	if len(f.Taken) != 2 || f.Taken[0].ID != "acme-designer" || f.Taken[0].Place != licenses.PlaceApp || f.Taken[1].ID != "acme-2026" || f.Taken[1].Place != licenses.PlaceOrg || active(f) != "acme-2026" {
		t.Fatalf("found = %+v", f)
	}
	for _, trial := range [][]byte{signOld(t, k, oldReleaseTrial), signAs(t, k, licenses.FileAPIVersion, oldTrial)} {
		put(t, oldAppFile(t), trial)
		f = found(t, release, "2026-11-19")
		if len(f.Taken) != 1 || len(f.Refused) != 1 || f.Refused[0].Reason != licenses.ReasonRetiredKind || f.Refused[0].File != oldAppFile(t) || f.Trial != nil {
			t.Fatalf("found = %+v", f)
		}
	}
}

// license add keeps the license in the org's licenses folder with --org, else in the app's own, and makes it the active one of this app only (ADR-L8).
func TestAddKeepsTheLicenseInItsPlaceAndChoosesItHere(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	ctx := context.Background()
	both := named("both", "2026-11-01", "events")
	both.Products["kanban"] = []string{"sync"}
	put(t, appPath(t, "kanban", "license-active"), []byte("kanban-own\n"))
	for _, c := range []struct {
		l    licenses.License
		org  bool
		file string
	}{
		{both, true, orgFile(t, "both")},
		{named("own", "2026-10-01", "sync"), false, appFile(t, "designer", "own")},
	} {
		raw := sign(t, k, c.l)
		got, err := checker(t, release, "2026-11-19").Add(ctx, raw, c.org)
		if err != nil {
			t.Fatal(err)
		}
		if kept, err := os.ReadFile(c.file); err != nil || !bytes.Equal(kept, raw) || got.File != c.file || !got.Active {
			t.Fatalf("%s: kept %q, %v; taken = %+v", c.l.ID, kept, err, got)
		}
		if f := found(t, release, "2026-11-19"); active(f) != c.l.ID || f.Chosen != c.l.ID {
			t.Fatalf("%s: found = %+v", c.l.ID, f)
		}
	}
	if raw, _ := os.ReadFile(appPath(t, "kanban", "license-active")); string(raw) != "kanban-own\n" {
		t.Fatalf("another app's choice changed: %q", raw)
	}
	if f, _ := checkerOf(t, "kanban", release, "2026-11-19", 1).Read(); len(f.Taken) != 1 || f.Taken[0].ID != "both" || active(f) != "" {
		t.Fatalf("kanban found = %+v", f)
	}
}

// Add keeps nothing it cannot take: an old file, a license that does not name the app, an id that cannot name a file, or garbage.
func TestAddWritesNothingItCannotTake(t *testing.T) {
	dir := org(t)
	k, release, _ := signer(t)
	other := named("acme-kanban", "2026-11-01")
	other.Products = map[string][]string{"kanban": {"sync"}}
	slash := named("acme/2026", "2026-11-01", "events")
	for raw, want := range map[string]string{
		string(signOld(t, k, oldOrg)):         licenses.ReasonUnreadable,
		string(sign(t, k, other)):             licenses.ReasonWrongPlace,
		string(sign(t, k, slash)):             licenses.ReasonUnreadable,
		"garbage":                             licenses.ReasonUnreadable,
		string(signOld(t, k, oldPersonTrial)): licenses.ReasonRetiredKind,
	} {
		for _, inOrg := range []bool{true, false} {
			if _, err := checker(t, release, "2026-11-19").Add(context.Background(), []byte(raw), inOrg); reason(err) != want {
				t.Fatalf("err = %v, want %s", err, want)
			}
		}
	}
	for _, d := range []string{dir, filepath.Dir(appPath(t, "designer", "x"))} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Fatalf("%s was made for a refused license", d)
		}
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
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "licenses", "acme-2026.license"): 0o600} {
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

// license use makes a license of this app's the active one; a license refused or not there is refused or not found, and the choice stays.
func TestUseChoosesOneOfTheAppsLicenses(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	ctx := context.Background()
	put(t, orgFile(t, "newest"), sign(t, k, named("newest", "2026-11-10", "events")))
	put(t, appFile(t, "designer", "older"), sign(t, k, named("older", "2026-11-01", "sync")))
	put(t, appFile(t, "designer", "broken"), []byte("not json"))
	c := checker(t, release, "2026-11-19")
	if got, err := c.Use(ctx, "older"); err != nil || !got.Active || got.ID != "older" {
		t.Fatalf("use = %+v, %v", got, err)
	}
	if f := found(t, release, "2026-11-19"); active(f) != "older" {
		t.Fatalf("found = %+v", f)
	}
	var p *problem.Problem
	if _, err := c.Use(ctx, "broken"); reason(err) != licenses.ReasonUnreadable {
		t.Fatalf("use broken: %v", err)
	}
	if _, err := c.Use(ctx, "nobody"); !errors.As(err, &p) || p.Type != problem.TypeNotFound {
		t.Fatalf("use nobody: %v", err)
	}
	if f := found(t, release, "2026-11-19"); active(f) != "older" {
		t.Fatalf("the choice changed: %+v", f)
	}
}

// remove takes away the files of one license and clears the choice when it named it, so the newest is active again; other licenses, the trial and other apps' choices stay.
func TestRemoveTakesOneLicenseAndClearsItsChoice(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	ctx := context.Background()
	put(t, orgFile(t, "newest"), sign(t, k, named("newest", "2026-11-10", "events")))
	put(t, appFile(t, "designer", "older"), sign(t, k, named("older", "2026-11-01", "sync")))
	put(t, appPath(t, "designer", "trial"), noteOn("designer", "2026-11-15"))
	put(t, appPath(t, "kanban", "license-active"), []byte("older\n"))
	c := checker(t, release, "2026-11-19")
	if _, err := c.Use(ctx, "older"); err != nil {
		t.Fatal(err)
	}
	shown, err := c.Removing("older")
	if err != nil || len(shown) != 1 || shown[0].File != appFile(t, "designer", "older") || shown[0].ID != "older" {
		t.Fatalf("removing = %+v, %v", shown, err)
	}
	if err := c.Remove(ctx, shown); err != nil {
		t.Fatal(err)
	}
	f := found(t, release, "2026-11-19")
	if len(f.Taken) != 1 || active(f) != "newest" || f.Chosen != "" || f.Trial == nil {
		t.Fatalf("found = %+v", f)
	}
	if raw, _ := os.ReadFile(appPath(t, "kanban", "license-active")); string(raw) != "older\n" {
		t.Fatalf("another app's choice changed: %q", raw)
	}
	// removing one that is not chosen leaves the choice
	put(t, appFile(t, "designer", "older"), sign(t, k, named("older", "2026-11-01", "sync")))
	if _, err := c.Use(ctx, "older"); err != nil {
		t.Fatal(err)
	}
	shown, _ = c.Removing("newest")
	if err := c.Remove(ctx, shown); err != nil {
		t.Fatal(err)
	}
	if f := found(t, release, "2026-11-19"); f.Chosen != "older" || active(f) != "older" {
		t.Fatalf("found = %+v", f)
	}
}

// remove takes away a refused file by the id it names, an old trial in the old place among them, and every copy of one id.
func TestRemoveTakesRefusedFilesAndEveryCopy(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	ctx := context.Background()
	put(t, oldAppFile(t), signAs(t, k, licenses.FileAPIVersion, oldTrial))
	put(t, orgFile(t, "acme-2026"), sign(t, k, license))
	put(t, appFile(t, "designer", "acme-2026"), sign(t, k, license))
	c := checker(t, release, "2026-11-19")
	for id, n := range map[string]int{"designer-1-trial": 1, "acme-2026": 2} {
		shown, err := c.Removing(id)
		if err != nil || len(shown) != n {
			t.Fatalf("%s: removing = %+v, %v", id, shown, err)
		}
		if err := c.Remove(ctx, shown); err != nil {
			t.Fatal(err)
		}
	}
	if f := found(t, release, "2026-11-19"); len(f.Taken) != 0 || len(f.Refused) != 0 {
		t.Fatalf("found = %+v", f)
	}
}

func TestRemoveWithNoSuchLicenseIsNotFound(t *testing.T) {
	org(t)
	_, release, _ := signer(t)
	var p *problem.Problem
	if _, err := checker(t, release, "2026-11-19").Removing("acme-2026"); !errors.As(err, &p) || p.Type != problem.TypeNotFound {
		t.Fatalf("err = %v", err)
	}
}

// Remove takes away only what was shown: a file changed since is refused, and no file is removed.
func TestRemoveTakesOnlyWhatWasShown(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, orgFile(t, "acme-2026"), sign(t, k, license))
	put(t, appFile(t, "designer", "acme-2026"), sign(t, k, license))
	c := checker(t, release, "2026-11-19")
	shown, err := c.Removing("acme-2026")
	if err != nil {
		t.Fatal(err)
	}
	changed := named("acme-2026", "2026-11-02", "events")
	put(t, orgFile(t, "acme-2026"), sign(t, k, changed))
	var p *problem.Problem
	if err := c.Remove(context.Background(), shown); !errors.As(err, &p) || p.Reason != store.ReasonChanged {
		t.Fatalf("err = %v", err)
	}
	for _, file := range []string{orgFile(t, "acme-2026"), appFile(t, "designer", "acme-2026")} {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("%s was removed: %v", file, err)
		}
	}
	if err := c.Remove(context.Background(), []licenses.Removal{{ID: "x", File: filepath.Join(t.TempDir(), "x.license")}}); err == nil {
		t.Fatal("removed a file that is no license file of the app")
	}
}

// A license before its first day opens nothing yet.
func TestALicenseBeforeItsFirstDayOpensNothing(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, orgFile(t, "acme-2026"), sign(t, k, license))
	if got := on(t, list(checker(t, release, "2026-11-05"))); !slices.Equal(got, []string{"edit"}) {
		t.Fatalf("on before its first day = %v", got)
	}
}

// Fleet puts license files in from its secret store as links (ADR-L8): a link to a file is read, a link to anything else refused.
func TestALinkToALicenseIsReadAndToAFolderRefused(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	secrets := t.TempDir()
	put(t, filepath.Join(secrets, "org"), sign(t, k, license))
	for _, f := range []string{orgFile(t, "x"), appFile(t, "designer", "x")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(secrets, "org"), orgFile(t, "acme-2026")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secrets, appFile(t, "designer", "folder")); err != nil {
		t.Fatal(err)
	}
	f := found(t, release, "2026-11-19")
	if len(f.Taken) != 1 || f.Taken[0].ID != "acme-2026" || len(f.Refused) != 1 || f.Refused[0].Reason != licenses.ReasonUnreadable {
		t.Fatalf("found = %+v", f)
	}
}

// A dev build keeps its licenses in <Org>-dev and <app>-dev (ADR-L5): it never reads the release's, and license add never writes over them.
func TestADevBuildNeverTouchesTheReleasesLicenses(t *testing.T) {
	dir := org(t)
	releases := filepath.Join(filepath.Dir(dir), "elderlygate", "licenses", "acme-2026.license")
	k, _, dev := signer(t)
	prodRaw := sign(t, k, license)
	put(t, releases, prodRaw)
	f, err := checker(t, dev, "2026-11-19").Read()
	if err != nil || len(f.Taken) != 0 || len(f.Refused) != 0 {
		t.Fatalf("a dev build read the release's license: %+v, %v", f, err)
	}
	devLicense := license
	devLicense.KeyID = "dev-2026"
	if _, err := checker(t, dev, "2026-11-19").Add(context.Background(), sign(t, k, devLicense), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(releases); !bytes.Equal(got, prodRaw) {
		t.Fatal("a dev build wrote over the release's license")
	}
	if _, err := os.Stat(orgFile(t, "acme-2026")); err != nil || !strings.HasSuffix(filepath.Dir(filepath.Dir(orgFile(t, "acme-2026"))), "-dev") {
		t.Fatalf("the dev license is not in the dev folder: %v", err)
	}
}

// A choice that is a folder names nothing: Read does not fail, no license is active, and use, add and remove write over it through the store.
func TestAChoiceThatIsAFolderNamesNothing(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	ctx := context.Background()
	put(t, orgFile(t, "acme-2026"), sign(t, k, license))
	path := appPath(t, "designer", "license-active")
	if err := os.MkdirAll(filepath.Join(path, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := checker(t, release, "2026-11-19")
	f, err := c.Read()
	if err != nil || active(f) != "" || f.Chosen != "" || len(f.Taken) != 1 {
		t.Fatalf("found = %+v, %v", f, err)
	}
	if got := on(t, list(c)); !slices.Equal(got, []string{"edit"}) {
		t.Fatalf("on = %v, want no license active", got)
	}
	shown, err := c.Removing("acme-2026")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(ctx, shown); err != nil {
		t.Fatalf("remove beside the choice: %v", err)
	}
	if _, err := c.Add(ctx, sign(t, k, license), true); err != nil {
		t.Fatalf("add over the choice: %v", err)
	}
	if f := found(t, release, "2026-11-19"); active(f) != "acme-2026" || f.Chosen != "acme-2026" {
		t.Fatalf("found after add = %+v", f)
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Use(ctx, "acme-2026"); err != nil {
		t.Fatalf("use over the choice: %v", err)
	}
	if f := found(t, release, "2026-11-19"); active(f) != "acme-2026" {
		t.Fatalf("found after use = %+v", f)
	}
}

// A choice the store cannot read, a pipe or a file it may not read, names nothing, and only a hand change makes it: use and add refuse, naming the path and saying to remove or fix it by hand, and write nothing.
func TestAChoiceTheStoreCannotReadRefusesUseAndAdd(t *testing.T) {
	for name, spoil := range map[string]func(t *testing.T, path string){
		"a pipe": func(t *testing.T, path string) {
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"no permission": func(t *testing.T, path string) {
			put(t, path, []byte("acme-2026\n"))
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			org(t)
			k, release, _ := signer(t)
			put(t, orgFile(t, "acme-2026"), sign(t, k, license))
			path := appPath(t, "designer", "license-active")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			spoil(t, path)
			c := checker(t, release, "2026-11-19")
			f, err := c.Read()
			if err != nil || active(f) != "" || f.Chosen != "" || len(f.Taken) != 1 {
				t.Fatalf("found = %+v, %v", f, err)
			}
			if got := on(t, list(c)); !slices.Equal(got, []string{"edit"}) {
				t.Fatalf("on = %v, want no license active", got)
			}
			refusesByHand(t, c, path, sign(t, k, license))
		})
	}
}

// refusesByHand checks that use and add refuse the choice at path with a Problem refused that names it and says to fix it by hand, and that nothing was written.
func refusesByHand(t *testing.T, c *licenses.Checker, path string, raw []byte) {
	t.Helper()
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := func() []string {
		var names []string
		_ = filepath.WalkDir(filepath.Dir(path), func(p string, _ fs.DirEntry, _ error) error { names = append(names, p); return nil })
		return names
	}
	was := entries()
	for what, run := range map[string]func() error{
		"use": func() error { _, err := c.Use(context.Background(), "acme-2026"); return err },
		"add": func() error { _, err := c.Add(context.Background(), raw, false); return err },
	} {
		err := run()
		var p *problem.Problem
		if !errors.As(err, &p) || p.Type != problem.TypeRefused || !strings.Contains(p.Detail, path) || !strings.Contains(p.Detail, "remove or fix it by hand, then try again") {
			t.Fatalf("%s: err = %v, want refused naming %s and saying to fix it by hand", what, err, path)
		}
	}
	if after, err := os.Lstat(path); err != nil || after.Mode() != before.Mode() {
		t.Fatalf("the choice changed: %v, %v", after, err)
	}
	if now := entries(); !slices.Equal(now, was) {
		t.Fatalf("files written: %v, was %v", now, was)
	}
}

// A file in a licenses folder is named by its id: one named otherwise is refused as id_mismatch under its file's name, in the app's folder and the org's; the old single file is not named by its id.
func TestAFileNotNamedByItsIDIsRefused(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, appFile(t, "designer", "other"), sign(t, k, license))
	put(t, orgFile(t, "elsewhere"), sign(t, k, named("ada-2026", "2026-11-01", "sync")))
	put(t, oldAppFile(t), sign(t, k, named("old-2026", "2026-11-01", "sync")))
	f := found(t, release, "2026-11-19")
	if len(f.Taken) != 1 || f.Taken[0].ID != "old-2026" || len(f.Refused) != 2 {
		t.Fatalf("found = %+v", f)
	}
	for _, r := range f.Refused {
		if r.Reason != licenses.ReasonIDMismatch || (r.ID != "other" && r.ID != "elsewhere") {
			t.Fatalf("refused = %+v", r)
		}
	}
}

// A refused file in a licenses folder is known by its file's name, never by the metadata.id nothing signed; the old single file, which has no name of its own, by its metadata.id.
func TestARefusedFileIsKnownByItsName(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	bad := edit(t, sign(t, k, license), func(f map[string]any) { f["spec"].(map[string]any)["signature"] = "AAAA" })
	put(t, appFile(t, "designer", "mine"), bad)
	put(t, oldOrgFile(t), bad)
	f := found(t, release, "2026-11-19")
	if len(f.Refused) != 2 || f.Refused[0].ID != "mine" || f.Refused[1].ID != "acme-2026" {
		t.Fatalf("refused = %+v", f.Refused)
	}
}

// A choice that is a link leading nowhere or to a file it may not read names nothing, and the store never writes through a link: use and add refuse as for a pipe, and the link and what it led to stay.
func TestAChoiceThatIsAnUnreadableLinkNamesNothing(t *testing.T) {
	for name, target := range map[string]func(t *testing.T, path string){
		"a link to nothing": func(*testing.T, string) {},
		"a link to a file it may not read": func(t *testing.T, path string) {
			put(t, path, []byte("acme-2026\n"))
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			org(t)
			k, release, _ := signer(t)
			put(t, orgFile(t, "acme-2026"), sign(t, k, license))
			path := appPath(t, "designer", "license-active")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			to := filepath.Join(t.TempDir(), "elsewhere")
			target(t, to)
			if err := os.Symlink(to, path); err != nil {
				t.Fatal(err)
			}
			c := checker(t, release, "2026-11-19")
			if f, err := c.Read(); err != nil || active(f) != "" || f.Chosen != "" {
				t.Fatalf("found = %+v, %v", f, err)
			}
			refusesByHand(t, c, path, sign(t, k, license))
			if link, err := os.Readlink(path); err != nil || link != to {
				t.Fatalf("the link changed: %q, %v", link, err)
			}
		})
	}
}

// An old single file too broken to name an id is known as old-org or old-app, by its place, so license remove still takes it away.
func TestAnOldFileWithNoIDIsKnownByItsPlace(t *testing.T) {
	org(t)
	_, release, _ := signer(t)
	put(t, oldOrgFile(t), []byte("not a license"))
	put(t, oldAppFile(t), []byte(`{"metadata":{}}`))
	c := checker(t, release, "2026-11-19")
	f, err := c.Read()
	if err != nil || len(f.Refused) != 2 || f.Refused[0].ID != "old-app" || f.Refused[1].ID != "old-org" {
		t.Fatalf("refused = %+v, %v", f.Refused, err)
	}
	for id, file := range map[string]string{"old-org": oldOrgFile(t), "old-app": oldAppFile(t)} {
		shown, err := c.Removing(id)
		if err != nil || len(shown) != 1 || shown[0].File != file {
			t.Fatalf("%s: removing = %+v, %v", id, shown, err)
		}
		if err := c.Remove(context.Background(), shown); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(file); err == nil {
			t.Fatalf("%s is still there", file)
		}
	}
}
