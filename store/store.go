// Package store is the one storage port every app keeps what it writes through (ADR-S22): a scope names its things by keys, reading never waits, and every change takes the thing's turn in the change queue (ADR-S14), is made all or nothing and is read back.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/tugbayatilla/mankit/v3/features"
	"github.com/tugbayatilla/mankit/v3/home"
	"github.com/tugbayatilla/mankit/v3/internal/disk"
	"github.com/tugbayatilla/mankit/v3/queue"
	"github.com/tugbayatilla/mankit/v3/serve"
)

// The reasons of the store's Problems (contracts problem/v1); a program decides by type and reason, never by the detail (G-STACK-15).
const (
	ReasonOutside    = "outside"           // the key leaves the scope or leads through a link
	ReasonChanged    = "changed"           // the thing no longer holds what the writer read
	ReasonNotOwned   = "not_owned"         // a private folder or a journal is not this user's own
	ReasonUnfinished = "unfinished_change" // a change cut short waits to be put back by the user who made it
)

// APIVersion is the form of the store's own files, a journal's lines and a change's marks, told apart by kind; two app versions share them, so it never changes (ADR-S15).
func APIVersion() string { return home.Org + "/mankit/store/v1" }

// The kinds of the store's files (contracts mankit/v1).
const (
	JournalKind       = "StoreJournal"
	JournalChangeKind = "StoreJournalChange"
	UnfinishedKind    = "StoreUnfinished"
)

// Key names a thing inside a scope: a slash path, never a machine path (ADR-S21); ParseKey makes one, in the one form the store writes (contracts mankit/v1 storeKey).
type Key string

// The steps no key may hold: the store's own folders inside a scope.
var reserved = map[string]bool{".queue": true, ".store": true}

