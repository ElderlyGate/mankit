package licenses

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/tugbayatilla/mankit/v4/build"
	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/store"
)

// name is the license file's name: the license in the org's own folder (home.OrgDir), a trial in its app's (ADR-L8).
const name = "license"

// most is the largest license file read; a license is a few hundred bytes.
const most = 64 << 10

// Config is what a Checker needs from its app: the product ID licenses name, the name and line its license show sentence says, its folder under the org's config folder when the two differ, and the keys its build trusts.
type Config struct {
	ID     string
	Title  string           // the app's name people read, filled from serve.App.Name() by licenses/cli.Checker, the way apps build their Checker
	Line   int              // the product line this build is, see ADR-L10; 0 is build.Line(), the build stamp, and only a test sets its own
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
	if c.Line == 0 {
		c.Line = build.Line()
	}
	return &Checker{c: c}, nil
}

// Taken is a license the app read and took, where it was read from, and how long it runs from today. A trial runs from its started day; one not started is the key as signed, which opens nothing.
type Taken struct {
	License
	File      string
	LastDay   time.Time // Until, but for a started trial its trial's last day: the started day plus its days, less one; zero when there is none
	DaysLeft  int       // the days to LastDay, 0 once it has ended or when there is no LastDay
	Ended     bool      // its features are locked again (ADR-L3)
	Early     bool      // its first day has not come, or a trial has not started, so it opens nothing yet
	Elsewhere bool      // a license that names neither this app nor, when it names lines, this build's line, so it opens nothing here (ADR-L10)
}

// Running reports whether the license opens its features today: its first day has come, its last has not passed and it names this build.
func (t Taken) Running() bool { return !t.Ended && !t.Early && !t.Elsewhere }

// Refused is a license file the app read but did not take, and why (G-CODE-6): never treated as no license.
type Refused struct {
	File   string
	Reason string
	Detail string
}

// Found is the app's licenses as read at one moment: the license first, then the trial, and the files refused.
type Found struct {
	Taken   []Taken
	Refused []Refused
	product string
}

// Read reads the license in the org's folder and the app's own file, its trial (ADR-L8); a missing file is no license, and one it cannot read or verify is Refused. It fails only when the folders cannot be found.
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

// place is what the org's place or the app's own holds: nothing, a license taken, or a file refused, with the bytes it was read from when it could be read whole.
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
		p.raw = raw
		l, err = c.take(raw, org)
	}
	var pr *problem.Problem
	switch {
	case errors.As(err, &pr):
		p.refused = &Refused{File: path, Reason: pr.Reason, Detail: pr.Detail}
	case err != nil:
		p.refused = &Refused{File: path, Reason: ReasonUnreadable, Detail: err.Error()}
	default:
		p.license = &l
	}
	return p, nil
}

// Check is the license raw holds as Add would keep it, or the Problem why not: what add shows before it asks. Only a license of FileAPIVersion is kept, in the one license file every app reads (ADR-L8), so org is not read.
func (c *Checker) Check(raw []byte, org bool) (Taken, error) {
	path, err := c.path(true)
	if err != nil {
		return Taken{}, err
	}
	l, err := Verify(raw, c.c.Trust)
	if err != nil {
		return Taken{}, err
	}
	if l.old != "" {
		return Taken{}, refused(ReasonUnreadable, fmt.Sprintf("it is a license file of the old form %s, which mankit reads for one release but never keeps; a new file is %s", APIVersion, FileAPIVersion))
	}
	if err := c.belongs(l, true); err != nil {
		return Taken{}, err
	}
	return c.taken(l, path), nil
}

// Add keeps the license raw holds in the org's folder, the one license file every app reads, written whole through the store (G-CODE-5), once it checks; it replaces the one there, and org is not read.
func (c *Checker) Add(ctx context.Context, raw []byte, org bool) (Taken, error) {
	t, err := c.Check(raw, true)
	if err != nil {
		return Taken{}, err
	}
	s, err := c.scope(true)
	if err != nil {
		return Taken{}, err
	}
	defer s.Close()
	err = s.Change(ctx, name, "license add", func(tx store.Tx) error { return tx.Put(name, raw, store.Anything()) })
	return t, err
}

// Removal is one file license remove takes away, and the sum it held when Removing read it, so Remove takes away only what was shown.
type Removal struct {
	File string
	Sum  string
}

