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

// productLicense is the app's own license file, in its config folder (home.File).
func productLicense(t *testing.T) string {
	t.Helper()
	path, err := home.File("designer", "license")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func checker(t *testing.T, trust licenses.Trust, today string) *licenses.Checker {
	t.Helper()
	c, err := licenses.New(licenses.Config{ID: "designer", Trust: trust, Now: func() time.Time { return day(today).Add(15 * time.Hour) }})
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
	dir := org(t)
	k, release, _ := signer(t)
	put(t, filepath.Join(dir, "license"), sign(t, k, orgLicense))
	if got := on(t, list(checker(t, release, "2027-11-06"))); !slices.Equal(got, []string{"edit", "events"}) {
		t.Fatalf("on its last day = %v", got)
	}
	// Ended, its features lock again (ADR-L3).
	if got := on(t, list(checker(t, release, "2027-11-07"))); !slices.Equal(got, []string{"edit"}) {
		t.Fatalf("after its last day = %v", got)
	}
}

// A trial key opens every enterprise feature and names none (ADR-L9).
func TestATrialKeyOpensEveryEnterpriseFeature(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	trial := licenses.License{ID: "acme-2026", Kind: licenses.Trial, For: "ada@acme.example", Issued: day("2026-11-19"), Until: day("2026-12-02"), KeyID: "prod-2026"}
	put(t, productLicense(t), sign(t, k, trial))
	if got := on(t, list(checker(t, release, "2026-11-19"))); !slices.Equal(got, []string{"edit", "events", "sync"}) {
		t.Fatalf("on = %v", got)
	}
}

// A file that cannot be read or verified is refused with its file and why, never taken for no license (G-CODE-6).
func TestAFileItCannotTakeIsRefusedWithItsFile(t *testing.T) {
	dir := org(t)
	k, _, dev := signer(t)
	put(t, filepath.Join(dir, "license"), sign(t, k, orgLicense))
	put(t, productLicense(t), []byte("garbage"))
	f, err := checker(t, dev, "2026-11-19").Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Taken) != 0 || len(f.Refused) != 2 ||
		f.Refused[0].File != filepath.Join(dir, "license") || f.Refused[0].Reason != licenses.ReasonOtherBuild ||
		f.Refused[1].File != productLicense(t) || f.Refused[1].Reason != licenses.ReasonUnreadable {
		t.Fatalf("found = %+v", f)
	}
}

