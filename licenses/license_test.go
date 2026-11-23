package licenses_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"

	"github.com/tugbayatilla/mankit/v4/licenses"
	"github.com/tugbayatilla/mankit/v4/problem"
)

// keys is a signer held in the test, as nw-license's would hold its keys.
type keys map[string]ed25519.PrivateKey

func (k keys) Sign(id string, message []byte) ([]byte, error) {
	p, ok := k[id]
	if !ok {
		return nil, errors.New("no such key")
	}
	return ed25519.Sign(p, message), nil
}

// signer is a signer with a prod key and a dev key, and the trust of a release and of a dev build in them.
func signer(t *testing.T) (keys, licenses.Trust, licenses.Trust) {
	t.Helper()
	prodPub, prod, _ := ed25519.GenerateKey(rand.Reader)
	devPub, dev, _ := ed25519.GenerateKey(rand.Reader)
	k := keys{"prod-2026": prod, "dev-2026": dev}
	trust := func(release bool) licenses.Trust {
		return licenses.Trust{Release: release, Prod: map[string]ed25519.PublicKey{"prod-2026": prodPub}, Dev: map[string]ed25519.PublicKey{"dev-2026": devPub}}
	}
	return k, trust(true), trust(false)
}

func day(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

var orgLicense = licenses.License{ID: "acme-2026", Kind: licenses.Org, For: "Acme Ltd", Products: map[string][]string{"designer": {"events"}}, Issued: day("2026-11-06"), Until: day("2027-11-06"), KeyID: "prod-2026"}

// releaseTrial is the trial key built into one release of designer: it may start a trial until its until, each trial running 14 days (ADR-L9).
var releaseTrial = licenses.License{ID: "designer-v1.2.0-trial", Kind: licenses.ReleaseTrial, For: "designer v1.2.0", Products: map[string][]string{"designer": nil}, Issued: day("2026-11-23"), Until: day("2027-11-23"), TrialDays: 14, KeyID: "prod-2026"}

// start is the license file raw with the trial it started on the day, as the app writes it beside the signed payload.
func start(t *testing.T, raw []byte, on string) []byte {
	t.Helper()
	return edit(t, raw, func(f map[string]any) {
		f["spec"].(map[string]any)["started"] = map[string]any{"day": on, "binary": "/home/ada/.local/bin/designer", "fileTime": on + "T09:14:05Z"}
	})
}

func sign(t *testing.T, k keys, l licenses.License) []byte {
	t.Helper()
	raw, err := licenses.Sign(l, k)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func reason(err error) string {
	var p *problem.Problem
	if errors.As(err, &p) && p.Type == problem.TypeRefused {
		return p.Reason
	}
	return ""
}

func TestASignedLicenseVerifiesAsSigned(t *testing.T) {
	k, release, _ := signer(t)
	got, err := licenses.Verify(sign(t, k, orgLicense), release)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != licenses.Org || got.For != "Acme Ltd" || !got.Names("designer", "events") || !got.Until.Equal(orgLicense.Until) || got.KeyID != "prod-2026" {
		t.Fatalf("license = %+v", got)
	}
}

// edit is the license file raw with change made to it as JSON, as someone with a text editor would.
func edit(t *testing.T, raw []byte, change func(f map[string]any)) []byte {
	t.Helper()
	var f map[string]any
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	change(f)
	out, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The signature covers the payload's bytes as stored: a change to one byte of what it holds is refused.
func TestAChangedLicenseIsRefusedForItsSignature(t *testing.T) {
	k, release, _ := signer(t)
	raw := edit(t, sign(t, k, orgLicense), func(f map[string]any) {
		spec := f["spec"].(map[string]any)
		body, _ := base64.StdEncoding.DecodeString(spec["payload"].(string))
		spec["payload"] = base64.StdEncoding.EncodeToString(bytes.Replace(body, []byte("2027-11-06"), []byte("2099-11-06"), 1))
	})
	if _, err := licenses.Verify(raw, release); reason(err) != licenses.ReasonSignature {
		t.Fatalf("err = %v, want refused for its signature", err)
	}
}

// The file's id names it only while it is the signed one (ADR-S15).
func TestAFileWhoseIdIsNotTheSignedOneIsRefused(t *testing.T) {
	k, release, _ := signer(t)
	raw := edit(t, sign(t, k, orgLicense), func(f map[string]any) { f["metadata"] = map[string]any{"id": "other"} })
	if _, err := licenses.Verify(raw, release); reason(err) != licenses.ReasonIDMismatch {
		t.Fatalf("err = %v, want refused for its id", err)
	}
}

// A release takes prod keys only, a dev build dev keys only (ADR-L5); a key neither trusts is untrusted (ADR-L6).
func TestEachBuildTakesItsOwnKeysOnly(t *testing.T) {
	k, release, dev := signer(t)
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	k["other"] = stranger
	devLicense := orgLicense
	devLicense.KeyID = "dev-2026"
	unknown := orgLicense
	unknown.KeyID = "other"
	for name, c := range map[string]struct {
		l     licenses.License
		trust licenses.Trust
		want  string
	}{
		"a dev key on a release": {devLicense, release, licenses.ReasonOtherBuild},
		"a prod key on dev":      {orgLicense, dev, licenses.ReasonOtherBuild},
		"a key nobody trusts":    {unknown, release, licenses.ReasonUntrustedKey},
		"a dev key on dev":       {devLicense, dev, ""},
	} {
		if _, err := licenses.Verify(sign(t, k, c.l), c.trust); reason(err) != c.want || (c.want == "" && err != nil) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestWhatIsNoLicenseFileIsUnreadable(t *testing.T) {
	_, release, _ := signer(t)
	payload := base64.StdEncoding.EncodeToString([]byte(`{"kind":"org"}`))
	for _, raw := range []string{"", "not json", `{"payload":"","signature":""}`, `{"payload":"` + payload + `","signature":"AA==","more":1}`} {
		if _, err := licenses.Verify([]byte(raw), release); reason(err) != licenses.ReasonUnreadable {
			t.Errorf("%q: err = %v, want unreadable", raw, err)
		}
	}
}

// counted is a signer that counts what it is asked to sign, so a test sees Sign refuse before it reaches a key.
type counted struct {
	keys
	asked int
}

func (c *counted) Sign(id string, message []byte) ([]byte, error) {
	c.asked++
	return c.keys.Sign(id, message)
}

// Check and Sign refuse the same licenses, one table for both: Sign refuses each before it asks its signer, and takes what Check takes.
func TestCheckRefusesExactlyWhatSignRefuses(t *testing.T) {
	k, _, _ := signer(t)
	with := func(change func(*licenses.License)) licenses.License {
		l := orgLicense
		l.Products = map[string][]string{"designer": {"events"}}
		change(&l)
		return l
	}
	trial := with(func(l *licenses.License) { l.Kind, l.For, l.Products = licenses.Trial, "ada@acme.example", nil })
	rt := func(change func(*licenses.License)) licenses.License {
		l := releaseTrial
		l.Products = map[string][]string{"designer": nil}
		change(&l)
		return l
	}
	for _, tc := range []struct {
		name  string
		l     licenses.License
		taken bool
	}{
		{"an org license", orgLicense, true},
		{"a product license", with(func(l *licenses.License) { l.Kind = licenses.Product }), true},
		{"a trial key", trial, true},
		{"a one-day license", with(func(l *licenses.License) { l.Until = l.Issued }), true},
		{"an unknown kind", with(func(l *licenses.License) { l.Kind = "site" }), false},
		{"no id", with(func(l *licenses.License) { l.ID = "" }), false},
		{"an id with a space", with(func(l *licenses.License) { l.ID = "acme 2026" }), false},
		{"nobody named", with(func(l *licenses.License) { l.For = "  " }), false},
		{"no key", with(func(l *licenses.License) { l.KeyID = "" }), false},
		{"a last day before its first", with(func(l *licenses.License) { l.Until = day("2026-01-01") }), false},
		{"a trial that names features", with(func(l *licenses.License) { l.Kind = licenses.Trial }), false},
		{"a product license naming two products", with(func(l *licenses.License) {
			l.Kind, l.Products = licenses.Product, map[string][]string{"designer": {"events"}, "kanban": {"sync"}}
		}), false},
		{"a product license naming none", with(func(l *licenses.License) { l.Kind, l.Products = licenses.Product, nil }), false},
		{"an org license naming none", with(func(l *licenses.License) { l.Products = nil }), false},
		{"a product with no id", with(func(l *licenses.License) { l.Products = map[string][]string{"": {"events"}} }), false},
		{"a product naming no feature", with(func(l *licenses.License) { l.Products = map[string][]string{"designer": {}} }), false},
		{"a product naming an empty feature", with(func(l *licenses.License) { l.Products = map[string][]string{"designer": {"events", ""}} }), false},
		{"a release-trial", releaseTrial, true},
		{"a release-trial of one day", rt(func(l *licenses.License) { l.TrialDays = 1 }), true},
		{"a release-trial naming a feature", rt(func(l *licenses.License) { l.Products = map[string][]string{"designer": {"events"}} }), false},
		{"a release-trial naming two products", rt(func(l *licenses.License) { l.Products = map[string][]string{"designer": nil, "kanban": nil} }), false},
		{"a release-trial naming no product", rt(func(l *licenses.License) { l.Products = nil }), false},
		{"a release-trial with an empty product id", rt(func(l *licenses.License) { l.Products = map[string][]string{"": nil} }), false},
		{"a release-trial of no days", rt(func(l *licenses.License) { l.TrialDays = 0 }), false},
		{"a release-trial of negative days", rt(func(l *licenses.License) { l.TrialDays = -1 }), false},
		{"days on an org license", with(func(l *licenses.License) { l.TrialDays = 14 }), false},
		{"days on a trial key", with(func(l *licenses.License) { l.Kind, l.Products, l.TrialDays = licenses.Trial, nil, 14 }), false},
		{"a started trial on an org license", with(func(l *licenses.License) {
			l.Started = &licenses.Started{Day: day("2026-12-03"), Binary: "/bin/designer"}
		}), false},
		{"a release-trial started after its last day to start one", rt(func(l *licenses.License) {
			l.Started = &licenses.Started{Day: day("2027-11-24"), Binary: "/bin/designer"}
		}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &counted{keys: k}
			checkErr := tc.l.Check()
			_, signErr := licenses.Sign(tc.l, s)
			if tc.taken {
				if checkErr != nil || signErr != nil {
					t.Fatalf("check = %v, sign = %v; want both taken", checkErr, signErr)
				}
				return
			}
			if !errors.Is(checkErr, licenses.ErrNotTaken) || !errors.Is(signErr, licenses.ErrNotTaken) {
				t.Fatalf("check = %v, sign = %v; want both ErrNotTaken", checkErr, signErr)
			}
			if s.asked != 0 {
				t.Fatalf("Sign asked its signer %d times for a license Check refuses", s.asked)
			}
		})
	}
}

// Sign writes the key as the signing tool does: a started trial is the app's to write, never signed.
func TestSignRefusesAStartedTrial(t *testing.T) {
	k, _, _ := signer(t)
	l := releaseTrial
	l.Started = &licenses.Started{Day: day("2026-12-03"), Binary: "/bin/designer"}
	if err := l.Check(); err != nil {
		t.Fatal(err)
	}
	s := &counted{keys: k}
	if _, err := licenses.Sign(l, s); err == nil || s.asked != 0 {
		t.Fatalf("err = %v, asked %d", err, s.asked)
	}
}

// A release-trial carries its days signed, and its length is them, not its issued to its until.
func TestAReleaseTrialVerifiesWithItsDays(t *testing.T) {
	k, release, _ := signer(t)
	got, err := licenses.Verify(sign(t, k, releaseTrial), release)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != licenses.ReleaseTrial || got.TrialDays != 14 || got.Days() != 14 || got.Started != nil || len(got.Products) != 1 || got.Products["designer"] == nil {
		t.Fatalf("license = %+v", got)
	}
	changed := edit(t, sign(t, k, releaseTrial), func(f map[string]any) {
		spec := f["spec"].(map[string]any)
		body, _ := base64.StdEncoding.DecodeString(spec["payload"].(string))
		spec["payload"] = base64.StdEncoding.EncodeToString(bytes.Replace(body, []byte(`"days":14`), []byte(`"days":99`), 1))
	})
	if _, err := licenses.Verify(changed, release); reason(err) != licenses.ReasonSignature {
		t.Fatalf("changed days: err = %v, want refused for its signature", err)
	}
}

// The started trial is read from beside the payload, unsigned, so writing it never breaks the signature; only a release-trial's file has it (contracts mankit/v1 license-file).
func TestAStartedTrialIsReadUnsignedFromAReleaseTrialsFileOnly(t *testing.T) {
	k, release, _ := signer(t)
	got, err := licenses.Verify(start(t, sign(t, k, releaseTrial), "2026-12-03"), release)
	if err != nil {
		t.Fatal(err)
	}
	if got.Started == nil || !got.Started.Day.Equal(day("2026-12-03")) || got.Started.Binary != "/home/ada/.local/bin/designer" || !got.Started.FileTime.Equal(day("2026-12-03").Add(9*time.Hour+14*time.Minute+5*time.Second)) {
		t.Fatalf("started = %+v", got.Started)
	}
	for name, raw := range map[string][]byte{
		"on an org license":               start(t, sign(t, k, orgLicense), "2026-12-03"),
		"after its last day to start one": start(t, sign(t, k, releaseTrial), "2027-11-24"),
		"with no day": edit(t, start(t, sign(t, k, releaseTrial), "2026-12-03"), func(f map[string]any) {
			delete(f["spec"].(map[string]any)["started"].(map[string]any), "day")
		}),
		"with no binary": edit(t, start(t, sign(t, k, releaseTrial), "2026-12-03"), func(f map[string]any) {
			f["spec"].(map[string]any)["started"].(map[string]any)["binary"] = ""
		}),
		"with a file time that is no time": edit(t, start(t, sign(t, k, releaseTrial), "2026-12-03"), func(f map[string]any) {
			f["spec"].(map[string]any)["started"].(map[string]any)["fileTime"] = "yesterday"
		}),
		"as text": edit(t, sign(t, k, releaseTrial), func(f map[string]any) { f["spec"].(map[string]any)["started"] = "2026-12-03" }),
	} {
		if _, err := licenses.Verify(raw, release); reason(err) != licenses.ReasonUnreadable {
			t.Errorf("a started trial %s: err = %v, want unreadable", name, err)
		}
	}
}

func TestATrialKeysLengthIsItsDays(t *testing.T) {
	trial := licenses.License{ID: "acme-2026", Kind: licenses.Trial, Issued: day("2026-11-19"), Until: day("2026-12-02")}
	if got := trial.Days(); got != 14 {
		t.Fatalf("days = %d, want 14", got)
	}
}

func TestAKeyThatIsNoEd25519KeyIsAnErrorNotAPanic(t *testing.T) {
	k, release, _ := signer(t)
	release.Prod["prod-2026"] = ed25519.PublicKey("short")
	if _, err := licenses.Verify(sign(t, k, orgLicense), release); err == nil {
		t.Fatal("verified with a malformed key")
	}
}

// A signed file and its payload meet contracts mankit/v1 license-file and license-payload, the home of what each field means.
func TestASignedFileMeetsItsSchema(t *testing.T) {
	k, _, _ := signer(t)
	product := orgLicense
	product.Kind, product.Products = licenses.Product, map[string][]string{"designer": {"sync"}}
	trial := licenses.License{ID: "acme-2026", Kind: licenses.Trial, For: "ada@acme.example", Issued: day("2026-11-19"), Until: day("2026-12-02"), KeyID: "prod-2026"}
	for _, l := range []licenses.License{orgLicense, product, trial, releaseTrial} {
		raw := sign(t, k, l)
		if err := contracts.Validate("mankit/v1/license-file.schema.json", raw); err != nil {
			t.Fatalf("%s file: %v\n%s", l.Kind, err, raw)
		}
		if l.Kind == licenses.ReleaseTrial {
			if started := start(t, raw, "2026-12-03"); contracts.Validate("mankit/v1/license-file.schema.json", started) != nil {
				t.Fatalf("a started release-trial's file does not meet its schema\n%s", started)
			}
		}
		var f struct{ Spec struct{ Payload []byte } }
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		if err := contracts.Validate("mankit/v1/license-payload.schema.json", f.Spec.Payload); err != nil {
			t.Fatalf("%s payload: %v\n%s", l.Kind, err, f.Spec.Payload)
		}
	}
}

// A field a newer nw-license adds to the spec or the signed payload is passed over, and the signature still covers the payload's every byte (ADR-S15, CT-RULE-4).
func TestANewerFileWithFieldsItDoesNotKnowIsTaken(t *testing.T) {
	k, release, _ := signer(t)
	body := []byte(`{"id":"acme-2026","kind":"org","for":"Acme Ltd","products":{"designer":["events"]},"issued":"2026-11-06","until":"2027-11-06","keyId":"prod-2026","note":"issued for testing"}`)
	sig, _ := k.Sign("prod-2026", body)
	raw, _ := json.Marshal(map[string]any{
		"apiVersion": licenses.APIVersion, "kind": licenses.FileKind, "metadata": map[string]any{"id": "acme-2026"},
		"spec": map[string]any{"payload": body, "signature": sig, "note": "from a newer nw-license"},
	})
	got, err := licenses.Verify(raw, release)
	if err != nil || got.ID != "acme-2026" || !got.Names("designer", "events") {
		t.Fatalf("license = %+v, %v", got, err)
	}
	changed := edit(t, raw, func(f map[string]any) {
		spec := f["spec"].(map[string]any)
		spec["payload"] = base64.StdEncoding.EncodeToString(bytes.Replace(body, []byte(`"note":"issued for testing"`), []byte(`"note":"issued for testinG"`), 1))
	})
	if _, err := licenses.Verify(changed, release); reason(err) != licenses.ReasonSignature {
		t.Fatalf("a changed unknown field: err = %v, want refused for its signature", err)
	}
}
