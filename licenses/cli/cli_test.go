package cli_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"go.naywee.com/contracts/v2"
	"gopkg.in/yaml.v3"

	"github.com/tugbayatilla/mankit/v4/ask"
	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/licenses"
	"github.com/tugbayatilla/mankit/v4/licenses/cli"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/serve"
)

const schema = "mankit/v3/license-show.schema.json"

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
	app   serve.App
	line  int // the product line the build is stamped with (V-10)
}

func newWorld(t *testing.T, today string) *world {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return &world{t: t, dir: filepath.Join(base, "elderlygate"), org: filepath.Join(base, "elderlygate-dev"), keys: keys{"test-2026": priv}, today: today,
		trust: licenses.Trust{Release: true, Prod: map[string]ed25519.PublicKey{"test-2026": pub}},
		app:   serve.App{ID: "designer", Title: "Designer", Version: "10.0.0", Contracts: []serve.Contract{{Version: "v1", Envelope: "v2"}}}, line: 1}
}

func day(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }

func (w *world) signed(l licenses.License) []byte {
	w.t.Helper()
	raw, err := licenses.Sign(l, w.keys)
	if err != nil {
		w.t.Fatal(err)
	}
	return raw
}

// license is a license for Acme Ltd naming designer's features, from issued until until.
func (w *world) license(features []string, issued, until string) []byte {
	return w.licenseOf("acme-2026", "Acme Ltd", features, issued, until)
}

// licenseOf is a license of the id for whom, naming designer's features, from issued until until.
func (w *world) licenseOf(id, whom string, features []string, issued, until string) []byte {
	return w.signed(licenses.License{ID: id, For: whom, Kind: licenses.KindLicense, Products: map[string][]string{"designer": features}, Issued: day(issued), Until: day(until), KeyID: "test-2026"})
}

// perpetual is a license that never ends, opening designer's events in Designer 1 (ADR-L10).
func (w *world) perpetual() []byte {
	return w.signed(licenses.License{ID: "acme-perpetual", Kind: licenses.KindLicense, For: "Acme Ltd", Products: map[string][]string{"designer": {"events"}}, Lines: map[string][]int{"designer": {1}}, Issued: day("2026-11-06"), KeyID: "test-2026"})
}

// note writes designer's trial note, started on the day (contracts mankit/v3 trial-started).
func (w *world) note(on string) {
	w.t.Helper()
	w.write(filepath.Join(w.dir, home.Folder("designer"), "trial"), []byte(`{"apiVersion":"elderlygate/mankit/trial/v1","kind":"TrialStarted","metadata":{"id":"designer"},"spec":{"day":"`+on+`","binary":"/home/ada/.local/bin/designer","fileTime":"`+on+`T09:14:05Z"}}`))
}

// old is a license file of the old form holding body.
func (w *world) old(body string) []byte {
	var p struct{ ID string }
	_ = json.Unmarshal([]byte(body), &p)
	sig, _ := w.keys.Sign("test-2026", []byte(body))
	raw, _ := json.Marshal(map[string]any{"apiVersion": licenses.APIVersion, "kind": licenses.FileKind, "metadata": map[string]any{"id": p.ID}, "spec": map[string]any{"payload": []byte(body), "signature": sig}})
	return raw
}

// id is the id the license file raw names.
func id(raw []byte) string {
	var f struct{ Metadata struct{ ID string } }
	_ = json.Unmarshal(raw, &f)
	return f.Metadata.ID
}

// appDir is designer's config folder.
func (w *world) appDir() string { return filepath.Join(w.dir, home.Folder("designer")) }

// putApp puts the license in the app's own licenses folder; putOrg in the org's.
func (w *world) putApp(raw []byte) {
	w.write(filepath.Join(w.appDir(), "licenses", id(raw)+".license"), raw)
}
func (w *world) putOrg(raw []byte) {
	w.write(filepath.Join(w.org, "licenses", id(raw)+".license"), raw)
}