// ParseKey is s as a key: empty and . steps and a trailing slash dropped; a .. step, a backslash, a control character, a machine path's start or one of the store's own folders refused as outside.
func ParseKey(s string) (Key, error) {
	refuse := func(why string) (Key, error) {
		return "", fail(serve.TypeRefused, ReasonOutside, "", fmt.Sprintf("%q is no key inside the scope: %s", s, why))
	}
	switch {
	case s == "":
		return refuse("it is empty")
	case strings.HasPrefix(s, "/"):
		return refuse("it starts at the machine's root")
	case strings.HasPrefix(s, "~"), strings.HasPrefix(s, " "), unicode.IsSpace(rune(s[0])):
		return refuse("it reads as a machine path")
	case strings.ContainsRune(s, '\\'):
		return refuse("it holds a backslash")
	case strings.ContainsFunc(s, unicode.IsControl):
		return refuse("it holds a control character")
	}
	var steps []string
	for _, step := range strings.Split(s, "/") {
		switch {
		case step == "" || step == ".":
			continue
		case step == "..":
			return refuse("a .. step leaves the scope")
		case reserved[step]:
			return refuse(step + " is the store's own folder")
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return refuse("it names the scope itself")
	}
	if first := steps[0]; schemeLike(first) {
		return refuse("its first step reads as a drive or a scheme")
	}
	return Key(strings.Join(steps, "/")), nil
}

// schemeLike says whether a first step starts like C: or file:, which would name a place outside the scope.
func schemeLike(step string) bool {
	i := strings.IndexByte(step, ':')
	if i < 1 {
		return false
	}
	for j, c := range step[:i] {
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !(letter || j > 0 && (c >= '0' && c <= '9' || c == '+' || c == '.' || c == '-')) {
			return false
		}
	}
	return true
}

// check is the key's own refusal when it is not in the one form ParseKey gives, so a Key made by a conversion never reaches outside.
func (k Key) check() error {
	got, err := ParseKey(string(k))
	if err != nil {
		return err
	}
	if got != k {
		return fail(serve.TypeRefused, ReasonOutside, string(k), fmt.Sprintf("%q is not in the one form a key takes, %q", k, got))
	}
	return nil
}

// thing is the key's first step: the thing whose turn a change to it takes (ADR-S14).
func (k Key) thing() Key {
	first, _, _ := strings.Cut(string(k), "/")
	return Key(first)
}

// within says whether k is at or inside the key at.
func (k Key) within(at Key) bool { return k == at || strings.HasPrefix(string(k), string(at)+"/") }

// rebase is k, at or inside from, named under to instead: where a move of from to to takes it. The one rule a change's view, its list and the read-back after it follow for a move.
func (k Key) rebase(from, to Key) Key { return to + k[len(from):] }

// overlaps says whether one of a and b is at or inside the other, so a change to one reaches the other.
func overlaps(a, b Key) bool { return a.within(b) || b.within(a) }

// Item is a thing as read: its body, and its Sum, which a Want of a later change names.
type Item struct {
	Body []byte
	Sum  string
}

// SumOf is a body's sum as the store names it: sha256: and its hex.
func SumOf(body []byte) string {
	s := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(s[:])
}

// Want is what a change expects a thing to hold before it writes, so a write is made only if the thing still holds what the writer read.
type Want struct {
	kind byte
	sum  string
}

// The kinds of Want; the zero Want is none, refused, so a write never overwrites by mistake.
const (
	wantAbsent byte = iota + 1
	wantHolds
	wantAnything
)

// Absent wants no thing at the key.
func Absent() Want { return Want{kind: wantAbsent} }

// Holds wants the thing whose Sum is sum.
func Holds(sum string) Want { return Want{kind: wantHolds, sum: sum} }

// Anything takes whatever is there, or nothing.
func Anything() Want { return Want{kind: wantAnything} }

// met says whether a thing with sum, there or not, is what w wants; a folder has no sum and meets only Anything.
func (w Want) met(there, folder bool, sum string) bool {
	switch w.kind {
	case wantAnything:
		return true
	case wantAbsent:
		return !there
	case wantHolds:
		return there && !folder && sum == w.sum
	}
	return false
}

func (w Want) String() string {
	switch w.kind {
	case wantAbsent:
		return "nothing there"
	case wantHolds:
		return w.sum
	case wantAnything:
		return "anything"
	}
	return "no Want"
}

// App is the app a scope writes for: its product ID and version, written on its tickets and journals, its feature list, through which an adapter other than files is picked (ADR-E9), and its queue waits.
type App struct {
	ID       string
	Command  string // the CLI's name, such as e-designer, which the unfinished_change refusal and store unfinished name; empty is ID
	Version  string
	Features *features.List // nil has no feature: files
	Wait     time.Duration  // how long a change waits for its turn; zero is queue.DefaultWait
	Limit    time.Duration  // how long a change may hold its turn; zero is queue.DefaultLimit
	Log      io.Writer      // where the queue says a ticket set aside; nil is stderr
}

// Scope is one folder an app keeps things in, opened once: the folder it serves, or its own config or data folder (G-STACK-8).
type Scope struct {
	app     App
	root    string
	private bool
	line    func(thing Key) string
	a       adapter
}

// Served is the folder root an app serves; line names the folder whose .queue holds each thing's turn, and line("") the folder's own (ADR-S14). A served file keeps the mode of the file it replaces.
func Served(app App, root string, line func(thing Key) string) (*Scope, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fail(serve.TypeFailed, "", "", fmt.Sprintf("the folder %s cannot be found: %v", root, err))
	}
	return open(app, abs, false, line)
}

// Config is the app's own config folder, private to this user; every change takes the one turn in its .queue.
func Config(app App) (*Scope, error) { return own(app, home.Product) }

// Data is the app's own data folder, private to this user; every change takes the one turn in its .queue.
func Data(app App) (*Scope, error) { return own(app, home.ProductData) }

