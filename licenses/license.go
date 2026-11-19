// Package licenses is what unlocks an app's enterprise features: Ed25519-signed license files, checked offline (ADR-L1) against the keys the app's build trusts (ADR-L5, L6); nw-license signs with Sign, every app checks with a Checker.
package licenses

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/tugbayatilla/mankit/v4/problem"
)

// Kind is what a license covers (ADR-L8, L9); contracts mankit/v1 license-show says what each means.
type Kind string

// The kinds of license.
const (
	Org     Kind = "org"
	Product Kind = "product"
	Trial   Kind = "trial"
)

// The reasons a license file is refused, Problem reasons of type refused (contracts problem/v1).
const (
	ReasonSignature    = "signature"
	ReasonUntrustedKey = "untrusted_key"
	ReasonOtherBuild   = "other_build"
	ReasonUnreadable   = "unreadable"
	ReasonWrongPlace   = "wrong_place"
	ReasonIDMismatch   = "id_mismatch"
)

// License is what a license file says, all of it signed; contracts mankit/v1 license-payload says what each field means, license-file the file around it.
type License struct {
	ID       string
	Kind     Kind
	For      string
	Products map[string][]string
	Issued   time.Time
	Until    time.Time
	KeyID    string
}

// Days is how many days the license runs, its first and last day included: a trial key's length (ADR-L9).
func (l License) Days() int { return daysBetween(l.Issued, l.Until) + 1 }

// Names reports whether the license names the feature id of the product id.
func (l License) Names(product, id string) bool { return slices.Contains(l.Products[product], id) }

// Signer signs message with the key id, as nw-license's signer does; the private key never leaves it.
type Signer interface {
	Sign(id string, message []byte) ([]byte, error)
}

// Trust is the keys the app's build trusts by key id, and whether its own build is a release: a release takes Prod keys only, every other build Dev keys only (ADR-L5).
type Trust struct {
	Release bool
	Prod    map[string]ed25519.PublicKey
	Dev     map[string]ed25519.PublicKey
}

// check refuses keys that are not Ed25519 public keys, or an id both builds name.
func (t Trust) check() error {
	for id, k := range t.Prod {
		if _, both := t.Dev[id]; both {
			return fmt.Errorf("licenses: the key id %s is both a prod and a dev key", id)
		}
		if len(k) != ed25519.PublicKeySize {
			return fmt.Errorf("licenses: the prod key %s is not an Ed25519 public key", id)
		}
	}
	for id, k := range t.Dev {
		if len(k) != ed25519.PublicKeySize {
			return fmt.Errorf("licenses: the dev key %s is not an Ed25519 public key", id)
		}
	}
	return nil
}

// file is a license file on disk (contracts mankit/v1 license-file).
type file struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		ID string `json:"id"`
	} `json:"metadata"`
	Spec struct {
		Payload   []byte `json:"payload"`
		Signature []byte `json:"signature"`
	} `json:"spec"`
}

// The license file's form and kind (ADR-S15, contracts apiversion/v1 forms.yaml).
const (
	APIVersion = "elderlygate/mankit/license/v1"
	FileKind   = "License"
)

// payload is what is signed.
type payload struct {
	ID       string              `json:"id"`
	Kind     Kind                `json:"kind"`
	For      string              `json:"for"`
	Products map[string][]string `json:"products,omitempty"`
	Issued   string              `json:"issued"`
	Until    string              `json:"until"`
	KeyID    string              `json:"keyId"`
}

const day = "2006-01-02"

// Sign is the license file of l, signed by s with the key l.KeyID; a license that says something no app takes is refused before anything is signed.
func Sign(l License, s Signer) ([]byte, error) {
	if err := l.check(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload{ID: l.ID, Kind: l.Kind, For: l.For, Products: l.Products, Issued: l.Issued.UTC().Format(day), Until: l.Until.UTC().Format(day), KeyID: l.KeyID})
	if err != nil {
		return nil, err
	}
	sig, err := s.Sign(l.KeyID, body)
	if err != nil {
		return nil, fmt.Errorf("licenses: the key %s did not sign: %w", l.KeyID, err)
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("licenses: the key %s gave no Ed25519 signature", l.KeyID)
	}
	f := file{APIVersion: APIVersion, Kind: FileKind}
	f.Metadata.ID, f.Spec.Payload, f.Spec.Signature = l.ID, body, sig
	out, err := json.MarshalIndent(f, "", "  ")
	return append(out, '\n'), err
}