// putOldApp and putOldOrg put the license in the old single files, read for one release.
func (w *world) putOldApp(raw []byte) { w.write(filepath.Join(w.appDir(), "license"), raw) }
func (w *world) putOldOrg(raw []byte) { w.write(filepath.Join(w.org, "license"), raw) }

// choose makes id the app's choice, as license use leaves it.
func (w *world) choose(id string) {
	w.write(filepath.Join(w.appDir(), "license-active"), []byte(id+"\n"))
}

func (w *world) write(path string, raw []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		w.t.Fatal(err)
	}
}

// door is the Checker and the feature list an app mounts the thing license with, on the world's day and line; the events feature is switched on, as an owner would.
func (w *world) door() (*licenses.Checker, *features.List) {
	w.t.Helper()
	cli.SetNow(w.t, func() time.Time { return day(w.today) })
	cli.SetLine(w.t, w.line)
	c, err := cli.Checker(w.app, w.trust)
	if err != nil {
		w.t.Fatal(err)
	}
	store := features.Store{Off: func() ([]string, error) { return nil, nil }, Keep: func(string, bool) error { return nil }, On: func() ([]string, error) { return []string{"events"}, nil }}
	return c, features.New(all, store, c.Closer(all))
}

// run runs `license` words as an app mounts the thing; the events feature is switched on, as an owner would.
func (w *world) run(words ...string) (string, error) {
	w.t.Helper()
	c, list := w.door()
	asked := func(cc *cobra.Command, a ask.Action) error {
		w.asked = a.What
		if f := cc.Flags().Lookup("yes"); f == nil || f.Value.String() != "true" {
			return ask.ErrRefused
		}
		return nil
	}
	root := &cobra.Command{Use: "e-designer", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(cli.Command(w.app, c, list, asked))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"license"}, words...))
	err := root.Execute()
	return out.String(), err
}

// show is `license show -o json`, checked against contracts mankit/v3 LicenseShow, and its data.
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

const licensed = "Licensed to Acme Ltd · Designer 1"

