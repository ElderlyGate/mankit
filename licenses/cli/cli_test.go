package cli_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"
	"github.com/spf13/cobra"

	"github.com/tugbayatilla/mankit/v4/ask"
	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/licenses"
	"github.com/tugbayatilla/mankit/v4/licenses/cli"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/serve"
)

const schema = "mankit/v1/license-show.schema.json"

var all = []features.Feature{
	{ID: "edit", Title: "Change a design", Covers: "apply and edit"},
	{ID: "events", Title: "Live events", Covers: "the /api/events stream", StartsOff: true, Tier: features.Enterprise},
	{ID: "multi-design", Title: "Many designs", Covers: "more than one design", Tier: features.Enterprise},
	{ID: "sync", Title: "Sync designs", Covers: "designs on several machines", Tier: features.Enterprise},
}

type keys map[string]ed25519.PrivateKey

func (k keys) Sign(id string, message []byte) ([]byte, error) {
	return ed25519.Sign(k[id], message), nil
}

// world is one machine: its org folder, a signer, the trust of a release, and the day it is.
type world struct {
	t     *testing.T
	dir   string // the org's config folder, where the app's folder is
	org   string // the folder of the org's own files (home.OrgDir): <Org>-dev, as a test binary is a dev build
	keys  keys
	trust licenses.Trust
	today string
	asked string // what the last question said
}

func newWorld(t *testing.T, today string) *world {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return &world{t: t, dir: filepath.Join(base, "elderlygate"), org: filepath.Join(base, "elderlygate-dev"), keys: keys{"prod-2026": priv}, today: today,
		trust: licenses.Trust{Release: true, Prod: map[string]ed25519.PublicKey{"prod-2026": pub}}}
}

func day(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }

func (w *world) sign(kind licenses.Kind, products map[string][]string, issued, until string) []byte {
	raw, err := licenses.Sign(licenses.License{ID: "acme-2026", Kind: kind, For: "Acme Ltd", Products: products, Issued: day(issued), Until: day(until), KeyID: "prod-2026"}, w.keys)
	if err != nil {
		w.t.Fatal(err)
	}
	return raw
}

func (w *world) put(rel string, raw []byte) { w.putIn(w.dir, rel, raw) }

// putOrg puts the org license in the folder of the org's own files.
func (w *world) putOrg(raw []byte) { w.putIn(w.org, "license", raw) }