func own(app App, where func(string) (string, error)) (*Scope, error) {
	dir, err := where(app.ID)
	if err != nil {
		return nil, fail(serve.TypeFailed, "", "", fmt.Sprintf("%s's own folder cannot be found: %v", app.ID, err))
	}
	// This user's own folder that others could read is made private, and said; another user's, or a link, is refused.
	tightened, err := disk.Private(dir)
	var skipped []string
	// What is inside is looked at only when the folder itself was open, so a private folder costs no walk on every open.
	if err == nil && len(tightened) > 0 {
		var inside []string
		inside, skipped, err = disk.Tighten(dir)
		tightened = append(tightened, inside...)
	}
	if err != nil {
		return nil, diskFail(err, "", fmt.Sprintf("%s's own folder %s", app.ID, dir))
	}
	for _, p := range tightened {
		fmt.Fprintf(logOf(app), "store: %s was open to other users; it is now this user's alone\n", p)
	}
	for _, p := range skipped {
		fmt.Fprintf(logOf(app), "store: %s is another user's, so it was left as it is\n", p)
	}
	return open(app, dir, true, func(Key) string { return dir })
}

func open(app App, root string, private bool, line func(Key) string) (*Scope, error) {
	s := &Scope{app: app, root: root, private: private, line: line}
	a, err := pick(app.Features, s)
	if err != nil {
		return nil, err
	}
	s.a = a
	return s, nil
}

// Close lets go of the scope's open folder.
func (s *Scope) Close() error { return s.a.close() }

// Get is the thing at key, and whether one is there; reading never takes a turn.
func (s *Scope) Get(key Key) (Item, bool, error) {
	if err := key.check(); err != nil {
		return Item{}, false, err
	}
	return s.a.get(key)
}

// List is every thing at or inside prefix, in order; an empty prefix lists the whole scope.
func (s *Scope) List(prefix Key) ([]Key, error) {
	if prefix != "" {
		if err := prefix.check(); err != nil {
			return nil, err
		}
	}
	return s.a.list(prefix)
}

// Revision moves whenever a thing in the scope changes, so a reader polls it, or a server's live events watch it, to learn that something moved (ADR-E3).
func (s *Scope) Revision() (string, error) { return s.a.revision() }

// Change runs fn in the turn of thing, a key's first step, or of the scope itself when thing is empty; what fn puts, removes and moves is made all or nothing once fn returns nil, then read back. what names the change on its ticket.
// A thing not there yet is added in the folder's turn as well, taken first; a change to the scope itself takes the turn of each thing it writes, as it writes it.
// fn must not call Change on the same scope: it would wait for the turn its own change holds, for ever.
func (s *Scope) Change(ctx context.Context, thing Key, what string, fn func(Tx) error) error {
	if thing != "" {
		if err := thing.check(); err != nil {
			return err
		}
		if thing.thing() != thing {
			return fail(serve.TypeRefused, ReasonOutside, string(thing), fmt.Sprintf("%s is inside a thing; a change takes the turn of the thing %s", thing, thing.thing()))
		}
	}
	if err := s.a.settle(ctx, thing); err != nil {
		return err
	}
	folder := thing == "" || !s.a.there(thing)
	for {
		t := s.turns(ctx, what)
		again := false
		err := func() error {
			if folder {
				if err := t.take(""); err != nil {
					return err
				}
				if err := s.a.blocked(t, ""); err != nil {
					return err
				}
			}
			if thing != "" {
				if err := t.take(thing); err != nil {
					return err
				}
				// Moved or removed while this change waited: it is added anew, in the folder's turn first.
				if !folder && !s.a.there(thing) {
					again = true
					return nil
				}
			}
			tx := &tx{s: s, t: t, thing: thing}
			if err := fn(tx); err != nil {
				return err
			}
			if len(tx.ops) == 0 {
				return nil
			}
			return s.a.commit(t, tx.ops)
		}()
		// Asked before the turns are given up, which forgets them.
		overran := t.over()
		err = t.give(err)
		if again {
			folder = true
			continue
		}
		// A change that was made and read back is done, even past the limit; the queue's change_too_long is for a write that had to stop.
		if err == nil && overran {
			fmt.Fprintf(logOf(s.app), "store: %s held its turn in %s past the queue's limit, and was made\n", what, s.root)
		}
		return err
	}
}

func logOf(app App) io.Writer {
	if app.Log != nil {
		return app.Log
	}
	return os.Stderr
}