// Each case is the three states plus warn, the day the active license ended and the sentence, as contracts mankit/v3 license-show names them (ADR-L3, L8, L9); every answer meets the schema.
func TestShowTheStateAndWarnOfEachCase(t *testing.T) {
	const paid, trialWarn = 14, 3 // licenses.LicenseExpiring's points
	if licenses.LicenseExpiring.Paid != paid || licenses.LicenseExpiring.Trial != trialWarn {
		t.Fatalf("LicenseExpiring = %+v", licenses.LicenseExpiring)
	}
	running := func(w *world) { w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06")) }
	ended := func(w *world) { w.putOrg(w.license([]string{"events"}, "2026-11-06", "2026-12-01")) }
	for _, c := range []struct {
		name, today   string
		put           func(*world)
		state         string
		warn          bool
		endedOn, says string
	}{
		{"no license", "2026-12-07", func(*world) {}, licenses.StateFree, false, "", ""},
		{"a license running", "2026-12-07", running, licenses.StateLicensed, false, "", licensed},
		{"a license a day before its warning", "2027-10-23", running, licenses.StateLicensed, false, "", licensed},
		{"a license in its warning", "2027-10-24", running, licenses.StateLicensed, true, "", licensed},
		{"a license on its last day", "2027-11-06", running, licenses.StateLicensed, true, "", licensed},
		{"a license ended", "2027-11-07", running, licenses.StateFree, true, "2027-11-06", "The license for Acme Ltd ended on 2027-11-06"},
		{"a license not begun", "2026-11-01", running, licenses.StateFree, false, "", "The license acme-2026 starts on 2026-11-06"},
		{"a license that never ends", "2040-01-01", func(w *world) { w.putOrg(w.perpetual()) }, licenses.StateLicensed, false, "", licensed},
		{"a license of another line", "2040-01-01", func(w *world) {
			w.line = 2
			w.putOrg(w.perpetual())
		}, licenses.StateFree, false, "", "The license acme-perpetual is for Designer 1, not this build"},
		{"a license of another line and a trial", "2026-12-07", func(w *world) {
			w.line = 2
			w.putApp(w.perpetual())
			w.note("2026-12-03")
		}, licenses.StateTrial, false, "", "The license acme-perpetual is for Designer 1, not this build · Trial: 10 days left, until 2026-12-16"},
		{"a trial running", "2026-12-07", func(w *world) { w.note("2026-12-03") }, licenses.StateTrial, false, "", "Trial: 10 days left, until 2026-12-16"},
		{"a trial a day before its last days", "2026-12-13", func(w *world) { w.note("2026-12-03") }, licenses.StateTrial, false, "", "Trial: 4 days left, until 2026-12-16"},
		{"a trial in its last days", "2026-12-14", func(w *world) { w.note("2026-12-03") }, licenses.StateTrial, true, "", "Trial: 3 days left, until 2026-12-16"},
		{"a trial on its last day", "2026-12-16", func(w *world) { w.note("2026-12-03") }, licenses.StateTrial, true, "", "Trial: 1 day left, until 2026-12-16"},
		{"a trial ended", "2026-12-17", func(w *world) { w.note("2026-12-03") }, licenses.StateFree, true, "", "The trial ended on 2026-12-16"},
		{"a license and a trial", "2026-12-07", func(w *world) {
			running(w)
			w.note("2026-12-03")
		}, licenses.StateLicensed, false, "", licensed},
		{"the active license ended while the trial runs", "2026-12-07", func(w *world) {
			ended(w)
			w.note("2026-12-03")
		}, licenses.StateTrial, false, "", "The license for Acme Ltd ended on 2026-12-01 · Trial: 10 days left, until 2026-12-16"},
		{"the active license and the trial both ended", "2026-12-20", func(w *world) {
			ended(w)
			w.note("2026-12-03")
		}, licenses.StateFree, true, "2026-12-01", "The license for Acme Ltd ended on 2026-12-01"},
		{"the chosen license ended beside one running: no switch", "2026-12-07", func(w *world) {
			ended(w)
			w.putApp(w.licenseOf("ada-2026", "Ada", []string{"events"}, "2026-01-01", "2027-12-01"))
			w.choose("acme-2026")
		}, licenses.StateFree, true, "2026-12-01", "The license for Acme Ltd ended on 2026-12-01"},
		{"the chosen license refused", "2026-12-07", func(w *world) {
			running(w)
			w.putApp([]byte(`{"metadata":{"id":"ada-2026"}}`))
			w.choose("ada-2026")
			w.note("2026-12-03")
		}, licenses.StateTrial, false, "", "The chosen license ada-2026 was refused: it is not a license file · Trial: 10 days left, until 2026-12-16"},
		{"the chosen license gone", "2026-12-07", func(w *world) {
			running(w)
			w.choose("ada-2026")
		}, licenses.StateFree, false, "", "The chosen license ada-2026 is not among this app's licenses"},
		{"the newest of two", "2026-12-07", func(w *world) {
			w.putApp(w.licenseOf("ada-2026", "Ada", []string{"events"}, "2026-01-01", "2026-12-01"))
			running(w)
		}, licenses.StateLicensed, false, "", licensed},
		{"an old org license", "2026-12-07", func(w *world) { w.putOldOrg(w.old(oldOrg)) }, licenses.StateLicensed, false, "", licensed},
		{"an old product license ended", "2027-03-02", func(w *world) { w.putOldApp(w.old(oldProduct)) }, licenses.StateFree, true, "2027-03-01", "The license for Acme Ltd ended on 2027-03-01"},
		{"an old release-trial is refused", "2026-12-07", func(w *world) { w.putOldApp(w.old(oldReleaseTrial)) }, licenses.StateFree, false, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, c.today)
			c.put(w)
			s := w.show()
			if s.State != c.state || s.Warn != c.warn || s.EndedOn != c.endedOn || s.Sentence != c.says {
				t.Fatalf("show = state %s, warn %t, endedOn %q, sentence %q", s.State, s.Warn, s.EndedOn, s.Sentence)
			}
			out, err := w.run("show")
			want := "STATE"
			if c.says != "" {
				want = c.says + "\n\nSTATE"
			}
			if err != nil || !strings.HasPrefix(out, want) {
				t.Fatalf("err = %v, out =\n%s", err, out)
			}
		})
	}
}

