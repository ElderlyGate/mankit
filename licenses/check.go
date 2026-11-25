package licenses

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"go.naywee.com/mankit/v5/features"
	"go.naywee.com/mankit/v5/home"
	"go.naywee.com/mankit/v5/problem"
	"go.naywee.com/mankit/v5/store"
)

// name is the license file's name, in the org's own folder (home.OrgDir) and in each app's (ADR-L8).
const name = "license"

// most is the largest license file read; a license is a few hundred bytes.
const most = 64 << 10

// Config is what a Checker needs from its app: the product ID licenses name, the name its license show sentence says, its folder under the org's config folder when the two differ, and the keys its build trusts.
type Config struct {
	ID     string
	Title  string           // the app's name people read, filled from serve.App.Name() by licenses/cli.Checker, the way apps build their Checker
	Folder string           // empty is ID
	Trust  Trust            // Trusted() in every app, as a hand-built one skips ADR-L5; only a test hands in its own
	Now    func() time.Time // nil is time.Now
}

// Checker reads the app's licenses and answers what they open; it reads the files anew on every call, so a license added or ended counts at once.
type Checker struct{ c Config }

// New is the Checker of c; it refuses an app without an ID or keys that are not Ed25519 public keys.
func New(c Config) (*Checker, error) {
	if c.ID == "" {
		return nil, errors.New("licenses: the app has no product ID")
	}
	if err := c.Trust.check(); err != nil {
		return nil, fmt.Errorf("licenses: %w", err)
	}
	if c.Folder == "" {
		c.Folder = c.ID
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Checker{c: c}, nil
}

// Taken is a license the app read and took, where it was read from, and how long it runs from today. A release-trial runs from its started day; one not started is the key as signed, which opens nothing.
type Taken struct {
	License
	File     string
	LastDay  time.Time // Until, but for a started release-trial its trial's last day: the started day plus its days, less one
	DaysLeft int       // 0 once it has ended
	Ended    bool      // its features are locked again (ADR-L3)
	Early    bool      // its first day has not come, or a release-trial has not started, so it opens nothing yet
}

// Running reports whether the license opens its features today: its first day has come and its last has not passed.
func (t Taken) Running() bool { return !t.Ended && !t.Early }

// Refused is a license file the app read but did not take, and why (G-CODE-6): never treated as no license.
type Refused struct {
	File   string
	Reason string
	Detail string
}

// Found is the app's licenses as read at one moment: the org license first, then the product license or trial key, and the files refused.
type Found struct {
	Taken   []Taken
	Refused []Refused
	product string
}

// Read reads the org license and the app's own (ADR-L8); a missing file is no license, and one it cannot read or verify is Refused. It fails only when the folders cannot be found.
func (c *Checker) Read() (Found, error) {
	f := Found{product: c.c.ID}
	for _, org := range []bool{true, false} {
		p, err := c.at(org)
		if err != nil {
			return Found{}, err
		}
		switch {
		case p.refused != nil:
			f.Refused = append(f.Refused, *p.refused)
		case p.license != nil:
			f.Taken = append(f.Taken, c.taken(*p.license, p.path))
		}
	}
	return f, nil
}

// place is what the org's place or the app's own holds: nothing, a license taken with the bytes it was read from, or a file refused.
type place struct {
	path    string
	raw     []byte
	license *License
	refused *Refused
}

// at reads the org's place or the app's own; it fails only when the folder cannot be found.
func (c *Checker) at(org bool) (place, error) {
	path, err := c.path(org)
	if err != nil {
		return place{}, err
	}
	p := place{path: path}
	raw, err := readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	var l License
	if err == nil {
		l, err = c.take(raw, org)
	}
	var pr *problem.Problem
	switch {
	case errors.As(err, &pr):
		p.refused = &Refused{File: path, Reason: pr.Reason, Detail: pr.Detail}
	case err != nil:
		p.refused = &Refused{File: path, Reason: ReasonUnreadable, Detail: err.Error()}
	default:
		p.raw, p.license = raw, &l
	}
	return p, nil
}

// Check is the license raw holds as the app would take it at the org's place or its own, or the Problem why not: what add shows before it asks.
func (c *Checker) Check(raw []byte, org bool) (Taken, error) {
	path, err := c.path(org)
	if err != nil {
		return Taken{}, err
	}
	l, err := c.take(raw, org)
	if err != nil {
		return Taken{}, err
	}
	return c.taken(l, path), nil
}

// Add keeps the license raw holds as the org's, read by every app, or as this app's own, written whole through the store (G-CODE-5), once it checks; it replaces the one there.
func (c *Checker) Add(ctx context.Context, raw []byte, org bool) (Taken, error) {
	t, err := c.Check(raw, org)
	if err != nil {
		return Taken{}, err
	}
	s, err := c.scope(org)
	if err != nil {
		return Taken{}, err
	}
	defer s.Close()
	err = s.Change(ctx, name, "license add", func(tx store.Tx) error { return tx.Put(name, raw, store.Anything()) })
	return t, err
}

// scope is the store scope the license is kept in: the app's own config folder, or the org's (home.OrgDir), opened with store.Own as a folder private to this user.
func (c *Checker) scope(org bool) (*store.Scope, error) {
	app := store.App{ID: c.c.Folder}
	if !org {
		return store.Config(app)
	}
	dir, err := home.OrgDir()
	if err != nil {
		return nil, err
	}
	return store.Own(app, dir)
}

// take verifies raw and refuses a license that does not belong where it is: an org license outside the org's place, a product license or trial key in it, or another product's license or release-trial.
func (c *Checker) take(raw []byte, org bool) (License, error) {
	l, err := Verify(raw, c.c.Trust)
	if err != nil {
		return License{}, err
	}
	switch {
	case org && l.Kind != Org:
		return License{}, refused(ReasonWrongPlace, fmt.Sprintf("it is a %s license, which an app keeps as its own, not as the org's", l.Kind))
	case !org && l.Kind == Org:
		return License{}, refused(ReasonWrongPlace, "it is an org license, which is kept as the org's (license add --org)")
	case (l.Kind == Product || l.Kind == ReleaseTrial) && !l.names(c.c.ID):
		return License{}, refused(ReasonWrongPlace, fmt.Sprintf("it is a %s license for another product, not %s", l.Kind, c.c.ID))
	}
	return l, nil
}

// names reports whether the license names the product id, with feature ids or, as a release-trial, without.
func (l License) names(product string) bool {
	_, ok := l.Products[product]
	return ok
}

// taken is l as it runs today: from its issued day to its until, a started release-trial from its started day for its days but never before its issued day, and one not started opens nothing and ends once its last day to start one has passed.
func (c *Checker) taken(l License, path string) Taken {
	first, last, waiting := l.Issued, l.Until, false
	if l.Kind == ReleaseTrial {
		if l.Started == nil {
			waiting = true
		} else {
			first = l.Started.Day
			last = date(first).AddDate(0, 0, l.TrialDays-1)
		}
	}
	left := max(daysBetween(c.c.Now(), last)+1, 0)
	early := waiting || daysBetween(first, c.c.Now()) < 0 || daysBetween(l.Issued, c.c.Now()) < 0
	return Taken{License: l, File: path, LastDay: last, DaysLeft: left, Ended: left == 0, Early: early && left > 0}
}

// path is the org's license file in home.OrgDir or the app's own (ADR-L5, L8).
func (c *Checker) path(org bool) (string, error) {
	if org {
		dir, err := home.OrgDir()
		return filepath.Join(dir, name), err
	}
	return home.File(c.c.Folder, name)
}

// readFile reads a regular file of at most most bytes, through a link as fleet's secret store puts them (ADR-L8), never waiting on a pipe.
func readFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	raw, err := io.ReadAll(io.LimitReader(f, most+1))
	if err == nil && len(raw) > most {
		err = fmt.Errorf("%s is larger than a license", path)
	}
	return raw, err
}