// An org license is kept as the org's, a product license or trial key as the app's own (ADR-L8).
func TestALicenseInThePlaceOfAnotherKindIsRefused(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	c := checker(t, release, "2026-11-19")
	product := orgLicense
	product.Kind = licenses.Product
	other := product
	other.Products = map[string][]string{"kanban": {"sync"}}
	for name, tc := range map[string]struct {
		l   licenses.License
		org bool
	}{"an org license as the app's": {orgLicense, false}, "a product license as the org's": {product, true}, "another product's": {other, false}} {
		if _, err := c.Check(sign(t, k, tc.l), tc.org); reason(err) != licenses.ReasonWrongPlace {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAddKeepsTheOrgLicenseForEveryAppAndTheProductOneForItsApp(t *testing.T) {
	dir := org(t)
	k, release, _ := signer(t)
	c := checker(t, release, "2026-11-19")
	product := orgLicense
	product.Kind, product.Products = licenses.Product, map[string][]string{"designer": {"sync"}}
	orgRaw, productRaw := sign(t, k, orgLicense), sign(t, k, product)
	if _, err := c.Add(context.Background(), orgRaw, true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Add(context.Background(), productRaw, false); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{filepath.Join(dir, "license"): orgRaw, productLicense(t): productRaw} {
		if got, err := os.ReadFile(path); err != nil || string(got) != string(want) {
			t.Fatalf("%s = %q, %v", path, got, err)
		}
	}
	if got := on(t, list(c)); !slices.Equal(got, []string{"edit", "events", "sync"}) {
		t.Fatalf("on = %v", got)
	}
}

// The org license lands in a folder private to the user, made by the store when it is missing.
func TestAddKeepsTheOrgLicenseInAFolderPrivateToTheUser(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	k, release, _ := signer(t)
	if _, err := checker(t, release, "2026-11-19").Add(context.Background(), sign(t, k, orgLicense), true); err != nil {
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
	_, err := checker(t, release, "2026-11-19").Add(context.Background(), sign(t, k, orgLicense), true)
	var p *problem.Problem
	if !errors.As(err, &p) || !strings.Contains(p.Detail, "the folder "+dir) {
		t.Fatalf("err = %v", err)
	}
}

func TestAddWritesNothingItCannotTake(t *testing.T) {
	dir := org(t)
	_, release, _ := signer(t)
	if _, err := checker(t, release, "2026-11-19").Add(context.Background(), []byte("garbage"), true); reason(err) != licenses.ReasonUnreadable {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "license")); !os.IsNotExist(err) {
		t.Fatal("a refused license was written")
	}
}

// A license before its first day opens nothing yet.
func TestALicenseBeforeItsFirstDayOpensNothing(t *testing.T) {
	dir := org(t)
	k, release, _ := signer(t)
	put(t, filepath.Join(dir, "license"), sign(t, k, orgLicense))
	if got := on(t, list(checker(t, release, "2026-11-05"))); !slices.Equal(got, []string{"edit"}) {
		t.Fatalf("on before its first day = %v", got)
	}
}

// Fleet puts license files in from its secret store as links (ADR-L8): a link to a file is read, a link to anything else refused.
func TestALinkToALicenseIsReadAndToAFolderRefused(t *testing.T) {
	dir := org(t)
	k, release, _ := signer(t)
	secrets := t.TempDir()
	put(t, filepath.Join(secrets, "org"), sign(t, k, orgLicense))
	for _, d := range []string{dir, filepath.Dir(productLicense(t))} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(secrets, "org"), filepath.Join(dir, "license")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secrets, productLicense(t)); err != nil {
		t.Fatal(err)
	}
	f, err := checker(t, release, "2026-11-19").Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Taken) != 1 || f.Taken[0].Kind != licenses.Org || len(f.Refused) != 1 || f.Refused[0].Reason != licenses.ReasonUnreadable {
		t.Fatalf("found = %+v", f)
	}
}

// A dev build keeps the org license in <Org>-dev (ADR-L5): it never reads the release's org license, and license add --org never writes over it.
func TestADevBuildNeverTouchesTheReleasesOrgLicense(t *testing.T) {
	dir := org(t)
	releases := filepath.Join(filepath.Dir(dir), "elderlygate", "license")
	k, _, dev := signer(t)
	prodRaw := sign(t, k, orgLicense)
	put(t, releases, prodRaw)
	f, err := checker(t, dev, "2026-11-19").Read()
	if err != nil || len(f.Taken) != 0 || len(f.Refused) != 0 {
		t.Fatalf("a dev build read the release's org license: %+v, %v", f, err)
	}
	devLicense := orgLicense
	devLicense.KeyID = "dev-2026"
	devRaw := sign(t, k, devLicense)
	taken, err := checker(t, dev, "2026-11-19").Add(context.Background(), devRaw, true)
	if err != nil || taken.File != filepath.Join(dir, "license") {
		t.Fatalf("add --org = %+v, %v; want it in %s", taken, err, dir)
	}
	if got, _ := os.ReadFile(releases); !bytes.Equal(got, prodRaw) {
		t.Fatal("add --org in a dev build wrote over the release's org license")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "license")); !bytes.Equal(got, devRaw) {
		t.Fatalf("the dev org license is not in %s", dir)
	}
	if f, err := checker(t, dev, "2026-11-19").Read(); err != nil || len(f.Taken) != 1 || f.Taken[0].File != filepath.Join(dir, "license") {
		t.Fatalf("found = %+v, %v", f, err)
	}
}

// A started release-trial opens every enterprise feature of its app, counted from its started day for its days: started 3 Dec for 14 days, its last day is 16 Dec (ADR-L9).
func TestAReleaseTrialRunsItsDaysFromItsStartedDay(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, productLicense(t), start(t, sign(t, k, releaseTrial), "2026-12-03"))
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

// A release-trial with no started trial is the key as signed: it opens nothing, and its last day is its last day to start one.
func TestAReleaseTrialNotStartedOpensNothing(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	put(t, productLicense(t), sign(t, k, releaseTrial))
	for today, ended := range map[string]bool{"2026-12-03": false, "2027-11-24": true} {
		c := checker(t, release, today)
		if got := on(t, list(c)); !slices.Equal(got, []string{"edit"}) {
			t.Errorf("%s: on = %v", today, got)
		}
		f, err := c.Read()
		if err != nil || len(f.Taken) != 1 {
			t.Fatalf("%s: found = %+v, %v", today, f, err)
		}
		if got := f.Taken[0]; got.Running() || got.Ended != ended || got.Early == ended || !got.LastDay.Equal(releaseTrial.Until) {
			t.Errorf("%s: taken = %+v", today, got)
		}
	}
}

// A release-trial opens its own app only (ADR-L2): another app refuses it, and it is never the org's.
func TestAReleaseTrialOfAnotherAppOrAsTheOrgsIsRefused(t *testing.T) {
	org(t)
	k, release, _ := signer(t)
	c := checker(t, release, "2026-12-03")
	other := releaseTrial
	other.For, other.Products = "kanban v1.2.0", map[string][]string{"kanban": nil}
	for name, tc := range map[string]struct {
		raw []byte
		org bool
	}{"another app's": {start(t, sign(t, k, other), "2026-12-03"), false}, "as the org's": {start(t, sign(t, k, releaseTrial), "2026-12-03"), true}} {
		if _, err := c.Check(tc.raw, tc.org); reason(err) != licenses.ReasonWrongPlace {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