func TestShowWithNoLicense(t *testing.T) {
	s := newWorld(t, "2026-11-19").show()
	if s.State != licenses.StateFree || !s.Build.Release || len(s.Licenses) != 0 || len(s.Refused) != 0 || s.Trial != nil || s.Counts != (licenses.Counts{Free: 1, Enterprise: 3, On: 1, Locked: 3}) {
		t.Fatalf("show = %+v", s)
	}
}

// Each license shows its id, place, issued day and whether it is active; each feature where it is unlocked from: the active license where it names it, else the trial.
func TestShowSaysWhereEachFeatureIsUnlockedFrom(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putOrg(w.license([]string{"events", "multi-design"}, "2026-11-06", "2027-11-06"))
	w.putApp(w.licenseOf("ada-2026", "Ada", []string{"sync"}, "2026-01-01", "2027-11-06"))
	w.note("2026-12-03")
	s := w.show()
	if got := from(s); got != "edit=none events=license multi-design=license sync=trial" {
		t.Fatalf("from = %s", got)
	}
	if s.Features[0].Until != "" || s.Features[1].Until != "2027-11-06" || s.Features[3].Until != "2026-12-16" || s.Counts.Locked != 0 {
		t.Fatalf("show = %+v", s)
	}
	if len(s.Licenses) != 2 || s.Licenses[0].ID != "ada-2026" || s.Licenses[0].Place != licenses.PlaceApp || s.Licenses[0].Active || s.Licenses[0].Issued != "2026-01-01" ||
		s.Licenses[1].ID != "acme-2026" || s.Licenses[1].Place != licenses.PlaceOrg || !s.Licenses[1].Active {
		t.Fatalf("licenses = %+v", s.Licenses)
	}
	if s.Trial == nil || s.Trial.Until != "2026-12-16" || s.Trial.DaysLeft != 10 || s.Trial.Window == nil || s.Trial.Window.Of != 14 {
		t.Fatalf("trial = %+v", s.Trial)
	}
	table, err := w.run("show")
	if err != nil || !regexp.MustCompile(`(?m)^\* +acme-2026 +org +Acme Ltd`).MatchString(table) || !strings.Contains(table, "multi-design  enterprise") || !strings.Contains(table, "TRIAL") {
		t.Fatalf("table %q, %v", table, err)
	}
	if list, err := w.run("list", "-o", "json"); err != nil || list != w.mustRun("show", "-o", "json") {
		t.Fatalf("list is not show: %v\n%s", err, list)
	}
}

// active is the id of the license show marks active, empty with none.
func active(s licenses.Show) string {
	for _, l := range s.Licenses {
		if l.Active {
			return l.ID
		}
	}
	return ""
}