// Removing is the files Remove takes away: the one in the org's folder, taken or refused, and in the app's own folder an old product license or a file refused, which would keep StartTrial from writing the trial; it never offers a trial there, taken or refused. A refused trial blocks StartTrial until it is deleted by hand, which gives a fresh trial as ADR-L9 accepts. With none it is a Problem not_found.
func (c *Checker) Removing() ([]Removal, error) {
	org, err := c.at(true)
	if err != nil {
		return nil, err
	}
	own, err := c.at(false)
	if err != nil {
		return nil, err
	}
	var out []Removal
	for i, p := range []place{org, own} {
		inOrg := i == 0
		if !inOrg && holdsTrial(p.raw) {
			continue
		}
		if p.refused == nil && (p.license == nil || (!inOrg && p.license.old != Product)) {
			continue
		}
		sum, err := c.sum(p, inOrg)
		if err != nil {
			return nil, err
		}
		out = append(out, Removal{File: p.path, Sum: sum})
	}
	if len(out) == 0 {
		return nil, problem.Fail(problem.TypeNotFound, "", "there is no license to remove")
	}
	return out, nil
}

// sum is the sum of the file p read; a file it could not read whole is summed as the store reads it.
func (c *Checker) sum(p place, org bool) (string, error) {
	if p.raw != nil {
		return store.SumOf(p.raw), nil
	}
	s, err := c.scope(org)
	if err != nil {
		return "", err
	}
	defer s.Close()
	it, _, err := s.Get(name)
	return it.Sum, err
}

