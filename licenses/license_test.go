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

// A license no app takes is never signed: a trial that names features, a product license naming two products, an org license naming none.
func TestALicenseNoAppTakesIsNotSigned(t *testing.T) {
	k, _, _ := signer(t)
	trial := licenses.License{ID: "acme-2026", Kind: licenses.Trial, For: "ada@acme.example", Products: map[string][]string{"designer": {"events"}}, Issued: day("2026-11-19"), Until: day("2026-12-02"), KeyID: "prod-2026"}
	two := orgLicense
	two.Kind, two.Products = licenses.Product, map[string][]string{"designer": {"events"}, "kanban": {"sync"}}
	none := orgLicense
	none.Products = nil
	backwards := orgLicense
	backwards.Until = day("2026-01-01")
	for _, l := range []licenses.License{trial, two, none, backwards} {
		if _, err := licenses.Sign(l, k); err == nil {
			t.Errorf("signed %+v", l)
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
	for _, l := range []licenses.License{orgLicense, product, trial} {
		raw := sign(t, k, l)
		if err := contracts.Validate("mankit/v1/license-file.schema.json", raw); err != nil {
			t.Fatalf("%s file: %v\n%s", l.Kind, err, raw)
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
	body := []byte(`{"id":"acme-2026","kind":"org","for":"Acme Ltd","products":{"designer":["events"]},"issued":"2026-11-06","until":"2027-11-06","keyId":"prod-2026","seats":5}`)
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
		spec["payload"] = base64.StdEncoding.EncodeToString(bytes.Replace(body, []byte(`"seats":5`), []byte(`"seats":500`), 1))
	})
	if _, err := licenses.Verify(changed, release); reason(err) != licenses.ReasonSignature {
		t.Fatalf("a changed unknown field: err = %v, want refused for its signature", err)
	}
}