func (w *world) putIn(dir, rel string, raw []byte) {
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// run runs `license` words as an app mounts the thing; the events feature is switched on, as an owner would.
func (w *world) run(words ...string) (string, error) {
	w.t.Helper()
	c, err := licenses.New(licenses.Config{ID: "designer", Trust: w.trust, Now: func() time.Time { return day(w.today) }})
	if err != nil {
		w.t.Fatal(err)
	}
	store := features.Store{Off: func() ([]string, error) { return nil, nil }, Keep: func(string, bool) error { return nil }, On: func() ([]string, error) { return []string{"events"}, nil }}
	list := features.New(all, store, c.Closer(all))
	app := serve.App{ID: "designer", Title: "Designer", Version: "9.0.0", Contracts: []serve.Contract{{Version: "v1"}}}
	asked := func(cc *cobra.Command, a ask.Action) error {
		w.asked = a.What
		if f := cc.Flags().Lookup("yes"); f == nil || f.Value.String() != "true" {
			return ask.ErrRefused
		}
		return nil
	}
	root := &cobra.Command{Use: "e-designer", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(cli.Command(app, c, list, asked))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(append([]string{"license"}, words...))
	err = root.Execute()
	return out.String(), err
}

// show is `license show -o json`, checked against contracts mankit/v1 LicenseShow, and its data.
func (w *world) show() licenses.Show {
	w.t.Helper()
	out, err := w.run("show", "-o", "json")
	if err != nil {
		w.t.Fatal(err)
	}
	if err := contracts.Validate(schema, []byte(out)); err != nil {
		w.t.Fatalf("not a LicenseShow answer: %v\n%s", err, out)
	}
	var e struct{ Data licenses.Show }
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		w.t.Fatal(err)
	}
	return e.Data
}

func from(s licenses.Show) string {
	var words []string
	for _, f := range s.Features {
		words = append(words, f.ID+"="+string(f.From))
	}
	return strings.Join(words, " ")
}

func TestShowWithNoLicense(t *testing.T) {
	s := newWorld(t, "2026-11-19").show()
	if s.State != licenses.StateNone || !s.Build.Release || len(s.Licenses) != 0 || s.Counts != (licenses.Counts{Free: 1, Enterprise: 3, On: 1, Locked: 3}) {
		t.Fatalf("show = %+v", s)
	}
}

// ADR-L8's example: each feature says where it is unlocked from, org, product, both or none.
func TestShowSaysWhereEachFeatureIsUnlockedFrom(t *testing.T) {
	w := newWorld(t, "2026-11-19")
	w.putOrg(w.sign(licenses.Org, map[string][]string{"designer": {"events", "multi-design"}, "kanban": {"sync"}}, "2026-11-06", "2027-11-06"))
	w.put(home.Folder("designer")+"/license", w.sign(licenses.Product, map[string][]string{"designer": {"multi-design"}}, "2026-03-01", "2027-03-01"))
	s := w.show()
	if s.State != licenses.StateLicensed || len(s.Licenses) != 2 || s.Licenses[0].Kind != licenses.Org || s.Licenses[0].Window != nil {
		t.Fatalf("show = %+v", s)
	}
	if got := from(s); got != "edit=none events=org multi-design=both sync=none" {
		t.Fatalf("from = %s", got)
	}
	if s.Features[2].Until != "2027-11-06" || s.Counts.Locked != 1 {
		t.Fatalf("show = %+v", s)
	}
	table, err := w.run("show")
	if err != nil || !strings.Contains(table, "licensed") || !strings.Contains(table, "multi-design  enterprise") {
		t.Fatalf("table %q, %v", table, err)
	}
}

func TestShowATrialKeyWithItsWindow(t *testing.T) {
	w := newWorld(t, "2026-11-23")
	w.put(home.Folder("designer")+"/license", w.sign(licenses.Trial, nil, "2026-11-19", "2026-12-02"))
	s := w.show()
	if s.State != licenses.StateTrial || *s.Licenses[0].Window != (licenses.Window{Of: 14}) || s.Licenses[0].DaysLeft != 10 || from(s) != "edit=none events=trial multi-design=trial sync=trial" {
		t.Fatalf("show = %+v", s)
	}
	w.today = "2026-11-18"
	if s := w.show(); s.State != licenses.StateNone || s.Licenses[0].Window != nil || s.Counts.Locked != 3 {
		t.Fatalf("before the trial = %+v", s)
	}
	w.today = "2026-12-03"
	if s := w.show(); s.State != licenses.StateTrialEnded || s.Licenses[0].Window != nil || !s.Licenses[0].Ended || s.Counts.Locked != 3 {
		t.Fatalf("after the trial = %+v", s)
	}
}

// releaseTrial is designer's release-trial key, started on the day when started is not empty, as the app writes it beside the signed payload.
func (w *world) releaseTrial(started string) []byte {
	raw, err := licenses.Sign(licenses.License{ID: "designer-v1.2.0-trial", Kind: licenses.ReleaseTrial, For: "designer v1.2.0", Products: map[string][]string{"designer": nil}, Issued: day("2026-11-23"), Until: day("2027-11-23"), TrialDays: 14, KeyID: "prod-2026"}, w.keys)
	if err != nil || started == "" {
		return raw
	}
	var f map[string]any
	_ = json.Unmarshal(raw, &f)
	f["spec"].(map[string]any)["started"] = map[string]any{"day": started, "binary": "/home/ada/.local/bin/designer", "fileTime": started + "T09:14:05Z"}
	raw, _ = json.Marshal(f)
	return raw
}

// A started release-trial shows its trial's last day, not the key's, and the days left of its window (contracts mankit/v1 license-show).
func TestShowAReleaseTrialFromItsStartedDay(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.put(home.Folder("designer")+"/license", w.releaseTrial("2026-12-03"))
	s := w.show()
	l := s.Licenses[0]
	if s.State != licenses.StateTrial || l.Kind != licenses.ReleaseTrial || l.For != "designer v1.2.0" || l.Until != "2026-12-16" || l.DaysLeft != 10 || l.Window == nil || *l.Window != (licenses.Window{Of: 14}) {
		t.Fatalf("show = %+v", s)
	}
	if from(s) != "edit=none events=trial multi-design=trial sync=trial" || s.Features[1].Until != "2026-12-16" {
		t.Fatalf("show = %+v", s)
	}
	w.today = "2026-12-17"
	if s := w.show(); s.State != licenses.StateTrialEnded || s.Licenses[0].Window != nil || !s.Licenses[0].Ended || s.Counts.Locked != 3 {
		t.Fatalf("after the trial = %+v", s)
	}
}

// A paid license comes before a release-trial in state: running, licensed; ended, ended on the org license's last day.
func TestShowAPaidLicenseBeforeAReleaseTrial(t *testing.T) {
	for _, c := range []struct{ until, state, endedOn string }{{"2027-11-06", licenses.StateLicensed, ""}, {"2026-12-01", licenses.StateEnded, "2026-12-01"}} {
		w := newWorld(t, "2026-12-07")
		w.putOrg(w.sign(licenses.Org, map[string][]string{"designer": {"events"}}, "2026-11-06", c.until))
		w.put(home.Folder("designer")+"/license", w.releaseTrial("2026-12-03"))
		if s := w.show(); s.State != c.state || s.EndedOn != c.endedOn || len(s.Licenses) != 2 || s.Licenses[1].Kind != licenses.ReleaseTrial || s.Licenses[1].Ended {
			t.Fatalf("org until %s: show = %+v", c.until, s)
		}
	}
}

// A release-trial not started is the key as signed: listed with its last day to start a trial, it opens nothing and counts as none.
func TestShowAReleaseTrialNotStarted(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.put(home.Folder("designer")+"/license", w.releaseTrial(""))
	s := w.show()
	if s.State != licenses.StateNone || len(s.Licenses) != 1 || s.Licenses[0].Until != "2027-11-23" || s.Licenses[0].Window != nil || s.Licenses[0].Ended || s.Counts.Locked != 3 {
		t.Fatalf("show = %+v", s)
	}
	w.today = "2027-11-24"
	if s := w.show(); s.State != licenses.StateNone || !s.Licenses[0].Ended || s.Licenses[0].DaysLeft != 0 {
		t.Fatalf("after its last day to start one = %+v", s)
	}
}

// A release-trial always shows its offer's end, the key's own until, and its trial's end only once started (contracts mankit/v1 license-show).
func TestShowAReleaseTrialsOfferAndTrialEnds(t *testing.T) {
	for _, c := range []struct{ started, trialEnds string }{{"", ""}, {"2026-12-03", "2026-12-16"}} {
		w := newWorld(t, "2026-12-07")
		w.put(home.Folder("designer")+"/license", w.releaseTrial(c.started))
		if l := w.show().Licenses[0]; l.OfferEnds != "2027-11-23" || l.TrialEnds != c.trialEnds {
			t.Fatalf("started %q: row = %+v", c.started, l)
		}
	}
}

// Only a release-trial has an offer's end or a trial's end.
func TestShowNoOfferOrTrialEndOnOtherKinds(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putOrg(w.sign(licenses.Org, map[string][]string{"designer": {"events"}}, "2026-11-06", "2027-11-06"))
	w.put(home.Folder("designer")+"/license", w.sign(licenses.Trial, nil, "2026-12-01", "2026-12-30"))
	for _, l := range w.show().Licenses {
		if l.OfferEnds != "" || l.TrialEnds != "" {
			t.Fatalf("row = %+v", l)
		}
	}
}

// The text of license show opens with the owner's sentence for its state, naming the app by its title.
func TestShowSaysTheStateInTheOwnersWords(t *testing.T) {
	for _, c := range []struct {
		name, today, says string
		put               func(*world)
	}{
		{"a trial key running", "2026-11-23", "Trial: 10 days left (ends 2026-12-02)", func(w *world) {
			w.put(home.Folder("designer")+"/license", w.sign(licenses.Trial, nil, "2026-11-19", "2026-12-02"))
		}},
		{"a release-trial running", "2026-12-07", "Trial: 10 days left (ends 2026-12-16)", func(w *world) {
			w.put(home.Folder("designer")+"/license", w.releaseTrial("2026-12-03"))
		}},
		{"a release-trial on its last day", "2026-12-16", "Trial: 1 day left (ends 2026-12-16)", func(w *world) {
			w.put(home.Folder("designer")+"/license", w.releaseTrial("2026-12-03"))
		}},
		{"a release-trial ended", "2026-12-17", "Your trial has ended. Buy a license to keep the paid features.", func(w *world) {
			w.put(home.Folder("designer")+"/license", w.releaseTrial("2026-12-03"))
		}},
		{"a key past its offer", "2027-11-24", "This version doesn't include a free trial anymore. Download the latest version to try Designer.", func(w *world) {
			w.put(home.Folder("designer")+"/license", w.releaseTrial(""))
		}},
		{"a bought license", "2026-12-07", "Licensed to Acme Ltd · Designer", func(w *world) {
			w.putOrg(w.sign(licenses.Org, map[string][]string{"designer": {"events"}}, "2026-11-06", "2027-11-06"))
		}},
	} {
		w := newWorld(t, c.today)
		c.put(w)
		out, err := w.run("show")
		if err != nil || !strings.HasPrefix(out, c.says+"\n\nSTATE") {
			t.Fatalf("%s: err = %v, out =\n%s", c.name, err, out)
		}
		w.show()
	}
}

// A state with no sentence opens with its table: no license, and a release-trial still on offer.
func TestShowSaysNothingWithoutASentence(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	for _, put := range []func(){func() {}, func() { w.put(home.Folder("designer")+"/license", w.releaseTrial("")) }} {
		put()
		if out, err := w.run("show"); err != nil || !strings.HasPrefix(out, "STATE") {
			t.Fatalf("err = %v, out =\n%s", err, out)
		}
	}
}

func TestShowAnEndedLicenseAndARefusedFile(t *testing.T) {
	w := newWorld(t, "2027-12-01")
	w.putOrg(w.sign(licenses.Org, map[string][]string{"designer": {"events"}}, "2026-11-06", "2027-11-06"))
	w.put(home.Folder("designer")+"/license", []byte("garbage"))
	s := w.show()
	if s.State != licenses.StateEnded || s.EndedOn != "2027-11-06" || s.Licenses[0].DaysLeft != 0 || len(s.Refused) != 1 || s.Refused[0].Reason != licenses.ReasonUnreadable || s.Refused[0].File != filepath.Join(w.dir, home.Folder("designer"), "license") {
		t.Fatalf("show = %+v", s)
	}
}

func TestShowNamesTheLatestEndOfTheEndedLicenses(t *testing.T) {
	for _, ends := range [][2]string{{"2027-11-06", "2027-03-01"}, {"2027-03-01", "2027-11-06"}} {
		w := newWorld(t, "2027-12-01")
		w.putOrg(w.sign(licenses.Org, map[string][]string{"designer": {"events"}}, "2026-11-06", ends[0]))
		w.put(home.Folder("designer")+"/license", w.sign(licenses.Product, map[string][]string{"designer": {"sync"}}, "2026-03-01", ends[1]))
		if s := w.show(); s.State != licenses.StateEnded || s.EndedOn != "2027-11-06" {
			t.Fatalf("org until %s, product until %s: show = %+v", ends[0], ends[1], s)
		}
	}
}

// A renewal not begun counts as none, so the ended license's day stays.
func TestShowNamesTheEndedLicenseNotARenewalNotBegun(t *testing.T) {
	w := newWorld(t, "2027-12-01")
	w.putOrg(w.sign(licenses.Org, map[string][]string{"designer": {"events"}}, "2026-11-06", "2027-11-06"))
	w.put(home.Folder("designer")+"/license", w.sign(licenses.Product, map[string][]string{"designer": {"events"}}, "2027-12-15", "2028-12-15"))
	if s := w.show(); s.State != licenses.StateEnded || s.EndedOn != "2027-11-06" || len(s.Licenses) != 2 {
		t.Fatalf("show = %+v", s)
	}
}

func TestShowNamesNoEndedDayUnlessEnded(t *testing.T) {
	for _, c := range []struct{ state, today, org, product string }{
		{licenses.StateNone, "2026-11-19", "", ""},
		{licenses.StateNone, "2026-11-19", "", "2026-12-01/2027-12-01"},
		{licenses.StateLicensed, "2027-06-01", "2026-11-06/2027-11-06", "2026-03-01/2027-03-01"},
		{licenses.StateTrial, "2026-11-23", "", "trial"},
		{licenses.StateTrialEnded, "2026-12-03", "", "trial"},
	} {
		w := newWorld(t, c.today)
		if c.org != "" {
			days := strings.Split(c.org, "/")
			w.putOrg(w.sign(licenses.Org, map[string][]string{"designer": {"events"}}, days[0], days[1]))
		}
		switch c.product {
		case "":
		case "trial":
			w.put(home.Folder("designer")+"/license", w.sign(licenses.Trial, nil, "2026-11-19", "2026-12-02"))
		default:
			days := strings.Split(c.product, "/")
			w.put(home.Folder("designer")+"/license", w.sign(licenses.Product, map[string][]string{"designer": {"sync"}}, days[0], days[1]))
		}
		if s := w.show(); s.State != c.state || s.EndedOn != "" {
			t.Fatalf("%+v: show = %+v", c, s)
		}
	}
}

func TestAddChecksAsksAndKeeps(t *testing.T) {
	w := newWorld(t, "2026-11-19")
	file := filepath.Join(t.TempDir(), "acme.license")
	if err := os.WriteFile(file, w.sign(licenses.Org, map[string][]string{"designer": {"sync"}}, "2026-11-06", "2027-11-06"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.run("add", "--org", file); !errors.Is(err, ask.ErrRefused) {
		t.Fatalf("without a yes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.org, "license")); !os.IsNotExist(err) {
		t.Fatal("kept without a yes")
	}
	out, err := w.run("add", "--org", file, "--yes", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate(schema, []byte(out)); err != nil || !strings.Contains(out, `"from": "org"`) {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestAddRefusesWhatItCannotTakeNamingTheFile(t *testing.T) {
	w := newWorld(t, "2026-11-19")
	file := filepath.Join(t.TempDir(), "acme.license")
	if err := os.WriteFile(file, w.sign(licenses.Org, map[string][]string{"designer": {"sync"}}, "2026-11-06", "2027-11-06"), 0o600); err != nil {
		t.Fatal(err)
	}
	for words, want := range map[string]string{file: licenses.ReasonWrongPlace, file + ".gone": licenses.ReasonUnreadable} {
		_, err := w.run("add", words, "--yes")
		var p *problem.Problem
		if !errors.As(err, &p) || p.Reason != want || len(p.At) != 1 || p.At[0].File != words {
			t.Fatalf("%s: err = %v, want %s at the file", words, err, want)
		}
	}
}

// Adding a trial key where the product license is says it replaces that license before it asks (G-STACK-13).
func TestAddNamesTheLicenseItReplaces(t *testing.T) {
	w := newWorld(t, "2026-11-19")
	w.put(home.Folder("designer")+"/license", w.sign(licenses.Product, map[string][]string{"designer": {"sync"}}, "2026-03-01", "2027-03-01"))
	file := filepath.Join(t.TempDir(), "trial.license")
	if err := os.WriteFile(file, w.sign(licenses.Trial, nil, "2026-11-19", "2026-12-02"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.run("add", file, "--yes"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.asked, "replacing the product license for Acme Ltd until 2027-03-01") {
		t.Fatalf("asked %q", w.asked)
	}
}