// Unfinished is every change cut short whose mark is in thing's turn, or the folder's own when thing is empty: whose it is and what it holds back; and this user's journals of the scope whose marks are gone (Left).
func (s *Scope) Unfinished(thing Key) ([]Unfinished, error) {
	if thing != "" {
		if err := thing.check(); err != nil {
			return nil, err
		}
	}
	return s.a.unfinished(thing)
}

// Clear removes the marks of every change cut short in thing's turn, each in its turns, and says which, with this user's journal when it is on this machine (File); it puts nothing back, and a journal stays in its user's data folder, shown as Left by Unfinished.
// A mark already gone once its turns are taken, put back or cleared meanwhile, is not counted.
//
// Deprecated: Clear removes marks a person may never have seen; use ClearShown with the ids the question showed.
func (s *Scope) Clear(ctx context.Context, thing Key) ([]Unfinished, error) {
	if thing != "" {
		if err := thing.check(); err != nil {
			return nil, err
		}
	}
	return s.a.clear(ctx, thing, nil)
}

// ClearShown is Clear of only the marks whose ids a person was shown: each is checked again once its turns are taken, so a mark put back, cleared or new meanwhile is left as it is, and what it says is what was removed; no id clears nothing.
// It is for store unfinished clear, which shows the change and asks first (G-STACK-13), once a person is sure the change was put back or given up; nothing clears a mark unasked.
func (s *Scope) ClearShown(ctx context.Context, thing Key, ids []string) ([]Unfinished, error) {
	if thing != "" {
		if err := thing.check(); err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		return []Unfinished{}, nil
	}
	return s.a.clear(ctx, thing, ids)
}

// command is the app's CLI name, the one the unfinished_change refusal and store unfinished name.
func (a App) command() string {
	if a.Command != "" {
		return a.Command
	}
	return a.ID
}

// Tx is what a change's fn reads and writes through, inside its turn; writes are made only once fn returns nil, so what Get reads is what was there when the change began.
type Tx interface {
	Get(key Key) (Item, bool, error)
	Put(key Key, body []byte, want Want) error
	Remove(key Key, want Want) error
	Move(from, to Key, want Want) error
}

// opKind is what one write of a change does.
type opKind byte

const (
	opPut opKind = iota
	opRemove
	opMove
)

// op is one write a change's fn asked for, made at commit.
type op struct {
	kind opKind
	key  Key // what is put or removed, or moved to
	from Key // what is moved
	body []byte
	want Want
}

// keys are what the op writes.
func (o op) keys() []Key {
	if o.kind == opMove {
		return []Key{o.from, o.key}
	}
	return []Key{o.key}
}

type tx struct {
	s     *Scope
	t     *turns
	thing Key
	ops   []op
}

// Get is the thing at key as the change has it so far: what was there when it began, with the writes fn has staged on top.
func (x *tx) Get(key Key) (Item, bool, error) {
	if err := key.check(); err != nil {
		return Item{}, false, err
	}
	body, there, folder, err := x.view(key)
	if err != nil || !there {
		return Item{}, false, err
	}
	if folder {
		return Item{}, false, fail(serve.TypeRefused, "", string(key), fmt.Sprintf("%s is a folder, not a file", key))
	}
	return Item{Body: body, Sum: SumOf(body)}, true, nil
}

// ListIn is every thing at or inside prefix as the change in has it so far, in the order Scope.List gives: what was there when the change began, with the writes its fn has staged on top. An empty prefix lists the whole scope.
func ListIn(in Tx, prefix Key) ([]Key, error) {
	x, ok := in.(*tx)
	if !ok {
		return nil, fmt.Errorf("store: ListIn takes the Tx a change hands its fn, not a %T", in)
	}
	if prefix != "" {
		if err := prefix.check(); err != nil {
			return nil, err
		}
	}
	return x.list(prefix)
}

