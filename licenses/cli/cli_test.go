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
	return w.signed(licenses.License{ID: "acme-2026", Kind: licenses.KindLicense, For: "Acme Ltd", Products: map[string][]string{"designer": features}, Issued: day(issued), Until: day(until), KeyID: "test-2026"})
}

// perpetual is a license that never ends, opening designer's events in Designer 1 (ADR-L10).
func (w *world) perpetual() []byte {
	return w.signed(licenses.License{ID: "acme-perpetual", Kind: licenses.KindLicense, For: "Acme Ltd", Products: map[string][]string{"designer": {"events"}}, Lines: map[string][]int{"designer": {1}}, Issued: day("2026-11-06"), KeyID: "test-2026"})
}

// trial is Designer 1's built-in trial key, started on the day when started is not empty, as the app writes it beside the signed payload.
func (w *world) trial(started string) []byte {
	raw := w.signed(licenses.License{ID: "designer-1-trial", Kind: licenses.Trial, For: "Designer 1", Lines: map[string][]int{"designer": {1}}, Issued: day("2026-11-23"), TrialDays: 14, KeyID: "test-2026"})
	return w.started(raw, started)
}

func (w *world) started(raw []byte, on string) []byte {
	if on == "" {
		return raw
	}
	var f map[string]any
	_ = json.Unmarshal(raw, &f)
	f["spec"].(map[string]any)["started"] = map[string]any{"day": on, "binary": "/home/ada/.local/bin/designer", "fileTime": on + "T09:14:05Z"}
	raw, _ = json.Marshal(f)
	return raw
}

// old is a license file of the old form holding body.
func (w *world) old(body string) []byte {
	var p struct{ ID string }
	_ = json.Unmarshal([]byte(body), &p)
	sig, _ := w.keys.Sign("test-2026", []byte(body))
	raw, _ := json.Marshal(map[string]any{"apiVersion": licenses.APIVersion, "kind": licenses.FileKind, "metadata": map[string]any{"id": p.ID}, "spec": map[string]any{"payload": []byte(body), "signature": sig}})
	return raw
}

// putApp puts the app's own file, its trial or an old product license.
func (w *world) putApp(raw []byte) { w.putIn(filepath.Join(w.dir, home.Folder("designer")), raw) }

// putOrg puts the license in the folder of the org's own files.
func (w *world) putOrg(raw []byte) { w.putIn(w.org, raw) }

