package licenses_test

import (
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

// org is the org's config folder of a test, set through XDG_CONFIG_HOME as on a machine.
func org(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	return filepath.Join(base, "elderlygate")
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
	dir := org(t)
	k, release, _ := signer(t)
	trial := licenses.License{ID: "acme-2026", Kind: licenses.Trial, For: "ada@acme.example", Issued: day("2026-11-19"), Until: day("2026-12-02"), KeyID: "prod-2026"}
	put(t, filepath.Join(dir, home.Folder("designer"), "license"), sign(t, k, trial))
	if got := on(t, list(checker(t, release, "2026-11-19"))); !slices.Equal(got, []string{"edit", "events", "sync"}) {
		t.Fatalf("on = %v", got)
	}
}

// A file that cannot be read or verified is refused with its file and why, never taken for no license (G-CODE-6).
func TestAFileItCannotTakeIsRefusedWithItsFile(t *testing.T) {
	dir := org(t)
	k, _, dev := signer(t)
	put(t, filepath.Join(dir, "license"), sign(t, k, orgLicense))
	put(t, filepath.Join(dir, home.Folder("designer"), "license"), []byte("garbage"))
	f, err := checker(t, dev, "2026-11-19").Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Taken) != 0 || len(f.Refused) != 2 ||
		f.Refused[0].File != filepath.Join(dir, "license") || f.Refused[0].Reason != licenses.ReasonOtherBuild ||
		f.Refused[1].File != filepath.Join(dir, home.Folder("designer"), "license") || f.Refused[1].Reason != licenses.ReasonUnreadable {
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
	for path, want := range map[string][]byte{filepath.Join(dir, "license"): orgRaw, filepath.Join(dir, home.Folder("designer"), "license"): productRaw} {
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
	dir := filepath.Join(os.Getenv("HOME"), ".config", home.Org)
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
	if err := os.MkdirAll(filepath.Join(dir, home.Folder("designer")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(secrets, "org"), filepath.Join(dir, "license")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secrets, filepath.Join(dir, home.Folder("designer"), "license")); err != nil {
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