// list replays the staged writes, first to last, over what is there under prefix and under every key a write moves from, then keeps what ends inside prefix.
// view is its twin for one key: what one lists, the other finds there; storetest's agree holds them to it, so a write kind added to one goes into both.
func (x *tx) list(prefix Key) ([]Key, error) {
	have := map[Key]bool{}
	add := func(at Key) error {
		keys, err := x.s.a.list(at)
		for _, k := range keys {
			have[k] = true
		}
		return err
	}
	if err := add(prefix); err != nil {
		return nil, err
	}
	for _, o := range x.ops {
		if o.kind == opMove {
			if err := add(o.from); err != nil {
				return nil, err
			}
		}
	}
	for _, o := range x.ops {
		switch o.kind {
		case opPut:
			have[o.key] = true
		case opRemove:
			maps.DeleteFunc(have, func(k Key, _ bool) bool { return k.within(o.key) })
		case opMove:
			for k := range have {
				if k.within(o.from) {
					delete(have, k)
					have[k.rebase(o.from, o.key)] = true
				}
			}
		}
	}
	out := []Key{}
	for k := range have {
		if prefix == "" || k.within(prefix) {
			out = append(out, k)
		}
	}
	// A walk of the folders: a key's steps compared one by one, so shop/a/x.yml comes before shop/a-b.yml as Scope.List has it.
	slices.SortFunc(out, func(a, b Key) int {
		return slices.Compare(strings.Split(string(a), "/"), strings.Split(string(b), "/"))
	})
	return out, nil
}

func (x *tx) Put(key Key, body []byte, want Want) error {
	return x.stage(op{kind: opPut, key: key, body: append([]byte{}, body...), want: want})
}

func (x *tx) Remove(key Key, want Want) error {
	return x.stage(op{kind: opRemove, key: key, want: want})
}

func (x *tx) Move(from, to Key, want Want) error {
	return x.stage(op{kind: opMove, key: to, from: from, want: want})
}

// view is key as the staged writes leave it, read from the last one back to the disk: a file's body, or a folder.
// list is its twin for a folder: what one finds there, the other lists; storetest's agree holds them to it, so a write kind added to one goes into both.
func (x *tx) view(key Key) (body []byte, there, folder bool, err error) {
	k := key
	for i := len(x.ops) - 1; i >= 0; i-- {
		o := x.ops[i]
		switch o.kind {
		case opPut:
			switch {
			case k == o.key:
				return o.body, true, false, nil
			case o.key.within(k):
				return nil, true, true, nil
			case k.within(o.key):
				// Under a file the change wrote, nothing can be.
				return nil, false, false, nil
			}
		case opRemove:
			if k.within(o.key) {
				return nil, false, false, nil
			}
		case opMove:
			switch {
			case k.within(o.key):
				// Before the move it was under the old name.
				k = k.rebase(o.key, o.from)
			case k.within(o.from):
				return nil, false, false, nil
			case o.key.within(k):
				return nil, true, true, nil
			}
		}
	}
	return x.s.a.look(k)
}

// stage checks an op before it is kept: its keys inside the change's thing, its turns taken, no change cut short holding them, and its Want met by what the change has so far.
func (x *tx) stage(o op) error {
	if o.want.kind == 0 {
		return fmt.Errorf("store: a write of %s names no Want (Absent, Holds or Anything)", o.key)
	}
	for _, k := range o.keys() {
		if err := k.check(); err != nil {
			return err
		}
		if x.thing != "" && !k.within(x.thing) {
			return fail(serve.TypeRefused, ReasonOutside, string(k), fmt.Sprintf("%s is outside the thing %s, whose turn this change holds", k, x.thing))
		}
		// The thing itself is added, moved or removed in the folder's turn, which must come before the thing's.
		if x.thing != "" && k == x.thing && o.kind != opPut && !x.t.holds("") {
			return fmt.Errorf("store: %s is moved or removed in a change of the scope itself, which takes the folder's turn first", k)
		}
		if x.thing == "" {
			if err := x.t.take(k.thing()); err != nil {
				return err
			}
		}
		if err := x.s.a.blocked(x.t, k); err != nil {
			return err
		}
	}
	if err := x.meets(o); err != nil {
		return err
	}
	x.ops = append(x.ops, o)
	return nil
}

