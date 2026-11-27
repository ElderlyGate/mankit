package licenses

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/tugbayatilla/mankit/v4/build"
	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/home"
	"github.com/tugbayatilla/mankit/v4/problem"
	"github.com/tugbayatilla/mankit/v4/store"
)

// The files a Checker reads and writes, in the org's folder (home.OrgDir) and in its app's (home.Product); the folder layout is the mankit README's (ADR-L8, L9).
const (
	folder = "licenses"       // the licenses folder, in the org's folder and in each app's
	ext    = ".license"       // a license file's name is its id and this
	old    = "license"        // the one file of the old places, read for one release
	choice = "license-active" // in the app's folder: the id of the license license use or add chose
	note   = "trial"          // in the app's folder: the trial note (contracts mankit/v3 trial-started)
)

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

// Checker reads the app's licenses and its trial and answers what they open; it reads the files anew on every call, so a license added, chosen or ended counts at once.
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

// Place is where a license file sits (ADR-L8); contracts mankit/v3 license-show says what each means.
type Place string

// The places of a license file.
const (
	PlaceOrg Place = "org"
	PlaceApp Place = "app"
)

// Taken is a license the app read and took, where it was read from, how long it runs from today, and whether it is the app's active one.
type Taken struct {
	License
	File      string
	Place     Place
	Active    bool      // the app's active license (ADR-L8): the one chosen, else the newest
	LastDay   time.Time // Until; zero when the license never ends
	DaysLeft  int       // the days to LastDay, 0 once it has ended or when there is no LastDay
	Ended     bool      // its features are locked again (ADR-L3)
	Early     bool      // its first day has not come, so it opens nothing yet
	Elsewhere bool      // it names lines but not this build's, so it opens nothing here (ADR-L10)
}

// Running reports whether the license opens its features today: its first day has come, its last has not passed and it names this build.
func (t Taken) Running() bool { return !t.Ended && !t.Early && !t.Elsewhere }

// Refused is a license file the app read but did not take, and why (G-CODE-6): never treated as no license.
type Refused struct {
	File   string
	ID     string // the id the file names, so license remove takes it away; empty when it names none
	Reason string
	Detail string
}

// TrialRun is the app's trial as its note says (ADR-L9): when and on which install it started, and how long it runs from today.
type TrialRun struct {
	Started
	File     string
	LastDay  time.Time // the note's day plus the trial's length, less one, as the first day counts
	DaysLeft int       // the days to LastDay, 0 once it has ended
	Ended    bool      // the features it opened are locked again (ADR-L3)
}

// Found is the app's licenses and trial as read at one moment: the app's own licenses, then the org's that name it, the files refused, the trial, and the choice license use or add made.
type Found struct {
	Taken   []Taken
	Refused []Refused
	Trial   *TrialRun // nil when the app has no trial
	Chosen  string    // the id of the license chosen in this app; empty when no choice is made
	product string
	stuck   error  // the Problem refused when the choice cannot be read and only a hand change fixes it, so use refuses before it asks
	unread  string // why the choice cannot be read, when it cannot: a choice that names nothing, so no license is active
}

// Active is the app's active license (ADR-L8), nil when there is none: the chosen one, else the newest by issued. A chosen license that is refused or not there, or a choice that cannot be read, leaves none, as the app never takes another by itself.
func (f Found) Active() *Taken {
	for i := range f.Taken {
		if f.Taken[i].Active {
			return &f.Taken[i]
		}
	}
	return nil
}

// Read reads the app's own licenses and the org's that name it, the old single files among them for one release, the app's choice and its trial (ADR-L8, L9); a missing file is none, and a license file it cannot read or verify is Refused, and a choice it cannot read names nothing, so no license is active. It fails only when the folders cannot be found.
func (c *Checker) Read() (Found, error) {
	f := Found{product: c.c.ID}
	for _, pl := range []Place{PlaceApp, PlaceOrg} {
		paths, refusedDir, err := c.files(pl)
		if err != nil {
			return Found{}, err
		}
		if refusedDir != nil {
			f.Refused = append(f.Refused, *refusedDir)
		}
		for _, path := range paths {
			c.read(&f, path, pl)
		}
	}
	chosen, unread, err := c.chosen()
	if err != nil {
		return Found{}, err
	}
	f.Chosen, f.unread = chosen, unread
	f.stuck = c.stuck()
	i := -1
	for j, t := range f.Taken {
		switch {
		case unread != "":
		case chosen != "" && t.ID == chosen && i < 0:
			i = j
		case chosen == "" && (i < 0 || t.Issued.After(f.Taken[i].Issued)):
			i = j
		}
	}
	if i >= 0 {
		f.Taken[i].Active = true
	}
	if f.Trial, err = c.trial(); err != nil {
		return Found{}, err
	}
	return f, nil
}