func (w *world) mustRun(words ...string) string {
	w.t.Helper()
	out, err := w.run(words...)
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

// A license that never ends shows its lines, no last day and no days left, and opens its features with no last day.
func TestShowALicenseThatNeverEnds(t *testing.T) {
	w := newWorld(t, "2040-01-01")
	w.putOrg(w.perpetual())
	out, err := w.run("show", "-o", "json")
	if err != nil || strings.Contains(out, "daysLeft") || strings.Contains(out, `"until"`) || strings.Contains(out, `"kind": "license"`) {
		t.Fatalf("err = %v, out =\n%s", err, out)
	}
	if y, err := w.run("show", "-o", "yaml"); err != nil || strings.Contains(y, "daysLeft") {
		t.Fatalf("err = %v, yaml =\n%s", err, y)
	}
	s := w.show()
	if l := s.Licenses[0]; l.Lines == nil || (*l.Lines)["designer"][0] != 1 || l.Ends() != "" || l.Window != nil || s.Features[1].From != licenses.FromLicense {
		t.Fatalf("show = %+v", s)
	}
	if y, err := yaml.Marshal(s); err != nil || strings.Contains(string(y), "daysLeft") {
		t.Fatalf("err = %v, yaml.Marshal =\n%s", err, y)
	}
	if y, err := yaml.Marshal(licenses.ShownFile{Until: "2027-11-06", DaysLeft: 3}); err != nil || !strings.Contains(string(y), "daysLeft: 3\n") {
		t.Fatalf("err = %v, a license with a last day =\n%s", err, y)
	}
}

// A running license shows a window of license-expiring's paid point from that point on, and none a day before.
func TestShowALicensesWindowFromItsPoint(t *testing.T) {
	last := day("2027-03-01")
	for _, c := range []struct {
		left   int
		window *licenses.Window
	}{{licenses.LicenseExpiring.Paid + 1, nil}, {licenses.LicenseExpiring.Paid, &licenses.Window{Of: licenses.LicenseExpiring.Paid}}} {
		w := newWorld(t, last.AddDate(0, 0, 1-c.left).Format("2006-01-02"))
		w.putOrg(w.license([]string{"sync"}, "2026-03-01", "2027-03-01"))
		got := w.show().Licenses[0]
		if got.DaysLeft != c.left || (got.Window == nil) != (c.window == nil) || (got.Window != nil && *got.Window != *c.window) {
			t.Fatalf("%d days left: row = %+v", c.left, got)
		}
	}
}

// The sentence names the app as serve.App does, its title, else its ID, and its line from the build stamp when it has one.
func TestShowSaysTheAppsName(t *testing.T) {
	for _, c := range []struct {
		title string
		line  int
		says  string
	}{{"Designer", 1, "Licensed to Acme Ltd · Designer 1"}, {"", 1, "Licensed to Acme Ltd · designer 1"}, {"Designer", 0, "Licensed to Acme Ltd · Designer"}} {
		w := newWorld(t, "2026-12-07")
		w.app.Title, w.line = c.title, c.line
		w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06"))
		if s := w.show(); s.Sentence != c.says {
			t.Fatalf("title %q, line %d: sentence %q", c.title, c.line, s.Sentence)
		}
	}
}

// A refused file is shown with its file and reason, an old trial as retired_kind.
func TestShowARefusedFile(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putOldOrg([]byte("garbage"))
	w.putOldApp(w.old(oldPersonTrial))
	s := w.show()
	if s.State != licenses.StateFree || len(s.Licenses) != 0 || len(s.Refused) != 2 ||
		s.Refused[0].Reason != licenses.ReasonRetiredKind || s.Refused[0].File != filepath.Join(w.appDir(), "license") ||
		s.Refused[1].Reason != licenses.ReasonUnreadable || s.Refused[1].File != filepath.Join(w.org, "license") {
		t.Fatalf("show = %+v", s)
	}
}

// The old form's payloads, as nw-license signed them before (licenses.APIVersion).
const (
	oldOrg          = `{"id":"acme-2026","kind":"org","for":"Acme Ltd","products":{"designer":["events"]},"issued":"2026-11-06","until":"2027-11-06","keyId":"test-2026"}`
	oldProduct      = `{"id":"acme-designer","kind":"product","for":"Acme Ltd","products":{"designer":["sync"]},"issued":"2026-03-01","until":"2027-03-01","keyId":"test-2026"}`
	oldReleaseTrial = `{"id":"designer-v1.2.0-trial","kind":"release-trial","for":"designer v1.2.0","products":{"designer":[]},"issued":"2026-11-23","until":"2027-11-23","days":14,"keyId":"test-2026"}`
	oldPersonTrial  = `{"id":"ada-trial","kind":"trial","for":"ada@acme.example","issued":"2026-11-19","until":"2026-12-02","keyId":"test-2026"}`
)

func (w *world) file(raw []byte) string {
	file := filepath.Join(w.t.TempDir(), "acme.license")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		w.t.Fatal(err)
	}
	return file
}