// Remove takes away the files shown, as Removing gave them, each through the store in the scope's own turn and only while it holds the sum it was shown with; a file changed since is a Problem refused, changed. It never removes the app's trial, so a removed license never starts a fresh one. Every file is checked before any is removed, and the app's own goes first, so a refusal leaves them all; a file changed by someone else between the check and its turn is still refused, after the ones before it went (best effort, not one transaction).
func (c *Checker) Remove(ctx context.Context, shown []Removal) error {
	orgPath, err := c.path(true)
	if err != nil {
		return err
	}
	ownPath, err := c.path(false)
	if err != nil {
		return err
	}
	order := slices.Clone(shown)
	slices.SortFunc(order, func(a, b Removal) int { return cmp.Compare(a.File, b.File) })
	order = slices.CompactFunc(order, func(a, b Removal) bool { return a.File == b.File })
	for _, r := range order {
		if r.File != orgPath && r.File != ownPath {
			return fmt.Errorf("licenses: %s is not a license file of %s", r.File, c.c.ID)
		}
	}
	slices.SortStableFunc(order, func(a, b Removal) int {
		switch {
		case (a.File == ownPath) == (b.File == ownPath):
			return 0
		case a.File == ownPath:
			return -1
		}
		return 1
	})
	for _, r := range order {
		if err := c.holds(r, r.File == orgPath); err != nil {
			return err
		}
	}
	for _, r := range order {
		org := r.File == orgPath
		s, err := c.scope(org)
		if err != nil {
			return err
		}
		err = s.Change(ctx, "", "license remove", func(tx store.Tx) error {
			if it, ok, err := tx.Get(name); err != nil {
				return err
			} else if ok && !org && holdsTrial(it.Body) {
				return refusedTrial(r.File)
			}
			return tx.Remove(name, store.Holds(r.Sum))
		})
		s.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// holds checks, before anything is removed, that the file r shows is still as shown and, in the app's folder, no trial.
func (c *Checker) holds(r Removal, org bool) error {
	s, err := c.scope(org)
	if err != nil {
		return err
	}
	defer s.Close()
	it, ok, err := s.Get(name)
	switch {
	case err != nil:
		return err
	case ok && !org && holdsTrial(it.Body):
		return refusedTrial(r.File)
	case !ok || it.Sum != r.Sum:
		return problem.FailAt(problem.TypeRefused, store.ReasonChanged, r.File, fmt.Sprintf("%s changed since it was shown, so nothing is removed", r.File))
	}
	return nil
}

// refusedTrial is the Problem for the app's own file when it is a trial, which stays (ADR-L9).
func refusedTrial(file string) error {
	return problem.FailAt(problem.TypeRefused, "", file, fmt.Sprintf("%s is a trial, which is never removed, so a removed license never starts a fresh one", file))
}

// holdsTrial reports whether the license file raw carries a payload of kind trial, or an old release-trial, signed or not.
func holdsTrial(raw []byte) bool {
	var f file
	var p payload
	if json.Unmarshal(raw, &f) != nil || json.Unmarshal(f.Spec.Payload, &p) != nil {
		return false
	}
	return p.Kind == Trial || p.Kind == ReleaseTrial
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

// take verifies raw and refuses a license that does not belong where it is (belongs).
func (c *Checker) take(raw []byte, org bool) (License, error) {
	l, err := Verify(raw, c.c.Trust)
	if err != nil {
		return License{}, err
	}
	return l, c.belongs(l, org)
}

// belongs refuses a license kept in the wrong place: a license outside the org's folder, a trial in it, or another product's trial; an old file keeps its old places, an org license in the org's folder and a product license as its own app's (ADR-L8).
func (c *Checker) belongs(l License, org bool) error {
	switch {
	case l.old == Product && org:
		return refused(ReasonWrongPlace, "it is an old product license, which its app keeps as its own, not in the org's folder")
	case l.old == Product && !l.names(c.c.ID):
		return refused(ReasonWrongPlace, fmt.Sprintf("it is an old product license for another product, not %s", c.c.ID))
	case l.old == Product:
	case l.Kind == KindLicense && !org:
		return refused(ReasonWrongPlace, "it is a license, which is kept in the org's folder and read by every app (license add)")
	case l.Kind == Trial && org:
		return refused(ReasonWrongPlace, "it is a trial, which its app carries built in and keeps as its own")
	case l.Kind == Trial && !l.names(c.c.ID):
		return refused(ReasonWrongPlace, fmt.Sprintf("it is a trial for another product, not %s", c.c.ID))
	}
	return nil
}

// names reports whether the license names the product id, in its products or its lines.
func (l License) names(product string) bool {
	_, ok := l.Products[product]
	_, line := l.Lines[product]
	return ok || line
}

// taken is l as it runs today: from its issued day to its until, or on with none; a started trial from its started day for its days but never before its issued day, and one not started opens nothing. A license opens nothing here unless it names this app and, when it names lines, this build's line (ADR-L10).
func (c *Checker) taken(l License, path string) Taken {
	t := Taken{License: l, File: path, LastDay: l.Until}
	first, today := l.Issued, c.c.Now()
	if l.Kind == Trial {
		t.LastDay = time.Time{}
		if l.Started != nil {
			first = l.Started.Day
			t.LastDay = date(first).AddDate(0, 0, l.TrialDays-1)
		}
	}
	if !t.LastDay.IsZero() {
		t.DaysLeft = max(daysBetween(today, t.LastDay)+1, 0)
		t.Ended = t.DaysLeft == 0
	}
	t.Early = !t.Ended && ((l.Kind == Trial && l.Started == nil) || daysBetween(first, today) < 0 || daysBetween(l.Issued, today) < 0)
	t.Elsewhere = l.Kind == KindLicense && (!l.names(c.c.ID) || (len(l.Lines) > 0 && !slices.Contains(l.Lines[c.c.ID], c.c.Line)))
	return t
}

// path is the license file in home.OrgDir, or the app's own, its trial (ADR-L5, L8).
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

// From is where a feature is unlocked from (ADR-L8, L9); contracts mankit/v3 license-show says what each means.
type From string

// The places a feature is unlocked from.
const (
	FromLicense From = "license"
	FromTrial   From = "trial"
	FromNone    From = "none"
	// Deprecated: an old org license unlocks from FromLicense.
	FromOrg From = "org"
	// Deprecated: an old product license unlocks from FromLicense.
	FromProduct From = "product"
	// Deprecated: an org and a product license unlock from FromLicense.
	FromBoth From = "both"
)

// From is where f is unlocked from now, and the last day it stays open: the running licenses that name it, until the latest of their last days and zero when one never ends, else the running trial, until its last day, else none. A free feature is from none: it needs no license (ADR-L2).
func (f Found) From(ft features.Feature) (From, time.Time) {
	if !ft.Enterprise() {
		return FromNone, time.Time{}
	}
	named, never := false, false
	var until time.Time
	for _, t := range f.Taken {
		if t.Kind != KindLicense || !t.Running() || !t.Names(f.product, ft.ID) {
			continue
		}
		named, never = true, never || t.LastDay.IsZero()
		if t.LastDay.After(until) {
			until = t.LastDay
		}
	}
	switch v := f.verdict(); {
	case named && never:
		return FromLicense, time.Time{}
	case named:
		return FromLicense, until
	case v.trial != nil:
		return FromTrial, v.trial.LastDay
	}
	return FromNone, time.Time{}
}

// Has reports whether f may run as far as licenses go: a free feature always, an enterprise one while a running license or trial opens it.
func (f Found) Has(ft features.Feature) bool {
	from, _ := f.From(ft)
	return !ft.Enterprise() || from != FromNone
}

// Closer closes every feature of all, the features the app's list holds, that no running license or trial opens; the list keeps it from a free one (ADR-L2, L3, L9).
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
