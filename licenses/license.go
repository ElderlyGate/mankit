// Package licenses is what unlocks an app's enterprise features: Ed25519-signed license files, checked offline (ADR-L1) against the keys the app's build trusts (ADR-L5, L6); nw-license signs with Sign, every app checks with a Checker.
package licenses

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"go.naywee.com/mankit/v5/problem"
)

// Kind is what a license covers (ADR-L8, L9); contracts mankit/v1 license-show says what each means.
type Kind string

// The kinds of license.
const (
	Org          Kind = "org"
	Product      Kind = "product"
	Trial        Kind = "trial"
	ReleaseTrial Kind = "release-trial"
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

// License is what a license file says, all of it signed but Started; contracts mankit/v1 license-payload says what each field means, license-file the file around it.
type License struct {
	ID        string
	Kind      Kind
	For       string
	Products  map[string][]string
	Issued    time.Time
	Until     time.Time
	TrialDays int // the payload's days: a release-trial's alone
	KeyID     string
	Started   *Started // unsigned, beside the payload; nil when the file has none
}

// Started is the trial a release-trial's file started, which the app writes beside the signed payload (contracts mankit/v1 license-file spec.started).
type Started struct {
	Day      time.Time
	Binary   string
	FileTime time.Time
}

// Days is how many days the license runs, its first and last day included: a trial key's length, or the length of each trial a release-trial starts (ADR-L9).
func (l License) Days() int {
	if l.Kind == ReleaseTrial {
		return l.TrialDays
	}
	return daysBetween(l.Issued, l.Until) + 1
}

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

// check refuses keys that are not Ed25519 public keys, or an id both builds name; its caller says where they came from.
func (t Trust) check() error {
	for id, k := range t.Prod {
		if _, both := t.Dev[id]; both {
			return fmt.Errorf("the key id %s is both a prod and a dev key", id)
		}
		if len(k) != ed25519.PublicKeySize {
			return fmt.Errorf("the prod key %s is not an Ed25519 public key", id)
		}
	}
	for id, k := range t.Dev {
		if len(k) != ed25519.PublicKeySize {
			return fmt.Errorf("the dev key %s is not an Ed25519 public key", id)
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
		Payload   []byte       `json:"payload"`
		Signature []byte       `json:"signature"`
		Started   *startedJSON `json:"started,omitempty"`
	} `json:"spec"`
}

// startedJSON is spec.started as the file holds it.
type startedJSON struct {
	Day      string `json:"day"`
	Binary   string `json:"binary"`
	FileTime string `json:"fileTime"`
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
	Days     int                 `json:"days,omitempty"`
	KeyID    string              `json:"keyId"`
}

const day = "2006-01-02"

// Sign is the license file of l, signed by s with the key l.KeyID; a license Check refuses is refused before anything is signed, and so is a Started, which only the app writes.
func Sign(l License, s Signer) ([]byte, error) {
	if err := l.Check(); err != nil {
		return nil, err
	}
	if l.Started != nil {
		return nil, errors.New("licenses: a signed file has no started trial; the app writes it")
	}
	// A copy, so a release-trial's nil feature list marshals as [], as the schema takes.
	products := make(map[string][]string, len(l.Products))
	for id, features := range l.Products {
		products[id] = append([]string{}, features...)
	}
	body, err := json.Marshal(payload{ID: l.ID, Kind: l.Kind, For: l.For, Products: products, Issued: l.Issued.UTC().Format(day), Until: l.Until.UTC().Format(day), Days: l.TrialDays, KeyID: l.KeyID})
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
	// The spec and the payload take fields a newer nw-license adds, as one org license serves several app versions (ADR-S15, CT-RULE-4).
	var f file
	var head struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			ID string `json:"id"`
		} `json:"metadata"`
		Spec json.RawMessage `json:"spec"`
	}
	if err := decode(raw, &head, true); err != nil || head.APIVersion != APIVersion || head.Kind != FileKind || decode(head.Spec, &f.Spec, false) != nil || len(f.Spec.Payload) == 0 || len(f.Spec.Signature) == 0 {
		return License{}, refused(ReasonUnreadable, "it is not a license file")
	}
	f.Metadata.ID = head.Metadata.ID
	var p payload
	if err := decode(f.Spec.Payload, &p, false); err != nil {
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
	l := License{ID: p.ID, Kind: p.Kind, For: p.For, Products: p.Products, Issued: issued, Until: until, TrialDays: p.Days, KeyID: p.KeyID}
	if f.Spec.Started != nil {
		if l.Started, err = f.Spec.Started.read(); err != nil {
			return License{}, err
		}
	}
	if err := l.Check(); err != nil {
		return License{}, refused(ReasonUnreadable, err.Error())
	}
	return l, nil
}

// read is the started record, or why the file is unreadable: a day, a binary and a file time it must have.
func (s startedJSON) read() (*Started, error) {
	d, derr := time.Parse(day, s.Day)
	ft, ferr := time.Parse(time.RFC3339, s.FileTime)
	if derr != nil || ferr != nil || s.Binary == "" {
		return nil, refused(ReasonUnreadable, "its started trial is not a day, a binary and a file time")
	}
	return &Started{Day: d, Binary: s.Binary, FileTime: ft}, nil
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

// ErrNotTaken is the code of every error Check gives: the license says something no app takes.
var ErrNotTaken = errors.New("licenses: no app takes this license")

// Check refuses a license no app takes, with ErrNotTaken: no id or one with a space, an unknown kind, nobody named, no key, a last day before its first, a trial that names features, a release-trial that names other than one product with no feature ids or runs no day, days or a started trial on another kind, a trial started after a release-trial's last day to start one, or a license that names none; it needs no signer, so a tool checks a license before it reads a key.
func (l License) Check() error {
	switch {
	case l.Kind != Org && l.Kind != Product && l.Kind != Trial && l.Kind != ReleaseTrial:
		return fmt.Errorf("%w: the kind %q is not %s, %s, %s or %s", ErrNotTaken, l.Kind, Org, Product, Trial, ReleaseTrial)
	case l.ID == "" || strings.ContainsFunc(l.ID, unicode.IsSpace):
		return fmt.Errorf("%w: its id %q is empty or holds a space", ErrNotTaken, l.ID)
	case strings.TrimSpace(l.For) == "":
		return fmt.Errorf("%w: it names nobody it is for", ErrNotTaken)
	case l.KeyID == "":
		return fmt.Errorf("%w: it names no key", ErrNotTaken)
	case date(l.Until).Before(date(l.Issued)):
		return fmt.Errorf("%w: its last day %s is before its first %s", ErrNotTaken, l.Until.Format(day), l.Issued.Format(day))
	case l.Kind == Trial && len(l.Products) > 0:
		return fmt.Errorf("%w: a trial key names no products; it opens every enterprise feature", ErrNotTaken)
	case l.Kind == Product && len(l.Products) != 1:
		return fmt.Errorf("%w: a product license names one product, not %d", ErrNotTaken, len(l.Products))
	case l.Kind == Org && len(l.Products) == 0:
		return fmt.Errorf("%w: an org license names its products", ErrNotTaken)
	case l.Kind != ReleaseTrial && l.TrialDays != 0:
		return fmt.Errorf("%w: a %s license has no days; only a release-trial has", ErrNotTaken, l.Kind)
	case l.Kind != ReleaseTrial && l.Started != nil:
		return fmt.Errorf("%w: a %s license has no started trial; only a release-trial's file has", ErrNotTaken, l.Kind)
	case l.Kind == ReleaseTrial:
		return l.checkReleaseTrial()
	}
	for product, ids := range l.Products {
		if product == "" || len(ids) == 0 || slices.Contains(ids, "") {
			return fmt.Errorf("%w: the product %q names no feature, or an empty one", ErrNotTaken, product)
		}
	}
	return nil
}

// checkReleaseTrial refuses a release-trial that names other than its one product, names a feature id, runs no day, or started its trial after its last day to start one (ADR-L9).
func (l License) checkReleaseTrial() error {
	if len(l.Products) != 1 {
		return fmt.Errorf("%w: a release-trial names one product, its app, not %d", ErrNotTaken, len(l.Products))
	}
	for product, ids := range l.Products {
		if product == "" || len(ids) > 0 {
			return fmt.Errorf("%w: a release-trial names its app's product id and no feature ids; it opens every enterprise feature of that app", ErrNotTaken)
		}
	}
	switch {
	case l.TrialDays < 1:
		return fmt.Errorf("%w: a release-trial runs %d days, not 1 or more", ErrNotTaken, l.TrialDays)
	case l.Started != nil && date(l.Started.Day).After(date(l.Until)):
		return fmt.Errorf("%w: its trial started on %s, after %s, its last day to start one", ErrNotTaken, l.Started.Day.Format(day), l.Until.Format(day))
	}
	return nil
}

// decode decodes raw into v, refusing anything after it, and a field v does not have when closed.
func decode(raw []byte, v any, closed bool) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	if closed {
		d.DisallowUnknownFields()
	}
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