// add asks first, keeps nothing without a yes, keeps the license in the app's own folder, or the org's with --org, and makes it active here.
func TestAddChecksAsksAndKeeps(t *testing.T) {
	for _, c := range []struct {
		flags []string
		kept  func(w *world) string
		where string
	}{
		{nil, func(w *world) string { return filepath.Join(w.appDir(), "licenses", "acme-2026.license") }, "as this app's own"},
		{[]string{"--org"}, func(w *world) string { return filepath.Join(w.org, "licenses", "acme-2026.license") }, "shared by every app it names"},
	} {
		w := newWorld(t, "2026-11-19")
		w.putApp(w.licenseOf("ada-2026", "Ada", []string{"events"}, "2026-11-10", "2027-11-06"))
		file := w.file(w.license([]string{"sync"}, "2026-11-06", "2027-11-06"))
		if _, err := w.run(append([]string{"add", file}, c.flags...)...); !errors.Is(err, ask.ErrRefused) {
			t.Fatalf("without a yes: %v", err)
		}
		if _, err := os.Stat(c.kept(w)); !os.IsNotExist(err) {
			t.Fatal("kept without a yes")
		}
		if want := "keep the license acme-2026 for Acme Ltd until 2027-11-06 in " + c.kept(w) + ", " + c.where + ", and make it the active license of this app"; w.asked != want {
			t.Fatalf("asked %q\nwant  %q", w.asked, want)
		}
		out, err := w.run(append([]string{"add", file, "--yes", "-o", "json"}, c.flags...)...)
		if err != nil {
			t.Fatal(err)
		}
		if err := contracts.Validate(schema, []byte(out)); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if s := w.show(); len(s.Licenses) != 2 || active(s) != "acme-2026" || s.State != licenses.StateLicensed {
			t.Fatalf("show = %+v", s)
		}
	}
}

func TestAddRefusesWhatItCannotTakeNamingTheFile(t *testing.T) {
	w := newWorld(t, "2026-11-19")
	for name, c := range map[string]struct{ file, want string }{
		"an old trial":       {w.file(w.old(oldPersonTrial)), licenses.ReasonRetiredKind},
		"an old org license": {w.file(w.old(oldOrg)), licenses.ReasonUnreadable},
		"a missing file":     {filepath.Join(t.TempDir(), "gone"), licenses.ReasonUnreadable},
	} {
		_, err := w.run("add", c.file, "--yes")
		var p *problem.Problem
		if !errors.As(err, &p) || p.Reason != c.want || len(p.At) != 1 || p.At[0].File != c.file {
			t.Fatalf("%s: err = %v, want %s at the file", name, err, c.want)
		}
	}
}

// Adding a license of an id that is there says it replaces that license before it asks (G-STACK-13).
func TestAddNamesTheLicenseItReplaces(t *testing.T) {
	w := newWorld(t, "2026-11-19")
	w.putOrg(w.license([]string{"sync"}, "2026-03-01", "2027-03-01"))
	if _, err := w.run("add", w.file(w.license([]string{"sync"}, "2026-03-01", "2028-03-01")), "--org", "--yes"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.asked, "replacing the license for Acme Ltd until 2027-03-01") {
		t.Fatalf("asked %q", w.asked)
	}
}

// use asks first, naming the license it replaces as the active one, and makes the license active; a license not there is not found.
func TestUseAsksAndChooses(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06"))
	w.putApp(w.licenseOf("ada-2026", "Ada", []string{"sync"}, "2026-01-01", "2027-11-06"))
	if _, err := w.run("use", "ada-2026"); !errors.Is(err, ask.ErrRefused) {
		t.Fatalf("without a yes: %v", err)
	}
	if want := "make the license ada-2026 for Ada until 2027-11-06 the active license of this app, in place of acme-2026"; w.asked != want {
		t.Fatalf("asked %q", w.asked)
	}
	if s := w.show(); !s.Licenses[1].Active {
		t.Fatalf("chose without a yes: %+v", s.Licenses)
	}
	out, err := w.run("use", "ada-2026", "--yes", "-o", "json")
	if err != nil || contracts.Validate(schema, []byte(out)) != nil {
		t.Fatalf("err = %v, out =\n%s", err, out)
	}
	if s := w.show(); !s.Licenses[0].Active || s.Licenses[1].Active || from(s) != "edit=none events=none multi-design=none sync=license" {
		t.Fatalf("show = %+v", s)
	}
	var p *problem.Problem
	if _, err := w.run("use", "nobody", "--yes"); !errors.As(err, &p) || p.Type != problem.TypeNotFound {
		t.Fatalf("use nobody: %v", err)
	}
}