// Verify is the license raw holds once its signature over the payload's stored bytes checks against a key t trusts for this build; otherwise a Problem refused with the reason why.
func Verify(raw []byte, t Trust) (License, error) {
	if err := t.check(); err != nil {
		return License{}, err
	}
	var f file
	if err := strict(raw, &f); err != nil || f.APIVersion != APIVersion || f.Kind != FileKind || len(f.Spec.Payload) == 0 || len(f.Spec.Signature) == 0 {
		return License{}, refused(ReasonUnreadable, "it is not a license file")
	}
	var p payload
	if err := strict(f.Spec.Payload, &p); err != nil {
		return License{}, refused(ReasonUnreadable, "its license cannot be read")
	}
	key, err := t.key(p.KeyID)
	if err != nil {
		return License{}, err
	}
	if !ed25519.Verify(key, f.Spec.Payload, f.Spec.Signature) {
		return License{}, refused(ReasonSignature, fmt.Sprintf("its signature does not match what it holds (key %s)", p.KeyID))
	}
	if f.Metadata.ID != p.ID {
		return License{}, refused(ReasonIDMismatch, fmt.Sprintf("its id %q is not the signed id %q", f.Metadata.ID, p.ID))
	}
	issued, ierr := time.Parse(day, p.Issued)
	until, uerr := time.Parse(day, p.Until)
	if ierr != nil || uerr != nil {
		return License{}, refused(ReasonUnreadable, "its days are not dates")
	}
	l := License{ID: p.ID, Kind: p.Kind, For: p.For, Products: p.Products, Issued: issued, Until: until, KeyID: p.KeyID}
	if err := l.check(); err != nil {
		return License{}, refused(ReasonUnreadable, err.Error())
	}
	return l, nil
}

// key is the public key id names for this build, or why the license is refused: a key of the other build (ADR-L5) or one this build does not trust (ADR-L6).
func (t Trust) key(id string) (ed25519.PublicKey, error) {
	this, other, build := t.Dev, t.Prod, "a dev build"
	if t.Release {
		this, other, build = t.Prod, t.Dev, "a release"
	}
	if k, ok := this[id]; ok {
		return k, nil
	}
	if _, ok := other[id]; ok {
		return nil, refused(ReasonOtherBuild, fmt.Sprintf("it is signed with the key %s of the other build, which %s does not take", id, build))
	}
	return nil, refused(ReasonUntrustedKey, fmt.Sprintf("it is signed with the key %q, which this build does not trust", id))
}

// check refuses a license no app takes: no id or one with a space, an unknown kind, nobody named, no key, a last day before its first, a trial that names features, or a license that names none.
func (l License) check() error {
	switch {
	case l.Kind != Org && l.Kind != Product && l.Kind != Trial:
		return fmt.Errorf("licenses: the kind %q is not %s, %s or %s", l.Kind, Org, Product, Trial)
	case l.ID == "" || strings.ContainsFunc(l.ID, unicode.IsSpace):
		return fmt.Errorf("licenses: its id %q is empty or holds a space", l.ID)
	case strings.TrimSpace(l.For) == "":
		return fmt.Errorf("licenses: it names nobody it is for")
	case l.KeyID == "":
		return fmt.Errorf("licenses: it names no key")
	case date(l.Until).Before(date(l.Issued)):
		return fmt.Errorf("licenses: its last day %s is before its first %s", l.Until.Format(day), l.Issued.Format(day))
	case l.Kind == Trial && len(l.Products) > 0:
		return fmt.Errorf("licenses: a trial key names no products; it opens every enterprise feature")
	case l.Kind == Product && len(l.Products) != 1:
		return fmt.Errorf("licenses: a product license names one product, not %d", len(l.Products))
	case l.Kind == Org && len(l.Products) == 0:
		return fmt.Errorf("licenses: an org license names its products")
	}
	for product, ids := range l.Products {
		if product == "" || len(ids) == 0 || slices.Contains(ids, "") {
			return fmt.Errorf("licenses: the product %q names no feature, or an empty one", product)
		}
	}
	return nil
}

// strict decodes raw into v, refusing a field v does not have and anything after it.
func strict(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.More() {
		return fmt.Errorf("more after the license")
	}
	return nil
}

func refused(reason, detail string) *problem.Problem {
	return problem.Fail(problem.TypeRefused, reason, detail)
}

// daysBetween is how many days from a to b, both taken as UTC dates.
func daysBetween(a, b time.Time) int {
	return int(date(b).Sub(date(a)).Hours() / 24)
}

func date(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
