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
	"os"
	"path"
	"path/filepath"
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
	if err := disk.Private(dir); err != nil {
		return nil, diskFail(err, "", fmt.Sprintf("%s's own folder %s", app.ID, dir))
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
	t := s.turns(ctx, what)
	tx := &tx{s: s, t: t, thing: thing}
	err := func() error {
		if thing == "" || !s.a.there(thing) {
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
		}
		if err := fn(tx); err != nil {
			return err
		}
		if len(tx.ops) == 0 {
			return nil
		}
		return s.a.commit(t, tx.ops)
	}()
	return t.give(err)
}

// Unfinished is every change cut short whose mark is in thing's turn, or the folder's own when thing is empty: whose it is and what it holds back.
func (s *Scope) Unfinished(thing Key) ([]Unfinished, error) {
	if thing != "" {
		if err := thing.check(); err != nil {
			return nil, err
		}
	}
	return s.a.unfinished(thing)
}

// Clear removes the marks of every change cut short in thing's turn, each in its turns, and says which; it puts nothing back, and a journal stays in its user's data folder.
// It is for the app's own command, which shows the change and asks first (G-STACK-13), once a person is sure the change was put back or given up; nothing clears a mark unasked.
func (s *Scope) Clear(ctx context.Context, thing Key) ([]Unfinished, error) {
	if thing != "" {
		if err := thing.check(); err != nil {
			return nil, err
		}
	}
	return s.a.clear(ctx, thing)
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

func (x *tx) Get(key Key) (Item, bool, error) { return x.s.Get(key) }

func (x *tx) Put(key Key, body []byte, want Want) error {
	return x.stage(op{kind: opPut, key: key, body: append([]byte{}, body...), want: want})
}

func (x *tx) Remove(key Key, want Want) error {
	return x.stage(op{kind: opRemove, key: key, want: want})
}

func (x *tx) Move(from, to Key, want Want) error {
	return x.stage(op{kind: opMove, key: to, from: from, want: want})
}

// stage checks an op before it is kept: its keys inside the change's thing, its turns taken, no change cut short holding them.
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
	x.ops = append(x.ops, o)
	return nil
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