// read adds the license file at path to f, taken or refused; a missing file, and an org license that does not name the app, add nothing. A file in a licenses folder is named by its id, so one named otherwise is refused as id_mismatch.
func (c *Checker) read(f *Found, path string, pl Place) {
	raw, err := readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	var l License
	if err == nil {
		l, err = Verify(raw, c.c.Trust)
	}
	if err == nil && !l.names(c.c.ID) {
		if pl == PlaceOrg {
			return
		}
		err = refused(ReasonWrongPlace, fmt.Sprintf("it is a license for other products, not %s; an app's folder keeps its own licenses only", c.c.ID))
	}
	if base := filepath.Base(path); err == nil && inFolder(path) && base != l.ID+ext {
		err = refused(ReasonIDMismatch, fmt.Sprintf("its file is named %s but its id is %q; a license file in a licenses folder is named <id>%s", base, l.ID, ext))
	}
	if err != nil {
		f.Refused = append(f.Refused, refusal(path, pl, raw, err))
		return
	}
	f.Taken = append(f.Taken, c.taken(l, path, pl))
}

// refusal is the Refused of the file at path in the place pl for err, naming the id the file gives: its name in a licenses folder, as the unsigned metadata.id of a refused file proves nothing, else, in an old single file, its metadata.id, or old-org or old-app when it is too broken to give one, so license remove still takes it away.
func refusal(path string, pl Place, raw []byte, err error) Refused {
	r := Refused{File: path, Reason: ReasonUnreadable, Detail: err.Error()}
	var head struct {
		Metadata struct {
			ID string `json:"id"`
		} `json:"metadata"`
	}
	switch {
	case inFolder(path):
		r.ID = strings.TrimSuffix(filepath.Base(path), ext)
	case decode(raw, &head, false) == nil && head.Metadata.ID != "":
		r.ID = head.Metadata.ID
	default:
		r.ID = "old-" + string(pl)
	}
	var p *problem.Problem
	if errors.As(err, &p) {
		r.Reason, r.Detail = p.Reason, p.Detail
	}
	return r
}

// inFolder reports whether path is a file of a licenses folder, not an old single file.
func inFolder(path string) bool { return filepath.Base(filepath.Dir(path)) == folder }

// files is the license files of the place in the order Read takes them: its licenses folder's, by name, then the old single file; a licenses folder that cannot be listed is Refused. It fails only when the folder cannot be found.
func (c *Checker) files(pl Place) ([]string, *Refused, error) {
	dir, err := c.dir(pl)
	if err != nil {
		return nil, nil, err
	}
	var paths []string
	entries, err := os.ReadDir(filepath.Join(dir, folder))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return []string{filepath.Join(dir, old)}, &Refused{File: filepath.Join(dir, folder), Reason: ReasonUnreadable, Detail: err.Error()}, nil
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ext) {
			paths = append(paths, filepath.Join(dir, folder, e.Name()))
		}
	}
	return append(paths, filepath.Join(dir, old)), nil, nil
}

// chosen is the id of the license chosen in this app, empty when none is, and why the choice cannot be read when it cannot (a folder, a pipe, no permission, a link that leads nowhere): a choice that names nothing. It fails only when the app's folder cannot be found.
func (c *Checker) chosen() (string, string, error) {
	path, err := home.File(c.c.Folder, choice)
	if err != nil {
		return "", "", err
	}
	raw, err := readFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist) && !there(path):
		return "", "", nil
	case err != nil:
		return "", err.Error(), nil
	}
	return strings.TrimSpace(string(raw)), "", nil
}

// there reports whether anything is at path, a link that leads nowhere among them.
func there(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// unreadable reports whether the file at path cannot be read for what it is, not a regular file (a folder, a pipe, a link), or for want of permission: a choice that names nothing.
func unreadable(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		return true
	}
	_, err = readFile(path)
	return errors.Is(err, fs.ErrPermission)
}

// noTrial reports whether the build runs no trial: a release with no line stamped (ADR-L9, L10).
func (c *Checker) noTrial() bool { return c.c.Trust.Release && c.c.Line == 0 }