func (w *world) putIn(dir string, raw []byte) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "license"), raw, 0o600); err != nil {
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

const (
	licensed = "Licensed to Acme Ltd · Designer 1"
	ended    = "Your trial has ended. Buy a license to keep the paid features."
)

// Each case is the three states plus warn, the day a license ended and the sentence, as contracts mankit/v3 license-show names them (ADR-L3).
func TestShowTheStateAndWarnOfEachCase(t *testing.T) {
	const paid, trialDays = 14, 3 // licenses.LicenseExpiring's points
	if licenses.LicenseExpiring.Paid != paid || licenses.LicenseExpiring.Trial != trialDays {
		t.Fatalf("LicenseExpiring = %+v", licenses.LicenseExpiring)
	}
	for _, c := range []struct {
		name, today   string
		put           func(*world)
		state         string
		warn          bool
		endedOn, says string
	}{
		{"no license", "2026-12-07", func(*world) {}, licenses.StateFree, false, "", ""},
		{"a license running", "2026-12-07", func(w *world) { w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06")) }, licenses.StateLicensed, false, "", licensed},
		{"a license a day before its warning", "2027-10-23", func(w *world) { w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06")) }, licenses.StateLicensed, false, "", licensed},
		{"a license in its warning", "2027-10-24", func(w *world) { w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06")) }, licenses.StateLicensed, true, "", licensed},
		{"a license on its last day", "2027-11-06", func(w *world) { w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06")) }, licenses.StateLicensed, true, "", licensed},
		{"a license ended", "2027-11-07", func(w *world) { w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06")) }, licenses.StateFree, true, "2027-11-06", ""},
		{"a license not begun", "2026-11-01", func(w *world) { w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06")) }, licenses.StateFree, false, "", ""},
		{"a license that never ends", "2040-01-01", func(w *world) { w.putOrg(w.perpetual()) }, licenses.StateLicensed, false, "", licensed},
		{"a license of another line", "2040-01-01", func(w *world) {
			w.line = 2
			w.putOrg(w.perpetual())
		}, licenses.StateFree, false, "", ""},
		{"a trial running", "2026-12-07", func(w *world) { w.putApp(w.trial("2026-12-03")) }, licenses.StateTrial, false, "", "Trial: 10 days left (ends 2026-12-16)"},
		{"a trial a day before its last days", "2026-12-13", func(w *world) { w.putApp(w.trial("2026-12-03")) }, licenses.StateTrial, false, "", "Trial: 4 days left (ends 2026-12-16)"},
		{"a trial in its last days", "2026-12-14", func(w *world) { w.putApp(w.trial("2026-12-03")) }, licenses.StateTrial, true, "", "Trial: 3 days left (ends 2026-12-16)"},
		{"a trial on its last day", "2026-12-16", func(w *world) { w.putApp(w.trial("2026-12-03")) }, licenses.StateTrial, true, "", "Trial: 1 day left (ends 2026-12-16)"},
		{"a trial ended", "2026-12-17", func(w *world) { w.putApp(w.trial("2026-12-03")) }, licenses.StateFree, true, "", ended},
		{"a trial not started", "2026-12-07", func(w *world) { w.putApp(w.trial("")) }, licenses.StateFree, false, "", ""},
		{"a license and a trial", "2026-12-07", func(w *world) {
			w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06"))
			w.putApp(w.trial("2026-12-03"))
		}, licenses.StateLicensed, false, "", licensed},
		{"a license ended and a trial running", "2026-12-07", func(w *world) {
			w.putOrg(w.license([]string{"events"}, "2026-11-06", "2026-12-01"))
			w.putApp(w.trial("2026-12-03"))
		}, licenses.StateTrial, false, "", "Trial: 10 days left (ends 2026-12-16)"},
		{"a license and a trial both ended", "2026-12-20", func(w *world) {
			w.putOrg(w.license([]string{"events"}, "2026-11-06", "2026-12-01"))
			w.putApp(w.trial("2026-12-03"))
		}, licenses.StateFree, true, "2026-12-01", ended},
		{"an old org license", "2026-12-07", func(w *world) { w.putOrg(w.old(oldOrg)) }, licenses.StateLicensed, false, "", licensed},
		{"an old product license ended", "2027-03-02", func(w *world) { w.putApp(w.old(oldProduct)) }, licenses.StateFree, true, "2027-03-01", ""},
		{"an old release-trial running", "2026-12-07", func(w *world) { w.putApp(w.started(w.old(oldReleaseTrial), "2026-12-03")) }, licenses.StateTrial, false, "", "Trial: 10 days left (ends 2026-12-16)"},
		{"the latest end of the ended licenses", "2027-12-01", func(w *world) {
			w.putOrg(w.old(oldOrg))
			w.putApp(w.old(oldProduct))
		}, licenses.StateFree, true, "2027-11-06", ""},
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
	if s.State != licenses.StateFree || !s.Build.Release || len(s.Licenses) != 0 || len(s.Refused) != 0 || s.Counts != (licenses.Counts{Free: 1, Enterprise: 3, On: 1, Locked: 3}) {
		t.Fatalf("show = %+v", s)
	}
}

// Each feature says where it is unlocked from, and until when: a license's until, a trial's end, none when nothing opens it.
func TestShowSaysWhereEachFeatureIsUnlockedFrom(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putOrg(w.license([]string{"events", "multi-design"}, "2026-11-06", "2027-11-06"))
	w.putApp(w.trial("2026-12-03"))
	s := w.show()
	if got := from(s); got != "edit=none events=license multi-design=license sync=trial" {
		t.Fatalf("from = %s", got)
	}
	if s.Features[0].Until != "" || s.Features[1].Until != "2027-11-06" || s.Features[3].Until != "2026-12-16" || s.Counts.Locked != 0 {
		t.Fatalf("show = %+v", s)
	}
	if len(s.Licenses) != 2 || s.Licenses[0].Kind != licenses.KindLicense || s.Licenses[1].Kind != licenses.Trial {
		t.Fatalf("licenses = %+v", s.Licenses)
	}
	table, err := w.run("show")
	if err != nil || !strings.Contains(table, "licensed") || !strings.Contains(table, "multi-design  enterprise") {
		t.Fatalf("table %q, %v", table, err)
	}
}

// A license that never ends shows its lines, no last day and no days left, and opens its features with no last day.
func TestShowALicenseThatNeverEnds(t *testing.T) {
	w := newWorld(t, "2040-01-01")
	w.putOrg(w.perpetual())
	out, err := w.run("show", "-o", "json")
	if err != nil || strings.Contains(out, "daysLeft") || strings.Contains(out, `"until"`) {
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
	if y, err := yaml.Marshal(licenses.ShownFile{Kind: licenses.KindLicense, Until: "2027-11-06", DaysLeft: 3}); err != nil || !strings.Contains(string(y), "daysLeft: 3\n") {
		t.Fatalf("err = %v, a license with a last day =\n%s", err, y)
	}
	table, err := w.run("show")
	if err != nil || !regexp.MustCompile(`(?m)^license +Acme Ltd +– +– +test-2026`).MatchString(table) {
		t.Fatalf("err = %v, table =\n%s", err, table)
	}
}

// A running trial shows its end and a window of its days; one not started shows neither.
func TestShowATrialFromItsStartedDay(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putApp(w.trial("2026-12-03"))
	l := w.show().Licenses[0]
	if l.Kind != licenses.Trial || l.For != "Designer 1" || l.TrialEnds != "2026-12-16" || l.Until != "" || l.DaysLeft != 10 || l.Window == nil || *l.Window != (licenses.Window{Of: 14}) {
		t.Fatalf("row = %+v", l)
	}
	w.putApp(w.trial(""))
	if l := w.show().Licenses[0]; l.TrialEnds != "" || l.Window != nil || l.Ended {
		t.Fatalf("not started: row = %+v", l)
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

func TestShowARefusedFile(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putOrg([]byte("garbage"))
	w.putApp(w.old(oldPersonTrial))
	s := w.show()
	if s.State != licenses.StateFree || len(s.Licenses) != 0 || len(s.Refused) != 2 || s.Refused[0].Reason != licenses.ReasonUnreadable || s.Refused[0].File != filepath.Join(w.org, "license") || s.Refused[1].Reason != licenses.ReasonUnreadable {
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

func TestAddChecksAsksAndKeeps(t *testing.T) {
	w := newWorld(t, "2026-11-19")
	file := w.file(w.license([]string{"sync"}, "2026-11-06", "2027-11-06"))
	if _, err := w.run("add", file); !errors.Is(err, ask.ErrRefused) {
		t.Fatalf("without a yes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.org, "license")); !os.IsNotExist(err) {
		t.Fatal("kept without a yes")
	}
	out, err := w.run("add", file, "--yes", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate(schema, []byte(out)); err != nil || !strings.Contains(out, `"from": "license"`) {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(w.asked, "keep the license for Acme Ltd until 2027-11-06, read by every app, in "+filepath.Join(w.org, "license")) {
		t.Fatalf("asked %q", w.asked)
	}
}

// --org is the old form for one release: hidden, still taken, and it keeps the one license.
func TestAddTakesTheOldOrgFlag(t *testing.T) {
	w := newWorld(t, "2026-11-19")
	out, err := w.run("add", "--org", w.file(w.license([]string{"sync"}, "2026-11-06", "2027-11-06")), "--yes")
	if err != nil || !strings.Contains(out, "deprecated") {
		t.Fatalf("err = %v, out =\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(w.org, "license")); err != nil {
		t.Fatal(err)
	}
	help, _ := w.run("add", "--help")
	if strings.Contains(help, "--org") {
		t.Fatalf("help lists --org:\n%s", help)
	}
}

func TestAddRefusesWhatItCannotTakeNamingTheFile(t *testing.T) {
	w := newWorld(t, "2026-11-19")
	for name, c := range map[string]struct{ file, want string }{
		"a trial":            {w.file(w.trial("")), licenses.ReasonWrongPlace},
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

// Adding a license where one is says it replaces that license before it asks (G-STACK-13).
func TestAddNamesTheLicenseItReplaces(t *testing.T) {
	w := newWorld(t, "2026-11-19")
	w.putOrg(w.license([]string{"sync"}, "2026-03-01", "2027-03-01"))
	if _, err := w.run("add", w.file(w.perpetual()), "--yes"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.asked, "keep the license for Acme Ltd, which never ends") || !strings.Contains(w.asked, "replacing the license for Acme Ltd until 2027-03-01") {
		t.Fatalf("asked %q", w.asked)
	}
}

// remove asks, naming the file, takes the license away and leaves the trial.
func TestRemoveAsksAndLeavesTheTrial(t *testing.T) {
	w := newWorld(t, "2026-12-07")
	w.putOrg(w.license([]string{"events"}, "2026-11-06", "2027-11-06"))
	w.putApp(w.trial("2026-12-03"))
	if _, err := w.run("remove"); !errors.Is(err, ask.ErrRefused) {
		t.Fatalf("without a yes: %v", err)
	}
	if w.asked != "remove the license file "+filepath.Join(w.org, "license")+"; the trial stays" {
		t.Fatalf("asked %q", w.asked)
	}
	out, err := w.run("remove", "--yes", "-o", "json")
	if err != nil || contracts.Validate(schema, []byte(out)) != nil {
		t.Fatalf("err = %v, out =\n%s", err, out)
	}
	if s := w.show(); s.State != licenses.StateTrial || len(s.Licenses) != 1 || s.Licenses[0].Kind != licenses.Trial {
		t.Fatalf("show = %+v", s)
	}
	_, err = w.run("remove", "--yes")
	var p *problem.Problem
	if !errors.As(err, &p) || p.Type != problem.TypeNotFound {
		t.Fatalf("with nothing to remove: %v", err)
	}
}
