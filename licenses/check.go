package licenses

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/store"
)

// name is the license file's name, in the org's config folder and in each app's (ADR-L8).
const name = "license"

// most is the largest license file read; a license is a few hundred bytes.
const most = 64 << 10

// Config is what a Checker needs from its app: the product ID licenses name, its folder under the org's config folder when the two differ, and the keys its build trusts.
type Config struct {
	ID     string
	Folder string           // empty is ID
	Trust  Trust            // the app's own build says Release (ADR-L5)
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
		return nil, err
	}
	if c.Folder == "" {
		c.Folder = c.ID
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Checker{c: c}, nil
}

// Taken is a license the app read and took, where it was read from, and how long it runs from today.
type Taken struct {
	License
	File     string
	DaysLeft int  // 0 once it has ended
	Ended    bool // its features are locked again (ADR-L3)
}

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
		path, err := c.path(org)
		if err != nil {
			return Found{}, err
		}
		raw, err := readFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		var l License
		if err == nil {
			l, err = c.take(raw, org)
		}
		var p *problem.Problem
		switch {
		case errors.As(err, &p):
			f.Refused = append(f.Refused, Refused{File: path, Reason: p.Reason, Detail: p.Detail})
		case err != nil:
			f.Refused = append(f.Refused, Refused{File: path, Reason: ReasonUnreadable, Detail: err.Error()})
		default:
			f.Taken = append(f.Taken, c.taken(l, path))
		}
	}
	return f, nil
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

// scope is the store scope the license is kept in: the app's own config folder, or the org's, made here until store.Own opens it (stopgap, card 20261119-17b4).
func (c *Checker) scope(org bool) (*store.Scope, error) {
	app := store.App{ID: c.c.Folder}
	if !org {
		return store.Config(app)
	}
	dir, err := home.Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return store.Served(app, dir, func(store.Key) string { return filepath.Join(dir, ".queue") })
}

// take verifies raw and refuses a license that does not belong where it is: an org license outside the org's place, a product license or trial key in it, or another product's license.
func (c *Checker) take(raw []byte, org bool) (License, error) {
	l, err := Verify(raw, c.c.Trust)
	if err != nil {
		return License{}, err
	}
	switch {
	case org && l.Kind != Org:
		return License{}, refused(ReasonWrongType, fmt.Sprintf("it is a %s license, which an app keeps as its own, not as the org's", l.Kind))
	case !org && l.Kind == Org:
		return License{}, refused(ReasonWrongType, "it is an org license, which is kept as the org's (license add --org)")
	case l.Kind == Product && l.Products[c.c.ID] == nil:
		return License{}, refused(ReasonWrongType, fmt.Sprintf("it is a product license for another product, not %s", c.c.ID))
	}
	return l, nil
}

func (c *Checker) taken(l License, path string) Taken {
	left := max(daysBetween(c.c.Now(), l.Until)+1, 0)
	return Taken{License: l, File: path, DaysLeft: left, Ended: left == 0}
}

// path is the org's license file or the app's own (ADR-L8).
func (c *Checker) path(org bool) (string, error) {
	if org {
		dir, err := home.Dir()
		return filepath.Join(dir, name), err
	}
	return home.File(c.c.Folder, name)
}

// readFile reads a regular file of at most most bytes, never waiting on a pipe or following a link.
func readFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, most+1))
	if err == nil && len(raw) > most {
		err = fmt.Errorf("%s is larger than a license", path)
	}
	return raw, err
}

// From is where a feature is unlocked from (ADR-L8, L9); contracts mankit/v1 license-show says what each means.
type From string

// The places a feature is unlocked from.
const (
	FromOrg     From = "org"
	FromProduct From = "product"
	FromBoth    From = "both"
	FromTrial   From = "trial"
	FromNone    From = "none"
)

// From is where f is unlocked from now, and the last day it stays open: the latest of the licenses that name it, else a running trial key's, else none. A free feature is from none: it needs no license (ADR-L2).
func (f Found) From(ft features.Feature) (From, time.Time) {
	if !ft.Enterprise() {
		return FromNone, time.Time{}
	}
	var org, product, trial bool
	var until, trialUntil time.Time
	for _, t := range f.Taken {
		switch {
		case t.Ended:
		case t.Kind == Trial:
			trial, trialUntil = true, t.Until
		case t.Names(f.product, ft.ID):
			org, product = org || t.Kind == Org, product || t.Kind == Product
			if t.Until.After(until) {
				until = t.Until
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