// trial is the app's trial as its note says, nil when it has none: no note, or one missing, broken, of another app or future-dated (ADR-L9), or a release with no line stamped, which runs no trial.
func (c *Checker) trial() (*TrialRun, error) {
	if c.noTrial() {
		return nil, nil
	}
	path, err := home.File(c.c.Folder, note)
	if err != nil {
		return nil, err
	}
	raw, err := readFile(path)
	if err != nil {
		return nil, nil
	}
	s, ok := c.note(raw)
	if !ok {
		return nil, nil
	}
	t := &TrialRun{Started: s, File: path, LastDay: date(s.Day).AddDate(0, 0, trialDays-1)}
	t.DaysLeft = max(daysBetween(c.c.Now(), t.LastDay)+1, 0)
	t.Ended = t.DaysLeft == 0
	return t, nil
}

// Check is the license raw holds as Add would keep it, in the org's licenses folder or the app's own, or the Problem why not: what add shows before it asks. Only a license of FileAPIVersion that names this app is kept.
func (c *Checker) Check(raw []byte, org bool) (Taken, error) {
	pl := placeOf(org)
	l, err := Verify(raw, c.c.Trust)
	if err != nil {
		return Taken{}, err
	}
	switch {
	case l.old != "":
		return Taken{}, refused(ReasonUnreadable, fmt.Sprintf("it is a license file of the old form %s, which mankit reads for one release but never keeps; a new file is %s", APIVersion, FileAPIVersion))
	case !l.names(c.c.ID):
		return Taken{}, refused(ReasonWrongPlace, fmt.Sprintf("it does not name %s; add it in an app it names", c.c.ID))
	}
	key, err := store.ParseKey(folder + "/" + l.ID + ext)
	if err != nil || strings.ContainsAny(l.ID, `/\`) || string(key) != folder+"/"+l.ID+ext {
		return Taken{}, refused(ReasonUnreadable, fmt.Sprintf("its id %q cannot name a file", l.ID))
	}
	dir, err := c.dir(pl)
	if err != nil {
		return Taken{}, err
	}
	if err := c.stuck(); err != nil {
		return Taken{}, err
	}
	return c.taken(l, filepath.Join(dir, folder, l.ID+ext), pl), nil
}

// Add keeps the license raw holds once it checks, in the org's licenses folder, shared by every app, or in the app's own, written whole through the store (G-CODE-5), and makes it this app's choice (ADR-L8); a file there of the same id is replaced, and other apps keep their choice.
func (c *Checker) Add(ctx context.Context, raw []byte, org bool) (Taken, error) {
	t, err := c.Check(raw, org)
	if err != nil {
		return Taken{}, err
	}
	s, err := c.scope(org)
	if err != nil {
		return Taken{}, err
	}
	key := store.Key(folder + "/" + t.ID + ext)
	err = s.Change(ctx, folder, "license add", func(tx store.Tx) error { return tx.Put(key, raw, store.Anything()) })
	s.Close()
	if err == nil {
		err = c.choose(ctx, t.ID, "license add")
	}
	t.Active = err == nil
	return t, err
}

// Use makes the license id this app's choice (ADR-L8), or is the Problem why not: a license refused, or none of that id among this app's; other apps keep their choice.
func (c *Checker) Use(ctx context.Context, id string) (Taken, error) {
	f, err := c.Read()
	if err != nil {
		return Taken{}, err
	}
	t, err := f.Find(id)
	if err != nil {
		return Taken{}, err
	}
	if err := c.choose(ctx, id, "license use"); err != nil {
		return Taken{}, err
	}
	t.Active = true
	return t, nil
}

// Find is the license id among f's, or the Problem why not: refused when the choice cannot be read and only a hand change fixes it, refused when the file of that id is refused, not_found when there is none.
func (f Found) Find(id string) (Taken, error) {
	if f.stuck != nil {
		return Taken{}, f.stuck
	}
	for _, t := range f.Taken {
		if t.ID == id {
			return t, nil
		}
	}
	for _, r := range f.Refused {
		if r.ID == id {
			return Taken{}, problem.FailAt(problem.TypeRefused, r.Reason, r.File, fmt.Sprintf("the license %s is refused: %s", id, r.Detail))
		}
	}
	return Taken{}, problem.Fail(problem.TypeNotFound, "", fmt.Sprintf("there is no license %s for %s", id, f.product))
}

// stuck is the Problem refused when the choice file cannot be read and the store cannot replace it: a pipe, a file it may not read, or a link to nothing or to one it may not read, which only a hand change makes (the store never writes through a link), so nothing is written until it is removed or fixed by hand. A choice that is not there, a folder, and a readable file are the store's; it is nil for them.
func (c *Checker) stuck() error {
	path, err := home.File(c.c.Folder, choice)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || info.IsDir() {
		return nil
	}
	if _, err := readFile(path); err != nil {
		return problem.FailAt(problem.TypeRefused, ReasonUnreadable, path, fmt.Sprintf("the choice file at %s cannot be read (%v); remove or fix it by hand, then try again", path, err))
	}
	return nil
}

// choose keeps id as this app's choice, through the store, writing over a choice that names nothing: a folder is removed first, in a change of the scope's own turn. A choice that cannot be read and the store cannot replace is refused by stuck, with nothing written.
func (c *Checker) choose(ctx context.Context, id, what string) error {
	if err := c.stuck(); err != nil {
		return err
	}
	path, err := home.File(c.c.Folder, choice)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	isDir := err == nil && info.IsDir()
	s, err := c.scope(false)
	if err != nil {
		return err
	}
	defer s.Close()
	if isDir {
		if err := s.Change(ctx, "", what, func(tx store.Tx) error { return tx.Remove(choice, store.Anything()) }); err != nil {
			return err
		}
	}
	return s.Change(ctx, choice, what, func(tx store.Tx) error { return tx.Put(choice, []byte(id+"\n"), store.Anything()) })
}

// Removal is one file license remove takes away, the license id it holds, its place and the sum it held when Removing read it, so Remove takes away only what was shown.
type Removal struct {
	ID    string
	File  string
	Place Place
	Sum   string
}

// Removing is the files Remove takes away for the license id: every file of this app's licenses, taken or refused, that holds it. With none it is a Problem not_found.
func (c *Checker) Removing(id string) ([]Removal, error) {
	f, err := c.Read()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, t := range f.Taken {
		if t.ID == id {
			files = append(files, t.File)
		}
	}
	for _, r := range f.Refused {
		if r.ID == id {
			files = append(files, r.File)
		}
	}
	if len(files) == 0 {
		return nil, problem.Fail(problem.TypeNotFound, "", fmt.Sprintf("there is no license %s to remove", id))
	}
	var out []Removal
	for _, file := range files {
		at, err := c.at(file)
		if err != nil {
			return nil, err
		}
		sum, err := c.sum(at)
		if err != nil {
			return nil, err
		}
		out = append(out, Removal{ID: id, File: file, Place: placeOf(at.org), Sum: sum})
	}
	return out, nil
}

// spot is where a license file sits in the store: its scope's place and its key there.
type spot struct {
	org bool
	key store.Key
}

// at is the spot of a license file of this app, or an error for any other file.
func (c *Checker) at(file string) (spot, error) {
	for _, org := range []bool{false, true} {
		dir, err := c.dir(placeOf(org))
		if err != nil {
			return spot{}, err
		}
		switch base := filepath.Base(file); {
		case file == filepath.Join(dir, old):
			return spot{org, old}, nil
		case filepath.Dir(file) == filepath.Join(dir, folder) && strings.HasSuffix(base, ext):
			return spot{org, store.Key(folder + "/" + base)}, nil
		}
	}
	return spot{}, fmt.Errorf("licenses: %s is not a license file of %s", file, c.c.ID)
}

// sum is the sum of the file at s, as the store reads it.
func (c *Checker) sum(at spot) (string, error) {
	s, err := c.scope(at.org)
	if err != nil {
		return "", err
	}
	defer s.Close()
	it, _, err := s.Get(at.key)
	return it.Sum, err
}

// Remove takes away the files shown, as Removing gave them, each through the store in its scope's own turn and only while it holds the sum it was shown with; a file changed since is a Problem refused, changed. It clears this app's choice when it named a license removed, so the newest is active again, and never touches the trial. Every file is checked before any is removed, and the app's own go first, so a refusal leaves them all; a file changed by someone else between the check and its turn is still refused, after the ones before it went (best effort, not one transaction).
func (c *Checker) Remove(ctx context.Context, shown []Removal) error {
	type removal struct {
		Removal
		at spot
	}
	var order []removal
	for _, r := range shown {
		at, err := c.at(r.File)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(order, func(o removal) bool { return o.File == r.File }) {
			order = append(order, removal{r, at})
		}
	}
	slices.SortStableFunc(order, func(a, b removal) int {
		switch {
		case a.at.org == b.at.org:
			return strings.Compare(a.File, b.File)
		case b.at.org:
			return -1
		}
		return 1
	})
	for _, r := range order {
		if err := c.holds(r.Removal, r.at); err != nil {
			return err
		}
	}
	for _, r := range order {
		s, err := c.scope(r.at.org)
		if err != nil {
			return err
		}
		err = s.Change(ctx, "", "license remove", func(tx store.Tx) error { return tx.Remove(r.at.key, store.Holds(r.Sum)) })
		s.Close()
		if err != nil {
			return err
		}
	}
	path, err := home.File(c.c.Folder, choice)
	if err != nil {
		return err
	}
	s, err := c.scope(false)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Change(ctx, "", "license remove", func(tx store.Tx) error { return unchoose(tx, path, shown) })
}

// unchoose clears the choice at path in tx when it names a license shown. A choice that cannot be read for what it is, or for want of permission, names nothing removed; any other error reading it is returned (G-CODE-6).
func unchoose(tx store.Tx, path string, shown []Removal) error {
	it, ok, err := tx.Get(choice)
	switch {
	case err != nil && unreadable(path):
		return nil
	case err != nil:
		return err
	case !ok || !slices.ContainsFunc(shown, func(r Removal) bool { return r.ID == strings.TrimSpace(string(it.Body)) }):
		return nil
	}
	return tx.Remove(choice, store.Holds(it.Sum))
}

// holds checks, before anything is removed, that the file r shows is still as shown.
func (c *Checker) holds(r Removal, at spot) error {
	sum, err := c.sum(at)
	switch {
	case err != nil:
		return err
	case sum != r.Sum:
		return problem.FailAt(problem.TypeRefused, store.ReasonChanged, r.File, fmt.Sprintf("%s changed since it was shown, so nothing is removed", r.File))
	}
	return nil
}

// scope is the store scope of the app's own folder, or of the org's (home.OrgDir), opened with store.Own as a folder private to this user.
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

// placeOf is the place org names.
func placeOf(org bool) Place {
	if org {
		return PlaceOrg
	}
	return PlaceApp
}

// dir is the folder of the place: home.OrgDir for the org's, home.Product for the app's (ADR-L5, L8).
func (c *Checker) dir(pl Place) (string, error) {
	if pl == PlaceOrg {
		return home.OrgDir()
	}
	return home.Product(c.c.Folder)
}

// names reports whether the license names the product id, in its products or its lines.
func (l License) names(product string) bool {
	_, ok := l.Products[product]
	_, line := l.Lines[product]
	return ok || line
}

// taken is l as it runs today, from its issued day to its until, or on with none; when it names lines, it opens nothing unless one is this build's line (ADR-L10).
func (c *Checker) taken(l License, path string, pl Place) Taken {
	t := Taken{License: l, File: path, Place: pl, LastDay: l.Until}
	today := c.c.Now()
	if !t.LastDay.IsZero() {
		t.DaysLeft = max(daysBetween(today, t.LastDay)+1, 0)
		t.Ended = t.DaysLeft == 0
	}
	t.Early = !t.Ended && daysBetween(l.Issued, today) < 0
	t.Elsewhere = !l.names(c.c.ID) || (len(l.Lines) > 0 && !slices.Contains(l.Lines[c.c.ID], c.c.Line))
	return t
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

// From is where ft is unlocked from now, and the last day it stays open: the active license while it runs and names it, until its last day and zero when it never ends; else the running trial, until its last day; else none. A free feature is from none: it needs no license (ADR-L2).
func (f Found) From(ft features.Feature) (From, time.Time) {
	if !ft.Enterprise() {
		return FromNone, time.Time{}
	}
	if a := f.Active(); a != nil && a.Running() && a.Names(f.product, ft.ID) {
		return FromLicense, a.LastDay
	}
	if f.Trial != nil && !f.Trial.Ended {
		return FromTrial, f.Trial.LastDay
	}
	return FromNone, time.Time{}
}

// Has reports whether f may run as far as licenses go: a free feature always, an enterprise one while the active license or the trial opens it.
func (f Found) Has(ft features.Feature) bool {
	from, _ := f.From(ft)
	return !ft.Enterprise() || from != FromNone
}

// Closer closes every feature of all, the features the app's list holds, that neither the active license nor the trial opens; the list keeps it from a free one (ADR-L2, L3, L9).
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