// meets refuses an op the change so far does not allow: a Want not met as changed, a file over a folder, a move onto a taken name or into itself.
func (x *tx) meets(o op) error {
	at := o.key
	if o.kind == opMove {
		at = o.from
		if o.key.within(o.from) {
			return fail(serve.TypeRefused, "", string(o.key), fmt.Sprintf("%s is inside %s, so %s cannot move there", o.key, o.from, o.from))
		}
		_, taken, _, err := x.view(o.key)
		if err != nil {
			return err
		}
		if taken {
			return fail(serve.TypeRefused, ReasonChanged, string(o.key), fmt.Sprintf("%s is there already, so %s is not moved onto it", o.key, o.from))
		}
	}
	if o.kind != opRemove {
		if err := x.noFileAbove(o.key); err != nil {
			return err
		}
	}
	body, there, folder, err := x.view(at)
	if err != nil {
		return err
	}
	switch {
	case o.kind == opPut && folder:
		return fail(serve.TypeRefused, ReasonChanged, string(at), fmt.Sprintf("%s is a folder, so no file is written there", at))
	case o.kind == opMove && !there:
		return fail(serve.TypeRefused, ReasonChanged, string(at), fmt.Sprintf("%s is not there, so it is not moved", at))
	}
	return unmet(at, o.want, there, folder, body)
}

// noFileAbove refuses as changed a key whose folder the change so far has as a file.
func (x *tx) noFileAbove(k Key) error {
	for p := path.Dir(string(k)); p != "."; p = path.Dir(p) {
		_, there, folder, err := x.view(Key(p))
		if err != nil {
			return err
		}
		if there && !folder {
			return fail(serve.TypeRefused, ReasonChanged, p, fmt.Sprintf("%s is a file, so nothing is written inside it", p))
		}
	}
	return nil
}

// unmet refuses as changed a thing that is not what want wants.
func unmet(at Key, want Want, there, folder bool, body []byte) error {
	sum := ""
	if there && !folder {
		sum = SumOf(body)
	}
	if want.met(there, folder, sum) {
		return nil
	}
	now := "nothing"
	switch {
	case folder:
		now = "a folder"
	case there:
		now = sum
	}
	return fail(serve.TypeRefused, ReasonChanged, string(at), fmt.Sprintf("%s holds %s, not %s as the writer read; read it again before trying again", at, now, want))
}

// fail is a Problem pointing at key, when it names one.
func fail(typ, reason string, key string, detail string) *serve.Problem {
	p := serve.Fail(typ, reason, detail)
	if key != "" {
		p.At = []serve.Place{{File: key}}
	}
	return p
}

// diskFail says what the safety core refused, with the store's reason for it; anything else is the disk failing.
func diskFail(err error, key, what string) error {
	var p *serve.Problem
	switch {
	case errors.As(err, &p):
		return err
	case errors.Is(err, disk.ErrLink):
		return fail(serve.TypeRefused, ReasonOutside, key, fmt.Sprintf("%s leads through a link, so it is not read or written: %v", what, err))
	case errors.Is(err, disk.ErrNotOwned):
		return fail(serve.TypeFailed, ReasonNotOwned, key, fmt.Sprintf("%s is not this user's own, or others can read or write it, so it is not used: %v", what, err))
	case errors.Is(err, disk.ErrNotFile):
		return fail(serve.TypeRefused, "", key, fmt.Sprintf("%s is not a regular file, so it is not read or written", what))
	case escapes(err):
		return fail(serve.TypeRefused, ReasonOutside, key, fmt.Sprintf("%s leads out of the scope, so it is not read or written", what))
	}
	return fail(serve.TypeFailed, "", key, fmt.Sprintf("%s: %v", what, err))
}

// escapes says whether an open folder refused a name for leading out of it.
func escapes(err error) bool {
	var pe *fs.PathError
	return errors.As(err, &pe) && strings.Contains(pe.Err.Error(), "escapes")
}

// slash is key as a name inside an open folder.
func slash(k Key) string { return path.Clean(string(k)) }

// hostname is this machine as it names itself.
func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// queueFor is the change queue of one line, as this app waits in it.
func (s *Scope) queueFor(line string) queue.Queue {
	return queue.Queue{Folder: line, App: s.app.ID, Version: s.app.Version, Wait: s.app.Wait, Limit: s.app.Limit, Log: s.app.Log}
}