// remove asks, naming the license and its file, takes it away, clears the choice that named it and leaves the trial.
func TestRemoveAsksAndLeavesTheTrial(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06"))
	w.choose("acme-2026")
	w.note("2026-12-03")
	if _, err := w.run("remove", "acme-2026"); !errors.Is(err, ask.ErrRefused) {
		t.Fatalf("without a yes: %v", err)
	}
	if want := "remove the license acme-2026, the file " + filepath.Join(w.org, "licenses", "acme-2026.license") + " in the org's licenses, shared by every app it names; the trial stays"; w.asked != want {
		t.Fatalf("asked %q", w.asked)
	}
	out, err := w.run("remove", "acme-2026", "--yes", "-o", "json")
	if err != nil || contracts.Validate(schema, []byte(out)) != nil {
		t.Fatalf("err = %v, out =\n%s", err, out)
	}
	if s := w.show(); s.State != licenses.StateTrial || len(s.Licenses) != 0 || s.Trial == nil {
		t.Fatalf("show = %+v", s)
	}
	if _, err := os.Stat(filepath.Join(w.appDir(), "license-active")); !os.IsNotExist(err) {
		t.Fatalf("the choice stays: %v", err)
	}
	_, err = w.run("remove", "acme-2026", "--yes")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeNotFound {
		t.Fatalf("with nothing to remove: %v", err)
	}
}

// A choice that cannot be read, here a folder, names nothing: no license is active, the sentence says why, and use writes over it.
func TestAChoiceThatCannotBeReadNamesNothing(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06"))
	at := filepath.Join(w.appDir(), "license-active")
	if err := os.MkdirAll(at, 0o700); err != nil {
		t.Fatal(err)
	}
	s := w.show()
	if want := "The choice of license cannot be read (" + at + " is not a regular file), so no license is active"; s.State != licenses.StateFree || active(s) != "" || s.Sentence != want {
		t.Fatalf("show = state %s, active %q, sentence %q", s.State, active(s), s.Sentence)
	}
	w.mustRun("use", "acme-2026", "--yes")
	if s := w.show(); s.State != licenses.StateLicensed || active(s) != "acme-2026" {
		t.Fatalf("show after use = %+v", s)
	}
}

// add and use say in their question when the license opens nothing here: it has ended, it has not started, or it does not run in this build.
func TestAddAndUseSayWhenTheLicenseDoesNotRunHere(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putApp(w.licenseOf("ada-2026", "Ada", []string{"sync"}, "2026-01-01", "2026-12-01"))
	w.run("use", "ada-2026")
	if want := "make the license ada-2026 for Ada until 2026-12-01, which has ended, the active license of this app"; w.asked != want {
		t.Fatalf("asked %q", w.asked)
	}
	w.line = 2
	w.run("add", w.file(w.perpetual()))
	if want := ", which never ends, which opens nothing in this build, in "; !strings.Contains(w.asked, want) {
		t.Fatalf("asked %q, want it to hold %q", w.asked, want)
	}
	w.line = 1
	w.putApp(w.licenseOf("ada-2027", "Ada", []string{"sync"}, "2027-01-01", "2027-12-01"))
	w.run("use", "ada-2027")
	if want := "make the license ada-2027 for Ada until 2027-12-01, which starts on 2027-01-01, the active license of this app"; w.asked != want {
		t.Fatalf("asked %q", w.asked)
	}
}

// remove names a file of the app's own as this app's.
func TestRemoveNamesTheAppsOwnFile(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putApp(w.license([]string{"events"}, "2026-11-06", "2027-11-06"))
	w.run("remove", "acme-2026")
	if want := "remove the license acme-2026, the file " + filepath.Join(w.appDir(), "licenses", "acme-2026.license") + " of this app; the trial stays"; w.asked != want {
		t.Fatalf("asked %q", w.asked)
	}
}
