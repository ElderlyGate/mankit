package licenses_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go.naywee.com/contracts/v2"

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
	k := keys{"test-2026": prod, "dev-2026": dev}
	trust := func(release bool) licenses.Trust {
		return licenses.Trust{Release: release, Prod: map[string]ed25519.PublicKey{"test-2026": prodPub}, Dev: map[string]ed25519.PublicKey{"dev-2026": devPub}}
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

// license is a license for designer's events until a day (ADR-L10).
var license = licenses.License{ID: "acme-2026", Kind: licenses.KindLicense, For: "Acme Ltd", Products: map[string][]string{"designer": {"events"}}, Issued: day("2026-11-06"), Until: day("2027-11-06"), KeyID: "test-2026"}

// perpetual is a license that never ends and opens designer's events and sync in Designer 1 (ADR-L10).
var perpetual = licenses.License{ID: "acme-perpetual", Kind: licenses.KindLicense, For: "Acme Ltd", Products: map[string][]string{"designer": {"events", "sync"}}, Lines: map[string][]int{"designer": {1}}, Issued: day("2026-11-06"), KeyID: "test-2026"}

// oldTrial is the payload of a trial key of the retired form a dev build wrote before the trial became a rule (ADR-L9).
const oldTrial = `{"id":"designer-1-trial","kind":"trial","for":"Designer 1","lines":{"designer":[1]},"issued":"2026-11-23","days":14,"keyId":"test-2026"}`

// The old form's payloads (licenses.APIVersion), as nw-license signed them before.
const (
	oldOrg          = `{"id":"acme-2026","kind":"org","for":"Acme Ltd","products":{"designer":["events"]},"issued":"2026-11-06","until":"2027-11-06","keyId":"test-2026"}`
	oldProduct      = `{"id":"acme-designer","kind":"product","for":"Acme Ltd","products":{"designer":["sync"]},"issued":"2026-03-01","until":"2027-03-01","keyId":"test-2026"}`
	oldReleaseTrial = `{"id":"designer-v1.2.0-trial","kind":"release-trial","for":"designer v1.2.0","products":{"designer":[]},"issued":"2026-11-23","until":"2027-11-23","days":14,"keyId":"test-2026"}`
	oldPersonTrial  = `{"id":"ada-trial","kind":"trial","for":"ada@acme.example","issued":"2026-11-19","until":"2026-12-02","keyId":"test-2026"}`
)

// signOld is a license file of the old form holding body, signed by k with the key test-2026.
func signOld(t *testing.T, k keys, body string) []byte {
	t.Helper()
	return signAs(t, k, licenses.APIVersion, body)
}

// signAs is a license file of the form apiVersion holding body as it is, signed by k with the key test-2026, as no Sign of today writes it.
func signAs(t *testing.T, k keys, apiVersion, body string) []byte {
	t.Helper()
	var p struct{ ID string }
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	sig, _ := k.Sign("test-2026", []byte(body))
	raw, err := json.Marshal(map[string]any{"apiVersion": apiVersion, "kind": licenses.FileKind, "metadata": map[string]any{"id": p.ID}, "spec": map[string]any{"payload": []byte(body), "signature": sig}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
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
	got, err := licenses.Verify(sign(t, k, license), release)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != licenses.KindLicense || got.For != "Acme Ltd" || !got.Names("designer", "events") || !got.Until.Equal(license.Until) || got.KeyID != "test-2026" {
		t.Fatalf("license = %+v", got)
	}
	got, err = licenses.Verify(sign(t, k, perpetual), release)
	if err != nil || !got.Until.IsZero() || len(got.Lines["designer"]) != 1 || got.Lines["designer"][0] != 1 || got.Days() != 0 {
		t.Fatalf("a license that never ends = %+v, %v", got, err)
	}
}

// An old org or product file is read for one release as a license (ADR-L8).
func TestAnOldFileIsReadAsALicense(t *testing.T) {
	k, release, _ := signer(t)
	for _, body := range []string{oldOrg, oldProduct} {
		got, err := licenses.Verify(signOld(t, k, body), release)
		if err != nil || got.Kind != licenses.KindLicense {
			t.Fatalf("%s: license = %+v, %v", body, got, err)
		}
	}
}

// Every trial file, of the old form or the retired trial key, a started one among them, is refused as retired_kind, saying why (ADR-L8, L9).
func TestAnOldTrialIsRefusedAsARetiredKind(t *testing.T) {
	k, release, _ := signer(t)
	started := func(raw []byte) []byte {
		return edit(t, raw, func(f map[string]any) {
			f["spec"].(map[string]any)["started"] = map[string]any{"day": "2026-12-03", "binary": "/bin/designer", "fileTime": "2026-12-03T09:14:05Z"}
		})
	}
	for name, raw := range map[string][]byte{
		"an old person's trial key": signOld(t, k, oldPersonTrial),
		"an old release-trial":      signOld(t, k, oldReleaseTrial),
		"a started release-trial":   started(signOld(t, k, oldReleaseTrial)),
		"a trial key":               signAs(t, k, licenses.FileAPIVersion, oldTrial),
		"a started trial key":       started(signAs(t, k, licenses.FileAPIVersion, oldTrial)),
	} {
		if _, err := licenses.Verify(raw, release); reason(err) != licenses.ReasonRetiredKind || !strings.Contains(err.Error(), "rule") {
			t.Errorf("%s: err = %v, want retired_kind", name, err)
		}
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
	raw := edit(t, sign(t, k, license), func(f map[string]any) {
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
	raw := edit(t, sign(t, k, license), func(f map[string]any) { f["metadata"] = map[string]any{"id": "other"} })
	if _, err := licenses.Verify(raw, release); reason(err) != licenses.ReasonIDMismatch {
		t.Fatalf("err = %v, want refused for its id", err)
	}
}

// A release takes prod keys only, a dev build dev keys only (ADR-L5); a key neither trusts is untrusted (ADR-L6).
func TestEachBuildTakesItsOwnKeysOnly(t *testing.T) {
	k, release, dev := signer(t)
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	k["other"] = stranger
	devLicense := license
	devLicense.KeyID = "dev-2026"
	unknown := license
	unknown.KeyID = "other"
	for name, c := range map[string]struct {
		l     licenses.License
		trust licenses.Trust
		want  string
	}{
		"a dev key on a release": {devLicense, release, licenses.ReasonOtherBuild},
		"a prod key on dev":      {license, dev, licenses.ReasonOtherBuild},
		"a key nobody trusts":    {unknown, release, licenses.ReasonUntrustedKey},
		"a dev key on dev":       {devLicense, dev, ""},
	} {
		if _, err := licenses.Verify(sign(t, k, c.l), c.trust); reason(err) != c.want || (c.want == "" && err != nil) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestWhatIsNoLicenseFileIsUnreadable(t *testing.T) {
	k, release, _ := signer(t)
	payload := base64.StdEncoding.EncodeToString([]byte(`{"kind":"license"}`))
	other := edit(t, sign(t, k, license), func(f map[string]any) { f["apiVersion"] = "elderlygate/mankit/license/v3" })
	for _, raw := range []string{"", "not json", `{"payload":"","signature":""}`, `{"payload":"` + payload + `","signature":"AA==","more":1}`, string(other)} {
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
	lic := func(change func(*licenses.License)) licenses.License {
		l := license
		l.Products = map[string][]string{"designer": {"events"}}
		change(&l)
		return l
	}
	for _, tc := range []struct {
		name  string
		l     licenses.License
		taken bool
	}{
		{"a license until a day", license, true},
		{"a license that names lines", perpetual, true},
		{"a one-day license", lic(func(l *licenses.License) { l.Until = l.Issued }), true},
		{"deprecated days, ignored", lic(func(l *licenses.License) { l.TrialDays = 14 }), true},
		{"a deprecated started trial, ignored", lic(func(l *licenses.License) {
			l.Started = &licenses.Started{Day: day("2026-12-03"), Binary: "/bin/designer"}
		}), true},
		{"a trial", lic(func(l *licenses.License) { l.Kind = licenses.Trial }), false},
		{"an old release-trial kind", lic(func(l *licenses.License) { l.Kind = licenses.ReleaseTrial }), false},
		{"an unknown kind", lic(func(l *licenses.License) { l.Kind = "site" }), false},
		{"an old org kind", lic(func(l *licenses.License) { l.Kind = licenses.Org }), false},
		{"an old product kind", lic(func(l *licenses.License) { l.Kind = licenses.Product }), false},
		{"no id", lic(func(l *licenses.License) { l.ID = "" }), false},
		{"an id with a space", lic(func(l *licenses.License) { l.ID = "acme 2026" }), false},
		{"nobody named", lic(func(l *licenses.License) { l.For = "  " }), false},
		{"no key", lic(func(l *licenses.License) { l.KeyID = "" }), false},
		{"a last day before its first", lic(func(l *licenses.License) { l.Until = day("2026-01-01") }), false},
		{"a license naming no product", lic(func(l *licenses.License) { l.Products = nil }), false},
		{"a product with no id", lic(func(l *licenses.License) { l.Products = map[string][]string{"": {"events"}} }), false},
		{"a product naming no feature", lic(func(l *licenses.License) { l.Products = map[string][]string{"designer": {}} }), false},
		{"a product naming an empty feature", lic(func(l *licenses.License) { l.Products = map[string][]string{"designer": {"events", ""}} }), false},
		{"a product naming a feature twice", lic(func(l *licenses.License) { l.Products = map[string][]string{"designer": {"events", "events"}} }), false},
		{"a license with neither until nor lines", lic(func(l *licenses.License) { l.Until = time.Time{} }), false},
		{"a license with both until and lines", lic(func(l *licenses.License) { l.Lines = map[string][]int{"designer": {1}} }), false},
		{"lines of another product", lic(func(l *licenses.License) { l.Until, l.Lines = time.Time{}, map[string][]int{"kanban": {1}} }), false},
		{"lines of fewer products", lic(func(l *licenses.License) {
			l.Until, l.Products, l.Lines = time.Time{}, map[string][]string{"designer": {"events"}, "kanban": {"sync"}}, map[string][]int{"designer": {1}}
		}), false},
		{"a line below 1", lic(func(l *licenses.License) { l.Until, l.Lines = time.Time{}, map[string][]int{"designer": {0}} }), false},
		{"a line twice", lic(func(l *licenses.License) { l.Until, l.Lines = time.Time{}, map[string][]int{"designer": {1, 1}} }), false},
		{"a product naming no line", lic(func(l *licenses.License) { l.Until, l.Lines = time.Time{}, map[string][]int{"designer": {}} }), false},
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

// Sign never writes what is deprecated: no days and no started trial reach the signed file.
func TestSignWritesNoTrial(t *testing.T) {
	k, release, _ := signer(t)
	l := license
	l.TrialDays, l.Started = 14, &licenses.Started{Day: day("2026-12-03"), Binary: "/bin/designer"}
	raw := sign(t, k, l)
	if bytes.Contains(raw, []byte("started")) {
		t.Fatalf("the file holds a started trial:\n%s", raw)
	}
	got, err := licenses.Verify(raw, release)
	if err != nil || got.TrialDays != 0 || got.Started != nil {
		t.Fatalf("license = %+v, %v", got, err)
	}
}

func TestAKeyThatIsNoEd25519KeyIsAnErrorNotAPanic(t *testing.T) {
	k, release, _ := signer(t)
	release.Prod["test-2026"] = ed25519.PublicKey("short")
	if _, err := licenses.Verify(sign(t, k, license), release); err == nil {
		t.Fatal("verified with a malformed key")
	}
}

// A signed file and its payload meet contracts mankit/v3 license-file and license-payload, the home of what each field means.
func TestASignedFileMeetsItsSchema(t *testing.T) {
	k, _, _ := signer(t)
	for _, l := range []licenses.License{license, perpetual} {
		raw := sign(t, k, l)
		if err := contracts.Validate("mankit/v3/license-file.schema.json", raw); err != nil {
			t.Fatalf("%s file: %v\n%s", l.ID, err, raw)
		}
		var f struct{ Spec struct{ Payload []byte } }
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		if err := contracts.Validate("mankit/v3/license-payload.schema.json", f.Spec.Payload); err != nil {
			t.Fatalf("%s payload: %v\n%s", l.ID, err, f.Spec.Payload)
		}
	}
}

// The old payloads in these tests meet contracts mankit/v1 license-payload, so what is read for one release is what was signed before.
func TestTheOldPayloadsMeetTheOldSchema(t *testing.T) {
	for _, body := range []string{oldOrg, oldProduct, oldReleaseTrial, oldPersonTrial} {
		if err := contracts.Validate("mankit/v1/license-payload.schema.json", []byte(body)); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
	}
}

// A field a newer nw-license adds to the spec or the signed payload is passed over, and the signature still covers the payload's every byte (ADR-S15, CT-RULE-4).
func TestANewerFileWithFieldsItDoesNotKnowIsTaken(t *testing.T) {
	k, release, _ := signer(t)
	body := []byte(`{"id":"acme-2026","kind":"license","for":"Acme Ltd","products":{"designer":["events"]},"issued":"2026-11-06","until":"2027-11-06","keyId":"test-2026","note":"issued for testing"}`)
	sig, _ := k.Sign("test-2026", body)
	raw, _ := json.Marshal(map[string]any{
		"apiVersion": licenses.FileAPIVersion, "kind": licenses.FileKind, "metadata": map[string]any{"id": "acme-2026"},
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