// From is where a feature is unlocked from (ADR-L8, L9); contracts mankit/v2 license-show says what each means.
type From string

// The places a feature is unlocked from.
const (
	FromOrg     From = "org"
	FromProduct From = "product"
	FromBoth    From = "both"
	FromTrial   From = "trial"
	FromNone    From = "none"
)

// From is where f is unlocked from now, and the last day it stays open: the latest of the licenses that name it, else a running trial key's or this app's release-trial's, else none. A free feature is from none: it needs no license (ADR-L2).
func (f Found) From(ft features.Feature) (From, time.Time) {
	if !ft.Enterprise() {
		return FromNone, time.Time{}
	}
	var org, product, trial bool
	var until, trialUntil time.Time
	for _, t := range f.Taken {
		switch {
		case !t.Running():
		case t.Kind == Trial || (t.Kind == ReleaseTrial && t.names(f.product)):
			trial, trialUntil = true, t.LastDay
		case t.Names(f.product, ft.ID):
			org, product = org || t.Kind == Org, product || t.Kind == Product
			if t.LastDay.After(until) {
				until = t.LastDay
			}
		}
	}
	switch {
	case org && product:
		return FromBoth, until
	case org:
		return FromOrg, until
	case product:
		return FromProduct, until
	case trial:
		return FromTrial, trialUntil
	}
	return FromNone, time.Time{}
}

// Has reports whether f may run as far as licenses go: a free feature always, an enterprise one while a running license or trial key opens it.
func (f Found) Has(ft features.Feature) bool {
	from, _ := f.From(ft)
	return !ft.Enterprise() || from != FromNone
}

// Closer closes every feature of all, the features the app's list holds, that no running license or trial key opens; the list keeps it from a free one (ADR-L2, L3, L9).
func (c *Checker) Closer(all []features.Feature) features.Closer {
	return features.Closer{By: features.License, Closed: func() ([]string, error) {
		f, err := c.Read()
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, ft := range all {
			if !f.Has(ft) {
				ids = append(ids, ft.ID)
			}
		}
		return ids, nil
	}}
}

// Release is whether the app's build is a release (ADR-L5).
func (c *Checker) Release() bool { return c.c.Trust.Release }
