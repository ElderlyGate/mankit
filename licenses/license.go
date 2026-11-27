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

	"github.com/tugbayatilla/mankit/v4/problem"
)

// Kind is what a license covers (ADR-L8, L9); contracts mankit/v3 license-payload says what each means.
type Kind string

// The kinds of license.
const (
	KindLicense Kind = "license"
	Trial       Kind = "trial"
)

// The kinds of an old file (APIVersion), read for one release and taken as KindLicense or Trial.
const (
	// Deprecated: an org license is read as a KindLicense; mankit signs KindLicense only.
	Org Kind = "org"
	// Deprecated: a product license is read as a KindLicense; mankit signs KindLicense only.
	Product Kind = "product"
	// Deprecated: a release-trial is read as a Trial; mankit signs Trial only.
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

// License is what a license file says, all of it signed but Started; contracts mankit/v3 license-payload says what each field means, license-file the file around it.
type License struct {
	ID        string
	Kind      Kind
	For       string
	Products  map[string][]string
	Lines     map[string][]int
	Issued    time.Time
	Until     time.Time // zero when the payload has none
	TrialDays int       // the payload's days: a trial's alone
	KeyID     string
	Started   *Started // unsigned, beside the payload; nil when the file has none
	old       Kind     // the kind an old file named, which keeps its old place; empty for a file of FileAPIVersion
}

// Started is the trial a trial's file started, which the app writes beside the signed payload (contracts mankit/v3 license-file spec.started).
type Started struct {
	Day      time.Time
	Binary   string
	FileTime time.Time
}

// Days is how many days the license runs, its first and last day included: the length of each trial a trial starts (ADR-L9), else issued to until, and 0 for a license that never ends.
func (l License) Days() int {
	switch {
	case l.Kind == Trial:
		return l.TrialDays
	case l.Until.IsZero():
		return 0
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

// file is a license file on disk (contracts mankit/v3 license-file, and mankit/v1's for an old one).
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

// The license file's form and kind (ADR-S15, contracts apiversion/v1 forms.yaml): Sign writes FileAPIVersion, Verify reads it and, for one release, APIVersion.
const (
	FileAPIVersion = "elderlygate/mankit/license/v2"
	FileKind       = "License"
	// Deprecated: the old form, read for one release (ADR-L8); Sign writes FileAPIVersion.
	APIVersion = "elderlygate/mankit/license/v1"
)

// payload is what is signed.
type payload struct {
	ID       string              `json:"id"`
	Kind     Kind                `json:"kind"`
	For      string              `json:"for"`
	Products map[string][]string `json:"products,omitempty"`
	Lines    map[string][]int    `json:"lines,omitempty"`
	Issued   string              `json:"issued"`
	Until    string              `json:"until,omitempty"`
	Days     int                 `json:"days,omitempty"`
	KeyID    string              `json:"keyId"`
}

const day = "2006-01-02"

// Sign is the license file of l in FileAPIVersion, signed by s with the key l.KeyID; a license Check refuses is refused before anything is signed, and so is a Started, which only the app writes.
func Sign(l License, s Signer) ([]byte, error) {
	if err := l.Check(); err != nil {
		return nil, err
	}
	if l.Started != nil {
		return nil, errors.New("licenses: a signed file has no started trial; the app writes it")
	}
	p := payload{ID: l.ID, Kind: l.Kind, For: l.For, Products: l.Products, Lines: l.Lines, Issued: l.Issued.UTC().Format(day), Days: l.TrialDays, KeyID: l.KeyID}
	if !l.Until.IsZero() {
		p.Until = l.Until.UTC().Format(day)
	}
	body, err := json.Marshal(p)
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
	f := file{APIVersion: FileAPIVersion, Kind: FileKind}
	f.Metadata.ID, f.Spec.Payload, f.Spec.Signature = l.ID, body, sig
	out, err := json.MarshalIndent(f, "", "  ")
	return append(out, '\n'), err
}

// Verify is the license raw holds once its signature over the payload's stored bytes checks against a key t trusts for this build; otherwise a Problem refused with the reason why. An old file (APIVersion) is taken as today's kinds, and a person's trial key in one is refused.
func Verify(raw []byte, t Trust) (License, error) {
	if err := t.check(); err != nil {
		return License{}, err
	}
	// The spec and the payload take fields a newer nw-license adds, as one license serves several app versions (ADR-S15, CT-RULE-4).
	var f file
	var head struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			ID string `json:"id"`
		} `json:"metadata"`
		Spec json.RawMessage `json:"spec"`
	}
	if err := decode(raw, &head, true); err != nil || (head.APIVersion != FileAPIVersion && head.APIVersion != APIVersion) || head.Kind != FileKind || decode(head.Spec, &f.Spec, false) != nil || len(f.Spec.Payload) == 0 || len(f.Spec.Signature) == 0 {
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
	var until time.Time
	var uerr error
	if p.Until != "" || head.APIVersion == APIVersion {
		until, uerr = time.Parse(day, p.Until)
	}
	if ierr != nil || uerr != nil {
		return License{}, refused(ReasonUnreadable, "its days are not dates")
	}
	l := License{ID: p.ID, Kind: p.Kind, For: p.For, Products: p.Products, Lines: p.Lines, Issued: issued, Until: until, TrialDays: p.Days, KeyID: p.KeyID}
	if f.Spec.Started != nil {
		if l.Started, err = f.Spec.Started.read(); err != nil {
			return License{}, err
		}
	}
	if head.APIVersion == APIVersion {
		return fromOld(l)
	}
	if err := l.Check(); err != nil {
		return License{}, refused(ReasonUnreadable, err.Error())
	}
	return l, nil
}

// fromOld is the license an old file (APIVersion) holds, checked by that form's rules and taken as today's kind: an org or product license as a KindLicense, a release-trial as a Trial whose offer never ends; a person's trial key no app takes any more (ADR-L8).
func fromOld(l License) (License, error) {
	if l.Kind == Trial {
		return License{}, refused(ReasonUnreadable, "it is a person's trial key of the old license form, which no app takes any more; every release starts its own trial (ADR-L8, L9)")
	}
	if len(l.Lines) > 0 {
		return License{}, refused(ReasonUnreadable, "it names lines, which the old license form has not")
	}
	if err := l.checkOld(); err != nil {
		return License{}, refused(ReasonUnreadable, err.Error())
	}
	l.old = l.Kind
	switch l.Kind {
	case Org, Product:
		l.Kind = KindLicense
	case ReleaseTrial:
		l.Kind, l.Until = Trial, time.Time{}
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

// Check refuses, with ErrNotTaken, a license Sign would not write: anything contracts mankit/v3 license-payload refuses, an old kind, or a license whose lines name other products than its products (ADR-L10); it needs no signer, so a tool checks a license before it reads a key.
func (l License) Check() error {
	switch {
	case l.Kind == Org || l.Kind == Product || l.Kind == ReleaseTrial:
		return fmt.Errorf("%w: the kind %q is of the old license form, which mankit reads for one release and never signs; it is %s or %s now", ErrNotTaken, l.Kind, KindLicense, Trial)
	case l.Kind != KindLicense && l.Kind != Trial:
		return fmt.Errorf("%w: the kind %q is not %s or %s", ErrNotTaken, l.Kind, KindLicense, Trial)
	}
	if err := l.checkCommon(); err != nil {
		return err
	}
	if err := checkLines(l.Lines); err != nil {
		return err
	}
	if l.Kind == Trial {
		return l.checkTrial()
	}
	switch {
	case l.TrialDays != 0:
		return fmt.Errorf("%w: a license has no days; only a trial has", ErrNotTaken)
	case l.Started != nil:
		return fmt.Errorf("%w: a license has no started trial; only a trial's file has", ErrNotTaken)
	case len(l.Products) == 0:
		return fmt.Errorf("%w: a license names its products", ErrNotTaken)
	case l.Until.IsZero() == (len(l.Lines) == 0):
		return fmt.Errorf("%w: a license runs until a day or names the lines it opens, one of the two (ADR-L10)", ErrNotTaken)
	}
	if err := checkFeatures(l.Products); err != nil {
		return err
	}
	for product := range l.Lines {
		if _, ok := l.Products[product]; !ok || len(l.Lines) != len(l.Products) {
			return fmt.Errorf("%w: its lines name other products than its products (ADR-L10)", ErrNotTaken)
		}
	}
	return nil
}

// checkTrial refuses a trial that names products or an until, runs no day, or names other than one line of one product (ADR-L9).
func (l License) checkTrial() error {
	switch {
	case len(l.Products) > 0:
		return fmt.Errorf("%w: a trial names no products; it opens every enterprise feature of its app", ErrNotTaken)
	case !l.Until.IsZero():
		return fmt.Errorf("%w: a trial has no until; its offer does not end", ErrNotTaken)
	case l.TrialDays < 1:
		return fmt.Errorf("%w: a trial runs %d days, not 1 or more", ErrNotTaken, l.TrialDays)
	case len(l.Lines) != 1:
		return fmt.Errorf("%w: a trial names the one line of its one product, not %d products", ErrNotTaken, len(l.Lines))
	}
	for _, lines := range l.Lines {
		if len(lines) != 1 {
			return fmt.Errorf("%w: a trial names the one line it was signed for, not %d", ErrNotTaken, len(lines))
		}
	}
	return nil
}

// checkCommon refuses what no kind takes, in today's form and the old one: no id or one with a space, nobody named, no key, or a last day before its first.
func (l License) checkCommon() error {
	switch {
	case l.ID == "" || strings.ContainsFunc(l.ID, unicode.IsSpace):
		return fmt.Errorf("%w: its id %q is empty or holds a space", ErrNotTaken, l.ID)
	case strings.TrimSpace(l.For) == "":
		return fmt.Errorf("%w: it names nobody it is for", ErrNotTaken)
	case l.KeyID == "":
		return fmt.Errorf("%w: it names no key", ErrNotTaken)
	case !l.Until.IsZero() && date(l.Until).Before(date(l.Issued)):
		return fmt.Errorf("%w: its last day %s is before its first %s", ErrNotTaken, l.Until.Format(day), l.Issued.Format(day))
	}
	return nil
}

// checkFeatures refuses a product without an id, or one that names no feature, an empty one or one twice.
func checkFeatures(products map[string][]string) error {
	for product, ids := range products {
		if product == "" || len(ids) == 0 || slices.Contains(ids, "") || len(slices.Compact(slices.Sorted(slices.Values(ids)))) != len(ids) {
			return fmt.Errorf("%w: the product %q names no feature, an empty one or one twice", ErrNotTaken, product)
		}
	}
	return nil
}

// checkLines refuses a product without an id, or one that names no line, one below 1 or one twice.
func checkLines(lines map[string][]int) error {
	for product, ns := range lines {
		if product == "" || len(ns) == 0 || slices.Min(append([]int{1}, ns...)) < 1 || len(slices.Compact(slices.Sorted(slices.Values(ns)))) != len(ns) {
			return fmt.Errorf("%w: the product %q names no line, one below 1 or one twice", ErrNotTaken, product)
		}
	}
	return nil
}

// checkOld refuses what the old form (APIVersion) did not take; contracts mankit/v1 license-payload and license-file say what that is (CT-RULE-5).
func (l License) checkOld() error {
	switch {
	case l.Kind != Org && l.Kind != Product && l.Kind != ReleaseTrial:
		return fmt.Errorf("%w: the kind %q is not %s, %s or %s", ErrNotTaken, l.Kind, Org, Product, ReleaseTrial)
	case l.Until.IsZero():
		return fmt.Errorf("%w: it has no last day", ErrNotTaken)
	}
	if err := l.checkCommon(); err != nil {
		return err
	}
	switch {
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
	return checkFeatures(l.Products)
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
